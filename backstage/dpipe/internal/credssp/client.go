package credssp

import (
	"crypto/rc4"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"dpipe/internal/ntlm"
)

// ClientConfig configures the outbound NLA handshake to a backend.
type ClientConfig struct {
	// Certificate is the leaf the backend presented in its TLS handshake.
	Certificate *x509.Certificate
	// SPN names the service being authenticated to, e.g. "TERMSRV/host". It is
	// echoed in the response as MsvAvTargetName.
	SPN string
	// Log, when set, records what the backend negotiated. Nothing secret is
	// logged: flags, the target name, and sizes.
	Log *slog.Logger
	Domain      string
	User        string
	Password    string
	Workstation string
}

// DialNLA runs the client half of CredSSP over an already-established TLS
// connection to the backend. On return the connection is authenticated and the
// caller may start relaying RDP.
func DialNLA(rw io.ReadWriter, cfg ClientConfig) error {
	pubKey, err := PublicKey(cfg.Certificate)
	if err != nil {
		return err
	}

	nego := &ntlm.Negotiate{Flags: ntlm.FlagUnicode | ntlm.FlagRequestTarget | ntlm.FlagNTLM |
		ntlm.FlagAlwaysSign | ntlm.FlagExtendedSessionSecurity | ntlm.FlagSign | ntlm.FlagSeal |
		ntlm.FlagKeyExch | ntlm.Flag128 | ntlm.Flag56}
	negoRaw := nego.Marshal()
	if err := writeTSRequest(rw, requestWithToken(Version, negoRaw)); err != nil {
		return err
	}

	resp, err := readTSRequest(rw)
	if err != nil {
		return err
	}
	if resp.ErrorCode != 0 {
		return fmt.Errorf("credssp: backend refused negotiation (0x%08x)", uint32(resp.ErrorCode))
	}
	version := min(resp.Version, Version)
	challengeRaw, err := resp.token()
	if err != nil {
		return err
	}
	challenge, err := ntlm.ParseChallenge(challengeRaw)
	if err != nil {
		return err
	}
	// The AUTHENTICATE must carry the flags the server agreed to, not the ones we
	// asked for. Unicode is the one that bites: without it the server decodes the
	// username as OEM bytes, computes a different NTOWFv2, and rejects the login
	// as a bad password rather than as a protocol error.
	flags, err := authenticateFlags(challenge.Flags)
	if err != nil {
		return err
	}
	if cfg.Log != nil {
		cfg.Log.Debug("credssp: backend challenge",
			"target", challenge.TargetName, "server_flags", challenge.Flags,
			"reply_flags", flags, "target_info_bytes", len(challenge.TargetInfo),
			"challenge_hex", hex.EncodeToString(challengeRaw))
	}

	cc, err := ntlm.RandomBytes(8)
	if err != nil {
		return err
	}
	var clientChallenge [8]byte
	copy(clientChallenge[:], cc)

	// An empty domain is not neutral. A server only adopts the domain from the
	// AUTHENTICATE message when the field is non-empty; left blank it keeps its
	// own, hashes NTOWFv2 with that, and reports the resulting mismatch as a bad
	// password. Naming the server's own target is what real clients do.
	domain := cfg.Domain
	if domain == "" {
		domain = challenge.TargetName
	}

	// A challenge carrying MsvAvTimestamp is asking for that exact value back,
	// together with MsvAvFlags marking a MIC and the MIC itself. Echoing our own
	// clock instead, or sending a MIC without the flag, yields a response that is
	// cryptographically correct and still refused as a bad password.
	timestamp := ntlm.WindowsTimeBytes(time.Now())
	targetInfo := challenge.TargetInfo
	useMIC := false
	if ts, ok := ntlm.AVPairValue(challenge.TargetInfo, ntlm.AvTimestamp); ok && len(ts) == 8 {
		timestamp = ts
		useMIC = true
		targetInfo = ntlm.AVPairsAppend(challenge.TargetInfo,
			ntlm.AVPair{ID: ntlm.AvFlags, Value: ntlm.LE32(ntlm.AvFlagMIC)},
			// No TLS channel binding is in use, which is expressed as a zeroed
			// hash rather than by omitting the pair.
			ntlm.AVPair{ID: ntlm.AvChannelBindings, Value: make([]byte, 16)},
			ntlm.AVPair{ID: ntlm.AvTargetName, Value: ntlm.UTF16LE(cfg.SPN)},
		)
	}

	blob := ntlm.NTLMv2Blob(clientChallenge, timestamp, targetInfo)
	ntResponse, baseKey := ntlm.ComputeNTLMv2(ntlm.NTHash(cfg.Password), cfg.User, domain,
		challenge.ServerChallenge, blob)

	// Without key exchange the base key is the session key, and sending an
	// encrypted one anyway would leave the two sides deriving different keys.
	exported := baseKey
	var encryptedKey []byte
	if flags&ntlm.FlagKeyExch != 0 {
		if exported, err = ntlm.RandomBytes(16); err != nil {
			return err
		}
		rc, err := rc4.NewCipher(baseKey)
		if err != nil {
			return err
		}
		encryptedKey = make([]byte, 16)
		rc.XORKeyStream(encryptedKey, exported)
	}

	auth := &ntlm.Authenticate{
		Flags:                     flags,
		Domain:                    domain,
		User:                      cfg.User,
		Workstation:               cfg.Workstation,
		NTResponse:                ntResponse,
		EncryptedRandomSessionKey: encryptedKey,
		// LM responses are not offered; NTLMv2 supersedes them and the 24 zero
		// bytes are what a modern client sends.
		LMResponse: make([]byte, 24),
	}
	authRaw := auth.Marshal()
	if useMIC {
		auth.SetMIC(exported, negoRaw, challengeRaw)
		authRaw = auth.Raw
	}

	sec, err := ntlm.NewSecurity(exported, false)
	if err != nil {
		return err
	}

	var nonce []byte
	if version >= nonceVersion {
		if nonce, err = ntlm.RandomBytes(nonceLen); err != nil {
			return err
		}
	}

	if cfg.Log != nil {
		// The whole message, so the NTLMv2 computation can be replayed offline
		// against the challenge above. It carries a challenge-bound proof, not
		// the password; still, only enable this while debugging.
		cfg.Log.Debug("credssp: backend authenticate",
			"authenticate_hex", hex.EncodeToString(authRaw),
			"nt_response_hex", hex.EncodeToString(ntResponse),
			"nt_hash_hex", hex.EncodeToString(ntlm.NTHash(cfg.Password)),
			"user", cfg.User, "domain", domain)
	}

	req := requestWithToken(version, authRaw)
	req.PubKeyAuth = sec.Wrap(clientBinding(version, nonce, pubKey))
	req.ClientNonce = nonce
	if err := writeTSRequest(rw, req); err != nil {
		return err
	}

	confirm, err := readTSRequest(rw)
	if err != nil {
		return err
	}
	if confirm.ErrorCode != 0 {
		// Carry what was negotiated: a rejection here is reported as a bad
		// password whatever the real cause, so the flags are the only clue as to
		// whether the two sides even agreed on how to encode the credential.
		// Sizes, never values: the password length alone distinguishes "wrong
		// password" from "no password reached us", which look identical here.
		return fmt.Errorf("%w: backend rejected credentials (0x%08x; server_flags=0x%08x reply_flags=0x%08x user=%q domain=%q target=%q password_len=%d target_info_bytes=%d nt_response_bytes=%d)",
			ErrNotAuthorized, uint32(confirm.ErrorCode), challenge.Flags, flags,
			cfg.User, domain, challenge.TargetName,
			len(cfg.Password), len(challenge.TargetInfo), len(ntResponse))
	}
	if len(confirm.PubKeyAuth) == 0 {
		return errors.New("credssp: backend sent no public key confirmation")
	}
	got, err := sec.Unwrap(confirm.PubKeyAuth)
	if err != nil {
		return fmt.Errorf("credssp: unwrap backend confirmation: %w", err)
	}
	if !ntlm.ConstantTimeEqual(got, serverBinding(version, nonce, pubKey)) {
		return errors.New("credssp: backend public key binding mismatch")
	}

	creds, err := marshalPasswordCreds(cfg.Domain, cfg.User, cfg.Password)
	if err != nil {
		return err
	}
	return writeTSRequest(rw, tsRequest{Version: version, AuthInfo: sec.Wrap(creds)})
}

// authenticateFlags reduces the server's offer to the subset this implementation
// actually performs. Anything the server did not offer is dropped; the two
// features there is no fallback for are required outright.
func authenticateFlags(serverFlags uint32) (uint32, error) {
	if serverFlags&ntlm.FlagUnicode == 0 {
		return 0, errors.New("credssp: backend did not negotiate unicode")
	}
	if serverFlags&ntlm.FlagExtendedSessionSecurity == 0 {
		return 0, errors.New("credssp: backend did not negotiate extended session security")
	}
	flags := ntlm.FlagUnicode | ntlm.FlagNTLM | ntlm.FlagExtendedSessionSecurity
	for _, f := range []uint32{
		ntlm.FlagRequestTarget, ntlm.FlagSign, ntlm.FlagSeal, ntlm.FlagAlwaysSign,
		// TargetInfo must be echoed: it is how the server knows the response is
		// NTLMv2. Dropping it makes the server compute the proof the v1 way and
		// report the mismatch as a bad password.
		ntlm.FlagTargetInfo, ntlm.FlagTargetTypeServer,
		ntlm.FlagKeyExch, ntlm.Flag128, ntlm.Flag56,
	} {
		if serverFlags&f != 0 {
			flags |= f
		}
	}
	return flags, nil
}
