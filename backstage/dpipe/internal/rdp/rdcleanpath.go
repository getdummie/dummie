package rdp

import (
	"encoding/asn1"
	"errors"
	"fmt"
)

// RDCleanPath is how IronRDP's browser client reaches an RDP server through a
// websocket: it cannot open TLS itself, so the proxy does the X.224 exchange and
// TLS on its behalf and hands back the server's certificate chain for CredSSP.
// This matches crates/ironrdp-rdcleanpath (DER, EXPLICIT context tags).
const RDCleanPathVersion = 3390

const (
	RDCleanPathErrGeneral     = 1
	RDCleanPathErrNegotiation = 2
)

// MaxRDCleanPathRequest bounds the request PDU; it carries a few strings and the
// X.224 request, so anything near this is not a client.
const MaxRDCleanPathRequest = 64 << 10

type RDCleanPathRequest struct {
	Version     int64         `asn1:"explicit,tag:0"`
	Error       asn1.RawValue `asn1:"optional,explicit,tag:1"`
	Destination string        `asn1:"optional,explicit,tag:2,utf8"`
	ProxyAuth   string        `asn1:"optional,explicit,tag:3,utf8"`
	ServerAuth  string        `asn1:"optional,explicit,tag:4,utf8"`
	PCB         string        `asn1:"optional,explicit,tag:5,utf8"`
	X224        []byte        `asn1:"optional,explicit,tag:6"`
	CertChain   asn1.RawValue `asn1:"optional,explicit,tag:7"`
	ServerAddr  string        `asn1:"optional,explicit,tag:9,utf8"`
}

func ParseRDCleanPathRequest(b []byte) (*RDCleanPathRequest, error) {
	var req RDCleanPathRequest
	rest, err := asn1.Unmarshal(b, &req)
	if err != nil {
		return nil, fmt.Errorf("rdcleanpath: decode: %w", err)
	}
	if len(rest) != 0 {
		return nil, errors.New("rdcleanpath: trailing bytes")
	}
	if req.Version != RDCleanPathVersion {
		return nil, fmt.Errorf("rdcleanpath: unsupported version %d", req.Version)
	}
	if len(req.X224) == 0 {
		return nil, errors.New("rdcleanpath: request carries no X.224 PDU")
	}
	return &req, nil
}

func derTLV(tag byte, content []byte) []byte {
	out := []byte{tag}
	switch n := len(content); {
	case n < 0x80:
		out = append(out, byte(n))
	case n <= 0xff:
		out = append(out, 0x81, byte(n))
	case n <= 0xffff:
		out = append(out, 0x82, byte(n>>8), byte(n))
	default:
		out = append(out, 0x83, byte(n>>16), byte(n>>8), byte(n))
	}
	return append(out, content...)
}

func derInt(v int64) []byte {
	b, _ := asn1.Marshal(v)
	return b
}

func explicit(tag int, inner []byte) []byte { return derTLV(0xa0|byte(tag), inner) }

func derSeq(parts ...[]byte) []byte {
	var body []byte
	for _, p := range parts {
		body = append(body, p...)
	}
	return derTLV(0x30, body)
}

func EncodeRDCleanPathResponse(serverAddr string, x224 []byte, certChain [][]byte) []byte {
	var certs []byte
	for _, c := range certChain {
		certs = append(certs, derTLV(0x04, c)...)
	}
	return derSeq(
		explicit(0, derInt(RDCleanPathVersion)),
		explicit(6, derTLV(0x04, x224)),
		explicit(7, derTLV(0x30, certs)),
		explicit(9, derTLV(0x0c, []byte(serverAddr))),
	)
}

// EncodeRDCleanPathError builds an error PDU; httpStatus 0 leaves it out.
func EncodeRDCleanPathError(code, httpStatus int64) []byte {
	body := explicit(0, derInt(code))
	if httpStatus != 0 {
		body = append(body, explicit(1, derInt(httpStatus))...)
	}
	return derSeq(
		explicit(0, derInt(RDCleanPathVersion)),
		explicit(1, derTLV(0x30, body)),
	)
}
