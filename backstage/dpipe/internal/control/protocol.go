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

	Protocol string `json:"protocol,omitempty"` // copy / ssh_accept / tls_accept / console_accept
	Listen   string `json:"listen,omitempty"`
	Target   string `json:"target,omitempty"` // listen_forward / resolved / console_accept

	Kind string `json:"kind,omitempty"` // resolve: "ssh" | "http"

	SSHUser        string `json:"ssh_user,omitempty"`
	SSHPubKey      string `json:"ssh_pubkey,omitempty"`
	SSHFingerprint string `json:"ssh_fp,omitempty"`

	Host string `json:"host,omitempty"` // resolve http / console_accept
	SNI  string `json:"sni,omitempty"`  // resolve http

	// console_accept only. Sub is who the proxy authenticated and is carried for
	// the audit log; dpipe never re-decides it. WSKey is the client's
	// Sec-WebSocket-Key, which dpipe needs to answer a handshake the proxy has
	// already validated. Prefix is base64 of whatever the client pipelined behind
	// the upgrade request, bounded by MaxConsolePrefix.
	Sub    string `json:"sub,omitempty"`
	WSKey  string `json:"ws_key,omitempty"`
	Prefix string `json:"prefix,omitempty"`

	ClientIP string `json:"client_ip,omitempty"`

	Authorized bool   `json:"authorized,omitempty"`
	RemoteUser string `json:"remote_user,omitempty"`

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
