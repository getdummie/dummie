package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"

	"github.com/labstack/echo/v5"
)

//go:embed docs
var docsFS embed.FS

var specCandidates = []string{
	"docs/openapi.json",
	"docs/swagger.json",
	"docs/placeholder.json",
}

func loadOpenAPISpec() []byte {
	for _, name := range specCandidates {
		b, err := fs.ReadFile(docsFS, name)
		if err == nil {
			return b
		}
	}
	log.Print("no openapi spec found in the embedded docs directory; /api/v1/openapi.json will 404")
	return nil
}

type apiError struct {
	Message string `json:"message"`
}

type sessionResp struct {
	AccessToken string  `json:"access_token"`
	User        userDTO `json:"user"`
}

type pagedVMs struct {
	Items  []vmDTO `json:"items"`
	Total  int64   `json:"total"`
	Limit  int32   `json:"limit"`
	Offset int32   `json:"offset"`
}

type tokenList struct {
	Items []patDTO `json:"items"`
}

type hostList struct {
	Items []hostDTO `json:"items"`
}

type artifactList struct {
	Items []userArtifactDTO `json:"items"`
}

type targetList struct {
	Items []vmTargetDTO `json:"items"`
}

type deniedEgressList struct {
	Items []deniedAttemptDTO `json:"items"`
	Available bool          `json:"available"`
	Recording deniedSources `json:"recording"`
}

type resolvedHost struct {
	Host      string   `json:"host"`
	Addresses []string `json:"addresses"`
	Truncated bool `json:"truncated"`
	Error string `json:"error,omitempty"`
}

type createdTokenResp struct {
	Token               string `json:"token"`
	PersonalAccessToken patDTO `json:"personal_access_token"`
}

// @Tags     docs
// @Produce  json
// @Success  200 {string} string "the OpenAPI document this page is rendered from"
// @Failure  404 {object} apiError "no spec was built into this binary"
// @Router   /openapi.json [get]
func openAPISpec(spec []byte) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if spec == nil {
			return echo.NewHTTPError(http.StatusNotFound, "no api documentation was built into this binary")
		}
		return c.Blob(http.StatusOK, "application/json", spec)
	}
}
