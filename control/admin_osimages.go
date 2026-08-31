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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type osImageConfigDTO struct {
	User         string   `json:"user"`
	Entrypoint   []string `json:"entrypoint"`
	Cmd          []string `json:"cmd"`
	Env          []string `json:"env"`
	ExposedPorts []int32  `json:"exposed_ports"`
}

type osImageDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FileName    string `json:"file_name"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
	SoftDeletedAt string `json:"soft_deleted_at"`
	DownloadURL string `json:"download_url,omitempty"`

	Source       string `json:"source"`
	OCIRef       string `json:"oci_ref"`
	OCIDigest    string `json:"oci_digest"`
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail"`
	DefaultPort  int32  `json:"default_port"`

	Config osImageConfigDTO `json:"config"`
}

func toOSImageDTO(o db.Osimage) osImageDTO {
	d := osImageDTO{
		ID:          uuid.UUID(o.ID.Bytes).String(),
		Name:        o.Name,
		Description: o.Description,
		FileName:    o.FileName,
		SizeBytes:   o.SizeBytes,
		CreatedAt:   o.CreatedAt.Time.Format(time.RFC3339),

		Source:       o.Source,
		OCIRef:       o.OCIRef,
		OCIDigest:    o.OCIDigest,
		Status:       o.Status,
		StatusDetail: o.StatusDetail,

		Config: osImageConfigDTO{
			User:         o.ConfigUser,
			Entrypoint:   emptySlice(o.ConfigEntrypoint),
			Cmd:          emptySlice(o.ConfigCmd),
			Env:          emptySlice(o.ConfigEnv),
			ExposedPorts: emptySlice(o.ConfigExposedPorts),
		},
	}
	if o.DefaultPort.Valid {
		d.DefaultPort = o.DefaultPort.Int32
	}
	if o.SoftDeletedAt.Valid {
		d.SoftDeletedAt = o.SoftDeletedAt.Time.Format(time.RFC3339)
	}
	return d
}

// emptySlice keeps a null out of the json for a column that is semantically a
// list; the console renders these directly.
func emptySlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
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
	if h.blobs != nil && !o.SoftDeletedAt.Valid && o.Status == osImageReady {
		url, err := h.blobs.PresignGet(ctx, o.ObjectKey, o.FileName)
		if err != nil {
			log.Printf("could not presign os image %s: %v", dto.ID, err)
		} else {
			dto.DownloadURL = url
		}
	}
	return c.JSON(http.StatusOK, dto)
}

const (
	osImagePending  = "pending"
	osImageBuilding = "building"
	osImageReady    = "ready"
	osImageFailed   = "failed"
)

type createOCIOSImageReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	OCIRef      string `json:"oci_ref"`
}

// CreateOSImage takes either a rootfs tar as multipart -- which is ready the
// moment it lands -- or a container image reference as json, which is queued for
// the build task to pull and flatten.
func (h *AdminHandler) CreateOSImage(c *echo.Context) error {
	if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "application/json") {
		return h.createOCIOSImage(c)
	}
	return h.createUploadedOSImage(c)
}

func (h *AdminHandler) createOCIOSImage(c *echo.Context) error {
	ctx := c.Request().Context()
	if h.blobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
	}

	var req createOCIOSImageReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.OCIRef = strings.TrimSpace(req.OCIRef)
	if req.Name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name is required")
	}
	if len(req.Name) > 128 {
		return echo.NewHTTPError(http.StatusBadRequest, "name is too long")
	}
	if req.OCIRef == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "oci_ref is required")
	}
	if err := validateOCIRef(req.OCIRef); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	o, err := h.q.CreateOCIOSImage(ctx, db.CreateOCIOSImageParams{
		Name:        req.Name,
		Description: strings.TrimSpace(req.Description),
		OCIRef:      req.OCIRef,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "an os image with that name already exists")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the os image")
	}

	if _, err := scheduleTask(ctx, h.q, scheduleTaskParams{
		Kind:        taskOSImageBuild,
		SubjectKind: subjectOSImage,
		SubjectID:   o.ID,
		Reason:      "pull " + req.OCIRef + " and flatten it into a rootfs",
		MaxAttempts: osImageBuildAttempts,
	}); err != nil {
		log.Printf("could not queue the build for os image %s: %v", req.Name, err)
		if err := h.q.FailOSImageBuild(ctx, db.FailOSImageBuildParams{
			ID: o.ID, StatusDetail: "the build could not be queued",
		}); err != nil {
			log.Printf("could not mark os image %s as failed: %v", req.Name, err)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not queue the image build")
	}

	return c.JSON(http.StatusCreated, toOSImageDTO(o))
}

func (h *AdminHandler) createUploadedOSImage(c *echo.Context) error {
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

type updateOSImageReq struct {
	Description string `json:"description"`
	DefaultPort *int32 `json:"default_port"`
}

func (h *AdminHandler) UpdateOSImage(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid os image id")
	}
	var req updateOSImageReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	var port pgtype.Int4
	if req.DefaultPort != nil {
		if *req.DefaultPort < 1 || *req.DefaultPort > 65535 {
			return echo.NewHTTPError(http.StatusBadRequest, "default_port must be between 1 and 65535")
		}
		port = pgtype.Int4{Int32: *req.DefaultPort, Valid: true}
	}
	o, err := h.q.UpdateOSImage(c.Request().Context(), db.UpdateOSImageParams{
		ID:          pgID,
		Description: strings.TrimSpace(req.Description),
		DefaultPort: port,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "os image not found, or withdrawn")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the os image")
	}
	return c.JSON(http.StatusOK, toOSImageDTO(o))
}

func (h *AdminHandler) DeleteOSImage(c *echo.Context) error {
	ctx := c.Request().Context()
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid os image id")
	}
	o, err := h.q.SoftDeleteOSImage(ctx, pgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "os image not found, or already withdrawn")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not withdraw the os image")
	}
	cancelTasksForSubject(ctx, h.q, subjectOSImage, pgID, "the os image was withdrawn")
	return c.JSON(http.StatusOK, toOSImageDTO(o))
}
