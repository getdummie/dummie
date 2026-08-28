package main

import (
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

const maxFormFieldBytes = 64 << 10

type uploadedBlob struct {
	name        string
	description string
	fileName    string
	key         string
	size        int64
}

func (h *AdminHandler) readUploadedBlob(c *echo.Context, kind string) (*uploadedBlob, error) {
	req := c.Request()
	req.Body = http.MaxBytesReader(c.Response(), req.Body, h.blobs.maxUploadBytes+(1<<20))
	mr, err := req.MultipartReader()
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "expected a multipart form with a file")
	}

	out := &uploadedBlob{}
	fail := func(err error) (*uploadedBlob, error) {
		if out.key != "" {
			if delErr := h.blobs.Delete(req.Context(), out.key); delErr != nil {
				log.Printf("orphaned %s object %q after a failed upload: %v", kind, out.key, delErr)
			}
		}
		return nil, err
	}

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(readError(kind, err))
		}

		switch part.FormName() {
		case "name", "description":
			v, err := readFormField(part)
			if err != nil {
				part.Close()
				return fail(readError(kind, err))
			}
			if part.FormName() == "name" {
				out.name = v
			} else {
				out.description = v
			}
		case "file":
			if out.key != "" {
				part.Close()
				return fail(echo.NewHTTPError(http.StatusBadRequest, "send exactly one file"))
			}
			out.fileName = sanitizeFileName(part.FileName())
			contentType := part.Header.Get("Content-Type")
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			key := h.blobs.newKey(kind, out.fileName)
			counted := &countingReader{r: part}
			if err := h.blobs.Put(req.Context(), key, contentType, counted); err != nil {
				part.Close()
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					return fail(echo.NewHTTPError(http.StatusRequestEntityTooLarge, "that file is larger than this server accepts"))
				}
				log.Printf("could not upload %s %q: %v", kind, out.fileName, err)
				return fail(echo.NewHTTPError(http.StatusBadGateway, "could not upload the file to object storage"))
			}
			out.key = key
			out.size = counted.n
		}
		part.Close()
	}

	if out.name = strings.TrimSpace(out.name); out.name == "" {
		return fail(echo.NewHTTPError(http.StatusBadRequest, "name is required"))
	}
	if len(out.name) > 128 {
		return fail(echo.NewHTTPError(http.StatusBadRequest, "name is too long"))
	}
	if out.key == "" {
		return fail(echo.NewHTTPError(http.StatusBadRequest, "a file is required"))
	}
	if out.size <= 0 {
		return fail(echo.NewHTTPError(http.StatusBadRequest, "that file is empty"))
	}
	if out.size > h.blobs.maxUploadBytes {
		return fail(echo.NewHTTPError(http.StatusRequestEntityTooLarge, "that file is larger than this server accepts"))
	}
	out.description = strings.TrimSpace(out.description)
	return out, nil
}

func readError(kind string, err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "that file is larger than this server accepts")
	}
	log.Printf("could not read the %s upload: %v", kind, err)
	return echo.NewHTTPError(http.StatusBadRequest, "could not read the upload")
}

func readFormField(p *multipart.Part) (string, error) {
	b, err := io.ReadAll(io.LimitReader(p, maxFormFieldBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxFormFieldBytes {
		return "", errors.New("form field is too long")
	}
	return string(b), nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
