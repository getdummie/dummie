package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
)

// These are codex's own oauth client and endpoints; a chatgpt subscription
// only lets that client in.
const (
	chatgptClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	chatgptAuthBase      = "https://auth.openai.com"
	chatgptVerifyURL     = chatgptAuthBase + "/codex/device"
	chatgptRedirectURI   = chatgptAuthBase + "/deviceauth/callback"
	// The backend hides models needing a newer codex than this, so it stays high.
	chatgptClientVersion = "99.0.0"

	chatgptRefreshSkew = 5 * time.Minute
	chatgptDeviceTTL   = 15 * time.Minute
)

var errChatGPTPending = errors.New("the device login has not been approved yet")

// chatgptCred is what a chatgpt row seals in place of an api key.
type chatgptCred struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	AccountID    string    `json:"account_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func sealChatGPTCred(s *llmSealer, cred chatgptCred, aad []byte) ([]byte, error) {
	raw, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	return s.seal(string(raw), aad)
}

func openChatGPTCred(s *llmSealer, blob, aad []byte) (chatgptCred, error) {
	var cred chatgptCred
	plain, err := s.open(blob, aad)
	if err != nil {
		return cred, err
	}
	err = json.Unmarshal([]byte(plain), &cred)
	return cred, err
}

type chatgptClaims struct {
	Exp  int64 `json:"exp"`
	Auth struct {
		AccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
}

// jwtClaims reads a token's claims without checking its signature. That is
// only sound because every token here came straight from auth.openai.com.
func jwtClaims(tok string) (chatgptClaims, error) {
	var c chatgptClaims
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return c, errors.New("not a jwt")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(raw, &c)
}

type chatgptTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// credFrom folds a token response onto prev, since a refresh may leave out
// the refresh token or the id token.
func (t chatgptTokens) credFrom(prev chatgptCred) (chatgptCred, error) {
	if t.AccessToken == "" {
		return prev, errors.New("the token response carried no access token")
	}
	cred := prev
	cred.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		cred.RefreshToken = t.RefreshToken
	}
	access, err := jwtClaims(t.AccessToken)
	if err != nil || access.Exp == 0 {
		return prev, errors.New("the access token carries no expiry")
	}
	cred.ExpiresAt = time.Unix(access.Exp, 0)
	for _, tok := range []string{t.IDToken, t.AccessToken} {
		if c, err := jwtClaims(tok); err == nil && c.Auth.AccountID != "" {
			cred.AccountID = c.Auth.AccountID
			break
		}
	}
	if cred.AccountID == "" || cred.RefreshToken == "" {
		return prev, errors.New("the login carried no chatgpt account or refresh token")
	}
	return cred, nil
}

func chatgptDo(ctx context.Context, req *http.Request, out any) (int, error) {
	resp, err := llmHTTPClient.Do(req.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("%s returned %d", req.URL.Path, resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, fmt.Errorf("could not decode %s: %w", req.URL.Path, err)
	}
	return resp.StatusCode, nil
}

func chatgptPostJSON(ctx context.Context, path string, body, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, chatgptAuthBase+path, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	return chatgptDo(ctx, req, out)
}

type chatgptDevice struct {
	DeviceAuthID string      `json:"device_auth_id"`
	UserCode     string      `json:"user_code"`
	Interval     json.Number `json:"interval"`
	expires      time.Time
}

func chatgptDeviceStart(ctx context.Context) (chatgptDevice, error) {
	var d chatgptDevice
	if _, err := chatgptPostJSON(ctx, "/api/accounts/deviceauth/usercode", map[string]string{"client_id": chatgptClientID}, &d); err != nil {
		return d, err
	}
	if d.DeviceAuthID == "" || d.UserCode == "" {
		return d, errors.New("the device login answer carried no code")
	}
	d.expires = time.Now().Add(chatgptDeviceTTL)
	return d, nil
}

// chatgptDevicePoll asks once whether d was approved, and if so trades it for
// a login. errChatGPTPending means ask again later.
func chatgptDevicePoll(ctx context.Context, d chatgptDevice) (chatgptCred, error) {
	var grant struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	status, err := chatgptPostJSON(ctx, "/api/accounts/deviceauth/token",
		map[string]string{"device_auth_id": d.DeviceAuthID, "user_code": d.UserCode}, &grant)
	if status == http.StatusForbidden || status == http.StatusNotFound {
		return chatgptCred{}, errChatGPTPending
	}
	if err != nil {
		return chatgptCred{}, err
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {grant.AuthorizationCode},
		"redirect_uri":  {chatgptRedirectURI},
		"client_id":     {chatgptClientID},
		"code_verifier": {grant.CodeVerifier},
	}
	req, err := http.NewRequest(http.MethodPost, chatgptAuthBase+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return chatgptCred{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var toks chatgptTokens
	if _, err := chatgptDo(ctx, req, &toks); err != nil {
		return chatgptCred{}, err
	}
	return toks.credFrom(chatgptCred{})
}

func chatgptRefresh(ctx context.Context, cred chatgptCred) (chatgptCred, error) {
	var toks chatgptTokens
	status, err := chatgptPostJSON(ctx, "/oauth/token", map[string]string{
		"client_id":     chatgptClientID,
		"grant_type":    "refresh_token",
		"refresh_token": cred.RefreshToken,
		"scope":         "openid profile email",
	}, &toks)
	if status == http.StatusBadRequest || status == http.StatusUnauthorized {
		return cred, errLLMKeyRejected
	}
	if err != nil {
		return cred, err
	}
	return toks.credFrom(cred)
}

func fetchChatGPTModels(ctx context.Context, plan llmPlan, cred chatgptCred) ([]llmModel, error) {
	req, err := http.NewRequest(http.MethodGet, plan.ResponsesBase+"/models?client_version="+chatgptClientVersion, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Chatgpt-Account-Id", cred.AccountID)
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("User-Agent", "codex_cli_rs/"+chatgptClientVersion)
	var raw json.RawMessage
	status, err := chatgptDo(ctx, req, &raw)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, errLLMKeyRejected
	}
	if err != nil {
		return nil, err
	}
	var list struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	_ = json.Unmarshal(raw, &list)
	out := make([]llmModel, 0, len(list.Models))
	for _, m := range list.Models {
		if m.Slug != "" {
			out = append(out, llmModel{ID: m.Slug, OwnedBy: "chatgpt"})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the model list had no models: %.500s", raw)
	}
	return out, nil
}

// chatgptAccess returns k's login, refreshed under the row's lock if close to
// expiry, since a refresh rotates the refresh token and two would race.
func (h *ClientHandler) chatgptAccess(ctx context.Context, k db.ListLLMKeysForVMRow) (chatgptCred, error) {
	aad := llmKeyAAD(k.Provider, llmKeyOwner(k))
	cred, err := openChatGPTCred(h.llmSealer, k.ApiKeyEnc, aad)
	if err != nil || time.Until(cred.ExpiresAt) > chatgptRefreshSkew {
		return cred, err
	}
	if h.pool == nil {
		return cred, errors.New("database unavailable")
	}
	// A refresh that lands after its caller gave up still has to be saved.
	ctx = context.WithoutCancel(ctx)
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return cred, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.q.WithTx(tx)

	row, err := qtx.GetLLMKeyForUpdate(ctx, k.ID)
	if err != nil {
		return cred, err
	}
	if cred, err = openChatGPTCred(h.llmSealer, row.ApiKeyEnc, aad); err != nil || time.Until(cred.ExpiresAt) > chatgptRefreshSkew {
		return cred, err
	}
	if cred, err = chatgptRefresh(ctx, cred); err != nil {
		return cred, err
	}
	sealed, err := sealChatGPTCred(h.llmSealer, cred, aad)
	if err != nil {
		return cred, err
	}
	if err := qtx.UpdateLLMKeySecret(ctx, db.UpdateLLMKeySecretParams{ID: k.ID, ApiKeyEnc: sealed}); err != nil {
		return cred, err
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("a refreshed chatgpt login for key %s was not saved and is now lost: %v", domainIDString(k.ID), err)
		return cred, err
	}
	return cred, nil
}

// chatgptDevices holds device logins in flight, one per owner and provider.
// The device_auth_id stays here and never reaches the browser.
type chatgptDevices struct {
	mu sync.Mutex
	m  map[string]chatgptDevice
}

func newChatGPTDevices() *chatgptDevices {
	return &chatgptDevices{m: map[string]chatgptDevice{}}
}

func chatgptDeviceKey(provider string, owner pgtype.UUID) string {
	return string(llmKeyAAD(provider, owner))
}

func (d *chatgptDevices) put(key string, dev chatgptDevice) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for k, v := range d.m {
		if now.After(v.expires) {
			delete(d.m, k)
		}
	}
	d.m[key] = dev
}

func (d *chatgptDevices) get(key string) (chatgptDevice, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dev, ok := d.m[key]
	if ok && time.Now().After(dev.expires) {
		delete(d.m, key)
		return dev, false
	}
	return dev, ok
}

func (d *chatgptDevices) drop(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.m, key)
}
