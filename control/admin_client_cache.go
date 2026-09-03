package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"control/internal/db"
	"control/internal/proto"
)

// cacheJobWait is how long an admin request holds open for a host to answer.
// Reading the cache is a ReadDir and a handful of stats, and a purge is a few
// unlinks, so a host that has not answered by now is not going to.
const cacheJobWait = 8 * time.Second

type cacheEntryDTO struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	SizeBytes  int64  `json:"size_bytes"`
	ModifiedAt string `json:"modified_at"`

	// InUseBy names the vms booting from this file, as the host reported them.
	// A file with none is the only kind that can be purged.
	InUseBy []string `json:"in_use_by"`

	// Artifact is the catalogue entry a downloaded file came from, worked out
	// from the object it would have been fetched from. Empty when nothing in the
	// catalogue matches, which is what a leftover from a deleted or re-pointed
	// artifact looks like.
	Artifact          string `json:"artifact"`
	ArtifactKind      string `json:"artifact_kind"`
	ArtifactWithdrawn bool   `json:"artifact_withdrawn"`
}

// artifactLabel is what a download cache key resolves to.
type artifactLabel struct {
	kind      string
	name      string
	withdrawn bool
}

// downloadCacheLabels maps the cache-key prefix a host would file each
// catalogue artifact under to that artifact. Presigning is a local signature,
// so building this for the whole catalogue costs nothing but cpu.
func (h *AdminHandler) downloadCacheLabels(ctx context.Context) map[string]artifactLabel {
	labels := map[string]artifactLabel{}
	if h.blobs == nil {
		return labels
	}

	add := func(kind, name, objectKey, fileName string, withdrawn bool) {
		key, err := h.blobs.DownloadCacheKey(ctx, objectKey, fileName)
		if err != nil {
			log.Printf("could not derive the cache key for %s %s: %v", kind, name, err)
			return
		}
		labels[key] = artifactLabel{kind: kind, name: name, withdrawn: withdrawn}
	}

	if rows, err := h.q.ListKernelObjects(ctx); err != nil {
		log.Printf("could not list kernel objects: %v", err)
	} else {
		for _, k := range rows {
			add("kernel", k.Name, k.ObjectKey, k.FileName, k.SoftDeletedAt.Valid)
		}
	}
	if rows, err := h.q.ListOSImageObjects(ctx); err != nil {
		log.Printf("could not list os image objects: %v", err)
	} else {
		for _, o := range rows {
			add("osimage", o.Name, o.ObjectKey, o.FileName, o.SoftDeletedAt.Valid)
		}
	}
	return labels
}

// vmNamesOnClient maps the short ids a host uses for its vms to the names the
// console knows them by. Only for display: whether a file is in use is decided
// by the host, not by this map, so a name missing from it costs nothing.
func (h *AdminHandler) vmNamesOnClient(ctx context.Context, clientID string) map[string]string {
	names := map[string]string{}
	pgID, err := parseUUID(clientID)
	if err != nil {
		return names
	}
	rows, err := h.q.ListVMsByClient(ctx, db.ListVMsByClientParams{
		ClientID: pgID, Limit: 500, Offset: 0,
	})
	if err != nil {
		log.Printf("could not list vms for the cache report: %v", err)
		return names
	}
	for _, v := range rows {
		if v.VMID != "" && v.Name != "" {
			names[v.VMID] = v.Name
		}
	}
	return names
}

// cacheResponse turns what a host said into what the console renders, naming
// the artifact behind each downloaded file and the vms holding each rootfs.
func (h *AdminHandler) cacheResponse(ctx context.Context, clientID string, res proto.CachePurgeResult) map[string]any {
	labels := h.downloadCacheLabels(ctx)
	vmNames := h.vmNamesOnClient(ctx, clientID)

	items := make([]cacheEntryDTO, 0, len(res.Entries))
	var total, reclaimable int64
	for _, e := range res.Entries {
		d := cacheEntryDTO{
			Name:      e.Name,
			Kind:      string(e.Kind),
			SizeBytes: e.SizeBytes,
			InUseBy:   make([]string, 0, len(e.InUseBy)),
		}
		if !e.ModifiedAt.IsZero() {
			d.ModifiedAt = e.ModifiedAt.Format(time.RFC3339)
		}
		for _, id := range e.InUseBy {
			if name := vmNames[id]; name != "" {
				d.InUseBy = append(d.InUseBy, name)
			} else {
				d.InUseBy = append(d.InUseBy, id)
			}
		}
		if l, found := labels[downloadKeyOf(e.Name)]; found {
			d.Artifact = l.name
			d.ArtifactKind = l.kind
			d.ArtifactWithdrawn = l.withdrawn
		}
		total += e.SizeBytes
		if len(e.InUseBy) == 0 {
			reclaimable += e.SizeBytes
		}
		items = append(items, d)
	}

	return map[string]any{
		"items":             items,
		"total_bytes":       total,
		"reclaimable_bytes": reclaimable,
		"reported_at":       time.Now().UTC().Format(time.RFC3339),
	}
}

// downloadKeyOf returns the "url-<hash>" a downloaded file is named after, or
// "" for a built rootfs, whose name is a digest of its contents and recipe
// rather than of where it came from.
func downloadKeyOf(name string) string {
	if !strings.HasPrefix(name, "url-") {
		return ""
	}
	hash, _, ok := strings.Cut(strings.TrimPrefix(name, "url-"), "-")
	if !ok {
		return ""
	}
	return "url-" + hash
}

func (h *AdminHandler) GetClientImageCache(c *echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if _, err := parseUUID(id); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	if !h.hub.Connected(id) {
		return echo.NewHTTPError(http.StatusConflict, "this host is not connected, so its image cache cannot be read")
	}

	res, err := h.hub.Ask(ctx, id, proto.Job{Kind: proto.KindCacheReport}, cacheJobWait)
	if err != nil {
		log.Printf("client %s: could not read the image cache: %v", id, err)
		return echo.NewHTTPError(http.StatusBadGateway, "the host did not report its image cache")
	}
	if !res.OK || res.Cache == nil {
		msg := res.Error
		if msg == "" {
			msg = "the host could not read its image cache"
		}
		return echo.NewHTTPError(http.StatusBadGateway, msg)
	}
	return c.JSON(http.StatusOK, h.cacheResponse(ctx, id, *res.Cache))
}

type purgeImageCacheReq struct {
	Names []string `json:"names"`
}

func (h *AdminHandler) PurgeClientImageCache(c *echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if _, err := parseUUID(id); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	var req purgeImageCacheReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	names := make([]string, 0, len(req.Names))
	for _, n := range req.Names {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "name at least one file to purge")
	}
	if !h.hub.Connected(id) {
		return echo.NewHTTPError(http.StatusConflict, "this host is not connected, so its cache cannot be purged right now")
	}

	// No check here on what is in use: the host is the only place that knows,
	// and it checks again itself before removing anything. Whatever it refuses
	// comes back in the result for the console to show.
	res, err := h.hub.Ask(ctx, id, proto.Job{
		Kind:  proto.KindCachePurge,
		Cache: &proto.CachePurge{Names: names},
	}, cacheJobWait)
	if err != nil {
		log.Printf("client %s: could not purge the image cache: %v", id, err)
		return echo.NewHTTPError(http.StatusBadGateway, "the host did not answer the purge; nothing may have been removed")
	}
	if res.Cache == nil {
		msg := res.Error
		if msg == "" {
			msg = "the host could not purge its image cache"
		}
		return echo.NewHTTPError(http.StatusBadGateway, msg)
	}

	log.Printf("client %s: purged %d file(s) from the image cache, freeing %d MiB",
		id, len(res.Cache.Removed), res.Cache.FreedBytes>>20)

	out := h.cacheResponse(ctx, id, *res.Cache)
	out["removed"] = emptySlice(res.Cache.Removed)
	out["freed_bytes"] = res.Cache.FreedBytes
	out["refused"] = emptySlice(res.Cache.Refused)
	return c.JSON(http.StatusOK, out)
}
