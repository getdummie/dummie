// Package ntlm implements the subset of NTLM (MS-NLMP) that RDP Network Level
// Authentication needs: the three message types, NTLMv2 response computation and
// verification, and the message-protection context CredSSP wraps its bindings in.
//
// SHARED between dpipe and dproxy. dpipe drives the handshake; dproxy verifies
// responses against a stored NT hash. Keep the two copies identical: this file has
// no module-prefixed imports, so it copies across verbatim.
package ntlm

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/crypto/md4"
)

var signature = []byte("NTLMSSP\x00")

const (
	msgNegotiate    = 1
	msgChallenge    = 2
	msgAuthenticate = 3
)

// Negotiate flags (MS-NLMP 2.2.2.5). Only the ones this implementation acts on.
// Typed, so that combining them infers uint32 rather than int.
const (
	FlagUnicode                 uint32 = 0x00000001
	FlagOEM                     uint32 = 0x00000002
	FlagRequestTarget           uint32 = 0x00000004
	FlagSign                    uint32 = 0x00000010
	FlagSeal                    uint32 = 0x00000020
	FlagNTLM                    uint32 = 0x00000200
	FlagAlwaysSign              uint32 = 0x00008000
	FlagTargetTypeServer        uint32 = 0x00020000
	FlagExtendedSessionSecurity uint32 = 0x00080000
	FlagTargetInfo              uint32 = 0x00800000
	FlagVersion                 uint32 = 0x02000000
	Flag128                     uint32 = 0x20000000
	FlagKeyExch                 uint32 = 0x40000000
	Flag56                      uint32 = 0x80000000
)

// AV_PAIR ids (MS-NLMP 2.2.2.1).
const (
	avEOL             = 0x0000
	avNbComputerName  = 0x0001
	avNbDomainName    = 0x0002
	avDNSComputerName = 0x0003
	avDNSDomainName   = 0x0004
	avTimestamp       = 0x0007
	avFlags           = 0x0006
	avChannelBindings = 0x000a
	avTargetName      = 0x0009
)

// The ids a client has to act on, exported for callers building a response.
const (
	AvTimestamp       uint16 = avTimestamp
	AvFlags           uint16 = avFlags
	AvChannelBindings uint16 = avChannelBindings
	AvTargetName      uint16 = avTargetName
)

// AvFlagMIC marks that the AUTHENTICATE carries a message integrity check. A
// server that sent a timestamp expects this pairing; sending a MIC without it,
// or the reverse, is what makes a correct response get refused.
const AvFlagMIC uint32 = 0x00000002

type AVPair struct {
	ID    uint16
	Value []byte
}

// AVPairValue returns the value of the first pair with the given id.
func AVPairValue(info []byte, id uint16) ([]byte, bool) {
	for len(info) >= 4 {
		got := binary.LittleEndian.Uint16(info)
		n := int(binary.LittleEndian.Uint16(info[2:]))
		if len(info) < 4+n {
			return nil, false
		}
		if got == avEOL {
			return nil, false
		}
		if got == id {
			return info[4 : 4+n], true
		}
		info = info[4+n:]
	}
	return nil, false
}

// AVPairsAppend inserts pairs ahead of the terminator, leaving the server's own
// pairs untouched and in order.
func AVPairsAppend(info []byte, pairs ...AVPair) []byte {
	body := info
	if i := avEOLOffset(info); i >= 0 {
		body = info[:i]
	}
	out := make([]byte, 0, len(body)+len(pairs)*24+4)
	out = append(out, body...)
	for _, p := range pairs {
		var h [4]byte
		binary.LittleEndian.PutUint16(h[0:], p.ID)
		binary.LittleEndian.PutUint16(h[2:], uint16(len(p.Value)))
		out = append(out, h[:]...)
		out = append(out, p.Value...)
	}
	return append(out, 0, 0, 0, 0)
}

func avEOLOffset(info []byte) int {
	off := 0
	for off+4 <= len(info) {
		id := binary.LittleEndian.Uint16(info[off:])
		n := int(binary.LittleEndian.Uint16(info[off+2:]))
		if id == avEOL {
			return off
		}
		if off+4+n > len(info) {
			return -1
		}
		off += 4 + n
	}
	return -1
}

// LE32 encodes a little-endian uint32, the form every AV_PAIR flag field takes.
func LE32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

// MaxNTLMMessage bounds every NTLM blob this package will parse or emit. The
// wire protocol carries these base64-encoded inside a 4096 byte control message,
// so an oversize blob is refused rather than truncated.
const MaxNTLMMessage = 2048

var errShort = errors.New("ntlm: ntlm message truncated")

func UTF16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[i*2:], r)
	}
	return b
}

func fromUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		return ""
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

// field reads a security buffer (len, maxlen, offset) at off and returns the
// bytes it points at within msg.
func field(msg []byte, off int) ([]byte, error) {
	if len(msg) < off+8 {
		return nil, errShort
	}
	n := int(binary.LittleEndian.Uint16(msg[off:]))
	start := int(binary.LittleEndian.Uint32(msg[off+4:]))
	if n == 0 {
		return nil, nil
	}
	if start < 0 || start > len(msg) || start+n > len(msg) {
		return nil, errShort
	}
	return msg[start : start+n], nil
}

func putField(hdr []byte, off int, payloadOff, n int) {
	binary.LittleEndian.PutUint16(hdr[off:], uint16(n))
	binary.LittleEndian.PutUint16(hdr[off+2:], uint16(n))
	binary.LittleEndian.PutUint32(hdr[off+4:], uint32(payloadOff))
}

func messageType(b []byte) (uint32, error) {
	if len(b) < 12 {
		return 0, errShort
	}
	if string(b[:8]) != string(signature) {
		return 0, errors.New("ntlm: not an NTLM message")
	}
	return binary.LittleEndian.Uint32(b[8:]), nil
}

// Negotiate is the NEGOTIATE_MESSAGE (type 1). Only the flags matter to us; the
// raw bytes are retained because the AUTHENTICATE MIC is computed over them.
type Negotiate struct {
	Flags uint32
	Raw   []byte
}

func ParseNegotiate(b []byte) (*Negotiate, error) {
	t, err := messageType(b)
	if err != nil {
		return nil, err
	}
	if t != msgNegotiate {
		return nil, fmt.Errorf("ntlm: expected NEGOTIATE, got type %d", t)
	}
	if len(b) < 16 {
		return nil, errShort
	}
	return &Negotiate{Flags: binary.LittleEndian.Uint32(b[12:]), Raw: b}, nil
}

func (n *Negotiate) Marshal() []byte {
	b := make([]byte, 32)
	copy(b, signature)
	binary.LittleEndian.PutUint32(b[8:], msgNegotiate)
	binary.LittleEndian.PutUint32(b[12:], n.Flags)
	// Empty domain and workstation buffers pointing just past the header.
	putField(b, 16, 32, 0)
	putField(b, 24, 32, 0)
	n.Raw = b
	return b
}

// Challenge is the CHALLENGE_MESSAGE (type 2).
type Challenge struct {
	Flags           uint32
	ServerChallenge [8]byte
	TargetName      string
	TargetInfo      []byte
	Raw             []byte
}

func ParseChallenge(b []byte) (*Challenge, error) {
	t, err := messageType(b)
	if err != nil {
		return nil, err
	}
	if t != msgChallenge {
		return nil, fmt.Errorf("ntlm: expected CHALLENGE, got type %d", t)
	}
	if len(b) < 48 {
		return nil, errShort
	}
	c := &Challenge{Flags: binary.LittleEndian.Uint32(b[20:]), Raw: b}
	copy(c.ServerChallenge[:], b[24:32])
	name, err := field(b, 12)
	if err != nil {
		return nil, err
	}
	c.TargetName = fromUTF16LE(name)
	info, err := field(b, 40)
	if err != nil {
		return nil, err
	}
	c.TargetInfo = info
	return c, nil
}

func (c *Challenge) Marshal() []byte {
	name := UTF16LE(c.TargetName)
	const hdr = 48
	b := make([]byte, hdr, hdr+len(name)+len(c.TargetInfo))
	copy(b, signature)
	binary.LittleEndian.PutUint32(b[8:], msgChallenge)
	putField(b, 12, hdr, len(name))
	binary.LittleEndian.PutUint32(b[20:], c.Flags)
	copy(b[24:32], c.ServerChallenge[:])
	putField(b, 40, hdr+len(name), len(c.TargetInfo))
	b = append(b, name...)
	b = append(b, c.TargetInfo...)
	c.Raw = b
	return b
}

// Authenticate is the AUTHENTICATE_MESSAGE (type 3).
type Authenticate struct {
	Flags                     uint32
	Domain                    string
	User                      string
	Workstation               string
	LMResponse                []byte
	NTResponse                []byte
	EncryptedRandomSessionKey []byte
	MIC                       []byte
	micOffset                 int
	Raw                       []byte
}

func ParseAuthenticate(b []byte) (*Authenticate, error) {
	t, err := messageType(b)
	if err != nil {
		return nil, err
	}
	if t != msgAuthenticate {
		return nil, fmt.Errorf("ntlm: expected AUTHENTICATE, got type %d", t)
	}
	if len(b) < 64 {
		return nil, errShort
	}
	a := &Authenticate{Flags: binary.LittleEndian.Uint32(b[60:]), Raw: b}
	for _, f := range []struct {
		off int
		dst *[]byte
	}{
		{12, &a.LMResponse},
		{20, &a.NTResponse},
		{52, &a.EncryptedRandomSessionKey},
	} {
		v, err := field(b, f.off)
		if err != nil {
			return nil, err
		}
		*f.dst = v
	}
	for _, f := range []struct {
		off int
		dst *string
	}{
		{28, &a.Domain},
		{36, &a.User},
		{44, &a.Workstation},
	} {
		v, err := field(b, f.off)
		if err != nil {
			return nil, err
		}
		*f.dst = fromUTF16LE(v)
	}
	// The MIC sits at a fixed offset when the header is long enough to hold it,
	// i.e. when the payload starts at or after 88.
	if payloadStart(b) >= 88 && len(b) >= 88 {
		a.MIC = b[72:88]
		a.micOffset = 72
	}
	return a, nil
}

// payloadStart is the smallest offset any security buffer points at, which tells
// us how long the (version-dependent) fixed header actually is.
func payloadStart(b []byte) int {
	min := len(b)
	for _, off := range []int{12, 20, 28, 36, 44, 52} {
		if len(b) < off+8 {
			continue
		}
		if n := int(binary.LittleEndian.Uint16(b[off:])); n == 0 {
			continue
		}
		if s := int(binary.LittleEndian.Uint32(b[off+4:])); s < min {
			min = s
		}
	}
	return min
}

// Marshal lays out an AUTHENTICATE_MESSAGE with a zeroed MIC field. SetMIC fills
// it in afterwards, since the MIC is computed over the message that contains it.
func (a *Authenticate) Marshal() []byte {
	const hdr = 88 // 64 fixed + 8 version + 16 MIC
	domain := UTF16LE(a.Domain)
	user := UTF16LE(a.User)
	ws := UTF16LE(a.Workstation)

	b := make([]byte, hdr)
	copy(b, signature)
	binary.LittleEndian.PutUint32(b[8:], msgAuthenticate)

	off := hdr
	put := func(fieldOff int, payload []byte) {
		putField(b, fieldOff, off, len(payload))
		off += len(payload)
	}
	put(12, a.LMResponse)
	put(20, a.NTResponse)
	put(28, domain)
	put(36, user)
	put(44, ws)
	put(52, a.EncryptedRandomSessionKey)
	binary.LittleEndian.PutUint32(b[60:], a.Flags|FlagVersion)
	// Version: 6.1 build 7601, NTLM revision 15. Windows servers log it; nothing verifies it.
	copy(b[64:72], []byte{6, 1, 0xb1, 0x1d, 0, 0, 0, 15})

	b = append(b, a.LMResponse...)
	b = append(b, a.NTResponse...)
	b = append(b, domain...)
	b = append(b, user...)
	b = append(b, ws...)
	b = append(b, a.EncryptedRandomSessionKey...)

	a.micOffset = 72
	a.Raw = b
	return b
}

// SetMIC computes HMAC_MD5(exported, negotiate||challenge||authenticate) over the
// marshalled message with a zeroed MIC field and writes it in place.
func (a *Authenticate) SetMIC(exported, negotiate, challenge []byte) {
	if a.micOffset == 0 || len(a.Raw) < a.micOffset+16 {
		return
	}
	for i := a.micOffset; i < a.micOffset+16; i++ {
		a.Raw[i] = 0
	}
	m := hmac.New(md5.New, exported)
	m.Write(negotiate)
	m.Write(challenge)
	m.Write(a.Raw)
	mic := m.Sum(nil)
	copy(a.Raw[a.micOffset:], mic)
	a.MIC = mic
}

// NTHash is MD4(UTF16-LE(password)) — the only form of the password that needs to
// leave the control server, since NTLMv2 verification never needs the plaintext.
func NTHash(password string) []byte {
	h := md4.New()
	h.Write(UTF16LE(password))
	return h.Sum(nil)
}

// ntowfv2 = HMAC_MD5(NTHash, UTF16-LE(upper(user) + domain)).
func ntowfv2(ntHash []byte, user, domain string) []byte {
	m := hmac.New(md5.New, ntHash)
	m.Write(UTF16LE(strings.ToUpper(user) + domain))
	return m.Sum(nil)
}

// TargetInfo builds the AV_PAIR list a server offers in its CHALLENGE.
func TargetInfo(computer, domain string, ts time.Time) []byte {
	var b []byte
	add := func(id uint16, v []byte) {
		var h [4]byte
		binary.LittleEndian.PutUint16(h[0:], id)
		binary.LittleEndian.PutUint16(h[2:], uint16(len(v)))
		b = append(b, h[:]...)
		b = append(b, v...)
	}
	add(avNbDomainName, UTF16LE(domain))
	add(avNbComputerName, UTF16LE(computer))
	add(avDNSDomainName, UTF16LE(domain))
	add(avDNSComputerName, UTF16LE(computer))
	var t [8]byte
	binary.LittleEndian.PutUint64(t[:], windowsTime(ts))
	add(avTimestamp, t[:])
	add(avEOL, nil)
	return b
}

// windowsTime converts to 100ns intervals since 1601-01-01 UTC.
func windowsTime(t time.Time) uint64 {
	const epochDelta = 116444736000000000
	return uint64(t.UTC().UnixNano()/100) + epochDelta
}

// WindowsTimeBytes is a timestamp in the 8-byte form an AV_PAIR carries.
func WindowsTimeBytes(t time.Time) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, windowsTime(t))
	return b
}

// NTLMv2Blob is the variable part of an NTLMv2 response: everything after the
// 16-byte NTProofStr. The timestamp is passed in rather than read from the clock
// because a challenge carrying MsvAvTimestamp must have that value echoed back.
func NTLMv2Blob(clientChallenge [8]byte, timestamp []byte, targetInfo []byte) []byte {
	b := make([]byte, 28, 32+len(targetInfo))
	b[0], b[1] = 1, 1
	copy(b[8:16], timestamp)
	copy(b[16:24], clientChallenge[:])
	b = append(b, targetInfo...)
	b = append(b, 0, 0, 0, 0)
	return b
}

// ComputeNTLMv2 returns the NT challenge response and the session base key.
func ComputeNTLMv2(ntHash []byte, user, domain string, serverChallenge [8]byte, blob []byte) (ntResponse, sessionBaseKey []byte) {
	key := ntowfv2(ntHash, user, domain)
	m := hmac.New(md5.New, key)
	m.Write(serverChallenge[:])
	m.Write(blob)
	proof := m.Sum(nil)

	ntResponse = make([]byte, 0, len(proof)+len(blob))
	ntResponse = append(ntResponse, proof...)
	ntResponse = append(ntResponse, blob...)

	m = hmac.New(md5.New, key)
	m.Write(proof)
	return ntResponse, m.Sum(nil)
}

// VerifyNTLMv2 recomputes NTProofStr over the blob the client sent and compares it
// in constant time. On success it returns the session base key.
func VerifyNTLMv2(ntHash []byte, user, domain string, serverChallenge [8]byte, ntResponse []byte) ([]byte, bool) {
	if len(ntResponse) < 16 {
		return nil, false
	}
	proof, blob := ntResponse[:16], ntResponse[16:]
	key := ntowfv2(ntHash, user, domain)
	m := hmac.New(md5.New, key)
	m.Write(serverChallenge[:])
	m.Write(blob)
	if !hmac.Equal(proof, m.Sum(nil)) {
		return nil, false
	}
	m = hmac.New(md5.New, key)
	m.Write(proof)
	return m.Sum(nil), true
}

const (
	clientSignMagic = "session key to client-to-server signing key magic constant\x00"
	serverSignMagic = "session key to server-to-client signing key magic constant\x00"
	clientSealMagic = "session key to client-to-server sealing key magic constant\x00"
	serverSealMagic = "session key to server-to-client sealing key magic constant\x00"
)

func deriveKey(exported []byte, magic string) []byte {
	h := md5.New()
	h.Write(exported)
	h.Write([]byte(magic))
	return h.Sum(nil)
}

// Security is an established NTLM message-protection context. Each direction has
// its own RC4 keystream, so the two ciphers are stateful and not interchangeable.
type Security struct {
	sendSeal *rc4.Cipher
	recvSeal *rc4.Cipher
	sendSign []byte
	recvSign []byte
	sendSeq  uint32
	recvSeq  uint32
}

// NewSecurity derives the four keys from the exported session key. server picks
// which direction is "send".
func NewSecurity(exported []byte, server bool) (*Security, error) {
	sendSignMagic, recvSignMagic := clientSignMagic, serverSignMagic
	sendSealMagic, recvSealMagic := clientSealMagic, serverSealMagic
	if server {
		sendSignMagic, recvSignMagic = serverSignMagic, clientSignMagic
		sendSealMagic, recvSealMagic = serverSealMagic, clientSealMagic
	}
	sendSeal, err := rc4.NewCipher(deriveKey(exported, sendSealMagic))
	if err != nil {
		return nil, err
	}
	recvSeal, err := rc4.NewCipher(deriveKey(exported, recvSealMagic))
	if err != nil {
		return nil, err
	}
	return &Security{
		sendSeal: sendSeal,
		recvSeal: recvSeal,
		sendSign: deriveKey(exported, sendSignMagic),
		recvSign: deriveKey(exported, recvSignMagic),
	}, nil
}

// Wrap seals msg and prefixes the 16-byte NTLM signature, which is the layout
// CredSSP's pubKeyAuth and authInfo fields carry.
func (s *Security) Wrap(msg []byte) []byte {
	sealed := make([]byte, len(msg))
	s.sendSeal.XORKeyStream(sealed, msg)

	m := hmac.New(md5.New, s.sendSign)
	var seq [4]byte
	binary.LittleEndian.PutUint32(seq[:], s.sendSeq)
	m.Write(seq[:])
	m.Write(msg)
	checksum := m.Sum(nil)[:8]
	s.sendSeal.XORKeyStream(checksum, checksum)

	out := make([]byte, 16, 16+len(sealed))
	binary.LittleEndian.PutUint32(out[0:], 1)
	copy(out[4:12], checksum)
	binary.LittleEndian.PutUint32(out[12:], s.sendSeq)
	s.sendSeq++
	return append(out, sealed...)
}

// Unwrap reverses Wrap and verifies the checksum.
func (s *Security) Unwrap(b []byte) ([]byte, error) {
	if len(b) < 16 {
		return nil, errors.New("ntlm: wrapped message too short")
	}
	sig, sealed := b[:16], b[16:]
	msg := make([]byte, len(sealed))
	s.recvSeal.XORKeyStream(msg, sealed)

	m := hmac.New(md5.New, s.recvSign)
	var seq [4]byte
	binary.LittleEndian.PutUint32(seq[:], binary.LittleEndian.Uint32(sig[12:]))
	m.Write(seq[:])
	m.Write(msg)
	want := m.Sum(nil)[:8]
	s.recvSeal.XORKeyStream(want, want)
	if !hmac.Equal(want, sig[4:12]) {
		return nil, errors.New("ntlm: message signature mismatch")
	}
	s.recvSeq++
	return msg, nil
}

func ConstantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("ntlm: random: %w", err)
	}
	return b, nil
}
