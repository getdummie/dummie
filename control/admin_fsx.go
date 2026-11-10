package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type adminFSxImageDTO struct {
	ID          string `json:"id"`
	Rootfs      string `json:"rootfs"`
	OSImageID   string `json:"osimage_id,omitempty"`
	OSImageName string `json:"osimage_name,omitempty"`
	SizeBytes   int64  `json:"size_bytes"`
	VolumeID    string `json:"volume_id,omitempty"`
	VolumePath  string `json:"volume_path,omitempty"`
	SnapshotID  string `json:"snapshot_id,omitempty"`
	Status      string `json:"status"`
	Claimed     bool   `json:"claimed"`
	DiskCount   int64  `json:"disk_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type adminFSxDiskDTO struct {
	ID          string `json:"id"`
	VMID        string `json:"vm_id,omitempty"`
	VMName      string `json:"vm_name,omitempty"`
	VMStatus    string `json:"vm_status,omitempty"`
	ImageID     string `json:"image_id"`
	ImageRootfs string `json:"image_rootfs"`
	VolumeID    string `json:"volume_id"`
	VolumePath  string `json:"volume_path"`
	Released    bool   `json:"released"`
	InUse       bool   `json:"in_use"`
	CreatedAt   string `json:"created_at"`
}

func (h *AdminHandler) ListFSxImages(c *echo.Context) error {
	rows, err := h.q.ListFSxImagesAdmin(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list the fsx images")
	}
	items := make([]adminFSxImageDTO, 0, len(rows))
	for _, r := range rows {
		items = append(items, adminFSxImageDTO{
			ID:          uuid.UUID(r.ID.Bytes).String(),
			Rootfs:      r.Rootfs,
			OSImageID:   domainIDString(r.OSImageID),
			OSImageName: r.OsimageName,
			SizeBytes:   r.SizeBytes,
			VolumeID:    r.VolumeID,
			VolumePath:  r.VolumePath,
			SnapshotID:  r.SnapshotID,
			Status:      r.Status,
			Claimed:     r.ClaimedBy.Valid,
			DiskCount:   r.DiskCount,
			CreatedAt:   r.CreatedAt.Time.Format(time.RFC3339),
			UpdatedAt:   r.UpdatedAt.Time.Format(time.RFC3339),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (h *AdminHandler) ListFSxDisks(c *echo.Context) error {
	rows, err := h.q.ListFSxDisksAdmin(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list the fsx disks")
	}
	items := make([]adminFSxDiskDTO, 0, len(rows))
	for _, r := range rows {
		items = append(items, adminFSxDiskDTO{
			ID:          uuid.UUID(r.ID.Bytes).String(),
			VMID:        domainIDString(r.Vm),
			VMName:      r.VMName,
			VMStatus:    r.VmStatus,
			ImageID:     domainIDString(r.Image),
			ImageRootfs: r.ImageRootfs,
			VolumeID:    r.VolumeID,
			VolumePath:  r.VolumePath,
			Released:    r.Released,
			InUse:       r.InUse,
			CreatedAt:   r.CreatedAt.Time.Format(time.RFC3339),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// DeleteFSxImage removes a golden image's volume, snapshot included, and its row.
// One that disks are still cloned from is refused, as FSx would refuse it too.
func (h *AdminHandler) DeleteFSxImage(c *echo.Context) error {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid fsx image id")
	}
	ctx := c.Request().Context()
	img, err := h.q.GetFSxImage(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "fsx image not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the fsx image")
	}
	n, err := h.q.CountFSxDisksForImage(ctx, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count the image's disks")
	}
	if n > 0 {
		return echo.NewHTTPError(http.StatusConflict, fmt.Sprintf("%d disk(s) are cloned from this image; delete them first", n))
	}
	if err := deleteFSxVolume(ctx, h.q, img.VolumeID); err != nil {
		return err
	}
	if err := h.q.DeleteFSxImage(ctx, id); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete the fsx image")
	}
	return c.NoContent(http.StatusNoContent)
}

// DeleteFSxDisk removes a disk whose vm is gone. A live vm's disk goes with
// the vm, so deleting it here is refused.
func (h *AdminHandler) DeleteFSxDisk(c *echo.Context) error {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid fsx disk id")
	}
	ctx := c.Request().Context()
	disk, err := h.q.GetFSxDisk(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "fsx disk not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the fsx disk")
	}
	if disk.Vm.Valid && !disk.Released {
		vm, err := h.q.GetVM(ctx, disk.Vm)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not read the disk's vm")
		}
		if err == nil && (vm.DiskBackend == diskBackendFSx || vm.DiskMoving) {
			return echo.NewHTTPError(http.StatusConflict, "this disk still belongs to a vm; delete the vm instead")
		}
	}
	if err := deleteFSxVolume(ctx, h.q, disk.VolumeID); err != nil {
		return err
	}
	if err := h.q.DeleteFSxDisk(ctx, id); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete the fsx disk")
	}
	return c.NoContent(http.StatusNoContent)
}

func deleteFSxVolume(ctx context.Context, q *db.Queries, volumeID string) error {
	if volumeID == "" {
		return nil
	}
	store, err := openFSx(ctx, q)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	if err := store.deleteVolume(ctx, volumeID); err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	return nil
}
