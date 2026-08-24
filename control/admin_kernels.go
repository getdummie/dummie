package main

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// kernelDTO is one catalogue entry. Everything here except the description is
// written once: the database refuses to change the rest even if something else
// tries (see 0015_kernels and 0016_kernel_description_editable).
type kernelDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FileName    string `json:"file_name"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
	// "" while the kernel is offered; the time it was withdrawn otherwise.
	SoftDeletedAt string `json:"soft_deleted_at"`
	// A presigned link, minted for this response and good for a few minutes.
	// Empty on the list, which does not mint one per row, and on a withdrawn
	// kernel, which is no longer handed out.
	DownloadURL string `json:"download_url,omitempty"`
}

func toKernelDTO(k db.Kernel) kernelDTO {
	d := kernelDTO{
		ID:          uuid.UUID(k.ID.Bytes).String(),
		Name:        k.Name,
		Description: k.Description,
		FileName:    k.FileName,
		SizeBytes:   k.SizeBytes,
		CreatedAt:   k.CreatedAt.Time.Format(time.RFC3339),
	}
	if k.SoftDeletedAt.Valid {
		d.SoftDeletedAt = k.SoftDeletedAt.Time.Format(time.RFC3339)
	}
	return d
}

func (h *AdminHandler) ListKernels(c *echo.Context) error {
	ctx := c.Request().Context()
	limit, offset := pageParams(c)
	total, err := h.q.CountKernels(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count kernels")
	}
	rows, err := h.q.ListKernels(ctx, db.ListKernelsParams{Limit: limit, Offset: offset})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list kernels")
	}
	items := make([]kernelDTO, 0, len(rows))
	for _, k := range rows {
		items = append(items, toKernelDTO(k))
	}
	return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

func (h *AdminHandler) GetKernel(c *echo.Context) error {
	ctx := c.Request().Context()
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid kernel id")
	}
	k, err := h.q.GetKernel(ctx, pgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "kernel not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load kernel")
	}

	dto := toKernelDTO(k)
	// A withdrawn kernel keeps its object but stops being handed out; a link is
	// the one thing the page should not still offer.
	if h.blobs != nil && !k.SoftDeletedAt.Valid {
		url, err := h.blobs.PresignGet(ctx, k.ObjectKey, k.FileName)
		if err != nil {
			// The row is real and worth showing even when the link cannot be minted,
			// so this degrades to a page without a download rather than a 500.
			log.Printf("could not presign kernel %s: %v", dto.ID, err)
		} else {
			dto.DownloadURL = url
		}
	}
	return c.JSON(http.StatusOK, dto)
}

// CreateKernel takes a multipart form: "name", optional "description", and the
// image as "file". The blob goes to S3 first and the row second, so a saved row
// always has an object behind it; a row that fails to save takes its orphan
// object with it.
func (h *AdminHandler) CreateKernel(c *echo.Context) error {
	ctx := c.Request().Context()
	if h.blobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
	}

	up, err := h.readUploadedBlob(c, "kernels")
	if err != nil {
		return err
	}

	k, err := h.q.CreateKernel(ctx, db.CreateKernelParams{
		Name:        up.name,
		Description: up.description,
		ObjectKey:   up.key,
		FileName:    up.fileName,
		SizeBytes:   up.size,
	})
	if err != nil {
		// Best effort: an object with no row is invisible to everything here, and
		// the alternative is failing the request twice over.
		if delErr := h.blobs.Delete(ctx, up.key); delErr != nil {
			log.Printf("orphaned kernel object %q after a failed insert: %v", up.key, delErr)
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "a kernel with that name already exists")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the kernel")
	}
	return c.JSON(http.StatusCreated, toKernelDTO(k))
}

type updateKernelDescriptionReq struct {
	Description string `json:"description"`
}

// UpdateKernelDescription changes the note on a kernel. Nothing else about a
// saved kernel can be changed, here or anywhere else.
func (h *AdminHandler) UpdateKernelDescription(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid kernel id")
	}
	var req updateKernelDescriptionReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	k, err := h.q.UpdateKernelDescription(c.Request().Context(), db.UpdateKernelDescriptionParams{
		ID:          pgID,
		Description: strings.TrimSpace(req.Description),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "kernel not found, or withdrawn")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the description")
	}
	return c.JSON(http.StatusOK, toKernelDTO(k))
}

// DeleteKernel withdraws a kernel: the row is marked and stops being listed,
// but neither it nor the object in the bucket is removed.
func (h *AdminHandler) DeleteKernel(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid kernel id")
	}
	k, err := h.q.SoftDeleteKernel(c.Request().Context(), pgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "kernel not found, or already withdrawn")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not withdraw the kernel")
	}
	return c.JSON(http.StatusOK, toKernelDTO(k))
}
