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

type blobStore struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	prefix string
	presignTTL time.Duration
	maxUploadBytes int64
}

var errNoBlobStore = errors.New("object storage is not configured on this server")

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

	presignClient := client
	if pub := strings.TrimSpace(os.Getenv("S3_PUBLIC_ENDPOINT")); pub != "" {
		pc, err := s3Client(ctx, pub)
		if err != nil {
			log.Printf("could not build the presigning S3 client; download links will use S3_ENDPOINT: %v", err)
		} else {
			presignClient = pc
		}
	}

	prefix := strings.Trim(strings.TrimSpace(os.Getenv("S3_PREFIX")), "/")
	if prefix != "" {
		prefix += "/"
	}

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

	if endpoint != "" {
		opts = append(opts, awsconfig.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, _ ...any) (aws.Endpoint, error) {
				if service != s3.ServiceID {
					return aws.Endpoint{}, &aws.EndpointNotFoundError{}
				}
				return aws.Endpoint{URL: endpoint, HostnameImmutable: true, SigningRegion: region}, nil
			}),
		))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" || strings.EqualFold(os.Getenv("S3_FORCE_PATH_STYLE"), "true") {
			o.UsePathStyle = true
		}

		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	}), nil
}

func (b *blobStore) newKey(kind, fileName string) string {
	return b.prefix + kind + "/" + uuid.New().String() + "/" + fileName
}

func (b *blobStore) Put(ctx context.Context, key, contentType string, r io.Reader) error {
	up := manager.NewUploader(b.client, func(u *manager.Uploader) {
		u.PartSize = 16 << 20
		u.Concurrency = 4
	})
	_, err := up.Upload(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        r,
		ContentType: aws.String(contentType),
	})
	return err
}

const maxInlineObjectBytes = 1 << 20

func (b *blobStore) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()

	body, err := io.ReadAll(io.LimitReader(out.Body, maxInlineObjectBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxInlineObjectBytes {
		return nil, fmt.Errorf("object %s is larger than %d bytes", key, maxInlineObjectBytes)
	}
	return body, nil
}

func (b *blobStore) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	return err
}

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
