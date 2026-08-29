package credssp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"dpipe/internal/ntlm"
)

func testCert(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	crt, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return crt
}

// authorizer verifies against a password the way dproxy does: from the NT hash
// alone, never the plaintext.
func authorizer(password, target string) Authorizer {
	hash := ntlm.NTHash(password)
	return func(_ context.Context, req AuthRequest) (AuthResult, error) {
		key, ok := ntlm.VerifyNTLMv2(hash, req.User, req.Domain, req.ServerChallenge, req.NTResponse)
		if !ok {
			return AuthResult{}, ErrNotAuthorized
		}
		return AuthResult{
			SessionBaseKey: key,
			Target:         target,
			RemoteUser:     "ubuntu",
			RemotePassword: "ubuntu",
		}, nil
	}
}

// TestNLARoundTrip runs the client half against the server half. It covers the
// parts a live client cannot: message marshalling, the MIC, key exchange, and
// both directions of the public-key binding.
func TestNLARoundTrip(t *testing.T) {
	cert := testCert(t)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	type outcome struct {
		res AuthResult
		err error
	}
	done := make(chan outcome, 1)
	seen := make(chan string, 1)
	inner := authorizer("s3cret", "10.0.0.1:3389")
	go func() {
		res, err := ServeNLA(context.Background(), server, ServerConfig{
			Certificate:  cert,
			ComputerName: "dpipe",
			DomainName:   "DPIPE",
			Authorize: func(ctx context.Context, req AuthRequest) (AuthResult, error) {
				seen <- req.Domain
				return inner(ctx, req)
			},
		})
		done <- outcome{res, err}
	}()

	if err := DialNLA(client, ClientConfig{
		Certificate: cert,
		User:        "alice",
		Password:    "s3cret",
		Workstation: "dpipe",
	}); err != nil {
		t.Fatalf("DialNLA: %v", err)
	}

	got := <-done
	if got.err != nil {
		t.Fatalf("ServeNLA: %v", got.err)
	}
	if got.res.Target != "10.0.0.1:3389" || got.res.RemoteUser != "ubuntu" {
		t.Errorf("unexpected result: %+v", got.res)
	}
	// With no domain configured the client must name the server's own target.
	// Sending an empty domain lets a server fall back to one of its own, hash
	// NTOWFv2 with that, and reject the login as a bad password.
	if d := <-seen; d != "dpipe" {
		t.Errorf("domain sent = %q, want the server target name %q", d, "dpipe")
	}
}

func TestNLAWrongPassword(t *testing.T) {
	cert := testCert(t)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := ServeNLA(context.Background(), server, ServerConfig{
			Certificate: cert,
			Authorize:   authorizer("s3cret", "10.0.0.1:3389"),
		})
		done <- err
	}()

	err := DialNLA(client, ClientConfig{Certificate: cert, User: "alice", Password: "wrong"})
	if err == nil {
		t.Fatal("DialNLA accepted a wrong password")
	}
	if serr := <-done; !errors.Is(serr, ErrNotAuthorized) {
		t.Errorf("ServeNLA error = %v, want ErrNotAuthorized", serr)
	}
}

// TestNLABindsToCertificate is the property that makes terminating NLA safe: a
// session is bound to the certificate of the leg it runs on, so a proxy that
// relayed the handshake to a different endpoint fails.
func TestNLABindsToCertificate(t *testing.T) {
	serverCert, clientCert := testCert(t), testCert(t)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := ServeNLA(context.Background(), server, ServerConfig{
			Certificate: serverCert,
			Authorize:   authorizer("s3cret", "10.0.0.1:3389"),
		})
		done <- err
	}()

	if err := DialNLA(client, ClientConfig{Certificate: clientCert, User: "alice", Password: "s3cret"}); err == nil {
		t.Fatal("DialNLA accepted a mismatched certificate binding")
	}
	if serr := <-done; serr == nil {
		t.Error("ServeNLA accepted a mismatched certificate binding")
	}
}

// TestAuthenticateRoundTrip pins the message layout the guest parses. A field at
// the wrong offset reads as a bad password rather than as a parse error, which is
// what makes it worth asserting directly.
func TestAuthenticateRoundTrip(t *testing.T) {
	in := &ntlm.Authenticate{
		Flags:                     ntlm.FlagUnicode | ntlm.FlagNTLM | ntlm.FlagExtendedSessionSecurity,
		Domain:                    "EXAMPLE",
		User:                      "alice",
		Workstation:               "dpipe",
		NTResponse:                []byte("nt-response-bytes"),
		LMResponse:                make([]byte, 24),
		EncryptedRandomSessionKey: []byte("0123456789abcdef"),
	}
	out, err := ntlm.ParseAuthenticate(in.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if out.User != in.User || out.Domain != in.Domain || out.Workstation != in.Workstation {
		t.Errorf("identity round-trip: got %q/%q/%q", out.Domain, out.User, out.Workstation)
	}
	if string(out.NTResponse) != string(in.NTResponse) {
		t.Errorf("nt response round-trip: got %q", out.NTResponse)
	}
	if string(out.EncryptedRandomSessionKey) != string(in.EncryptedRandomSessionKey) {
		t.Errorf("session key round-trip: got %q", out.EncryptedRandomSessionKey)
	}
}

// TestAuthenticateFlagsEcho pins the flag negotiation against a real server. The
// loopback test cannot catch a dropped bit here, because our own server ignores
// what the client echoes back -- FreeRDP does not, and a missing TARGET_INFO
// makes it read an NTLMv2 response as v1 and reject the login as a bad password.
func TestAuthenticateFlagsEcho(t *testing.T) {
	// What gnome-remote-desktop (FreeRDP) offers.
	const serverFlags = 0xe0888235

	got, err := authenticateFlags(serverFlags)
	if err != nil {
		t.Fatal(err)
	}
	if got != serverFlags {
		t.Errorf("authenticateFlags(0x%08x) = 0x%08x, dropped 0x%08x",
			uint32(serverFlags), got, uint32(serverFlags)&^got)
	}
}

func TestAuthenticateFlagsRejectsUnusable(t *testing.T) {
	cases := []struct {
		name  string
		flags uint32
	}{
		{"no unicode", 0xe0888235 &^ ntlm.FlagUnicode},
		{"no extended session security", 0xe0888235 &^ ntlm.FlagExtendedSessionSecurity},
	}
	for _, c := range cases {
		if _, err := authenticateFlags(c.flags); err == nil {
			t.Errorf("%s: accepted flags 0x%08x", c.name, c.flags)
		}
	}
}

// TestAuthenticateFlagsDropsUnsupported keeps the reply to what we implement, so
// a server offering something exotic does not get told we support it.
func TestAuthenticateFlagsDropsUnsupported(t *testing.T) {
	const unsupported = 0x00100000 // NTLMSSP_TARGET_TYPE_SHARE, which we never handle
	got, err := authenticateFlags(0xe0888235 | unsupported)
	if err != nil {
		t.Fatal(err)
	}
	if got&unsupported != 0 {
		t.Errorf("echoed an unsupported flag: 0x%08x", got)
	}
}

// TestAVPairsAppend covers the helper that builds the response's AV pairs. The
// server's own pairs must survive unchanged and the terminator must stay last;
// a malformed list here produces a response that verifies as a bad password.
func TestAVPairsAppend(t *testing.T) {
	server := ntlm.TargetInfo("host", "DOMAIN", time.Unix(1700000000, 0))

	ts, ok := ntlm.AVPairValue(server, ntlm.AvTimestamp)
	if !ok || len(ts) != 8 {
		t.Fatalf("server target info has no usable timestamp: %x", server)
	}

	got := ntlm.AVPairsAppend(server,
		ntlm.AVPair{ID: ntlm.AvFlags, Value: ntlm.LE32(ntlm.AvFlagMIC)},
		ntlm.AVPair{ID: ntlm.AvTargetName, Value: ntlm.UTF16LE("TERMSRV/host")},
	)

	// The server's pairs are still readable, including the timestamp we echo.
	if again, ok := ntlm.AVPairValue(got, ntlm.AvTimestamp); !ok || string(again) != string(ts) {
		t.Errorf("timestamp lost: %x", got)
	}
	flags, ok := ntlm.AVPairValue(got, ntlm.AvFlags)
	if !ok || string(flags) != string(ntlm.LE32(ntlm.AvFlagMIC)) {
		t.Errorf("MsvAvFlags not appended: %x", got)
	}
	if name, ok := ntlm.AVPairValue(got, ntlm.AvTargetName); !ok || string(name) != string(ntlm.UTF16LE("TERMSRV/host")) {
		t.Errorf("MsvAvTargetName not appended: %x", got)
	}
	// A terminator, exactly one, at the end.
	if n := len(got); n < 4 || string(got[n-4:]) != string([]byte{0, 0, 0, 0}) {
		t.Errorf("list does not end with a terminator: %x", got)
	}
}
