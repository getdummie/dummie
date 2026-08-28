package main

import (
	"fmt"
	"log"
	"os"
)

const resolvConfPath = "/etc/resolv.conf"

func resolvConfRecipe(resolver string) string { return "resolvconf=" + resolver }

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
