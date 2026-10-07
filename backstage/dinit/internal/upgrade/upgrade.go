// Package upgrade replaces /sbin/dinit inside a running guest. The write lands
// in the vm's own overlay, so it outlives a restart without touching the
// rootfs image it was built from.
package upgrade

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	repo        = "getdummie/dummie"
	latestURL   = "https://api.github.com/repos/" + repo + "/releases/latest"
	assetURL    = "https://github.com/" + repo + "/releases/download/v%s/%s"
	timeout     = 2 * time.Minute
	maxDownload = 128 << 20
)

var versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

var elfMachines = map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}

func Run(current string, args []string) int {
	fs := flag.NewFlagSet("dinit upgrade", flag.ContinueOnError)
	source := fs.String("source", "", "https/http url or local path of a dinit binary or tarball; default is the latest GitHub release")
	version := fs.String("version", "", "GitHub release to install, e.g. 0.0.37; default is the latest")
	sum := fs.String("sha256", "", "expected sha256 of what --source points at")
	dest := fs.String("dest", "/sbin/dinit", "where to install")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *source != "" && *version != "" {
		fmt.Fprintln(os.Stderr, "dinit upgrade: --source and --version are exclusive")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := run(ctx, current, *source, *version, *sum, *dest); err != nil {
		fmt.Fprintln(os.Stderr, "dinit upgrade:", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, current, source, version, sum, dest string) error {
	if source == "" {
		if version == "" {
			v, err := latestVersion(ctx)
			if err != nil {
				return err
			}
			version = v
		}
		if !versionRe.MatchString(version) {
			return fmt.Errorf("%q is not a release number", version)
		}
		if version == current {
			fmt.Printf("dinit %s is already installed\n", current)
			return nil
		}
		asset := fmt.Sprintf("dinit_%s_linux_%s.tar.gz", version, runtime.GOARCH)
		source = fmt.Sprintf(assetURL, version, asset)
		s, err := releaseChecksum(ctx, version, asset)
		if err != nil {
			return err
		}
		sum = s
	}

	fmt.Printf("fetching %s\n", source)
	body, err := fetch(ctx, source)
	if err != nil {
		return err
	}
	if sum != "" {
		got := sha256.Sum256(body)
		if !strings.EqualFold(hex.EncodeToString(got[:]), strings.TrimSpace(sum)) {
			return fmt.Errorf("sha256 mismatch for %s", source)
		}
	} else {
		fmt.Println("warning: no checksum to verify against; pass --sha256 to check one")
	}

	bin := body
	if isGzip(body) {
		if bin, err = fromTarball(body, "dinit"); err != nil {
			return err
		}
	}
	if err := checkBinary(bin); err != nil {
		return err
	}
	if err := install(bin, dest); err != nil {
		return err
	}
	fmt.Printf("installed %s (was %s)\n", dest, current)
	fmt.Println("the agent tab picks it up the next time it connects; pid 1 on the next boot")
	return nil
}

func latestVersion(ctx context.Context) (string, error) {
	b, err := fetch(ctx, latestURL)
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &rel); err != nil {
		return "", fmt.Errorf("could not read the latest release: %w", err)
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

func releaseChecksum(ctx context.Context, version, asset string) (string, error) {
	b, err := fetch(ctx, fmt.Sprintf(assetURL, version, "checksums.txt"))
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == asset {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt of v%s does not list %s", version, asset)
}

func fetch(ctx context.Context, src string) ([]byte, error) {
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return readLimited(os.Open(src))
	}
	if u.Scheme == "http" {
		fmt.Println("warning: fetching over plain http")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %s", src, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

func readLimited(f *os.File, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxDownload))
}

func isGzip(b []byte) bool {
	return len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b
}

func fromTarball(b []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("the tarball contains no %s", want)
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == want {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

// checkBinary refuses anything that could not boot as pid 1 in this guest.
func checkBinary(b []byte) error {
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("not an elf binary: %w", err)
	}
	defer f.Close()
	if want, ok := elfMachines[runtime.GOARCH]; ok && f.Machine != want {
		return fmt.Errorf("built for %s, but this vm is %s", f.Machine, runtime.GOARCH)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return errors.New("dynamically linked; dinit has to be built with CGO_ENABLED=0")
		}
	}
	return nil
}

// install renames over dest so a boot never sees a half-written init.
func install(b []byte, dest string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dinit-upgrade-*")
	if err != nil {
		return fmt.Errorf("%w (run it with sudo)", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}
