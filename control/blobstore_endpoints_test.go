package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"testing"
)

func TestBlobStoreSignsBrowserAndHostDownloadsSeparately(t *testing.T) {
	t.Setenv("S3_BUCKET", "dummie")
	t.Setenv("S3_REGION", "us-east-1")
	t.Setenv("S3_ACCESS_KEY_ID", "test-key")
	t.Setenv("S3_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("S3_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("S3_PUBLIC_ENDPOINT", "http://127.0.0.1:9000")
	t.Setenv("S3_HOST_ENDPOINT", "http://10.68.0.1:9000")
	t.Setenv("S3_FORCE_PATH_STYLE", "true")

	blobs := loadBlobStore(context.Background())
	if blobs == nil {
		t.Fatal("blob store did not load")
	}
	ctx := context.Background()
	browserURL, err := blobs.PresignGet(ctx, "kernels/test/kernel", "kernel")
	if err != nil {
		t.Fatal(err)
	}
	hostURL, err := blobs.PresignGetForHost(ctx, "kernels/test/kernel", "kernel")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ raw, wantHost string }{
		{browserURL, "127.0.0.1:9000"},
		{hostURL, "10.68.0.1:9000"},
	} {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		if u.Host != tc.wantHost || u.Query().Get("X-Amz-Signature") == "" {
			t.Fatalf("signed download URL = %q, want host %q and a signature", tc.raw, tc.wantHost)
		}
	}

	got, err := blobs.DownloadCacheKey(ctx, "kernels/test/kernel", "kernel")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(hostURL)
	if err != nil {
		t.Fatal(err)
	}
	u.RawQuery = ""
	sum := sha256.Sum256([]byte(u.String()))
	want := "url-" + hex.EncodeToString(sum[:])[:16]
	if got != want {
		t.Fatalf("host cache key = %q, want %q", got, want)
	}

	t.Setenv("S3_HOST_ENDPOINT", "")
	blobs = loadBlobStore(ctx)
	defaultHostURL, err := blobs.PresignGetForHost(ctx, "kernels/test/kernel", "kernel")
	if err != nil {
		t.Fatal(err)
	}
	u, err = url.Parse(defaultHostURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "127.0.0.1:9000" {
		t.Fatalf("host URL without override = %q, want the browser endpoint", defaultHostURL)
	}
}
