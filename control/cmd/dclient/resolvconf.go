package main

import (
	"fmt"
	"log"
	"os"
)

// A guest has exactly one resolver it may talk to: the filtering one on the
// gateway. dhcp hands that address out, but a lease only becomes a resolver
// configuration if something in the image turns it into one, and plenty of
// images have nothing that does.
//
// An exported container is the common case. docker bind-mounts /etc/resolv.conf
// over the build, so the file captured in the tar is whatever was underneath it
// -- almost always zero bytes. glibc reads an empty resolv.conf as 127.0.0.1:53
// and nothing listens there, so every lookup fails inside the guest without a
// packet ever reaching the tap. Debian images happen to survive it because their
// nsswitch has systemd-resolved's `resolve` module ahead of `dns`; Ubuntu's does
// not, and there the same tar cannot resolve anything.
//
// That failure is the one worth fixing here rather than in any image, because it
// is invisible: no query reaches the resolver, so nothing is refused, so the
// VM's denied list stays empty while the guest prints "Temporary failure
// resolving" and the owner has nothing to look at.
//
// Written in the same window authorized_keys is -- after the tar is extracted,
// before mkfs -- and shared by every VM built from that tar, which is correct
// because the gateway is a property of the host and not of a VM.
const resolvConfPath = "/etc/resolv.conf"

// resolvConfRecipe is folded into a built image's cache identity, so an image
// built against a gateway this host no longer uses is a different image rather
// than a stale hit.
func resolvConfRecipe(resolver string) string { return "resolvconf=" + resolver }

// ensureResolvConf points the image at the address guests are allowed to query.
//
// A symlink is left exactly as it is. That is what an image whose resolv.conf is
// managed by systemd-resolved, NetworkManager or resolvconf looks like, and all
// of them learn the resolver from the same dhcp lease; replacing the link with a
// file would take the file away from its manager and freeze at build time an
// answer they keep current.
func ensureResolvConf(root, resolver string) error {
	if resolver == "" {
		return nil
	}
	p, err := imagePath(root, resolvConfPath)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(p)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		log.Printf("%s is a symlink in this image, so its own resolver configuration is left alone", resolvConfPath)
		return nil
	case err != nil && !os.IsNotExist(err):
		return err
	}
	body := fmt.Sprintf("# Written by dclient. This is the only resolver a guest may query.\nnameserver %s\n", resolver)
	return os.WriteFile(p, []byte(body), 0o644)
}
