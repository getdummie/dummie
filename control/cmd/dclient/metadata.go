package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// The metadata service answers "who am I?" for a guest. Identity comes from the
// source address and nothing else: a VM cannot ask about another VM, because
// the only VM it can describe is the one the packet came from, and the
// anti-spoof rule guarantees that address really is the sender's.
type metadataServer struct {
	data string
	cfg  netConfig
}

// identity is the public shape of a VM, as seen from inside it.
type identity struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	IP       string   `json:"ip"`
	Gateway  string   `json:"gateway"`
	MAC      string   `json:"mac"`
	Egress   []string `json:"egress"`
	RateMbit int      `json:"rate_mbit,omitempty"`
}

func (s *metadataServer) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/identity", s.identity)
	// Plain text, because the first thing a guest does with it is run it.
	mux.HandleFunc("GET /v1/network", s.network)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return mux
}

// caller resolves the requesting VM by source address.
func (s *metadataServer) caller(r *http.Request) (vm, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return vm{}, false
	}
	vms, err := listVMs(s.data)
	if err != nil {
		return vm{}, false
	}
	for _, v := range vms {
		if v.Net != nil && v.Net.IP == host {
			return v, true
		}
	}
	return vm{}, false
}

func (s *metadataServer) identity(w http.ResponseWriter, r *http.Request) {
	v, ok := s.caller(r)
	if !ok {
		http.Error(w, "unknown caller", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(identity{
		ID:       v.ID,
		Name:     v.Name,
		IP:       v.Net.IP,
		Gateway:  v.Net.Gateway,
		MAC:      v.Net.MAC,
		Egress:   v.Net.Egress,
		RateMbit: v.Net.RateMbit,
	})
}

func (s *metadataServer) network(w http.ResponseWriter, r *http.Request) {
	v, ok := s.caller(r)
	if !ok {
		http.Error(w, "unknown caller", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, guestConfig(v.Net))
}

// serve binds to the gateway address only. FREEBIND is required because the
// gateway lives on the taps: with no VMs running the address does not exist
// yet, and the service still has to be up when the first one starts.
func (s *metadataServer) serve(ctx context.Context) error {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var err error
			cerr := c.Control(func(fd uintptr) {
				err = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_FREEBIND, 1)
			})
			if cerr != nil {
				return cerr
			}
			return err
		},
	}
	addr := net.JoinHostPort(s.cfg.Gateway, fmt.Sprint(metadataPort))
	ln, err := lc.Listen(ctx, "tcp4", addr)
	if err != nil {
		return fmt.Errorf("could not listen on %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	log.Printf("metadata service on http://%s", addr)
	if err := srv.Serve(ln); err != nil && !strings.Contains(err.Error(), "Server closed") {
		return err
	}
	return nil
}
