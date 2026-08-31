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

	"control/internal/proto"
)

type artifact struct {
	ref    string
	sha256 string
}

func (a artifact) empty() bool { return strings.TrimSpace(a.ref) == "" }

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
		sum := sha256.Sum256([]byte(cacheURL(u)))
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

func cacheURL(u *url.URL) string {
	trimmed := *u
	trimmed.RawQuery = ""
	trimmed.ForceQuery = false
	trimmed.Fragment = ""
	trimmed.RawFragment = ""
	return trimmed.String()
}

const tarSlack = 256 << 20

func ext4FromTar(ctx context.Context, cache, tarPath, pubKey, resolver string, image *proto.ImageConfig, sizeBytes int64, logf func(string, ...any)) (string, bool, error) {
	digest, err := fileDigest(tarPath)
	if err != nil {
		return "", false, err
	}

	guestInit, guestInitDigest, guestInitErr := guestInitSource()
	if guestInitErr != nil {
		logf("WARNING: this rootfs will be built without %s: %v", guestInitService, guestInitErr)
		logf("WARNING: the image must then bring up its own init, network and sshd")
	}

	digest = keyedDigest(digest, imageRecipe(pubKey, resolver, guestInitDigest)+imageConfigRecipe(image))
	dst := filepath.Join(cache, digest[:32]+"-rootfs.ext4")
	if _, err := os.Stat(dst); err == nil {
		return dst, guestInit != "", nil
	}

	if sizeBytes == 0 {
		info, err := os.Stat(tarPath)
		if err != nil {
			return "", false, err
		}
		sizeBytes = info.Size() + info.Size()/2 + tarSlack
	}

	if os.Geteuid() != 0 {
		log.Print("WARNING: not running as root; file ownership and device nodes in the rootfs will not be preserved")
	}

	log.Printf("building ext4 rootfs from %s (%d MiB)", filepath.Base(tarPath), sizeBytes>>20)

	work, err := os.MkdirTemp(cache, ".extract-*")
	if err != nil {
		return "", false, err
	}
	defer func() { _ = os.RemoveAll(work) }()

	out, err := exec.CommandContext(ctx, "tar", "-xpf", tarPath, "-C", work, "--numeric-owner").CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("could not extract %s: %v: %s", tarPath, err, strings.TrimSpace(string(out)))
	}

	if err := stripContainerMarkers(work); err != nil {
		return "", false, fmt.Errorf("could not strip the container markers from the rootfs: %w", err)
	}

	if err := ensureResolvConf(work, resolver); err != nil {
		return "", false, fmt.Errorf("could not point the rootfs at the resolver: %w", err)
	}

	if pubKey != "" {
		if err := injectAuthorizedKey(work, pubKey, imageConfigUser(image)); err != nil {
			return "", false, fmt.Errorf("could not install the dpipe key into the rootfs: %w", err)
		}
	}

	if guestInit != "" {
		if err := injectGuestInit(work, guestInit); err != nil {
			return "", false, fmt.Errorf("could not install %s into the rootfs: %w", guestInitService, err)
		}
		if err := injectImageConfig(work, image); err != nil {
			return "", false, fmt.Errorf("could not record the image configuration in the rootfs: %w", err)
		}
	} else if init := imageInit(work); init == "" {
		// Without an init of its own the kernel falls back to /bin/sh: the vm
		// looks like it booted, with no network, no sshd and no pid 1 worth the
		// name. Better to refuse than to hand over that.
		return "", false, fmt.Errorf("this image has no init of its own and %s is not installed on this host, so the vm would boot into a bare shell: %w",
			guestInitService, guestInitErr)
	} else {
		logf("the image brings its own init (%s)", init)
	}

	tmp, err := os.CreateTemp(cache, ".rootfs-*.ext4")
	if err != nil {
		return "", false, err
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpName) }()

	if err := os.Truncate(tmpName, sizeBytes); err != nil {
		return "", false, err
	}
	out, err = exec.CommandContext(ctx, "mkfs.ext4", "-F", "-q", "-d", work, tmpName).CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("mkfs.ext4: %v: %s\n(if the image is too small for the tar, pass a larger --rootfs-size)",
			err, strings.TrimSpace(string(out)))
	}

	if err := os.Chmod(tmpName, 0o644); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return "", false, err
	}
	return dst, guestInit != "", nil
}

// imageInit reports what would run as pid 1 if dinit were not installed. The
// kernel's own fallbacks past these are /bin/init and /bin/sh, neither of which
// is an init.
func imageInit(root string) string {
	for _, p := range []string{"/lib/systemd/systemd", "/usr/lib/systemd/systemd", "/sbin/init", "/etc/init"} {
		full, err := imagePath(root, p)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return ""
}

func imageRecipe(pubKey, resolver, guestInitDigest string) string {
	r := "recipe=4;strip=" + strings.Join(containerMarkers, ",") + ";" +
		authKeyRecipe() + ";" + resolvConfRecipe(resolver)
	if pubKey != "" {
		r += ";key=" + pubKey
	}
	if guestInitDigest != "" {
		r += ";dinit=" + guestInitDigest
	}
	return r
}

var containerMarkers = []string{".dockerenv", "run/.containerenv"}

func stripContainerMarkers(root string) error {
	for _, m := range containerMarkers {
		p, err := imagePath(root, m)
		if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func keyedDigest(digest, extra string) string {
	sum := sha256.Sum256([]byte(digest + "\x00" + extra))
	return hex.EncodeToString(sum[:])
}

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
