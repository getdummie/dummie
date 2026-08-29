package credssp

import (
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
)

// MaxTSRequest bounds a single CredSSP message. The largest legitimate one carries
// an AUTHENTICATE plus a wrapped binding hash, comfortably under 8 KiB.
const MaxTSRequest = 8192

// readTSRequest reads exactly one DER SEQUENCE off the wire. It cannot use
// asn1.Unmarshal directly because the transport is a stream with no framing of
// its own: the length has to come from the DER header itself.
func readTSRequest(r io.Reader) (tsRequest, error) {
	var req tsRequest

	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return req, err
	}
	if hdr[0] != 0x30 {
		return req, fmt.Errorf("credssp: expected a DER sequence, got tag 0x%02x", hdr[0])
	}

	buf := []byte{hdr[0], hdr[1]}
	var bodyLen int
	if hdr[1] < 0x80 {
		bodyLen = int(hdr[1])
	} else {
		n := int(hdr[1] & 0x7f)
		if n == 0 || n > 4 {
			return req, errors.New("credssp: unsupported DER length form")
		}
		lb := make([]byte, n)
		if _, err := io.ReadFull(r, lb); err != nil {
			return req, err
		}
		buf = append(buf, lb...)
		for _, b := range lb {
			bodyLen = bodyLen<<8 | int(b)
		}
	}
	if bodyLen < 0 || len(buf)+bodyLen > MaxTSRequest {
		return req, fmt.Errorf("credssp: TSRequest too large (%d bytes)", bodyLen)
	}

	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return req, err
	}
	buf = append(buf, body...)

	rest, err := asn1.Unmarshal(buf, &req)
	if err != nil {
		return req, fmt.Errorf("credssp: parse TSRequest: %w", err)
	}
	if len(rest) != 0 {
		return req, errors.New("credssp: trailing bytes after TSRequest")
	}
	return req, nil
}

func writeTSRequest(w io.Writer, req tsRequest) error {
	b, err := asn1.Marshal(req)
	if err != nil {
		return fmt.Errorf("credssp: marshal TSRequest: %w", err)
	}
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("credssp: write TSRequest: %w", err)
	}
	return nil
}

// writeError reports a failure to the peer in the field CredSSP reserves for it,
// so a client shows a real message instead of a dropped connection.
func writeError(w io.Writer, version int, code int) {
	_ = writeTSRequest(w, tsRequest{Version: version, ErrorCode: code})
}

// SEC_E_LOGON_DENIED, which is 0x8009030C. The field is a signed INTEGER, so the
// HRESULT travels as its two's-complement 32-bit value; writing the hex literal
// instead would not compile, since it does not fit an int32.
const errLogonDenied = -2146893044
