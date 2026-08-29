package credssp

import (
	"context"
	"crypto/rc4"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"time"

	"dpipe/internal/ntlm"
)

// AuthRequest is what the server half learned from the client's AUTHENTICATE
// message. Everything in it came off the wire and is untrusted until an
// Authorizer verifies NTResponse against a hash it holds.
type AuthRequest struct {
	User            string
	Domain          string
	Workstation     string
	ServerChallenge [8]byte
	NTResponse      []byte

	// Flags and HasMIC describe how the client chose to answer. They are not
	// used in the verdict; they exist so a working client can be compared
	// against what this package's own client half sends.
	Flags  uint32
	HasMIC bool
}

// AuthResult is the Authorizer's verdict. SessionBaseKey is HMAC_MD5 over the
// NTProofStr the Authorizer just verified; without it the session cannot proceed,
// which is what keeps the password itself out of this process.
type AuthResult struct {
	SessionBaseKey []byte
	Target         string
	RemoteUser     string
	RemotePassword string
}

// Authorizer verifies a client's NTLMv2 response and says which backend it may
// reach. Returning an error refuses the connection.
type Authorizer func(ctx context.Context, req AuthRequest) (AuthResult, error)

// ServerConfig configures one accepted NLA handshake.
type ServerConfig struct {
	// Certificate is the leaf this side presented in the TLS handshake; CredSSP
	// binds the NLA exchange to its public key.
	Certificate *x509.Certificate
	// ComputerName and DomainName are advertised in the CHALLENGE. They are
	// cosmetic — clients display them — but must be present for some clients.
	ComputerName string
	DomainName   string
	Authorize    Authorizer
}

// ErrNotAuthorized is returned when the peer's credentials are refused. Callers
// log it at info, not warn: a wrong password is routine.
var ErrNotAuthorized = errors.New("credssp: not authorized")

// ServeNLA runs the server half of CredSSP over an already-established TLS
// connection and returns the backend the caller should now reach.
func ServeNLA(ctx context.Context, rw io.ReadWriter, cfg ServerConfig) (AuthResult, error) {
	var zero AuthResult

	pubKey, err := PublicKey(cfg.Certificate)
	if err != nil {
		return zero, err
	}

	first, err := readTSRequest(rw)
	if err != nil {
		return zero, err
	}
	version := min(first.Version, Version)
	negoRaw, err := first.token()
	if err != nil {
		return zero, err
	}
	nego, err := ntlm.ParseNegotiate(negoRaw)
	if err != nil {
		return zero, err
	}

	challenge, err := buildChallenge(nego.Flags, cfg)
	if err != nil {
		return zero, err
	}
	challengeRaw := challenge.Marshal()
	if err := writeTSRequest(rw, requestWithToken(version, challengeRaw)); err != nil {
		return zero, err
	}

	second, err := readTSRequest(rw)
	if err != nil {
		return zero, err
	}
	authRaw, err := second.token()
	if err != nil {
		return zero, err
	}
	auth, err := ntlm.ParseAuthenticate(authRaw)
	if err != nil {
		return zero, err
	}
	if len(auth.NTResponse) < 16 || len(auth.NTResponse) > ntlm.MaxNTLMMessage {
		return zero, fmt.Errorf("%w: unusable NT response", ErrNotAuthorized)
	}

	res, err := cfg.Authorize(ctx, AuthRequest{
		User:            auth.User,
		Domain:          auth.Domain,
		Workstation:     auth.Workstation,
		ServerChallenge: challenge.ServerChallenge,
		NTResponse:      auth.NTResponse,
		Flags:           auth.Flags,
		HasMIC:          hasMIC(auth.MIC),
	})
	if err != nil {
		writeError(rw, version, errLogonDenied)
		return zero, err
	}
	if len(res.SessionBaseKey) != 16 {
		writeError(rw, version, errLogonDenied)
		return zero, errors.New("credssp: authorizer returned no session key")
	}

	exported, err := exportedSessionKey(res.SessionBaseKey, auth)
	if err != nil {
		return zero, err
	}
	sec, err := ntlm.NewSecurity(exported, true)
	if err != nil {
		return zero, err
	}

	// The client's pubKeyAuth proves it holds the same session key *and* that it
	// is talking to this TLS endpoint rather than relaying our handshake elsewhere.
	got, err := sec.Unwrap(second.PubKeyAuth)
	if err != nil {
		writeError(rw, version, errLogonDenied)
		return zero, fmt.Errorf("%w: %w", ErrNotAuthorized, err)
	}
	want := clientBinding(version, second.ClientNonce, pubKey)
	if !ntlm.ConstantTimeEqual(got, want) {
		writeError(rw, version, errLogonDenied)
		return zero, fmt.Errorf("%w: public key binding mismatch", ErrNotAuthorized)
	}

	reply := serverBinding(version, second.ClientNonce, pubKey)
	if err := writeTSRequest(rw, tsRequest{Version: version, PubKeyAuth: sec.Wrap(reply)}); err != nil {
		return zero, err
	}

	// The client now delegates the credentials it wanted to pass through. We
	// authenticated it against our own policy, so the payload is read to keep the
	// exchange in step and then discarded — the backend gets its own credentials.
	third, err := readTSRequest(rw)
	if err != nil {
		return zero, err
	}
	if len(third.AuthInfo) == 0 {
		return zero, errors.New("credssp: client sent no delegated credentials")
	}
	if _, err := sec.Unwrap(third.AuthInfo); err != nil {
		return zero, fmt.Errorf("credssp: unwrap credentials: %w", err)
	}
	return res, nil
}

func buildChallenge(clientFlags uint32, cfg ServerConfig) (*ntlm.Challenge, error) {
	sc, err := ntlm.RandomBytes(8)
	if err != nil {
		return nil, err
	}
	c := &ntlm.Challenge{
		TargetName:   cfg.ComputerName,
		TargetInfo:   ntlm.TargetInfo(cfg.ComputerName, cfg.DomainName, time.Now()),
	}
	copy(c.ServerChallenge[:], sc)

	// Mirror only the flags we actually implement, and require the ones the rest
	// of this package assumes.
	c.Flags = ntlm.FlagUnicode | ntlm.FlagRequestTarget | ntlm.FlagNTLM | ntlm.FlagAlwaysSign |
		ntlm.FlagExtendedSessionSecurity | ntlm.FlagTargetInfo | ntlm.FlagTargetTypeServer |
		ntlm.Flag128 | ntlm.Flag56
	for _, f := range []uint32{ntlm.FlagSign, ntlm.FlagSeal, ntlm.FlagKeyExch} {
		if clientFlags&f != 0 {
			c.Flags |= f
		}
	}
	if clientFlags&ntlm.FlagUnicode == 0 {
		return nil, errors.New("credssp: client did not offer unicode")
	}
	if clientFlags&ntlm.FlagExtendedSessionSecurity == 0 {
		return nil, errors.New("credssp: client did not offer extended session security")
	}
	return c, nil
}

// exportedSessionKey unwraps the client's randomly generated session key when key
// exchange was negotiated; otherwise the base key is used directly.
func exportedSessionKey(base []byte, auth *ntlm.Authenticate) ([]byte, error) {
	if auth.Flags&ntlm.FlagKeyExch == 0 || len(auth.EncryptedRandomSessionKey) == 0 {
		return base, nil
	}
	if len(auth.EncryptedRandomSessionKey) != 16 {
		return nil, errors.New("credssp: bad encrypted session key length")
	}
	c, err := rc4.NewCipher(base)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	c.XORKeyStream(out, auth.EncryptedRandomSessionKey)
	return out, nil
}

func hasMIC(mic []byte) bool {
	for _, b := range mic {
		if b != 0 {
			return true
		}
	}
	return false
}
