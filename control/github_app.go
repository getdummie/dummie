package main

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	githubAPIBase = "https://api.github.com"

	// githubAppJWTTTL is under github's 10 minute ceiling, and the backdated iat
	// absorbs clock skew between us and github.
	githubAppJWTTTL  = 9 * time.Minute
	githubAppJWTSkew = 60 * time.Second

	// githubTokenRenewBefore is how long before expiry a cached installation
	// token stops being handed out. github issues them for an hour.
	githubTokenRenewBefore = 5 * time.Minute
)

var githubHTTPClient = &http.Client{Timeout: 15 * time.Second}

type githubApp struct {
	appID int64
	key   *rsa.PrivateKey
}

func parseGitHubApp(appID int64, keyPEM string) (*githubApp, error) {
	if appID <= 0 {
		return nil, errors.New("the github app id is not set")
	}
	block, _ := pem.Decode([]byte(strings.TrimSpace(keyPEM)))
	if block == nil {
		return nil, errors.New("the github app private key is not a PEM block")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return &githubApp{appID: appID, key: key}, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the github app private key could not be read: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the github app private key is not an RSA key")
	}
	return &githubApp{appID: appID, key: key}, nil
}

func (a *githubApp) jwt(now time.Time) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    fmt.Sprint(a.appID),
		IssuedAt:  jwt.NewNumericDate(now.Add(-githubAppJWTSkew)),
		ExpiresAt: jwt.NewNumericDate(now.Add(githubAppJWTTTL)),
	})
	return tok.SignedString(a.key)
}

// githubStatusError carries github's own status so the caller can map it onto
// something intproxy can render, rather than collapsing everything to a 502.
type githubStatusError struct {
	status     int
	message    string
	retryAfter string
}

func (e *githubStatusError) Error() string {
	return fmt.Sprintf("github returned %d: %s", e.status, e.message)
}

type installationToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type githubPermissions map[string]string

// installationPermissions narrows the token to what the request needs. github
// intersects this with what the app was granted, so asking for write on a
// read-only integration is refused before this is ever reached.
func installationPermissions(write bool) githubPermissions {
	level := "read"
	if write {
		level = "write"
	}
	return githubPermissions{
		"metadata":      "read",
		"contents":      level,
		"pull_requests": level,
		"issues":        level,
	}
}

func (a *githubApp) mintInstallationToken(ctx context.Context, installationID int64, repos []string, write bool) (installationToken, error) {
	var out installationToken

	appJWT, err := a.jwt(time.Now())
	if err != nil {
		return out, fmt.Errorf("could not sign the app token: %w", err)
	}

	payload := map[string]any{"permissions": installationPermissions(write)}
	if len(repos) > 0 {
		payload["repositories"] = repos
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return out, err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPIBase, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return out, fmt.Errorf("could not reach github: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusCreated {
		return out, githubError(resp, raw)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("could not read github's token response: %w", err)
	}
	if out.Token == "" {
		return out, errors.New("github returned an empty token")
	}
	return out, nil
}

func githubError(resp *http.Response, raw []byte) error {
	var body struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &body)
	msg := strings.TrimSpace(body.Message)
	if msg == "" {
		msg = resp.Status
	}
	return &githubStatusError{
		status:     resp.StatusCode,
		message:    msg,
		retryAfter: resp.Header.Get("Retry-After"),
	}
}

type githubAccount struct {
	InstallationID      int64  `json:"-"`
	AccountLogin        string `json:"-"`
	AccountType         string `json:"-"`
	RepositorySelection string `json:"repository_selection"`
	Account             struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
}

func (a *githubApp) getInstallation(ctx context.Context, installationID int64) (githubAccount, error) {
	var out githubAccount

	appJWT, err := a.jwt(time.Now())
	if err != nil {
		return out, err
	}

	url := fmt.Sprintf("%s/app/installations/%d", githubAPIBase, installationID)
	raw, err := a.get(ctx, url, appJWT)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("could not read github's installation response: %w", err)
	}
	out.InstallationID = installationID
	out.AccountLogin = out.Account.Login
	out.AccountType = out.Account.Type
	if out.AccountType == "" {
		out.AccountType = "Organization"
	}
	return out, nil
}

type githubRepo struct {
	Owner string
	Name  string
}

// listInstallationRepos is what the console's repo picker offers. The list comes
// from what was selected on github, so it is not something we can compile here.
func (a *githubApp) listInstallationRepos(ctx context.Context, installationID int64) ([]githubRepo, error) {
	tok, err := a.mintInstallationToken(ctx, installationID, nil, false)
	if err != nil {
		return nil, err
	}

	var repos []githubRepo
	for page := 1; page <= 10; page++ {
		url := fmt.Sprintf("%s/installation/repositories?per_page=100&page=%d", githubAPIBase, page)
		raw, err := a.get(ctx, url, tok.Token)
		if err != nil {
			return nil, err
		}
		var body struct {
			Repositories []struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("could not read github's repository list: %w", err)
		}
		for _, r := range body.Repositories {
			repos = append(repos, githubRepo{Owner: r.Owner.Login, Name: r.Name})
		}
		if len(body.Repositories) < 100 {
			break
		}
	}
	return repos, nil
}

func (a *githubApp) get(ctx context.Context, url, bearer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach github: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, githubError(resp, raw)
	}
	return raw, nil
}

// tokenCache saves a github api call per git request. intproxy keeps a second,
// shorter cache of its own; this one exists because minting is rate limited and
// is shared across every host in the fleet.
type tokenCache struct {
	mu sync.Mutex
	m  map[tokenCacheKey]*tokenCacheEntry
}

type tokenCacheKey struct {
	installationID int64
	repos          string
	write          bool
}

type tokenCacheEntry struct {
	ready chan struct{}
	tok   installationToken
	err   error
}

func newTokenCache() *tokenCache {
	return &tokenCache{m: map[tokenCacheKey]*tokenCacheEntry{}}
}

func (c *tokenCache) get(ctx context.Context, app *githubApp, installationID int64, repos []string, write bool) (installationToken, error) {
	sorted := append([]string(nil), repos...)
	sort.Strings(sorted)
	key := tokenCacheKey{installationID: installationID, repos: strings.Join(sorted, ","), write: write}

	c.mu.Lock()
	if e, ok := c.m[key]; ok {
		select {
		case <-e.ready:
			if e.err == nil && time.Until(e.tok.ExpiresAt) > githubTokenRenewBefore {
				c.mu.Unlock()
				return e.tok, nil
			}
			delete(c.m, key)
		default:
			c.mu.Unlock()
			select {
			case <-e.ready:
			case <-ctx.Done():
				return installationToken{}, ctx.Err()
			}
			return e.tok, e.err
		}
	}
	entry := &tokenCacheEntry{ready: make(chan struct{})}
	c.m[key] = entry
	c.mu.Unlock()

	entry.tok, entry.err = app.mintInstallationToken(ctx, installationID, sorted, write)
	close(entry.ready)

	if entry.err != nil {
		c.mu.Lock()
		if c.m[key] == entry {
			delete(c.m, key)
		}
		c.mu.Unlock()
	}
	return entry.tok, entry.err
}
