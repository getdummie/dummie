package main

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"control/internal/proto"
)

// dinit is the guest side of dclient: it is copied into every rootfs built from a
// container tar and booted as pid 1, so the image itself needs no init, no dhcp
// client and no sshd. The command line below is the whole contract between the
// two halves; see backstage/dinit.
//
// It arrives the same way dproxy and dpipe do -- a version or a download URL the
// control server names, per host or fleet-wide -- but it is not a service: there
// is no unit and nothing to start, it only has to be on disk when a rootfs is
// built.
const (
	guestInitService = "dinit"
	guestInitPath    = "/sbin/dinit"

	// The image's configuration is too large and too quote-hostile for the
	// kernel command line, so it is written into the rootfs instead and dinit
	// reads it from there. Absent, it runs as it always did: root, no workload.
	guestImageConfigPath = "/etc/dclient/image.json"

	paramIP      = "dclient.ip"
	paramGateway = "dclient.gw"
	paramDNS     = "dclient.dns"
)

func ensureGuestInit(ctx context.Context, data string, rel proto.ServiceRelease, force bool) error {
	src, err := resolveRelease(guestInitService, rel)
	if err != nil {
		return err
	}
	replaced, err := ensureBinary(ctx, data, guestInitService, src, force)
	if err != nil {
		return err
	}
	// A binary that cannot be pid 1 in a guest is kept where it is, so the next
	// rootfs build reports the same precise reason instead of "not installed".
	// Only the marker goes, so a fixed build behind the same url is picked up on
	// the next connect rather than needing a forced upgrade.
	if err := checkGuestInit(serviceBinary(guestInitService)); err != nil {
		_ = os.Remove(sourceMarker(data, guestInitService))
		return err
	}
	if replaced {
		log.Printf("%s is installed from %s; rootfs images are rebuilt with it as vms are created",
			guestInitService, src)
	}
	return nil
}

// guestInitBinary is the dinit to copy into a rootfs. It is whatever the control
// server last installed here; nothing is fetched at build time, so an image is
// never built against a version the fleet did not ask for.
func guestInitBinary() (string, error) {
	p := serviceBinary(guestInitService)
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s is not installed yet; the control server installs it on connect (set a %s version or download url on this host, or fleet-wide in settings)",
				p, guestInitService)
		}
		return "", err
	}
	return p, nil
}

func guestInitSource() (path, digest string, err error) {
	if path, err = guestInitBinary(); err != nil {
		return "", "", err
	}
	if err := checkGuestInit(path); err != nil {
		return "", "", err
	}
	if digest, err = fileDigest(path); err != nil {
		return "", "", err
	}
	return path, digest, nil
}

var elfMachines = map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}

// checkGuestInit rejects a dinit that cannot run as a guest's pid 1 before it can
// turn into a vm that boots into a bare shell.
func checkGuestInit(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("%s is not an elf binary: %w", path, err)
	}
	defer f.Close()

	if want, ok := elfMachines[runtime.GOARCH]; ok && f.Machine != want {
		return fmt.Errorf("%s is built for %s, but this host is %s", path, f.Machine, runtime.GOARCH)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return fmt.Errorf("%s is dynamically linked, so it cannot be pid 1 in an arbitrary guest; build it with CGO_ENABLED=0 (`just build` in backstage/dinit)", path)
		}
	}
	return nil
}

func injectGuestInit(root, src string) error {
	dst, err := imagePath(root, guestInitPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := copyFile(src, dst, 0o755); err != nil {
		return err
	}
	log.Printf("installed %s in the rootfs at %s", guestInitService, guestInitPath)
	return nil
}

// injectImageConfig writes the image's declared user, entrypoint, command and
// environment into the rootfs for dinit to read at boot. It is canonicalised
// json so the same config always produces the same bytes, and the same rootfs
// cache key.
func injectImageConfig(root string, cfg *proto.ImageConfig) error {
	if cfg == nil {
		return nil
	}
	dst, err := imagePath(root, guestImageConfigPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// The environment can carry tokens the image was built with, so it is
	// readable only by root; dinit reads it as pid 1 before dropping privilege.
	if err := os.WriteFile(dst, imageConfigJSON(cfg), 0o600); err != nil {
		return err
	}
	log.Printf("recorded the image configuration in the rootfs at %s (user %q, %d env)",
		guestImageConfigPath, cfg.User, len(cfg.Env))
	return nil
}

// imageConfigUser is the image's user, or empty when the image declares none or
// carries no config at all.
func imageConfigUser(cfg *proto.ImageConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.User
}

func imageConfigJSON(cfg *proto.ImageConfig) []byte {
	b, err := json.Marshal(cfg)
	if err != nil {
		// ImageConfig is strings and string slices, so this cannot fail.
		return []byte("{}")
	}
	return b
}

// imageConfigRecipe keys the rootfs cache on the config, so editing an image's
// user or entrypoint rebuilds the images built from it instead of reusing a
// cached ext4 that still has the old one baked in.
func imageConfigRecipe(cfg *proto.ImageConfig) string {
	if cfg == nil {
		return ""
	}
	sum := sha256.Sum256(imageConfigJSON(cfg))
	return ";imageconfig=" + hex.EncodeToString(sum[:])
}

// withGuestInit boots the injected init and hands it the address on the
// cmdline, so a guest with no dhcp client of its own still comes up.
func withGuestInit(line string, v vm) string {
	if !v.GuestInit {
		return line
	}
	for _, f := range strings.Fields(line) {
		if strings.HasPrefix(f, "init=") {
			return line
		}
	}
	add := []string{"init=" + guestInitPath}
	if v.Net != nil {
		add = append(add, paramIP+"="+v.Net.IP, paramGateway+"="+v.Net.Gateway)
		if v.Net.DNS != "" {
			add = append(add, paramDNS+"="+v.Net.DNS)
		}
	}
	return strings.TrimSpace(line + " " + strings.Join(add, " "))
}
