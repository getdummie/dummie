package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vishvananda/netlink"

	"control/internal/proto"
)

const (
	intproxyConfigDir  = "/etc/intproxy"
	intproxyCertDir    = intproxyConfigDir + "/certs"
	intproxyCertFile   = intproxyCertDir + "/fullchain.pem"
	intproxyKeyFile    = intproxyCertDir + "/privkey.pem"
	intproxyRuntimeDir = "/run/intproxy"
	intproxyBrokerSock = intproxyRuntimeDir + "/broker.sock"

	// intproxyAddr is the last usable host in the vm pool, so a sequential
	// allocator never reaches it. Guests get there over the default route they
	// already have, so nothing in the guest or in dhcp changes.
	intproxyAddr  = "10.64.255.254"
	intproxyLink  = "dint0"
	intproxyLabel = "int"
)

// defaultIntproxyConfig is a placeholder, not a working configuration: it names
// no domain, so intproxy refuses it. The unit is installed stopped until the
// control server sends a real one, and intproxyConfigured compares against this
// to tell the two apart.
const defaultIntproxyConfig = `# Bootstrap only. The control server replaces this whole file.
listen: "` + intproxyAddr + `:443"
freebind: true
reuseport: true
tld: ""
label: "` + intproxyLabel + `"
tls:
  cert: ` + intproxyCertFile + `
  key: ` + intproxyKeyFile + `
broker:
  socket: ` + intproxyBrokerSock + `
log_level: info
`

func intproxyCertPresent() bool {
	for _, p := range []string{intproxyCertFile, intproxyKeyFile} {
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

// intproxyConfigured reports whether the control server has pushed a real
// config yet. The bootstrap one names no domain and intproxy refuses to start
// on it, so this -- not the presence of a certificate -- is what decides
// whether the unit may start: a fleet with no tls has no certificate and is
// still perfectly serviceable over http.
func intproxyConfigured() bool {
	b, err := os.ReadFile(serviceConfigPath(intproxyService))
	if err != nil {
		return false
	}
	return string(b) != defaultIntproxyConfig
}

// ensureIntproxyAddr puts the integration proxy's address on a dummy link.
// intproxy binds it specifically so it can own :443 while dproxy holds the
// wildcard, and IP_FREEBIND means the order the two come up in does not matter.
func ensureIntproxyAddr() error {
	link, err := netlink.LinkByName(intproxyLink)
	if err != nil {
		add := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: intproxyLink}}
		if err := netlink.LinkAdd(add); err != nil && !os.IsExist(err) {
			return fmt.Errorf("could not create %s: %w", intproxyLink, err)
		}
		link, err = netlink.LinkByName(intproxyLink)
		if err != nil {
			return fmt.Errorf("%s vanished: %w", intproxyLink, err)
		}
	}

	addr, err := netlink.ParseAddr(intproxyAddr + "/32")
	if err != nil {
		return err
	}
	if err := netlink.AddrReplace(link, addr); err != nil {
		return fmt.Errorf("could not add %s to %s: %w", intproxyAddr, intproxyLink, err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("could not bring %s up: %w", intproxyLink, err)
	}
	return nil
}

func removeIntproxyHost() {
	if link, err := netlink.LinkByName(intproxyLink); err == nil {
		if err := netlink.LinkDel(link); err != nil {
			fmt.Println(err)
		} else {
			fmt.Println("removed " + intproxyLink)
		}
	}
	if err := os.RemoveAll(intproxyConfigDir); err == nil {
		fmt.Println("removed " + intproxyConfigDir)
	} else if !os.IsNotExist(err) {
		fmt.Println(err)
	}
	_ = os.RemoveAll(intproxyRuntimeDir)
}

// brokerServer hands intproxy short-lived github tokens. It is a relay and
// nothing more: dclient adds the host's own credential, and the control server
// makes every authorization decision, so there is one place a rule can be wrong.
//
// intproxy needs no credential of its own because reaching a 0600 root-owned
// socket is the authorization. That matters: the client token is the host's
// whole identity and can create and destroy VMs.
type brokerServer struct {
	client *http.Client
	base   *url.URL
	token  string
}

func (s *brokerServer) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/token", s.token1)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return mux
}

func (s *brokerServer) token1(w http.ResponseWriter, r *http.Request) {
	var req proto.IntegrationTokenRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, `{"message":"could not decode the request"}`, http.StatusBadRequest)
		return
	}

	body, err := json.Marshal(req)
	if err != nil {
		http.Error(w, `{"message":"could not encode the request"}`, http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	endpoint := s.base.JoinPath(proto.IntegrationTokenPath).String()
	up, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"message":"could not build the upstream request"}`, http.StatusInternalServerError)
		return
	}
	up.Header.Set("Authorization", "Bearer "+s.token)
	up.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(up)
	if err != nil {
		log.Printf("broker: could not reach the control server: %v", err)
		http.Error(w, `{"message":"the control server could not be reached"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		w.Header().Set("Retry-After", ra)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 64<<10))
}

// serve listens on the unix socket. Go's unix listener honours umask, so the
// mode is set explicitly rather than assumed.
func (s *brokerServer) serve(ctx context.Context) error {
	if err := os.MkdirAll(intproxyRuntimeDir, 0o700); err != nil {
		return fmt.Errorf("could not create %s: %w", intproxyRuntimeDir, err)
	}
	if err := removeStaleBrokerSocket(); err != nil {
		return err
	}

	ln, err := net.Listen("unix", intproxyBrokerSock)
	if err != nil {
		return fmt.Errorf("could not listen on %s: %w", intproxyBrokerSock, err)
	}
	if err := os.Chmod(intproxyBrokerSock, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("could not restrict %s: %w", intproxyBrokerSock, err)
	}

	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	log.Printf("integration token broker on %s", intproxyBrokerSock)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func removeStaleBrokerSocket() error {
	st, err := os.Lstat(intproxyBrokerSock)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket", intproxyBrokerSock)
	}
	if err := os.Remove(intproxyBrokerSock); err != nil {
		return fmt.Errorf("could not remove the stale socket %s: %w", intproxyBrokerSock, err)
	}
	return nil
}

func checkIntproxy() (result, string) {
	if !serviceInstalled(intproxyService) {
		return pass, "intproxy is not installed on this host; integrations are off"
	}
	if !intproxyConfigured() {
		return warn, "intproxy is installed but the control server has not configured it yet; " +
			"give this host's fleet a domain to turn integrations on"
	}
	if !intproxyAddrPresent() {
		return fail, intproxyAddr + " is not on " + intproxyLink + "; intproxy has nothing to bind"
	}
	out, _ := exec.Command("systemctl", "is-active", intproxyService+".service").Output()
	if strings.TrimSpace(string(out)) != "active" {
		return warn, "intproxy is configured but the unit is not active; no vm can reach an integration"
	}

	if cfg, err := loadNetConfig(defaultDataDir()); err == nil && !cfg.Suricata {
		return warn, "intproxy is running but suricata mode is off, so coredns is not resolving *." +
			intproxyLabel + ".<tld> for guests"
	}
	if !intproxyCertPresent() {
		return warn, "intproxy is serving integrations over plain http on " + intproxyAddr +
			":80; reissue the fleet certificate so it covers *." + intproxyLabel + ".<tld> to move it to tls"
	}
	return pass, "intproxy is serving integrations on " + intproxyAddr + ":443"
}

func intproxyAddrPresent() bool {
	link, err := netlink.LinkByName(intproxyLink)
	if err != nil {
		return false
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return false
	}
	want := net.ParseIP(intproxyAddr)
	for _, a := range addrs {
		if a.IP.Equal(want) {
			return true
		}
	}
	return false
}
