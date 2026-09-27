package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "e30." + base64.RawURLEncoding.EncodeToString(raw) + ".sig"
}

func TestChatGPTCredFromALogin(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	toks := chatgptTokens{
		IDToken:      fakeJWT(t, map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct-1"}}),
		AccessToken:  fakeJWT(t, map[string]any{"exp": exp}),
		RefreshToken: "rt-1",
	}
	cred, err := toks.credFrom(chatgptCred{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccountID != "acct-1" || cred.RefreshToken != "rt-1" || cred.ExpiresAt.Unix() != exp {
		t.Fatalf("cred = %+v", cred)
	}
}

// A refresh may leave out the id token and the refresh token; the old ones stand.
func TestChatGPTRefreshKeepsWhatItLeftOut(t *testing.T) {
	prev := chatgptCred{AccessToken: "old", RefreshToken: "rt-1", AccountID: "acct-1"}
	cred, err := chatgptTokens{AccessToken: fakeJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})}.credFrom(prev)
	if err != nil {
		t.Fatal(err)
	}
	if cred.RefreshToken != "rt-1" || cred.AccountID != "acct-1" || cred.AccessToken == "old" {
		t.Fatalf("cred = %+v", cred)
	}
}

func TestChatGPTCredRefusesAnIncompleteLogin(t *testing.T) {
	for name, toks := range map[string]chatgptTokens{
		"no access":  {RefreshToken: "rt"},
		"not a jwt":  {AccessToken: "opaque", RefreshToken: "rt"},
		"no account": {AccessToken: fakeJWT(t, map[string]any{"exp": 1}), RefreshToken: "rt"},
	} {
		if _, err := toks.credFrom(chatgptCred{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestChatGPTCredSealRoundTrip(t *testing.T) {
	s, err := newLLMSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	aad := llmKeyAAD("chatgpt", pgtype.UUID{Bytes: [16]byte{1}, Valid: true})
	want := chatgptCred{AccessToken: "at", RefreshToken: "rt", AccountID: "acct", ExpiresAt: time.Unix(1700000000, 0).UTC()}
	blob, err := sealChatGPTCred(s, want, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := openChatGPTCred(s, blob, aad)
	if err != nil || got != want {
		t.Fatalf("open = %+v, %v", got, err)
	}
	if _, err := openChatGPTCred(s, blob, llmKeyAAD("chatgpt", pgtype.UUID{})); err == nil {
		t.Fatal("a user's login opened as the global one")
	}
}

func TestChatGPTDevicesExpire(t *testing.T) {
	d := newChatGPTDevices()
	d.put("a", chatgptDevice{DeviceAuthID: "x", expires: time.Now().Add(-time.Second)})
	if _, ok := d.get("a"); ok {
		t.Fatal("an expired login was handed back")
	}
}
