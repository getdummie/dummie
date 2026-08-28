package control

import (
	"crypto/rand"
	"encoding/hex"
)

const (
	Version = 1

	MaxMsgSize = 4096

	MaxFDs = 2

	MaxRecvFDs = 64

	MaxConsolePrefix = 512

	MaxResolveDetails = 3072
	MaxResolveHeader  = 256
	MaxResolvePath    = 1024

	MaxNotice = 1024
)

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

const (
	KindSSH  = "ssh"
	KindHTTP = "http"
)

const (
	ProtoTCP     = "tcp"
	ProtoHTTP    = "http"
	ProtoSSH     = "ssh"
	ProtoTLS     = "tls"
	ProtoConsole = "console"
)

const (
	SockControl       = "control"
	SockUpgrade       = "upgrade"
	SockListenForward = "listen_forward"
)

type Msg struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`

	Protocol string `json:"protocol,omitempty"`
	Listen   string `json:"listen,omitempty"`
	Target   string `json:"target,omitempty"`

	Kind string `json:"kind,omitempty"`

	SSHUser        string `json:"ssh_user,omitempty"`
	SSHPubKey      string `json:"ssh_pubkey,omitempty"`
	SSHFingerprint string `json:"ssh_fp,omitempty"`

	Host string `json:"host,omitempty"`
	SNI  string `json:"sni,omitempty"`

	Cookie     string `json:"cookie,omitempty"`
	Path       string `json:"path,omitempty"`
	Accept     string `json:"accept,omitempty"`
	Upgrade    string `json:"upgrade,omitempty"`
	Connection string `json:"connection,omitempty"`
	WSVersion  string `json:"ws_version,omitempty"`

	Status    int    `json:"status,omitempty"`
	Location  string `json:"location,omitempty"`
	SetCookie string `json:"set_cookie,omitempty"`

	Sub    string `json:"sub,omitempty"`
	WSKey  string `json:"ws_key,omitempty"`
	Prefix string `json:"prefix,omitempty"`

	ClientIP string `json:"client_ip,omitempty"`

	Authorized bool   `json:"authorized,omitempty"`
	RemoteUser string `json:"remote_user,omitempty"`

	Notice string `json:"notice,omitempty"`

	Error string `json:"error,omitempty"`

	ActiveConns    int  `json:"active_conns,omitempty"`
	ListenForwards int  `json:"listen_forwards,omitempty"`
	Draining       bool `json:"draining,omitempty"`

	Sockets []SockDesc `json:"sockets,omitempty"`
}

type SockDesc struct {
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Listen string `json:"listen,omitempty"`
	Target string `json:"target,omitempty"`
}

func IsReply(t string) bool {
	switch t {
	case TypeOK, TypeError, TypeResolved:
		return true
	}
	return false
}

func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

func OK(id string) Msg { return Msg{V: Version, Type: TypeOK, ID: id} }

func Err(id, msg string) Msg {
	return Msg{V: Version, Type: TypeError, ID: id, Error: msg}
}
