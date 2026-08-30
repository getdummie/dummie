package rdp

import (
	"encoding/binary"
	"fmt"
	"io"
	"unicode/utf16"
)

// RDSTLS (MS-RDPBCGR 2.2.17) is what a client speaks when it reconnects after a
// Server Redirection: X.224 selects PROTOCOL_RDSTLS, TLS comes up, and then
// three messages cross the TLS stream — server capabilities, a client
// authentication request carrying the redirection's one-time credentials, and a
// server result code. Unlike most RDP PDUs these are sent raw, with no TPKT or
// X.224 framing, so each side reads exactly the fields it expects.

const (
	rdstlsVersion1 uint16 = 0x0001

	rdstlsTypeCapabilities uint16 = 0x0001
	rdstlsTypeAuthReq      uint16 = 0x0002
	rdstlsTypeAuthRsp      uint16 = 0x0004

	rdstlsDataCapabilities uint16 = 0x0001
	rdstlsDataResultCode   uint16 = 0x0001

	// RDSTLSDataPasswordCreds and RDSTLSDataAutoReconnectCookie are the two
	// shapes an authentication request comes in.
	RDSTLSDataPasswordCreds       uint16 = 0x0001
	RDSTLSDataAutoReconnectCookie uint16 = 0x0002

	// MaxRDSTLSBlob bounds one length-prefixed field. The password is the
	// largest and is a public-key blob of a few hundred bytes.
	MaxRDSTLSBlob = 8192
)

// Authentication response result codes (MS-RDPBCGR 2.2.17.4).
const (
	RDSTLSSuccess      uint32 = 0x00000000
	RDSTLSAccessDenied uint32 = 0x00000005
)

// WriteRDSTLSCapabilities sends the server's opening PDU.
func WriteRDSTLSCapabilities(w io.Writer) error {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint16(b[0:], rdstlsVersion1)
	binary.LittleEndian.PutUint16(b[2:], rdstlsTypeCapabilities)
	binary.LittleEndian.PutUint16(b[4:], rdstlsDataCapabilities)
	binary.LittleEndian.PutUint16(b[6:], rdstlsVersion1)
	_, err := w.Write(b)
	return err
}

// ReadRDSTLSCapabilities reads a backend's opening PDU.
func ReadRDSTLSCapabilities(r io.Reader) error {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return err
	}
	if v := binary.LittleEndian.Uint16(b[0:]); v != rdstlsVersion1 {
		return fmt.Errorf("rdp: rdstls version %d", v)
	}
	if t := binary.LittleEndian.Uint16(b[2:]); t != rdstlsTypeCapabilities {
		return fmt.Errorf("rdp: expected rdstls capabilities, got type 0x%04x", t)
	}
	if d := binary.LittleEndian.Uint16(b[4:]); d != rdstlsDataCapabilities {
		return fmt.Errorf("rdp: unexpected rdstls capabilities data type 0x%04x", d)
	}
	if binary.LittleEndian.Uint16(b[6:])&rdstlsVersion1 == 0 {
		return fmt.Errorf("rdp: backend does not support rdstls version 1")
	}
	return nil
}

// RDSTLSAuthRequest is the client's authentication request. The credentials are
// minted by the guest during redirection and verified by it, so the proxy only
// ever moves them: Raw holds the PDU exactly as it arrived.
type RDSTLSAuthRequest struct {
	DataType uint16

	// Password credentials. The strings are UTF-16LE as sent, including their
	// terminators; Password is a public-key blob when the redirection set
	// LB_PASSWORD_IS_PK_ENCRYPTED.
	RedirectionGUID []byte
	UserName        []byte
	Domain          []byte
	Password        []byte

	// Auto-reconnect cookie.
	SessionID           uint32
	AutoReconnectCookie []byte

	Raw []byte
}

// ReadRDSTLSAuthRequest reads a client's authentication request.
func ReadRDSTLSAuthRequest(r io.Reader) (*RDSTLSAuthRequest, error) {
	var h [6]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	if v := binary.LittleEndian.Uint16(h[0:]); v != rdstlsVersion1 {
		return nil, fmt.Errorf("rdp: rdstls version %d", v)
	}
	if t := binary.LittleEndian.Uint16(h[2:]); t != rdstlsTypeAuthReq {
		return nil, fmt.Errorf("rdp: expected rdstls authentication request, got type 0x%04x", t)
	}

	req := &RDSTLSAuthRequest{
		DataType: binary.LittleEndian.Uint16(h[4:]),
		Raw:      append([]byte(nil), h[:]...),
	}
	switch req.DataType {
	case RDSTLSDataPasswordCreds:
		for _, dst := range []*[]byte{&req.RedirectionGUID, &req.UserName, &req.Domain, &req.Password} {
			b, err := readRDSTLSBlob(r, &req.Raw)
			if err != nil {
				return nil, err
			}
			*dst = b
		}
	case RDSTLSDataAutoReconnectCookie:
		var sid [4]byte
		if _, err := io.ReadFull(r, sid[:]); err != nil {
			return nil, err
		}
		req.SessionID = binary.LittleEndian.Uint32(sid[:])
		req.Raw = append(req.Raw, sid[:]...)
		b, err := readRDSTLSBlob(r, &req.Raw)
		if err != nil {
			return nil, err
		}
		req.AutoReconnectCookie = b
	default:
		return nil, fmt.Errorf("rdp: unknown rdstls data type 0x%04x", req.DataType)
	}
	return req, nil
}

func readRDSTLSBlob(r io.Reader, raw *[]byte) ([]byte, error) {
	var n [2]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	l := int(binary.LittleEndian.Uint16(n[:]))
	if l > MaxRDSTLSBlob {
		return nil, fmt.Errorf("rdp: rdstls field of %d bytes", l)
	}
	b := make([]byte, l)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	*raw = append(*raw, n[:]...)
	*raw = append(*raw, b...)
	return b, nil
}

// WriteRDSTLSAuthResponse answers an authentication request.
func WriteRDSTLSAuthResponse(w io.Writer, code uint32) error {
	b := make([]byte, 10)
	binary.LittleEndian.PutUint16(b[0:], rdstlsVersion1)
	binary.LittleEndian.PutUint16(b[2:], rdstlsTypeAuthRsp)
	binary.LittleEndian.PutUint16(b[4:], rdstlsDataResultCode)
	binary.LittleEndian.PutUint32(b[6:], code)
	_, err := w.Write(b)
	return err
}

// ReadRDSTLSAuthResponse reads a backend's verdict and returns its result code.
func ReadRDSTLSAuthResponse(r io.Reader) (uint32, error) {
	var b [10]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	if v := binary.LittleEndian.Uint16(b[0:]); v != rdstlsVersion1 {
		return 0, fmt.Errorf("rdp: rdstls version %d", v)
	}
	if t := binary.LittleEndian.Uint16(b[2:]); t != rdstlsTypeAuthRsp {
		return 0, fmt.Errorf("rdp: expected rdstls authentication response, got type 0x%04x", t)
	}
	if d := binary.LittleEndian.Uint16(b[4:]); d != rdstlsDataResultCode {
		return 0, fmt.Errorf("rdp: unexpected rdstls response data type 0x%04x", d)
	}
	return binary.LittleEndian.Uint32(b[6:]), nil
}

// RDSTLSString decodes one of the UTF-16LE fields for logging.
func RDSTLSString(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		v := binary.LittleEndian.Uint16(b[i:])
		if v == 0 {
			break
		}
		u = append(u, v)
	}
	return string(utf16.Decode(u))
}
