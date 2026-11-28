package rdp

import (
	"bytes"
	"encoding/asn1"
	"testing"
)

func TestRDCleanPathRequestRoundTrip(t *testing.T) {
	x224 := []byte{3, 0, 0, 11, 6, 0xe0, 0, 0, 0, 0, 0}
	req := derSeq(
		explicit(0, derInt(RDCleanPathVersion)),
		explicit(2, derTLV(0x0c, []byte("vm:3389"))),
		explicit(3, derTLV(0x0c, []byte("secret"))),
		explicit(6, derTLV(0x04, x224)),
	)
	got, err := ParseRDCleanPathRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Destination != "vm:3389" || got.ProxyAuth != "secret" || !bytes.Equal(got.X224, x224) {
		t.Fatalf("bad parse: %+v", got)
	}
}

func TestRDCleanPathRequestRejectsWrongVersion(t *testing.T) {
	req := derSeq(explicit(0, derInt(1)), explicit(6, derTLV(0x04, []byte{3})))
	if _, err := ParseRDCleanPathRequest(req); err == nil {
		t.Fatal("expected a version error")
	}
}

func TestRDCleanPathResponseDecodes(t *testing.T) {
	big := bytes.Repeat([]byte{0xab}, 1500)
	resp := EncodeRDCleanPathResponse("10.0.0.2:3389", []byte{3, 0, 0, 4}, [][]byte{big, {1, 2}})
	var out struct {
		Version    int64    `asn1:"explicit,tag:0"`
		X224       []byte   `asn1:"optional,explicit,tag:6"`
		Chain      [][]byte `asn1:"optional,explicit,tag:7"`
		ServerAddr string   `asn1:"optional,explicit,tag:9,utf8"`
	}
	if _, err := asn1.Unmarshal(resp, &out); err != nil {
		t.Fatal(err)
	}
	if out.Version != RDCleanPathVersion || len(out.Chain) != 2 || !bytes.Equal(out.Chain[0], big) || out.ServerAddr != "10.0.0.2:3389" {
		t.Fatalf("bad decode: %+v", out)
	}

	var e struct {
		Version int64 `asn1:"explicit,tag:0"`
		Err     struct {
			Code int64 `asn1:"explicit,tag:0"`
			HTTP int64 `asn1:"optional,explicit,tag:1"`
		} `asn1:"explicit,tag:1"`
	}
	if _, err := asn1.Unmarshal(EncodeRDCleanPathError(1, 403), &e); err != nil || e.Err.Code != 1 || e.Err.HTTP != 403 {
		t.Fatalf("bad error pdu: %+v %v", e, err)
	}
}
