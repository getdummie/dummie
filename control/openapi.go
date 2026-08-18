package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"

	"github.com/labstack/echo/v5"
)

// docsFS carries whatever swag last generated. The directory is embedded rather
// than read from disk so the binary is the whole deployment, the way it already
// is for everything except the migrations -- and those are read at runtime
// because an operator runs them, not the server.
//
//go:embed docs
var docsFS embed.FS

// specCandidates is the file swag wrote the spec to, most specific first.
// swag has named its output differently across versions, and rather than pin
// the build to a guess about the generator, this resolves it once at startup.
// placeholder.json is committed so `go build` works in a tree where swag has
// never run; it is only ever served when neither generated file is there.
var specCandidates = []string{
	"docs/openapi.json",
	"docs/swagger.json",
	"docs/placeholder.json",
}

// loadOpenAPISpec reads the spec out of the embedded directory at startup. Read
// once: it cannot change while the process runs, and re-reading it per request
// would be work done to reach the same bytes.
func loadOpenAPISpec() []byte {
	for _, name := range specCandidates {
		b, err := fs.ReadFile(docsFS, name)
		if err == nil {
			return b
		}
	}
	// Unreachable while placeholder.json is committed, which is the point of
	// committing it -- but a serve that quietly returned nothing would be worse
	// than one that says the file went missing.
	log.Print("no openapi spec found in the embedded docs directory; /api/v1/openapi.json will 404")
	return nil
}

// apiError is the body Echo's error handler writes for every failure on these
// routes. Declared so the spec can name one shape instead of describing the
// same two fields at forty call sites.
type apiError struct {
	Message string `json:"message"`
}

// The envelopes below exist for the generator, not for the server. Their
// handlers build the same shape from a map -- pageEnvelope and the various
// {"items": ...} literals -- and a shape assembled at runtime is one swag
// cannot read. Keeping them next to each other, rather than beside the handlers
// they describe, is what makes it obvious they are documentation.

// sessionResp is what sign-up, sign-in and refresh return in the body. They
// also set the httpOnly cookies, which is what a browser actually uses; the
// access token is in the body for a client that holds it in memory instead.
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
	// Available is false when nothing is collecting, which is a different fact
	// from an empty list and has to be readable as one.
	Available bool          `json:"available"`
	Recording deniedSources `json:"recording"`
}

type resolvedHost struct {
	Host      string   `json:"host"`
	Addresses []string `json:"addresses"`
	// Truncated is true when the name answered with more addresses than are
	// worth recording; see resolveMaxAddresses.
	Truncated bool `json:"truncated"`
	// Error is set, with a 200, when the name did not resolve. That is an answer
	// about the name rather than a failure of this server.
	Error string `json:"error,omitempty"`
}

// createdTokenResp is the one response in the API that carries a secret. It is
// returned exactly once, by POST /me/tokens, and nothing stores the raw value.
type createdTokenResp struct {
	Token               string `json:"token"`
	PersonalAccessToken patDTO `json:"personal_access_token"`
}

// openAPISpec serves the generated document Scalar renders.
//
// Public, like the /docs page that reads it: every route it describes enforces
// its own auth, and an installation that already serves a sign-up page is not
// keeping the existence of its API secret.
//
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
