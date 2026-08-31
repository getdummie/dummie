package proto

import (
	"encoding/json"
	"time"
)

const (
	EnrollPath  = "/api/v1/client/enroll"
	ConnectPath = "/api/v1/client/connect"
)

type MsgType string

const (
	TypeHello     MsgType = "hello"
	TypeHelloAck  MsgType = "hello_ack"
	TypeJob       MsgType = "job"
	TypeResult    MsgType = "result"
	TypeMetrics   MsgType = "metrics"
	TypeInventory MsgType = "inventory"
	TypeError     MsgType = "error"
)

type Envelope struct {
	Type    MsgType         `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func NewEnvelope(t MsgType, id string, payload any) (Envelope, error) {
	env := Envelope{Type: t, ID: id}
	if payload == nil {
		return env, nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	env.Payload = b
	return env, nil
}

type Hello struct {
	Version   string `json:"version"`
	MachineID string `json:"machine_id"`
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	OSVersion string `json:"os_version"`
	Arch      string `json:"arch"`

	Pool string `json:"pool,omitempty"`

	Services *ServicesState `json:"services,omitempty"`
}

type ServicesState struct {
	Dpipe string `json:"dpipe,omitempty"`
	Proxy string `json:"dproxy,omitempty"`
	Dinit string `json:"dinit,omitempty"`
}

type HelloAck struct {
	ClientID   string    `json:"client_id"`
	ServerTime time.Time `json:"server_time"`
}

type ErrorPayload struct {
	Message string `json:"message"`
}

type JobKind string

const (
	KindVMCreate JobKind = "vm.create"
	KindVMStop    JobKind = "vm.stop"
	KindVMStart   JobKind = "vm.start"
	KindVMDestroy JobKind = "vm.destroy"

	KindSuricataRules JobKind = "suricata.rules"

	KindProxyConfig JobKind = "proxy.config"

	KindSuricataConfig JobKind = "suricata.config"

	KindDpipeConfig JobKind = "dpipe.config"

	KindVectorConfig JobKind = "vector.config"

	KindCoreDNSConfig JobKind = "coredns.config"

	KindServicesConfig JobKind = "services.config"

	KindCustomCert JobKind = "cert.custom"
)

type Job struct {
	Kind JobKind `json:"kind"`
	VM   *VMSpec `json:"vm,omitempty"`
	VMID string `json:"vm_id,omitempty"`
	Suricata *SuricataRules `json:"suricata,omitempty"`
	Proxy *ProxyConfig `json:"proxy,omitempty"`
	Vector *VectorConfig `json:"vector,omitempty"`
	File *FileConfig `json:"file,omitempty"`
	DpipeCerts *DpipeCerts `json:"dpipe_certs,omitempty"`
	Services *ServicesConfig `json:"services,omitempty"`
	CustomCert *CustomCertOrder `json:"custom_cert,omitempty"`
}

// CustomCertOrder asks a host to obtain a certificate for one name over the
// HTTP-01 challenge. The name already points at this host, which is what makes
// the challenge answerable there rather than here.
type CustomCertOrder struct {
	Domain    string `json:"domain"`
	Email     string `json:"email"`
	Directory string `json:"directory"`
}

type ServicesConfig struct {
	Dclient ServiceRelease `json:"dclient"`
	Dpipe   ServiceRelease `json:"dpipe"`
	Proxy   ServiceRelease `json:"dproxy"`

	// Dinit is not a service: it is the guest init dclient copies into every
	// rootfs it builds from a container tar. It is delivered the same way.
	Dinit ServiceRelease `json:"dinit"`

	Force bool `json:"force,omitempty"`
}

type ServiceRelease struct {
	Version string `json:"version,omitempty"`

	DownloadURL string `json:"download_url,omitempty"`
}

type FileConfig struct {
	Config string `json:"config"`
}

type DpipeCerts struct {
	Cert string `json:"cert"`
	Key  string `json:"key"`
	Fingerprint string `json:"fingerprint,omitempty"`

	// Named certificates, one per custom domain, selected by SNI. The pair
	// above stays the fallback for everything under the fleet's own domain.
	Named []DpipeNamedCert `json:"named,omitempty"`
}

type DpipeNamedCert struct {
	SNI  string `json:"sni"`
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

type VectorConfig struct {
	Version string `json:"version"`

	Config string `json:"config,omitempty"`
}

type SuricataRules struct {
	Rules string `json:"rules"`
}

type ProxyConfig struct {
	Config string `json:"config"`
	CookieSecret     string `json:"cookie_secret,omitempty"`
	CookieSecretPath string `json:"cookie_secret_path,omitempty"`

	SiteHTML string `json:"site_html,omitempty"`
	SitePath string `json:"site_path,omitempty"`
}

type VMSpec struct {
	Name string `json:"name,omitempty"`
	Boot string `json:"boot,omitempty"`

	Kernel       string `json:"kernel,omitempty"`
	KernelSHA    string `json:"kernel_sha256,omitempty"`
	Initrd       string `json:"initrd,omitempty"`
	InitrdSHA    string `json:"initrd_sha256,omitempty"`
	Rootfs       string `json:"rootfs,omitempty"`
	RootfsSHA    string `json:"rootfs_sha256,omitempty"`
	RootfsTar    string `json:"rootfs_tar,omitempty"`
	RootfsTarSHA string `json:"rootfs_tar_sha256,omitempty"`
	RootfsSize   string `json:"rootfs_size,omitempty"`
	Disk         string `json:"disk,omitempty"`
	DiskSHA      string `json:"disk_sha256,omitempty"`
	DiskSize     string `json:"disk_size,omitempty"`
	Append       string `json:"append,omitempty"`
	Firmware     string `json:"firmware,omitempty"`

	CPUs   int `json:"cpus,omitempty"`
	Memory int `json:"memory_mib,omitempty"`

	NoNetwork bool     `json:"no_network,omitempty"`
	IP        string   `json:"ip,omitempty"`
	Egress    []string `json:"egress,omitempty"`
	EgressAny bool     `json:"egress_any,omitempty"`
	RateMbit  int      `json:"rate_mbit,omitempty"`
	BurstKbit int      `json:"burst_kbit,omitempty"`
}

type JobResult struct {
	Kind  JobKind `json:"kind"`
	OK    bool    `json:"ok"`
	Error string  `json:"error,omitempty"`
	VM    *VMInfo `json:"vm,omitempty"`
	Services *ServicesState `json:"services,omitempty"`

	Domain string `json:"domain,omitempty"`
	Cert   *IssuedCert `json:"cert,omitempty"`
}

type IssuedCert struct {
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

type VMInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Boot      string `json:"boot"`
	CPUs      int    `json:"cpus"`
	MemoryMiB int    `json:"memory_mib"`
	IP        string `json:"ip,omitempty"`
}

type Inventory struct {
	VMs []VMState `json:"vms"`
}

type VMState struct {
	VMInfo
	Running   bool      `json:"running"`
	CreatedAt time.Time `json:"created_at"`
}

type Metrics struct {
	CPUCount       int     `json:"cpu_count"`
	CPUPercent     float64 `json:"cpu_percent"`
	Load1          float64 `json:"load1"`
	Load5          float64 `json:"load5"`
	Load15         float64 `json:"load15"`
	MemTotalBytes  int64   `json:"mem_total_bytes"`
	MemUsedBytes   int64   `json:"mem_used_bytes"`
	DiskTotalBytes int64   `json:"disk_total_bytes"`
	DiskUsedBytes  int64   `json:"disk_used_bytes"`
	UptimeSeconds  int64   `json:"uptime_seconds"`
}

type EnrollRequest struct {
	Key       string `json:"key"`
	MachineID string `json:"machine_id"`
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	OSVersion string `json:"os_version"`
	Arch      string `json:"arch"`
	Version   string `json:"version"`
}

type EnrollResponse struct {
	ClientID string `json:"client_id"`
	Token    string `json:"token"`
}
