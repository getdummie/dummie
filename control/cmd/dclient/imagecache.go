package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"control/internal/proto"
)

// cacheUsage maps each file in the image cache to the vms booting from it. A
// vm's rootfs is the qcow2 backing file of its own overlay and its kernel is
// read at every boot, so anything named here is load-bearing for as long as
// that vm exists.
func cacheUsage(data string) (map[string][]string, error) {
	vms, err := listVMs(data)
	if err != nil {
		return nil, err
	}
	cache := imagesDir(data)
	used := map[string][]string{}
	for _, v := range vms {
		for _, p := range []string{v.Backing, v.Kernel, v.Initrd} {
			if p == "" {
				continue
			}
			dir, name := filepath.Split(p)
			if filepath.Clean(dir) != cache {
				continue
			}
			used[name] = append(used[name], v.ID)
		}
	}
	return used, nil
}

// cacheReport lists what the image cache is holding. Partial files from a
// download or a build in flight are left out: they carry a dot prefix, are
// nobody's to remove but the writer's, and would only read as junk.
func cacheReport(data string) ([]proto.CacheEntry, error) {
	used, err := cacheUsage(data)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(imagesDir(data))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	out := make([]proto.CacheEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, proto.CacheEntry{
			Name:       e.Name(),
			Kind:       cacheKind(e.Name()),
			SizeBytes:  info.Size(),
			ModifiedAt: info.ModTime().UTC(),
			InUseBy:    used[e.Name()],
		})
	}
	return out, nil
}

func cacheKind(name string) proto.CacheKind {
	switch {
	case strings.HasSuffix(name, ".ext4"):
		return proto.CacheRootfs
	case strings.HasSuffix(name, ".tar"):
		return proto.CacheTar
	default:
		return proto.CacheDownload
	}
}

// purgeCache removes the named files. Usage is recomputed here rather than
// taken from whatever the last report said, so a file that has become a vm's
// backing store in the meantime is refused instead of pulled out from under it.
func purgeCache(data string, names []string) (proto.CachePurgeResult, error) {
	used, err := cacheUsage(data)
	if err != nil {
		return proto.CachePurgeResult{}, err
	}
	cache := imagesDir(data)

	var res proto.CachePurgeResult
	refuse := func(name, reason string) {
		res.Refused = append(res.Refused, proto.CacheRefusal{Name: name, Reason: reason})
	}
	for _, name := range names {
		if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
			refuse(name, "not a name in the image cache")
			continue
		}
		if vms := used[name]; len(vms) > 0 {
			refuse(name, "in use by "+strings.Join(vms, ", "))
			continue
		}
		p := filepath.Join(cache, name)
		info, err := os.Stat(p)
		if err != nil {
			refuse(name, "no longer here")
			continue
		}
		if info.IsDir() {
			refuse(name, "not a file")
			continue
		}
		if err := os.Remove(p); err != nil {
			refuse(name, err.Error())
			continue
		}
		res.Removed = append(res.Removed, name)
		res.FreedBytes += info.Size()
	}
	// The caller asked to change the cache, so it gets the cache back rather
	// than having to ask again for what it just altered.
	entries, err := cacheReport(data)
	if err != nil {
		return res, err
	}
	res.Entries = entries

	if len(res.Removed) == 0 && len(res.Refused) > 0 {
		return res, fmt.Errorf("nothing could be removed")
	}
	return res, nil
}
