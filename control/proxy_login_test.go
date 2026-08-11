package main

import (
  "crypto/hmac"
  "crypto/sha256"
  "encoding/base64"
  "encoding/json"
  "net/url"
  "strings"
  "testing"
  "time"
)

const testProxySecret = "0123456789abcdef0123456789abcdef"

// TestMintProxyTokenIsVerifiableTheWayTheProxyVerifiesIt rebuilds the check from
// the other side rather than comparing against a recorded string: the contract
// is "hmac over the base64 text", and a token that only matches a golden value
// would still pass if both this test and the code drifted the same way.
func TestMintProxyTokenIsVerifiableTheWayTheProxyVerifiesIt(t *testing.T) {
  now := time.Unix(1_700_000_000, 0)

  tok, err := mintProxyToken(testProxySecret, "user@example.com", "vm.example.com", now)
  if err != nil {
    t.Fatalf("could not mint: %v", err)
  }

  b, sig, ok := strings.Cut(tok, ".")
  if !ok {
    t.Fatalf("token %q has no separator", tok)
  }
  if strings.Contains(tok, "=") {
    t.Errorf("token %q is padded", tok)
  }

  mac := hmac.New(sha256.New, []byte(testProxySecret))
  mac.Write([]byte(b))
  want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
  if sig != want {
    t.Errorf("signature = %q, want %q", sig, want)
  }

  raw, err := base64.RawURLEncoding.DecodeString(b)
  if err != nil {
    t.Fatalf("payload is not unpadded base64url: %v", err)
  }
  var got proxyTokenPayload
  if err := json.Unmarshal(raw, &got); err != nil {
    t.Fatalf("payload is not json: %v", err)
  }
  if got.Sub != "user@example.com" {
    t.Errorf("sub = %q", got.Sub)
  }
  if got.Aud != "vm.example.com" {
    t.Errorf("aud = %q, want the host param", got.Aud)
  }
  if want := now.Add(5 * time.Minute).Unix(); got.Exp != want {
    t.Errorf("exp = %d, want %d", got.Exp, want)
  }
}

// The signature covers the encoded payload, so a token whose payload is edited
// in flight -- to a longer exp, or to another host -- must not verify.
func TestMintProxyTokenSignatureCoversThePayload(t *testing.T) {
  now := time.Unix(1_700_000_000, 0)
  tok, err := mintProxyToken(testProxySecret, "user@example.com", "vm.example.com", now)
  if err != nil {
    t.Fatalf("could not mint: %v", err)
  }
  _, sig, _ := strings.Cut(tok, ".")

  tampered, err := json.Marshal(proxyTokenPayload{
    Sub: "user@example.com",
    Aud: "other.example.com",
    Exp: now.Add(999 * time.Hour).Unix(),
  })
  if err != nil {
    t.Fatalf("could not marshal: %v", err)
  }
  mac := hmac.New(sha256.New, []byte(testProxySecret))
  mac.Write([]byte(base64.RawURLEncoding.EncodeToString(tampered)))
  if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) == sig {
    t.Error("an edited payload verified under the original signature")
  }
}

// CONTROL_URL is an origin an operator types, so it may or may not end in a
// slash; the path it gets is this server's, not theirs.
func TestProxyAuthConfigLoginURL(t *testing.T) {
  for _, in := range []string{"http://control.example.com:1323", "http://control.example.com:1323/"} {
    got := proxyAuthConfig{controlURL: in}.loginURL()
    if want := "http://control.example.com:1323/login"; got != want {
      t.Errorf("loginURL() for %q = %q, want %q", in, got, want)
    }
  }
}

func TestParseRedirectTarget(t *testing.T) {
  cases := []struct {
    name string
    rd   string
    host string
    ok   bool
  }{
    {"the callback proxy sends", "http://vm.example.com/__auth/callback", "vm.example.com", true},
    {"https", "https://vm.example.com/__auth/callback", "vm.example.com", true},
    {"host case is not significant", "http://VM.example.com/__auth/callback", "vm.example.com", true},
    {"another host entirely", "http://evil.tld/steal", "vm.example.com", false},
    {"the real host in the path", "http://evil.tld/vm.example.com", "vm.example.com", false},
    {"the real host as userinfo", "http://vm.example.com@evil.tld/", "vm.example.com", false},
    {"a subdomain of the host", "http://x.vm.example.com/", "vm.example.com", false},
    {"protocol-relative", "//evil.tld/", "vm.example.com", false},
    {"a bare path", "/__auth/callback", "vm.example.com", false},
    {"a non-http scheme", "javascript:alert(1)", "vm.example.com", false},
    {"no rd at all", "", "vm.example.com", false},
    {"no host at all", "http://vm.example.com/", "", false},
    {"a host that is not a hostname", "http://vm.example.com/", "vm.example.com/x", false},
  }
  for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
      _, err := parseRedirectTarget(tc.rd, tc.host)
      if tc.ok && err != nil {
        t.Errorf("parseRedirectTarget(%q, %q) = %v, want ok", tc.rd, tc.host, err)
      }
      if !tc.ok && err == nil {
        t.Errorf("parseRedirectTarget(%q, %q) was accepted", tc.rd, tc.host)
      }
    })
  }
}

func TestSafeNextPath(t *testing.T) {
  cases := map[string]string{
    "/dashboard":            "/dashboard",
    "/a/b?c=d#e":            "/a/b?c=d#e",
    "":                      "/",
    "//evil.tld":            "/",
    "https://evil.tld":      "/",
    "dashboard":             "/",
    "/ok\r\nSet-Cookie: x=1": "/",
  }
  for in, want := range cases {
    if got := safeNextPath(in); got != want {
      t.Errorf("safeNextPath(%q) = %q, want %q", in, got, want)
    }
  }
}

// The sign-in bounce has to carry all three params, and carry them as one
// encoded value -- an unescaped nested query would arrive at /signin as its own
// params and the hand-off would resume with none of them.
func TestSigninReturnURLRoundTrips(t *testing.T) {
  got := signinReturnURL("http://vm.example.com/__auth/callback", "vm.example.com", "/dashboard")

  u, err := url.Parse(got)
  if err != nil {
    t.Fatalf("could not parse %q: %v", got, err)
  }
  if u.Path != "/signin" {
    t.Errorf("path = %q, want /signin", u.Path)
  }
  back, err := url.Parse(u.Query().Get("redirect"))
  if err != nil {
    t.Fatalf("could not parse the redirect: %v", err)
  }
  if back.Path != proxyLoginPath {
    t.Errorf("redirect path = %q, want %q", back.Path, proxyLoginPath)
  }
  q := back.Query()
  if q.Get("rd") != "http://vm.example.com/__auth/callback" {
    t.Errorf("rd = %q", q.Get("rd"))
  }
  if q.Get("host") != "vm.example.com" {
    t.Errorf("host = %q", q.Get("host"))
  }
  if q.Get("next") != "/dashboard" {
    t.Errorf("next = %q", q.Get("next"))
  }
}
