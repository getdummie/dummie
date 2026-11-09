package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
	"control/internal/proto"
)

const (
	integrationKindHTTP = "http"

	// userProxyFamily is the intproxy integration answering every
	// <name>-u.int.<tld>; the suffix is what keeps them clear of the built-ins.
	userProxyFamily = "u"
	userProxySuffix = "-" + userProxyFamily
)

var httpProxyMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

const (
	maxProxyHeaders     = 32
	maxProxyHeaderValue = 8 << 10
)

var headerNameRe = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")

// forbiddenProxyHeaders would change how the request is framed or routed
// rather than what it says.
var forbiddenProxyHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true,
	"Keep-Alive": true, "Upgrade": true, "Te": true, "Trailer": true,
	"Proxy-Connection": true, "Proxy-Authorization": true, "Proxy-Authenticate": true,
}

// httpProxyHeader with a nil value keeps the stored one, which is how a
// write-only value survives an edit.
type httpProxyHeader struct {
	Name  string  `json:"name"`
	Value *string `json:"value,omitempty"`
}

type httpProxyReq struct {
	TargetURL string            `json:"target_url"`
	Methods   []string          `json:"methods"`
	VerifyTLS *bool             `json:"verify_tls"`
	Headers   []httpProxyHeader `json:"headers"`
}

type storedHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type httpProxySpec struct {
	target    string
	methods   []string
	verifyTLS bool
	headers   []storedHeader
}

func normalizeProxyTarget(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("the target must be http(s)://host[:port][/path], with no credentials, query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

func normalizeProxyMethods(raw []string) ([]string, error) {
	want := map[string]bool{}
	for _, m := range raw {
		m = strings.ToUpper(strings.TrimSpace(m))
		if !slices.Contains(httpProxyMethods, m) {
			return nil, errors.New("unknown method " + m)
		}
		want[m] = true
	}
	var out []string
	for _, m := range httpProxyMethods {
		if want[m] {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("allow at least one method")
	}
	return out, nil
}

func mergeProxyHeaders(req []httpProxyHeader, existing []storedHeader) ([]storedHeader, error) {
	if len(req) > maxProxyHeaders {
		return nil, errors.New("a proxy may set at most 32 headers")
	}
	old := map[string]string{}
	for _, h := range existing {
		old[h.Name] = h.Value
	}
	seen := map[string]bool{}
	out := make([]storedHeader, 0, len(req))
	for _, h := range req {
		name := strings.TrimSpace(h.Name)
		if !headerNameRe.MatchString(name) {
			return nil, errors.New("not a valid header name: " + name)
		}
		name = textproto.CanonicalMIMEHeaderKey(name)
		if forbiddenProxyHeaders[name] {
			return nil, errors.New(name + " cannot be set on a proxy")
		}
		if seen[name] {
			return nil, errors.New(name + " is listed twice")
		}
		seen[name] = true

		if h.Value == nil {
			v, ok := old[name]
			if !ok {
				return nil, errors.New("a value is required for " + name)
			}
			out = append(out, storedHeader{Name: name, Value: v})
			continue
		}
		v := strings.TrimSpace(*h.Value)
		if len(v) > maxProxyHeaderValue || strings.ContainsAny(v, "\r\n\x00") {
			return nil, errors.New("the value of " + name + " is too long or holds a line break")
		}
		out = append(out, storedHeader{Name: name, Value: v})
	}
	return out, nil
}

func httpProxyAAD(owner, id pgtype.UUID) []byte {
	return []byte("http-proxy|" + domainIDString(owner) + "|" + domainIDString(id))
}

func (h *IntegrationHandler) sealProxyHeaders(headers []storedHeader, owner, id pgtype.UUID) ([]byte, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	if h.sealer == nil {
		return nil, echo.NewHTTPError(http.StatusServiceUnavailable, "custom headers need an encryption key on this server")
	}
	raw, err := json.Marshal(headers)
	if err != nil {
		return nil, err
	}
	sealed, err := h.sealer.seal(string(raw), httpProxyAAD(owner, id))
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "could not encrypt the headers")
	}
	return sealed, nil
}

func openProxyHeaders(s *llmSealer, blob []byte, owner, id pgtype.UUID) ([]storedHeader, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if s == nil {
		return nil, errors.New("no encryption key is configured")
	}
	raw, err := s.open(blob, httpProxyAAD(owner, id))
	if err != nil {
		return nil, err
	}
	var out []storedHeader
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseHTTPProxyReq(req httpProxyReq, existing []storedHeader) (httpProxySpec, error) {
	target, err := normalizeProxyTarget(req.TargetURL)
	if err != nil {
		return httpProxySpec{}, err
	}
	methods, err := normalizeProxyMethods(req.Methods)
	if err != nil {
		return httpProxySpec{}, err
	}
	headers, err := mergeProxyHeaders(req.Headers, existing)
	if err != nil {
		return httpProxySpec{}, err
	}
	verify := req.VerifyTLS == nil || *req.VerifyTLS
	return httpProxySpec{target: target, methods: methods, verifyTLS: verify, headers: headers}, nil
}

func headerNames(hs []storedHeader) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Name)
	}
	return out
}

func (h *IntegrationHandler) createHTTPProxy(c *echo.Context, owner pgtype.UUID, name string, req httpProxyReq) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if err := validateIntegrationName(name); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	spec, err := parseHTTPProxyReq(req, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	sealed, err := h.sealProxyHeaders(spec.headers, owner, id)
	if err != nil {
		return err
	}

	row, err := h.q.CreateHTTPProxyIntegration(c.Request().Context(), db.CreateHTTPProxyIntegrationParams{
		ID:              id,
		OwnerID:         owner,
		Name:            name,
		ProxyTargetURL:  spec.target,
		ProxyMethods:    spec.methods,
		ProxyVerifyTLS:  spec.verifyTLS,
		ProxyHeadersEnc: sealed,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "that name is taken, by one of your integrations or by another user's proxy")
		}
		log.Printf("could not create an http proxy for %s: %v", domainIDString(owner), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create the proxy")
	}
	verify := row.ProxyVerifyTLS
	return c.JSON(http.StatusCreated, integrationDTO{
		ID:          domainIDString(row.ID),
		Name:        row.Name,
		Kind:        row.Kind,
		Connected:   true,
		TargetURL:   row.ProxyTargetURL,
		Methods:     row.ProxyMethods,
		VerifyTLS:   &verify,
		HeaderNames: headerNames(spec.headers),
		CreatedAt:   row.CreatedAt.Time.Format(time.RFC3339),
	})
}

// @Summary     Replace an http proxy's target, methods, tls check and headers
// @Tags        integrations
// @Accept      json
// @Router      /integrations/{id}/http [put]
func (h *IntegrationHandler) UpdateHTTPProxy(c *echo.Context) error {
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return err
	}
	var req httpProxyReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	ctx := c.Request().Context()
	row, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}
	if row.Kind != integrationKindHTTP {
		return echo.NewHTTPError(http.StatusBadRequest, "this is not an http proxy")
	}
	existing, err := openProxyHeaders(h.sealer, row.ProxyHeadersEnc, owner, id)
	if err != nil {
		log.Printf("could not open the headers of proxy %s: %v", domainIDString(id), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "the stored headers could not be decrypted")
	}
	spec, err := parseHTTPProxyReq(req, existing)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	sealed, err := h.sealProxyHeaders(spec.headers, owner, id)
	if err != nil {
		return err
	}
	if _, err := h.q.UpdateHTTPProxyIntegration(ctx, db.UpdateHTTPProxyIntegrationParams{
		ID:              id,
		OwnerID:         owner,
		ProxyTargetURL:  spec.target,
		ProxyMethods:    spec.methods,
		ProxyVerifyTLS:  spec.verifyTLS,
		ProxyHeadersEnc: sealed,
	}); err != nil {
		log.Printf("could not save proxy %s: %v", domainIDString(id), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the proxy")
	}
	// No config push: intproxy asks the broker, so this lands within max_age.
	return c.NoContent(http.StatusNoContent)
}

// httpProxyToken answers the broker for <name>-u. The name is global, but the
// lookup still goes through the vm's own attachments and owner.
func (h *ClientHandler) httpProxyToken(c *echo.Context, client db.Client, ip, name string) error {
	if !integrationNameShapeRe.MatchString(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "not a proxy name")
	}
	ctx := c.Request().Context()
	g, err := h.q.GetHTTPProxyGrantForVM(ctx, db.GetHTTPProxyGrantForVMParams{
		ClientID: client.ID, VMIP: ip, ProxyName: name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("integration denied: host=%s vm_ip=%s integration=%s%s reason=not attached",
			clientLabel(client), ip, name, userProxySuffix)
		return echo.NewHTTPError(http.StatusForbidden, "no proxy named "+name+" is attached to this vm")
	}
	if err != nil {
		log.Printf("could not read the proxy %s for %s on client %s: %v", name, ip, clientLabel(client), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the proxies for this vm")
	}

	stored, err := openProxyHeaders(h.llmSealer, g.ProxyHeadersEnc, g.OwnerID, g.IntegrationID)
	if err != nil {
		log.Printf("could not open the headers of proxy %s: %v", domainIDString(g.IntegrationID), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "the stored headers could not be decrypted; set them again")
	}
	headers := make(map[string]string, len(stored))
	for _, s := range stored {
		headers[s.Name] = s.Value
	}

	log.Printf("integration allowed: host=%s vm=%s integration=%s%s",
		clientLabel(client), g.VMName, g.IntegrationName, userProxySuffix)
	return c.JSON(http.StatusOK, proto.IntegrationTokenResponse{
		ExpiresAt:   time.Now().Add(time.Hour),
		Upstream:    g.ProxyTargetURL,
		Headers:     headers,
		Methods:     g.ProxyMethods,
		InsecureTLS: !g.ProxyVerifyTLS,
	})
}
