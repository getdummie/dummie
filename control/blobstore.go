package main

import (
  "context"
  "errors"
  "fmt"
  "io"
  "log"
  "os"
  "strings"
  "time"

  "github.com/aws/aws-sdk-go-v2/aws"
  awsconfig "github.com/aws/aws-sdk-go-v2/config"
  "github.com/aws/aws-sdk-go-v2/credentials"
  "github.com/aws/aws-sdk-go-v2/feature/s3/manager"
  "github.com/aws/aws-sdk-go-v2/service/s3"
  "github.com/google/uuid"
)

// blobStore is the S3 bucket uploaded artifacts -- kernels and OS images --
// live in.
//
// The bucket is private: nothing here ever hands out a bare object URL. Reads
// go through a presigned GET minted per request, so access is bounded in time
// and revoking it is a matter of not minting another.
type blobStore struct {
  client  *s3.Client
  presign *s3.PresignClient
  bucket  string
  // Key prefix, "" or ending in a slash. Lets one bucket hold other things;
  // each kind of artifact adds its own segment under it (see newKey).
  prefix string
  // How long a presigned download link stays valid.
  presignTTL time.Duration
  // Largest upload accepted, in bytes.
  maxUploadBytes int64
}

// errNoBlobStore is what every kernel route answers with when S3 was never
// configured. A missing bucket is an operator's omission, not a caller's
// mistake, so it is reported as such rather than as a broken upload.
var errNoBlobStore = errors.New("object storage is not configured on this server")

// loadBlobStore builds the store from the environment, or returns nil when
// S3_BUCKET is unset. Nil is a supported state: the rest of the control plane
// does not need object storage, and a deployment without it should still start.
//
// Credentials come from the default AWS chain (environment, shared config,
// instance role) unless S3_ACCESS_KEY_ID is set, which is what a MinIO or R2
// deployment usually does.
func loadBlobStore(ctx context.Context) *blobStore {
  bucket := strings.TrimSpace(os.Getenv("S3_BUCKET"))
  if bucket == "" {
    log.Print("S3_BUCKET not set; the kernels section will report object storage as unconfigured")
    return nil
  }

  client, err := s3Client(ctx, strings.TrimSpace(os.Getenv("S3_ENDPOINT")))
  if err != nil {
    log.Printf("could not build the S3 client; kernels will report object storage as unconfigured: %v", err)
    return nil
  }

  // Presigning is done by its own client when the store is reachable under two
  // names. Inside compose the endpoint is a service name; a browser on the host
  // cannot resolve it, and the host is part of what SigV4 signs -- so a link
  // minted against the internal name cannot be rewritten afterwards without
  // breaking the signature. It has to be signed for the public name to begin
  // with.
  presignClient := client
  if pub := strings.TrimSpace(os.Getenv("S3_PUBLIC_ENDPOINT")); pub != "" {
    pc, err := s3Client(ctx, pub)
    if err != nil {
      log.Printf("could not build the presigning S3 client; download links will use S3_ENDPOINT: %v", err)
    } else {
      presignClient = pc
    }
  }

  // Optional: unset means the bucket's root, under which each kind of artifact
  // still gets its own segment.
  prefix := strings.Trim(strings.TrimSpace(os.Getenv("S3_PREFIX")), "/")
  if prefix != "" {
    prefix += "/"
  }

  // envInt takes any number it can parse, and zero or less means "no download
  // links" and "no upload is small enough" respectively -- a typo that would
  // read as a broken feature rather than a bad setting.
  presignMins := envInt("S3_PRESIGN_EXPIRY_MINS", 15)
  if presignMins < 1 {
    log.Printf("S3_PRESIGN_EXPIRY_MINS must be positive; using 15")
    presignMins = 15
  }
  maxUploadMiB := envInt("KERNEL_MAX_UPLOAD_MIB", 2048)
  if maxUploadMiB < 1 {
    log.Printf("KERNEL_MAX_UPLOAD_MIB must be positive; using 2048")
    maxUploadMiB = 2048
  }

  return &blobStore{
    client:         client,
    presign:        s3.NewPresignClient(presignClient),
    bucket:         bucket,
    prefix:         prefix,
    presignTTL:     time.Duration(presignMins) * time.Minute,
    maxUploadBytes: int64(maxUploadMiB) * 1024 * 1024,
  }
}

// s3Client builds a client addressing the store at endpoint, or at AWS itself
// when endpoint is empty. Region and credentials are the same either way: two
// clients for one store differ only in the host they talk to, and a presigned
// link is only valid if it was signed with the same key and region as a request
// to the internal name would be.
func s3Client(ctx context.Context, endpoint string) (*s3.Client, error) {
  opts := []func(*awsconfig.LoadOptions) error{}
  if region := strings.TrimSpace(os.Getenv("S3_REGION")); region != "" {
    opts = append(opts, awsconfig.WithRegion(region))
  }
  if id := strings.TrimSpace(os.Getenv("S3_ACCESS_KEY_ID")); id != "" {
    opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
      id, os.Getenv("S3_SECRET_ACCESS_KEY"), "",
    )))
  }

  // Set for anything that is not AWS itself (MinIO, Ceph, R2, rustfs). Resolved
  // through the endpoint resolver rather than the client's BaseEndpoint, which
  // the SDK version this module builds against does not have.
  if endpoint != "" {
    opts = append(opts, awsconfig.WithEndpointResolverWithOptions(
      aws.EndpointResolverWithOptionsFunc(func(service, region string, _ ...any) (aws.Endpoint, error) {
        if service != s3.ServiceID {
          // Everything else -- STS, IMDS -- keeps its real endpoint.
          return aws.Endpoint{}, &aws.EndpointNotFoundError{}
        }
        // Immutable: the SDK otherwise rewrites the host to bucket.<endpoint>,
        // which is the one thing a local endpoint has no DNS for.
        return aws.Endpoint{URL: endpoint, HostnameImmutable: true, SigningRegion: region}, nil
      }),
    ))
  }

  cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
  if err != nil {
    return nil, err
  }
  return s3.NewFromConfig(cfg, func(o *s3.Options) {
    // A custom endpoint is almost always path-style, for the same reason the
    // hostname above is left alone.
    if endpoint != "" || strings.EqualFold(os.Getenv("S3_FORCE_PATH_STYLE"), "true") {
      o.UsePathStyle = true
    }
  }), nil
}

// newKey is where a freshly uploaded artifact goes: the configured prefix, the
// kind of thing it is, and a uuid.
//
// The uuid, not the operator's name, is what makes the key unique -- a key
// derived from a name would collide with the object of a withdrawn entry that
// had the same one. fileName is kept as the last segment so the key alone says
// what the object is when read from a bucket listing.
func (b *blobStore) newKey(kind, fileName string) string {
  return b.prefix + kind + "/" + uuid.New().String() + "/" + fileName
}

// Put streams r into the bucket under key. The uploader splits large bodies
// into parts itself, so a kernel image is never held in memory whole.
func (b *blobStore) Put(ctx context.Context, key, contentType string, r io.Reader) error {
  up := manager.NewUploader(b.client)
  _, err := up.Upload(ctx, &s3.PutObjectInput{
    Bucket:      aws.String(b.bucket),
    Key:         aws.String(key),
    Body:        r,
    ContentType: aws.String(contentType),
  })
  return err
}

// Delete removes an object. Only used to clean up after an upload whose row
// failed to save -- a saved kernel's object is kept even when it is withdrawn.
func (b *blobStore) Delete(ctx context.Context, key string) error {
  _, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
    Bucket: aws.String(b.bucket),
    Key:    aws.String(key),
  })
  return err
}

// PresignGet returns a short-lived download URL. fileName is what the browser
// saves it as: the key is a uuid, which would otherwise land on disk as one.
func (b *blobStore) PresignGet(ctx context.Context, key, fileName string) (string, error) {
  req, err := b.presign.PresignGetObject(ctx, &s3.GetObjectInput{
    Bucket: aws.String(b.bucket),
    Key:    aws.String(key),
    ResponseContentDisposition: aws.String(
      fmt.Sprintf("attachment; filename=%q", sanitizeFileName(fileName)),
    ),
  }, s3.WithPresignExpires(b.presignTTL))
  if err != nil {
    return "", err
  }
  return req.URL, nil
}

// sanitizeFileName keeps a name usable inside a quoted Content-Disposition
// header: no path, no quotes, no control characters, never empty.
func sanitizeFileName(name string) string {
  name = name[strings.LastIndexAny(name, `/\`)+1:]
  name = strings.Map(func(r rune) rune {
    if r < 0x20 || r == '"' || r == 0x7f {
      return -1
    }
    return r
  }, name)
  if name == "" {
    return "kernel"
  }
  return name
}
