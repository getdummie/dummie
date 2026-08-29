package credssp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"

	"dpipe/internal/ntlm"
)

// Version is the highest CredSSP version this implementation speaks. Versions 5
// and up bind pubKeyAuth through a client-supplied nonce; below that the bare
// public key is wrapped and the server replies with the key plus one.
const Version = 6

const (
	// nonceVersion is the first CredSSP version using the hashed binding.
	nonceVersion = 5
	nonceLen     = 32
)

type negoToken struct {
	Token []byte `asn1:"explicit,tag:0"`
}

// tsRequest is MS-CSSP 2.2.1. Go's asn1 marshaller omits optional fields holding
// their zero value, so one struct serves both directions.
type tsRequest struct {
	Version     int         `asn1:"explicit,tag:0"`
	NegoTokens  []negoToken `asn1:"explicit,optional,tag:1"`
	AuthInfo    []byte      `asn1:"explicit,optional,tag:2"`
	PubKeyAuth  []byte      `asn1:"explicit,optional,tag:3"`
	ErrorCode   int         `asn1:"explicit,optional,tag:4"`
	ClientNonce []byte      `asn1:"explicit,optional,tag:5"`
}

type tsPasswordCreds struct {
	DomainName []byte `asn1:"explicit,tag:0"`
	UserName   []byte `asn1:"explicit,tag:1"`
	Password   []byte `asn1:"explicit,tag:2"`
}

type tsCredentials struct {
	CredType    int    `asn1:"explicit,tag:0"`
	Credentials []byte `asn1:"explicit,tag:1"`
}

func (r *tsRequest) token() ([]byte, error) {
	if len(r.NegoTokens) == 0 {
		return nil, errors.New("credssp: TSRequest carried no nego token")
	}
	t := r.NegoTokens[0].Token
	if len(t) > ntlm.MaxNTLMMessage {
		return nil, fmt.Errorf("credssp: nego token too large (%d bytes)", len(t))
	}
	return t, nil
}

func requestWithToken(version int, token []byte) tsRequest {
	return tsRequest{Version: version, NegoTokens: []negoToken{{Token: token}}}
}

// marshalPasswordCreds builds the TSCredentials blob delegated to the far end.
func marshalPasswordCreds(domain, user, password string) ([]byte, error) {
	inner, err := asn1.Marshal(tsPasswordCreds{
		DomainName: ntlm.UTF16LE(domain),
		UserName:   ntlm.UTF16LE(user),
		Password:   ntlm.UTF16LE(password),
	})
	if err != nil {
		return nil, fmt.Errorf("credssp: marshal password creds: %w", err)
	}
	// credType 1 == password credentials
	b, err := asn1.Marshal(tsCredentials{CredType: 1, Credentials: inner})
	if err != nil {
		return nil, fmt.Errorf("credssp: marshal credentials: %w", err)
	}
	return b, nil
}

// PublicKey returns the certificate's public key in the form CredSSP binds to:
// the bare key, not the SubjectPublicKeyInfo wrapper. This matches OpenSSL's
// i2d_PublicKey, which is what every RDP implementation in the wild uses.
func PublicKey(cert *x509.Certificate) ([]byte, error) {
	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		return x509.MarshalPKCS1PublicKey(pub), nil
	case *ecdsa.PublicKey:
		return elliptic.Marshal(pub.Curve, pub.X, pub.Y), nil
	default:
		return nil, fmt.Errorf("credssp: unsupported certificate key type %T", cert.PublicKey)
	}
}

const (
	clientBindingMagic = "CredSSP Client-To-Server Binding Hash\x00"
	serverBindingMagic = "CredSSP Server-To-Client Binding Hash\x00"
)

func bindingHash(magic string, nonce, pubKey []byte) []byte {
	h := sha256.New()
	h.Write([]byte(magic))
	h.Write(nonce)
	h.Write(pubKey)
	return h.Sum(nil)
}

// clientBinding is the plaintext a client wraps into pubKeyAuth.
func clientBinding(version int, nonce, pubKey []byte) []byte {
	if version >= nonceVersion && len(nonce) > 0 {
		return bindingHash(clientBindingMagic, nonce, pubKey)
	}
	return pubKey
}

// serverBinding is the plaintext a server wraps back. Before the nonce versions
// the server proves possession by incrementing the first byte of the public key.
func serverBinding(version int, nonce, pubKey []byte) []byte {
	if version >= nonceVersion && len(nonce) > 0 {
		return bindingHash(serverBindingMagic, nonce, pubKey)
	}
	inc := make([]byte, len(pubKey))
	copy(inc, pubKey)
	if len(inc) > 0 {
		inc[0]++
	}
	return inc
}
