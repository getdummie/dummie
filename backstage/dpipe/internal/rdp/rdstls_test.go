package rdp

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

func utf16z(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2+2)
	for _, v := range u {
		b = binary.LittleEndian.AppendUint16(b, v)
	}
	return append(b, 0, 0)
}

func passwordCredsPDU(guid, user, domain, password []byte) []byte {
	b := make([]byte, 0, 64)
	b = binary.LittleEndian.AppendUint16(b, rdstlsVersion1)
	b = binary.LittleEndian.AppendUint16(b, rdstlsTypeAuthReq)
	b = binary.LittleEndian.AppendUint16(b, RDSTLSDataPasswordCreds)
	for _, f := range [][]byte{guid, user, domain, password} {
		b = binary.LittleEndian.AppendUint16(b, uint16(len(f)))
		b = append(b, f...)
	}
	return b
}

func TestReadRDSTLSAuthRequestPasswordCreds(t *testing.T) {
	guid := bytes.Repeat([]byte{0xAB}, 16)
	pdu := passwordCredsPDU(guid, utf16z("ubuntu"), utf16z(""), []byte("opaque-blob"))

	req, err := ReadRDSTLSAuthRequest(bytes.NewReader(pdu))
	if err != nil {
		t.Fatal(err)
	}
	if req.DataType != RDSTLSDataPasswordCreds {
		t.Fatalf("data type = 0x%04x", req.DataType)
	}
	if !bytes.Equal(req.RedirectionGUID, guid) {
		t.Fatalf("redirection guid = %x", req.RedirectionGUID)
	}
	if got := RDSTLSString(req.UserName); got != "ubuntu" {
		t.Fatalf("user name = %q", got)
	}
	if got := RDSTLSString(req.Domain); got != "" {
		t.Fatalf("domain = %q", got)
	}
	if string(req.Password) != "opaque-blob" {
		t.Fatalf("password = %q", req.Password)
	}
	// The backend leg replays Raw, so it has to be the whole PDU and nothing else.
	if !bytes.Equal(req.Raw, pdu) {
		t.Fatalf("raw = %x, want %x", req.Raw, pdu)
	}
}

func TestReadRDSTLSAuthRequestAutoReconnectCookie(t *testing.T) {
	cookie := bytes.Repeat([]byte{0x11}, 28)
	b := make([]byte, 0, 64)
	b = binary.LittleEndian.AppendUint16(b, rdstlsVersion1)
	b = binary.LittleEndian.AppendUint16(b, rdstlsTypeAuthReq)
	b = binary.LittleEndian.AppendUint16(b, RDSTLSDataAutoReconnectCookie)
	b = binary.LittleEndian.AppendUint32(b, 0xDEADBEEF)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(cookie)))
	b = append(b, cookie...)

	req, err := ReadRDSTLSAuthRequest(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if req.SessionID != 0xDEADBEEF {
		t.Fatalf("session id = 0x%08x", req.SessionID)
	}
	if !bytes.Equal(req.AutoReconnectCookie, cookie) {
		t.Fatalf("cookie = %x", req.AutoReconnectCookie)
	}
	if !bytes.Equal(req.Raw, b) {
		t.Fatalf("raw = %x, want %x", req.Raw, b)
	}
}

// A request cut short must not be read as a valid one: the credentials go
// straight to the guest, so a partial read would forward garbage.
func TestReadRDSTLSAuthRequestTruncated(t *testing.T) {
	pdu := passwordCredsPDU(bytes.Repeat([]byte{1}, 16), utf16z("ubuntu"), nil, []byte("pw"))
	for n := 1; n < len(pdu); n++ {
		if _, err := ReadRDSTLSAuthRequest(bytes.NewReader(pdu[:n])); err == nil {
			t.Fatalf("truncation to %d bytes accepted", n)
		}
	}
}

func TestRDSTLSCapabilitiesRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRDSTLSCapabilities(&buf); err != nil {
		t.Fatal(err)
	}
	if err := ReadRDSTLSCapabilities(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
}

func TestRDSTLSAuthResponseRoundTrip(t *testing.T) {
	for _, want := range []uint32{RDSTLSSuccess, RDSTLSAccessDenied, 0x0000052e} {
		var buf bytes.Buffer
		if err := WriteRDSTLSAuthResponse(&buf, want); err != nil {
			t.Fatal(err)
		}
		got, err := ReadRDSTLSAuthResponse(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("result code = 0x%08x, want 0x%08x", got, want)
		}
	}
}

// The capabilities PDU and an authentication response share a version and differ
// only in type, so each reader has to reject the other's PDU.
func TestRDSTLSReadersRejectTheWrongPDU(t *testing.T) {
	var caps, rsp bytes.Buffer
	if err := WriteRDSTLSCapabilities(&caps); err != nil {
		t.Fatal(err)
	}
	if err := WriteRDSTLSAuthResponse(&rsp, RDSTLSSuccess); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRDSTLSAuthResponse(bytes.NewReader(append(caps.Bytes(), 0, 0))); err == nil {
		t.Fatal("capabilities accepted as an authentication response")
	}
	if err := ReadRDSTLSCapabilities(bytes.NewReader(rsp.Bytes())); err == nil {
		t.Fatal("authentication response accepted as capabilities")
	}
}
