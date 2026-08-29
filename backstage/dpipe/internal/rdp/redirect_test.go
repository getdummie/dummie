package rdp

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// redirectPDU builds a Server Redirection PDU carrying loadBalance, shaped the
// way gnome-remote-desktop sends one: LB_LOAD_BALANCE_INFO followed by
// LB_USERNAME, no target net address.
func redirectPDU(loadBalance, username []byte) []byte {
	var body bytes.Buffer

	body.Write([]byte{2, 0xF0, 0x80})               // X.224 data
	body.Write([]byte{mcsSendDataIndication << 2})  // MCS choice
	body.Write([]byte{0, 8, 0x03, 0xEB, 0x70})      // initiator, channelId, priority
	body.Write([]byte{0x80, 0x00})                  // two-byte per length

	share := make([]byte, 6)
	binary.LittleEndian.PutUint16(share[2:], 0x0010|pduTypeServerRedirection)
	body.Write(share)

	// Two bytes, then flags, length, sessionID, redirFlags.
	hdr := make([]byte, 14)
	binary.LittleEndian.PutUint16(hdr[2:], secRedirectionPkt)
	binary.LittleEndian.PutUint32(hdr[10:], lbLoadBalanceInfo|0x00000004)
	body.Write(hdr)

	for _, f := range [][]byte{loadBalance, username} {
		n := make([]byte, 4)
		binary.LittleEndian.PutUint32(n, uint32(len(f)))
		body.Write(n)
		body.Write(f)
	}

	out := make([]byte, tpktHeader, tpktHeader+body.Len())
	out[0] = tpktVersion
	binary.BigEndian.PutUint16(out[2:], uint16(tpktHeader+body.Len()))
	return append(out, body.Bytes()...)
}

// fastPathPDU builds a server output PDU, which is what most of the stream is
// once the session is active.
func fastPathPDU(n int) []byte {
	out := make([]byte, n+2)
	out[1] = byte(n + 2)
	return out
}

func TestRedirectScannerFindsLoadBalanceInfo(t *testing.T) {
	want := []byte("dum-vm-session-42")
	pdu := redirectPDU(want, []byte("x<@a12"))

	var s RedirectScanner
	if got := s.Scan(fastPathPDU(40)); got != nil {
		t.Fatalf("fast-path output reported as a redirect: %q", got)
	}
	got := s.Scan(pdu)
	if !bytes.Equal(got, want) {
		t.Fatalf("load balance info = %q, want %q", got, want)
	}
	if s.Scan(pdu) != nil {
		t.Fatal("scanner reported a second redirect after going inert")
	}
}

func TestRedirectScannerAcrossChunkBoundaries(t *testing.T) {
	want := []byte("dum-vm-session-42")
	stream := append(fastPathPDU(9), redirectPDU(want, []byte("u"))...)

	var s RedirectScanner
	var got []byte
	for i := 0; i < len(stream); i++ {
		if v := s.Scan(stream[i : i+1]); v != nil {
			got = v
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("load balance info = %q, want %q", got, want)
	}
}

func TestRedirectScannerIgnoresOrdinaryPDUs(t *testing.T) {
	// The same frame with a share control type other than server redirection.
	plain := redirectPDU([]byte("x"), nil)
	binary.LittleEndian.PutUint16(plain[tpktHeader+13:], 0x0017)

	var s RedirectScanner
	if got := s.Scan(plain); got != nil {
		t.Fatalf("ordinary PDU reported as a redirect: %q", got)
	}
}

// captured is the header of a real gnome-remote-desktop redirection PDU, taken
// off the wire up to the end of its load balance info. The offsets in this
// package were wrong until they were checked against it, so it is the test that
// matters.
const captured = "03000e86" + "02f080" + "68" + "0008" + "03eb" + "70" + "8e77" +
	"770e" + "1a00" + "0000" + "0000" + "0004" + "6f0e" + "00000000" + "16c00100" +
	"19000000" + "436f6f6b69653a206d737473"

func TestRedirectScannerAgainstCapturedFrame(t *testing.T) {
	head, err := hex.DecodeString(captured)
	if err != nil {
		t.Fatal(err)
	}
	frame := append(head, []byte("=1704979740\r\n")...)
	// The real frame runs to its TPKT length; the tail is the credentials and
	// certificate, which the scanner never looks at.
	frame = append(frame, make([]byte, 3718-len(frame))...)

	var s RedirectScanner
	got := s.Scan(frame)
	if string(got) != "Cookie: msts=1704979740\r\n" {
		t.Fatalf("load balance info = %q", got)
	}
}
