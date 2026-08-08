package main

import (
  "context"
  "crypto/sha256"
  "encoding/hex"
  "encoding/json"
  "fmt"
  "io"
  "log"
  "net/http"
  "net/url"
  "os"
  "os/exec"
  "path"
  "path/filepath"
  "strings"
)

// artifact is one input image: a URL to fetch, or a path that already exists on
// this machine. A local path is not copied into the cache -- it is used where it
// lies, which keeps development iteration cheap.
type artifact struct {
  ref    string // URL or local path
  sha256 string // optional; verified after download
}

func (a artifact) empty() bool { return strings.TrimSpace(a.ref) == "" }

// resolve returns an absolute path to the artifact, downloading it into the
// shared cache if needed.
func (a artifact) resolve(ctx context.Context, cache string) (string, error) {
  ref := strings.TrimSpace(a.ref)
  u, err := url.Parse(ref)
  if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
    p, err := filepath.Abs(ref)
    if err != nil {
      return "", err
    }
    if _, err := os.Stat(p); err != nil {
      return "", fmt.Errorf("image %q is neither an http(s) URL nor an existing file: %w", ref, err)
    }
    return p, nil
  }
  return download(ctx, cache, u, a.sha256)
}

// download is content-addressed when a digest is known, so the same image
// requested by two different URLs is stored once. Without a digest the URL is
// the identity, which is the best that can be done.
//
// A cache hit is not re-hashed: verification happens once, at download time.
func download(ctx context.Context, cache string, u *url.URL, want string) (string, error) {
  if err := os.MkdirAll(cache, 0o755); err != nil {
    return "", err
  }

  base := path.Base(u.Path)
  if base == "" || base == "." || base == "/" {
    base = "image"
  }
  key := want
  if key == "" {
    sum := sha256.Sum256([]byte(u.String()))
    key = "url-" + hex.EncodeToString(sum[:])[:16]
  }
  dst := filepath.Join(cache, key[:min(len(key), 32)]+"-"+base)

  if _, err := os.Stat(dst); err == nil {
    return dst, nil
  }

  log.Printf("downloading %s", u)
  req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
  if err != nil {
    return "", err
  }
  res, err := http.DefaultClient.Do(req)
  if err != nil {
    return "", fmt.Errorf("could not fetch %s: %w", u, err)
  }
  defer res.Body.Close()
  if res.StatusCode != http.StatusOK {
    return "", fmt.Errorf("could not fetch %s: %s", u, res.Status)
  }

  tmp, err := os.CreateTemp(cache, ".download-*")
  if err != nil {
    return "", err
  }
  tmpName := tmp.Name()
  defer func() { _ = os.Remove(tmpName) }()

  h := sha256.New()
  if _, err := io.Copy(io.MultiWriter(tmp, h), res.Body); err != nil {
    _ = tmp.Close()
    return "", fmt.Errorf("download of %s failed: %w", u, err)
  }
  if err := tmp.Close(); err != nil {
    return "", err
  }

  got := hex.EncodeToString(h.Sum(nil))
  if want != "" && !strings.EqualFold(got, want) {
    return "", fmt.Errorf("checksum mismatch for %s: want %s, got %s", u, want, got)
  }
  if want == "" {
    log.Printf("no checksum given for %s (sha256 is %s)", base, got)
  }

  if err := os.Chmod(tmpName, 0o644); err != nil {
    return "", err
  }
  if err := os.Rename(tmpName, dst); err != nil {
    return "", err
  }
  return dst, nil
}

// --- tar root filesystems ---------------------------------------------------

// tarSlack is added on top of one and a half times the tar's own size: a tar is
// a packed stream, so the filesystem that unpacks it needs room for block
// rounding, metadata, and whatever the guest writes once it boots.
const tarSlack = 256 << 20

// ext4FromTar turns a tar of a root filesystem -- what `docker export` produces
// -- into a mountable ext4 image, and caches the result. A tar is a stream of
// files, not a block device; handing one straight to virtio-blk gets the guest
// as far as a root-mount panic and no further.
//
// The image is built with mkfs.ext4 -d, which populates from a directory
// without a loop mount, so nothing here needs to mount anything. That window --
// the rootfs as a plain directory -- is also where pubKey, when there is one,
// is installed into the guest's authorized_keys.
//
// The result is shared by every VM built from the same tar, so pubKey must be a
// host-wide key rather than anything per VM.
func ext4FromTar(ctx context.Context, cache, tarPath, pubKey string, sizeBytes int64) (string, error) {
  digest, err := fileDigest(tarPath)
  if err != nil {
    return "", err
  }
  // Keyed on everything that goes into the image, which is the tar and -- since
  // the dpipe key is baked in below -- the key too. Keying on the tar alone
  // would mean a host that gained or rotated a key kept serving the image built
  // before it, forever and silently: the filename would still match, so the
  // build that installs the new key would never run.
  //
  // Left as the bare tar digest when there is no key, so a host that does not
  // run dpipe keeps the images it has already built.
  if pubKey != "" {
    digest = keyedDigest(digest, pubKey)
  }
  // Rebuilding a 400 MiB image on every create, for inputs that have not
  // changed, is a minute of nothing.
  dst := filepath.Join(cache, digest[:32]+"-rootfs.ext4")
  if _, err := os.Stat(dst); err == nil {
    return dst, nil
  }

  if sizeBytes == 0 {
    info, err := os.Stat(tarPath)
    if err != nil {
      return "", err
    }
    sizeBytes = info.Size() + info.Size()/2 + tarSlack
  }

  if os.Geteuid() != 0 {
    // Extraction as a normal user cannot restore file ownership or device
    // nodes, so the guest would boot with a root filesystem owned by nobody in
    // particular. Better to say so than to produce a subtly broken image.
    log.Print("WARNING: not running as root; file ownership and device nodes in the rootfs will not be preserved")
  }

  log.Printf("building ext4 rootfs from %s (%d MiB)", filepath.Base(tarPath), sizeBytes>>20)

  work, err := os.MkdirTemp(cache, ".extract-*")
  if err != nil {
    return "", err
  }
  defer func() { _ = os.RemoveAll(work) }()

  // GNU tar rather than archive/tar: hardlinks, sparse members, pax headers and
  // xattrs are all things it already gets right.
  out, err := exec.CommandContext(ctx, "tar", "-xpf", tarPath, "-C", work, "--numeric-owner").CombinedOutput()
  if err != nil {
    return "", fmt.Errorf("could not extract %s: %v: %s", tarPath, err, strings.TrimSpace(string(out)))
  }

  // Between the extraction and the mkfs is the only moment the root filesystem
  // is an ordinary directory this process can write to. Fatal rather than a
  // warning: the cache key says this image has the key in it, so building one
  // without it would poison the cache with an image no later create can fix.
  if pubKey != "" {
    if err := injectAuthorizedKey(work, pubKey); err != nil {
      return "", fmt.Errorf("could not install the dpipe key into the rootfs: %w", err)
    }
  }

  tmp, err := os.CreateTemp(cache, ".rootfs-*.ext4")
  if err != nil {
    return "", err
  }
  tmpName := tmp.Name()
  _ = tmp.Close()
  defer func() { _ = os.Remove(tmpName) }()

  // Sparse: only the blocks mkfs and the payload actually touch are allocated.
  if err := os.Truncate(tmpName, sizeBytes); err != nil {
    return "", err
  }
  out, err = exec.CommandContext(ctx, "mkfs.ext4", "-F", "-q", "-d", work, tmpName).CombinedOutput()
  if err != nil {
    return "", fmt.Errorf("mkfs.ext4: %v: %s\n(if the image is too small for the tar, pass a larger --rootfs-size)",
      err, strings.TrimSpace(string(out)))
  }

  if err := os.Chmod(tmpName, 0o644); err != nil {
    return "", err
  }
  if err := os.Rename(tmpName, dst); err != nil {
    return "", err
  }
  return dst, nil
}

// keyedDigest folds a second input into a digest. Used to make the cache
// identity of a built rootfs cover the injected key as well as the tar, so
// changing either one is a different image rather than a stale hit.
//
// The two are separated by a byte that cannot appear in a hex digest, so no pair
// of inputs can concatenate into the same string as another pair.
func keyedDigest(digest, extra string) string {
  sum := sha256.Sum256([]byte(digest + "\x00" + extra))
  return hex.EncodeToString(sum[:])
}

// fileDigest is the cache identity for a built image: the same tar always
// yields the same ext4, so the tar's own hash is the right key.
func fileDigest(p string) (string, error) {
  f, err := os.Open(p)
  if err != nil {
    return "", err
  }
  defer f.Close()
  h := sha256.New()
  if _, err := io.Copy(h, f); err != nil {
    return "", err
  }
  return hex.EncodeToString(h.Sum(nil)), nil
}

// --- overlays ---------------------------------------------------------------

// newOverlay creates the per-VM writable layer. The cached base image is shared
// by every VM created from it and is never written to, so a VM costs only its
// own dirty blocks.
//
// sizeBytes grows the virtual disk beyond the base image when non-zero; the
// guest still has to grow its filesystem to use the space.
func newOverlay(ctx context.Context, base, dst string, sizeBytes int64) error {
  format, err := imageFormat(ctx, base)
  if err != nil {
    return err
  }
  args := []string{"create", "-q", "-f", "qcow2", "-F", format, "-b", base, dst}
  if sizeBytes > 0 {
    args = append(args, fmt.Sprintf("%d", sizeBytes))
  }
  out, err := exec.CommandContext(ctx, "qemu-img", args...).CombinedOutput()
  if err != nil {
    return fmt.Errorf("qemu-img create: %v: %s", err, strings.TrimSpace(string(out)))
  }
  return nil
}

// imageFormat asks qemu-img rather than guessing from the extension: passing
// the wrong backing format makes qemu refuse to open the chain.
func imageFormat(ctx context.Context, p string) (string, error) {
  out, err := exec.CommandContext(ctx, "qemu-img", "info", "--output=json", p).Output()
  if err != nil {
    return "", fmt.Errorf("qemu-img info %s: %w", p, err)
  }
  var info struct {
    Format string `json:"format"`
  }
  if err := json.Unmarshal(out, &info); err != nil {
    return "", fmt.Errorf("could not parse qemu-img info for %s: %w", p, err)
  }
  if info.Format == "" {
    return "", fmt.Errorf("qemu-img could not determine the format of %s", p)
  }
  return info.Format, nil
}
