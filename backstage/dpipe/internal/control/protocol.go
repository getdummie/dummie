// Package control implements Wire Protocol v1 spoken between proxy and dpipe
// over an AF_UNIX SOCK_STREAM socket.
//
// This package is SHARED SOURCE: it must stay byte-for-byte compatible with the
// copy in the proxy repository.
package control

import (
	"crypto/rand"
	"encoding/hex"
)

const (
	// Version is the wire protocol version carried in every message.
	Version = 1

	// MaxMsgSize is the maximum JSON payload size of a single message.
	MaxMsgSize = 4096

	// MaxFDs is the maximum number of file descriptors carried by a message on
	// the control socket (copy sends two: client + backend).
	MaxFDs = 2

	// MaxRecvFDs bounds the SCM_RIGHTS buffer when receiving. The handover
	// handshake on the upgrade socket carries one listener fd per socket
	// descriptor, which can exceed MaxFDs; per-type fd counts are validated by
	// the handlers.
	MaxRecvFDs = 64

	// MaxConsolePrefix bounds the bytes a client may pipeline behind a console
	// upgrade request. They travel base64-encoded inside a console_accept, which
	// MaxMsgSize caps, so the limit has to leave room for every other field.
	MaxConsolePrefix = 512

	// MaxResolveDetails bounds the request details an http resolve carries in
	// total, so the message cannot outgrow MaxMsgSize; MaxResolveHeader and
	// MaxResolvePath bound one small header and the request target within it. The
	// small headers are filled first and the cookie last, from what is left: a
	// dropped field reads as absent and fails closed, and the cookie is the only
	// one big enough to be worth crowding out.
	MaxResolveDetails = 3072
	MaxResolveHeader  = 256
	MaxResolvePath    = 1024

	// MaxNotice bounds the text an ssh resolve can send back for dpipe to print,
	// so a key owning many VMs cannot push the reply past MaxMsgSize.
	MaxNotice = 1024
)

// Message types.
const (
	TypeCopy          = "copy"
	TypeSSHAccept     = "ssh_accept"
	TypeTLSAccept     = "tls_accept"
	TypeConsoleAccept = "console_accept"
	TypeListenForward = "listen_forward"
	TypeStop          = "stop"
	TypeStatus        = "status"
	TypeResolve       = "resolve"

	TypeOK       = "ok"
	TypeError    = "error"
	TypeResolved = "resolved"

	TypeHandoverRequest = "handover_request"
	TypeHandover        = "handover"
	TypeHandoverAck     = "handover_ack"
)

// resolve kinds.
const (
	KindSSH  = "ssh"
	KindHTTP = "http"
)

// protocol values for copy / ssh_accept / tls_accept / console_accept.
const (
	ProtoTCP     = "tcp"
	ProtoHTTP    = "http"
	ProtoSSH     = "ssh"
	ProtoTLS     = "tls"
	ProtoConsole = "console"
)

// socket kinds used by the handover handshake.
const (
	SockControl       = "control"
	SockUpgrade       = "upgrade"
	SockListenForward = "listen_forward"
)

// Msg is the single message struct of the protocol. Only the fields relevant to
// a given type are populated.
type Msg struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`

	Protocol string `json:"protocol,omitempty"` // copy / ssh_accept / tls_accept / console_accept; "console" on a resolved
	Listen   string `json:"listen,omitempty"`
	Target   string `json:"target,omitempty"` // listen_forward / resolved / console_accept

	Kind string `json:"kind,omitempty"` // resolve: "ssh" | "http"

	SSHUser        string `json:"ssh_user,omitempty"`
	SSHPubKey      string `json:"ssh_pubkey,omitempty"`
	SSHFingerprint string `json:"ssh_fp,omitempty"`

	Host string `json:"host,omitempty"` // resolve http / console_accept
	SNI  string `json:"sni,omitempty"`  // resolve http

	// resolve http only. dpipe has terminated TLS and read the decrypted header
	// block, so it forwards the request details the proxy's policy reads -- the
	// Cookie header verbatim, the request target, the headers that decide whether a
	// redirect is a useful answer, and the websocket handshake headers a console
	// request carries (with WSKey below). dpipe interprets none of them: it holds
	// neither the cookie secret nor the per-host port policy.
	Cookie     string `json:"cookie,omitempty"`
	Path       string `json:"path,omitempty"`
	Accept     string `json:"accept,omitempty"`
	Upgrade    string `json:"upgrade,omitempty"`
	Connection string `json:"connection,omitempty"`
	WSVersion  string `json:"ws_version,omitempty"`

	// resolved for an http resolve that was not authorized: the response dpipe
	// must write on the TLS connection before closing it. Location makes it a 302
	// (with SetCookie when the login round trip just completed); otherwise Status
	// alone is written as a plain error response.
	Status    int    `json:"status,omitempty"`
	Location  string `json:"location,omitempty"`
	SetCookie string `json:"set_cookie,omitempty"`

	// console_accept, and an http resolve that turns out to be a console: on the
	// request WSKey is what dpipe read off the wire, on the reply it is what the
	// proxy validated and is the one dpipe answers the handshake with. Sub is who
	// the proxy authenticated and is carried for the audit log; dpipe never
	// re-decides it. Prefix is base64 of whatever the client pipelined behind the
	// upgrade request, bounded by MaxConsolePrefix — console_accept only, since a
	// console reached over the https ingress never leaves the process that read it.
	Sub    string `json:"sub,omitempty"`
	WSKey  string `json:"ws_key,omitempty"`
	Prefix string `json:"prefix,omitempty"`

	ClientIP string `json:"client_ip,omitempty"`

	Authorized bool   `json:"authorized,omitempty"`
	RemoteUser string `json:"remote_user,omitempty"`

	// resolved for an ssh resolve whose key is known but whose login name named no
	// VM the key owns: the key authenticates, there is nothing to route to, and
	// this is the text dpipe prints before closing the session. Bounded by
	// MaxNotice. Empty with authorized false is a plain denial.
	Notice string `json:"notice,omitempty"`

	Error string `json:"error,omitempty"`

	ActiveConns    int  `json:"active_conns,omitempty"`
	ListenForwards int  `json:"listen_forwards,omitempty"`
	Draining       bool `json:"draining,omitempty"`

	Sockets []SockDesc `json:"sockets,omitempty"`
}

// SockDesc describes one listening socket transferred during a handover.
type SockDesc struct {
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Listen string `json:"listen,omitempty"`
	Target string `json:"target,omitempty"`
}

// IsReply reports whether a message type is a reply correlated by id rather
// than a request.
func IsReply(t string) bool {
	switch t {
	case TypeOK, TypeError, TypeResolved:
		return true
	}
	return false
}

// NewID returns a random message/connection id.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read never fails on the supported platforms; fall back to
		// a fixed marker rather than panicking in a connection path.
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// OK builds an ok reply for the given request id.
func OK(id string) Msg { return Msg{V: Version, Type: TypeOK, ID: id} }

// Errorf builds an error reply for the given request id.
func Err(id, msg string) Msg {
	return Msg{V: Version, Type: TypeError, ID: id, Error: msg}
}
