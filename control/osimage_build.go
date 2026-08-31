package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// osImageConfig is the part of an oci image config a vm built from it needs: what
// the image would have run, as whom, with what environment. `docker export` --
// which is what the flattened tar below is -- keeps none of it, so it is read off
// the image config and stored on the row instead.
type osImageConfig struct {
	User         string
	Entrypoint   []string
	Cmd          []string
	Env          []string
	ExposedPorts []int32
}

type osImageBuild struct {
	Digest    string
	ObjectKey string
	FileName  string
	SizeBytes int64
	Config    osImageConfig
}

// osImagePlatform is what the fleet runs; a multi-arch index is resolved to this.
var osImagePlatform = v1.Platform{OS: "linux", Architecture: "amd64"}

var errOSImageTooLarge = errors.New("the flattened image is larger than this server accepts")

// validateOCIRef rejects a reference the registry client could never resolve, so
// a typo comes back as a 400 rather than as a row that fails five times first.
func validateOCIRef(ref string) error {
	if _, err := name.ParseReference(strings.TrimSpace(ref)); err != nil {
		return fmt.Errorf("%q is not a container image reference", ref)
	}
	return nil
}

// buildOSImageFromOCI pulls ref, flattens its layers into one rootfs tar and
// streams that to object storage. It is the in-process equivalent of
// `docker create` + `docker export`, minus the docker daemon: mutate.Extract
// applies the layers in order and resolves whiteouts, so the result is the same
// shape dclient's ext4FromTar already consumes.
func buildOSImageFromOCI(ctx context.Context, blobs *blobStore, imageName, ref string) (osImageBuild, error) {
	var out osImageBuild

	r, err := name.ParseReference(strings.TrimSpace(ref))
	if err != nil {
		return out, fmt.Errorf("%q is not a container image reference: %w", ref, err)
	}

	img, err := remote.Image(r,
		remote.WithContext(ctx),
		remote.WithPlatform(osImagePlatform),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	)
	if err != nil {
		return out, fmt.Errorf("could not pull %s: %w", r, err)
	}

	digest, err := img.Digest()
	if err != nil {
		return out, fmt.Errorf("could not read the digest of %s: %w", r, err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return out, fmt.Errorf("could not read the image config of %s: %w", r, err)
	}

	out.Digest = digest.String()
	out.Config = osImageConfigFrom(cfg.Config)
	out.FileName = sanitizeFileName(imageName + "-rootfs.tar")
	out.ObjectKey = blobs.newKey("osimages", out.FileName)

	limit := osImageMaxBytes(blobs)
	tar := mutate.Extract(img)
	defer tar.Close()

	counted := &countingReader{r: io.LimitReader(tar, limit+1)}
	if err := blobs.Put(ctx, out.ObjectKey, "application/x-tar", counted); err != nil {
		blobs.deleteQuietly(ctx, out.ObjectKey)
		return out, fmt.Errorf("could not upload the rootfs for %s: %w", r, err)
	}
	if counted.n > limit {
		blobs.deleteQuietly(ctx, out.ObjectKey)
		return out, fmt.Errorf("%s flattens to more than %d MiB: %w", r, limit>>20, errOSImageTooLarge)
	}
	if counted.n == 0 {
		blobs.deleteQuietly(ctx, out.ObjectKey)
		return out, fmt.Errorf("%s flattened to an empty rootfs", r)
	}
	out.SizeBytes = counted.n

	return out, nil
}

func osImageConfigFrom(c v1.Config) osImageConfig {
	return osImageConfig{
		User:         c.User,
		Entrypoint:   append([]string{}, c.Entrypoint...),
		Cmd:          append([]string{}, c.Cmd...),
		Env:          append([]string{}, c.Env...),
		ExposedPorts: exposedPorts(c.ExposedPorts),
	}
}

// exposedPorts turns docker's {"8080/tcp": {}} into a sorted [8080]. Udp-only
// ports are kept too: what the image exposes is worth recording either way.
func exposedPorts(m map[string]struct{}) []int32 {
	ports := make([]int32, 0, len(m))
	seen := make(map[int32]struct{}, len(m))
	for spec := range m {
		num, _, _ := strings.Cut(spec, "/")
		p, err := strconv.Atoi(strings.TrimSpace(num))
		if err != nil || p < 1 || p > 65535 {
			continue
		}
		if _, dup := seen[int32(p)]; dup {
			continue
		}
		seen[int32(p)] = struct{}{}
		ports = append(ports, int32(p))
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return ports
}

const defaultOSImageMaxMiB = 8192

func osImageMaxBytes(blobs *blobStore) int64 {
	if mib := envInt("OSIMAGE_MAX_MIB", defaultOSImageMaxMiB); mib > 0 {
		return int64(mib) * 1024 * 1024
	}
	return blobs.maxUploadBytes
}

func (b *blobStore) deleteQuietly(ctx context.Context, key string) {
	// The build's own context may already be cancelled or timed out, which is
	// exactly when the cleanup matters most.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := b.Delete(ctx, key); err != nil {
		log.Printf("orphaned os image object %q: %v", key, err)
	}
}
