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

type osImageDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FileName    string `json:"file_name"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
	SoftDeletedAt string `json:"soft_deleted_at"`
	DownloadURL string `json:"download_url,omitempty"`
}

func toOSImageDTO(o db.Osimage) osImageDTO {
	d := osImageDTO{
		ID:          uuid.UUID(o.ID.Bytes).String(),
		Name:        o.Name,
		Description: o.Description,
		FileName:    o.FileName,
		SizeBytes:   o.SizeBytes,
		CreatedAt:   o.CreatedAt.Time.Format(time.RFC3339),
	}
	if o.SoftDeletedAt.Valid {
		d.SoftDeletedAt = o.SoftDeletedAt.Time.Format(time.RFC3339)
	}
	return d
}

func (h *AdminHandler) ListOSImages(c *echo.Context) error {
	ctx := c.Request().Context()
	limit, offset := pageParams(c)
	total, err := h.q.CountOSImages(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count os images")
	}
	rows, err := h.q.ListOSImages(ctx, db.ListOSImagesParams{Limit: limit, Offset: offset})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list os images")
	}
	items := make([]osImageDTO, 0, len(rows))
	for _, o := range rows {
		items = append(items, toOSImageDTO(o))
	}
	return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

func (h *AdminHandler) GetOSImage(c *echo.Context) error {
	ctx := c.Request().Context()
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid os image id")
	}
	o, err := h.q.GetOSImage(ctx, pgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "os image not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load os image")
	}

	dto := toOSImageDTO(o)
	if h.blobs != nil && !o.SoftDeletedAt.Valid {
		url, err := h.blobs.PresignGet(ctx, o.ObjectKey, o.FileName)
		if err != nil {
			log.Printf("could not presign os image %s: %v", dto.ID, err)
		} else {
			dto.DownloadURL = url
		}
	}
	return c.JSON(http.StatusOK, dto)
}

func (h *AdminHandler) CreateOSImage(c *echo.Context) error {
	ctx := c.Request().Context()
	if h.blobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
	}

	up, err := h.readUploadedBlob(c, "osimages")
	if err != nil {
		return err
	}

	o, err := h.q.CreateOSImage(ctx, db.CreateOSImageParams{
		Name:        up.name,
		Description: up.description,
		ObjectKey:   up.key,
		FileName:    up.fileName,
		SizeBytes:   up.size,
	})
	if err != nil {
		if delErr := h.blobs.Delete(ctx, up.key); delErr != nil {
			log.Printf("orphaned os image object %q after a failed insert: %v", up.key, delErr)
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "an os image with that name already exists")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the os image")
	}
	return c.JSON(http.StatusCreated, toOSImageDTO(o))
}

type updateOSImageDescriptionReq struct {
	Description string `json:"description"`
}

func (h *AdminHandler) UpdateOSImageDescription(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid os image id")
	}
	var req updateOSImageDescriptionReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	o, err := h.q.UpdateOSImageDescription(c.Request().Context(), db.UpdateOSImageDescriptionParams{
		ID:          pgID,
		Description: strings.TrimSpace(req.Description),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "os image not found, or withdrawn")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the description")
	}
	return c.JSON(http.StatusOK, toOSImageDTO(o))
}

func (h *AdminHandler) DeleteOSImage(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid os image id")
	}
	o, err := h.q.SoftDeleteOSImage(c.Request().Context(), pgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "os image not found, or already withdrawn")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not withdraw the os image")
	}
	return c.JSON(http.StatusOK, toOSImageDTO(o))
}
