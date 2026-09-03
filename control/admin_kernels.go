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

type kernelDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FileName    string `json:"file_name"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
	SoftDeletedAt string `json:"soft_deleted_at"`
	ObjectKey   string `json:"object_key"`
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
		ObjectKey:   k.ObjectKey,
	}
	if k.SoftDeletedAt.Valid {
		d.SoftDeletedAt = k.SoftDeletedAt.Time.Format(time.RFC3339)
	}
	return d
}

func (h *AdminHandler) ListKernels(c *echo.Context) error {
	ctx := c.Request().Context()
	limit, offset := pageParams(c)

	var (
		total int64
		rows  []db.Kernel
		err   error
	)
	if withdrawnParam(c) {
		if total, err = h.q.CountWithdrawnKernels(ctx); err == nil {
			rows, err = h.q.ListWithdrawnKernels(ctx, db.ListWithdrawnKernelsParams{Limit: limit, Offset: offset})
		}
	} else {
		if total, err = h.q.CountKernels(ctx); err == nil {
			rows, err = h.q.ListKernels(ctx, db.ListKernelsParams{Limit: limit, Offset: offset})
		}
	}
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
	if h.blobs != nil && !k.SoftDeletedAt.Valid {
		url, err := h.blobs.PresignGet(ctx, k.ObjectKey, k.FileName)
		if err != nil {
			log.Printf("could not presign kernel %s: %v", dto.ID, err)
		} else {
			dto.DownloadURL = url
		}
	}
	return c.JSON(http.StatusOK, dto)
}

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

// PurgeKernel is the second half of a delete: withdrawing takes a kernel out of
// the catalogue but leaves the row and the object, and this drops both. No vm
// can be hurt by it -- a host downloads its kernel once, from a link minted at
// create time, and keeps its own copy from then on.
func (h *AdminHandler) PurgeKernel(c *echo.Context) error {
	ctx := c.Request().Context()
	if h.blobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
	}
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
	if !k.SoftDeletedAt.Valid {
		return echo.NewHTTPError(http.StatusConflict, "withdraw the kernel before purging it")
	}

	// The object goes first: a failure here leaves the row in place so the purge
	// can be retried, where the reverse would lose track of the object for good.
	if err := h.blobs.Delete(ctx, k.ObjectKey); err != nil {
		log.Printf("could not delete kernel object %q: %v", k.ObjectKey, err)
		return echo.NewHTTPError(http.StatusBadGateway, "could not delete the kernel from object storage")
	}
	if _, err := h.q.PurgeKernel(ctx, pgID); err != nil {
		log.Printf("kernel object %q is gone but its row could not be deleted: %v", k.ObjectKey, err)
		return echo.NewHTTPError(http.StatusInternalServerError, "the object was deleted but the record could not be")
	}
	return c.NoContent(http.StatusNoContent)
}
