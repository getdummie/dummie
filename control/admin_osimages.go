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

// osImageDTO is one catalogue entry. Everything here except the description is
// written once: the database refuses to change the rest even if something else
// tries (see 0017_osimages).
type osImageDTO struct {
  ID          string `json:"id"`
  Name        string `json:"name"`
  Description string `json:"description"`
  FileName    string `json:"file_name"`
  SizeBytes   int64  `json:"size_bytes"`
  CreatedAt   string `json:"created_at"`
  // "" while the image is offered; the time it was withdrawn otherwise.
  SoftDeletedAt string `json:"soft_deleted_at"`
  // A presigned link, minted for this response and good for a few minutes.
  // Empty on the list, which does not mint one per row, and on a withdrawn
  // image, which is no longer handed out.
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
  // A withdrawn image keeps its object but stops being handed out; a link is
  // the one thing the page should not still offer.
  if h.blobs != nil && !o.SoftDeletedAt.Valid {
    url, err := h.blobs.PresignGet(ctx, o.ObjectKey, o.FileName)
    if err != nil {
      // The row is real and worth showing even when the link cannot be minted,
      // so this degrades to a page without a download rather than a 500.
      log.Printf("could not presign os image %s: %v", dto.ID, err)
    } else {
      dto.DownloadURL = url
    }
  }
  return c.JSON(http.StatusOK, dto)
}

// CreateOSImage takes a multipart form: "name", optional "description", and the
// image as "file". The blob goes to S3 first and the row second, so a saved row
// always has an object behind it; a row that fails to save takes its orphan
// object with it.
func (h *AdminHandler) CreateOSImage(c *echo.Context) error {
  ctx := c.Request().Context()
  if h.blobs == nil {
    return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
  }

  req := c.Request()
  // Caps the whole request, not just the file part, and fails the read rather
  // than buffering an oversized upload to disk first.
  req.Body = http.MaxBytesReader(c.Response(), req.Body, h.blobs.maxUploadBytes+(1<<20))
  // Parts above this stay on disk instead of in memory; an OS image is always
  // above it.
  if err := req.ParseMultipartForm(32 << 20); err != nil {
    var tooLarge *http.MaxBytesError
    if errors.As(err, &tooLarge) {
      return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "that file is larger than this server accepts")
    }
    return echo.NewHTTPError(http.StatusBadRequest, "expected a multipart form with a file")
  }
  defer func() {
    if req.MultipartForm != nil {
      _ = req.MultipartForm.RemoveAll()
    }
  }()

  name := strings.TrimSpace(req.FormValue("name"))
  if name == "" {
    return echo.NewHTTPError(http.StatusBadRequest, "name is required")
  }
  if len(name) > 128 {
    return echo.NewHTTPError(http.StatusBadRequest, "name is too long")
  }
  description := strings.TrimSpace(req.FormValue("description"))

  file, header, err := req.FormFile("file")
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "a file is required")
  }
  defer file.Close()
  if header.Size <= 0 {
    return echo.NewHTTPError(http.StatusBadRequest, "that file is empty")
  }
  if header.Size > h.blobs.maxUploadBytes {
    return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "that file is larger than this server accepts")
  }

  fileName := sanitizeFileName(header.Filename)
  contentType := header.Header.Get("Content-Type")
  if contentType == "" {
    contentType = "application/octet-stream"
  }
  key := h.blobs.newKey("osimages", fileName)

  if err := h.blobs.Put(ctx, key, contentType, file); err != nil {
    log.Printf("could not upload os image %q: %v", name, err)
    return echo.NewHTTPError(http.StatusBadGateway, "could not upload the file to object storage")
  }

  o, err := h.q.CreateOSImage(ctx, db.CreateOSImageParams{
    Name:        name,
    Description: description,
    ObjectKey:   key,
    FileName:    fileName,
    SizeBytes:   header.Size,
  })
  if err != nil {
    // Best effort: an object with no row is invisible to everything here, and
    // the alternative is failing the request twice over.
    if delErr := h.blobs.Delete(ctx, key); delErr != nil {
      log.Printf("orphaned os image object %q after a failed insert: %v", key, delErr)
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

// UpdateOSImageDescription changes the note on an image. Nothing else about a
// saved image can be changed, here or anywhere else.
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

// DeleteOSImage withdraws an image: the row is marked and stops being listed,
// but neither it nor the object in the bucket is removed.
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
