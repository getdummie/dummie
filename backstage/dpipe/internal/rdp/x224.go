// Package rdp implements the X.224 connection exchange that precedes TLS on an
// RDP connection. It stops there: once the security protocol is agreed, nothing
// in this package looks at the stream again.
package rdp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Security protocols a client may request in the negotiation request
// (MS-RDPBCGR 2.2.1.1.1).
const (
	ProtocolRDP      uint32 = 0x00000000
	ProtocolSSL      uint32 = 0x00000001
	ProtocolHybrid   uint32 = 0x00000002
	ProtocolHybridEx uint32 = 0x00000008
)

const (
	tpktVersion = 3
	tpktHeader  = 4

	crCDT = 0xE0
	ccCDT = 0xD0

	negReqType     = 0x01
	negRspType     = 0x02
	negFailureType = 0x03
	negLen         = 8

	// MaxPDU bounds a connection PDU. These are tens of bytes in practice; the
	// cookie is the only variable part and RDP caps it far below this.
	MaxPDU = 4096
)

// ConnectionRequest is the client's opening PDU.
type ConnectionRequest struct {
	// Cookie is the `Cookie: mstshash=…` or routing token line, without its
	// trailing CRLF. Empty when the client sent none.
	Cookie string
	// RequestedProtocols is zero when the client sent no negotiation request,
	// which means it only speaks legacy RDP security.
	RequestedProtocols uint32
	// HasNegotiation distinguishes "requested plain RDP" from "sent no request".
	HasNegotiation bool
	SrcRef         uint16
	// Raw is the whole TPKT frame, kept so a connection that turns out to be
	// someone else's to answer can be replayed to them unchanged.
	Raw []byte
}

func readTPKT(r io.Reader) ([]byte, error) {
	var h [tpktHeader]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	if h[0] != tpktVersion {
		return nil, fmt.Errorf("rdp: bad TPKT version %d", h[0])
	}
	n := int(binary.BigEndian.Uint16(h[2:]))
	if n < tpktHeader || n > MaxPDU {
		return nil, fmt.Errorf("rdp: bad TPKT length %d", n)
	}
	body := make([]byte, n-tpktHeader)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeTPKT(w io.Writer, body []byte) error {
	buf := make([]byte, tpktHeader, tpktHeader+len(body))
	buf[0] = tpktVersion
	binary.BigEndian.PutUint16(buf[2:], uint16(tpktHeader+len(body)))
	buf = append(buf, body...)
	_, err := w.Write(buf)
	return err
}

// ReadConnectionRequest reads the client's X.224 Connection Request.
func ReadConnectionRequest(r io.Reader) (*ConnectionRequest, error) {
	body, err := readTPKT(r)
	if err != nil {
		return nil, err
	}
	// li(1) code(1) dst-ref(2) src-ref(2) class(1)
	if len(body) < 7 {
		return nil, errors.New("rdp: connection request too short")
	}
	if body[1] != crCDT {
		return nil, fmt.Errorf("rdp: expected connection request, got code 0x%02x", body[1])
	}
	raw := make([]byte, tpktHeader, tpktHeader+len(body))
	raw[0] = tpktVersion
	binary.BigEndian.PutUint16(raw[2:], uint16(tpktHeader+len(body)))
	raw = append(raw, body...)

	req := &ConnectionRequest{SrcRef: binary.BigEndian.Uint16(body[4:]), Raw: raw}

	rest := body[7:]
	// A cookie or routing token, when present, runs to the first CRLF.
	if i := indexCRLF(rest); i >= 0 {
		req.Cookie = string(rest[:i])
		rest = rest[i+2:]
	}
	if len(rest) >= negLen && rest[0] == negReqType {
		req.HasNegotiation = true
		req.RequestedProtocols = binary.LittleEndian.Uint32(rest[4:])
	}
	return req, nil
}

func indexCRLF(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == '\r' && b[i+1] == '\n' {
			return i
		}
	}
	return -1
}

// Negotiation response flags (MS-RDPBCGR 2.2.1.2.1). The client mirrors these
// into its GCC client core data: without DynVCGFXProtocolSupported it never sets
// RNS_UD_CS_SUPPORT_DYNVC_GFX_PROTOCOL, and a server that requires the graphics
// pipeline then drops the connection during licensing.
const (
	ExtendedClientDataSupported byte = 0x01
	DynVCGFXProtocolSupported   byte = 0x02
)

// WriteConnectionConfirm answers a Connection Request with the protocol this side
// selected and the negotiation flags it advertises.
func WriteConnectionConfirm(w io.Writer, srcRef uint16, flags byte, protocol uint32) error {
	body := make([]byte, 7, 7+negLen)
	body[1] = ccCDT
	binary.BigEndian.PutUint16(body[2:], srcRef)
	body[0] = byte(len(body) - 1 + negLen)

	neg := make([]byte, negLen)
	neg[0] = negRspType
	neg[1] = flags
	binary.LittleEndian.PutUint16(neg[2:], negLen)
	binary.LittleEndian.PutUint32(neg[4:], protocol)
	return writeTPKT(w, append(body, neg...))
}

// NegotiationFailure codes (MS-RDPBCGR 2.2.1.2.2).
const (
	FailSSLRequiredByServer      uint32 = 0x00000001
	FailSSLNotAllowedByServer    uint32 = 0x00000002
	FailHybridRequiredByServer   uint32 = 0x00000005
	FailInconsistentFlags        uint32 = 0x00000004
	FailSSLWithUserAuthRequired  uint32 = 0x00000006
)

// WriteConnectionFailure refuses the negotiation with a reason the client can
// render, rather than dropping the connection.
func WriteConnectionFailure(w io.Writer, srcRef uint16, code uint32) error {
	body := make([]byte, 7, 7+negLen)
	body[1] = ccCDT
	binary.BigEndian.PutUint16(body[2:], srcRef)
	body[0] = byte(len(body) - 1 + negLen)

	neg := make([]byte, negLen)
	neg[0] = negFailureType
	binary.LittleEndian.PutUint16(neg[2:], negLen)
	binary.LittleEndian.PutUint32(neg[4:], code)
	return writeTPKT(w, append(body, neg...))
}

// WriteConnectionRequest sends a Connection Request to a backend.
func WriteConnectionRequest(w io.Writer, cookie string, protocols uint32) error {
	body := make([]byte, 7)
	body[1] = crCDT
	if cookie != "" {
		body = append(body, cookie...)
		body = append(body, '\r', '\n')
	}
	neg := make([]byte, negLen)
	neg[0] = negReqType
	binary.LittleEndian.PutUint16(neg[2:], negLen)
	binary.LittleEndian.PutUint32(neg[4:], protocols)
	body = append(body, neg...)
	body[0] = byte(len(body) - 1)
	return writeTPKT(w, body)
}

// ReadConnectionConfirm reads a backend's answer and returns the protocol it
// selected.
func ReadConnectionConfirm(r io.Reader) (uint32, error) {
	body, err := readTPKT(r)
	if err != nil {
		return 0, err
	}
	if len(body) < 7 {
		return 0, errors.New("rdp: connection confirm too short")
	}
	if body[1] != ccCDT {
		return 0, fmt.Errorf("rdp: expected connection confirm, got code 0x%02x", body[1])
	}
	rest := body[7:]
	if len(rest) < negLen {
		// No negotiation response means the backend fell back to legacy RDP security.
		return ProtocolRDP, nil
	}
	switch rest[0] {
	case negRspType:
		return binary.LittleEndian.Uint32(rest[4:]), nil
	case negFailureType:
		return 0, fmt.Errorf("rdp: backend refused negotiation (code %d)",
			binary.LittleEndian.Uint32(rest[4:]))
	default:
		return 0, fmt.Errorf("rdp: unexpected negotiation response type 0x%02x", rest[0])
	}
}
