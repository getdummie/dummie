package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"time"
)

// The daemon owns everything privileged; the CLI is a client. That removes the
// sudo from day-to-day use, and it removes a real hazard: with both a running
// netd and an occasional `sudo vm create` mutating nftables, there were two
// writers to the same kernel state.
//
// The socket is the privilege boundary. Anyone who can write to it can start a
// VM from an arbitrary kernel and an arbitrary tar, which is root by another
// route -- so it is 0660 root:<group>, and joining that group is as
// consequential as passwordless sudo.

// apiServer serves the unix socket.
type apiServer struct {
	data string
	cfg  netConfig
}

func (s *apiServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/vms", s.createVM)
	mux.HandleFunc("GET /v1/vms", s.listVMs)
	mux.HandleFunc("POST /v1/vms/{id}/start", s.startVM)
	mux.HandleFunc("POST /v1/vms/{id}/stop", s.stopVM)
	mux.HandleFunc("DELETE /v1/vms/{id}", s.removeVM)
	mux.HandleFunc("GET /v1/vms/{id}/console", s.console)
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": version})
	})
	return mux
}

// --- handlers ---------------------------------------------------------------

// createVM streams NDJSON progress rather than answering once at the end:
// downloading an image and building a filesystem takes minutes, and a silent
// connection for that long is indistinguishable from a hang.
func (s *apiServer) createVM(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{"could not decode the request: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	logf := func(format string, args ...any) {
		_ = enc.Encode(event{Log: fmt.Sprintf(format, args...)})
		flush()
	}

	v, err := createVM(r.Context(), s.data, req, logf)
	if err != nil {
		_ = enc.Encode(event{Error: err.Error()})
		flush()
		return
	}
	_ = enc.Encode(event{VM: &v})
	flush()
}

func (s *apiServer) listVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := listVMs(s.data)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{err.Error()})
		return
	}
	out := make([]vmStatus, 0, len(vms))
	for _, v := range vms {
		out = append(out, vmStatus{VM: v, PID: vmPID(s.data, v.ID)})
	}
	writeJSON(w, http.StatusOK, out)
}

// startVM streams NDJSON for the same reason createVM does: bringing the tap and
// policy back and waiting out qemu's startup grace is seconds, not milliseconds,
// and the progress lines are the same ones a create prints.
func (s *apiServer) startVM(w http.ResponseWriter, r *http.Request) {
	v, err := resolveVM(s.data, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody{err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	logf := func(format string, args ...any) {
		_ = enc.Encode(event{Log: fmt.Sprintf(format, args...)})
		flush()
	}

	// Detached from the request: a client that hangs up must not leave a VM whose
	// tap exists but whose qemu was never started.
	if _, err := startVM(context.WithoutCancel(r.Context()), s.data, v, logf); err != nil {
		_ = enc.Encode(event{Error: err.Error()})
		flush()
		return
	}
	// Re-read rather than reusing v: startVM rewrites vm.json with the tap and
	// cgroup this run got, and the client should see those, not the stale ones.
	if started, err := loadVM(s.data, v.ID); err == nil {
		v = started
	}
	_ = enc.Encode(event{VM: &v})
	flush()
}

func (s *apiServer) stopVM(w http.ResponseWriter, r *http.Request) {
	v, err := resolveVM(s.data, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody{err.Error()})
		return
	}
	timeout := defaultStopWait
	if q := r.URL.Query().Get("timeout"); q != "" {
		if d, err := time.ParseDuration(q); err == nil {
			timeout = d
		}
	}
	if err := stopVM(r.Context(), s.data, v.ID, timeout); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{err.Error()})
		return
	}
	if err := teardownVMNetwork(v); err != nil {
		log.Printf("could not fully tear down the network for %s: %v", v.ID, err)
	}
	if err := removeCgroup(v.Cgroup); err != nil {
		log.Printf("could not remove cgroup %s: %v", v.Cgroup, err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": v.ID})
}

func (s *apiServer) removeVM(w http.ResponseWriter, r *http.Request) {
	v, err := resolveVM(s.data, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody{err.Error()})
		return
	}
	if err := removeVM(r.Context(), s.data, v); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": v.ID})
}

// console hijacks the connection and proxies raw bytes to the guest's serial
// port. This is the one exchange that is not request/response -- a terminal is
// bidirectional and unframed, and wrapping it in anything would only get in the
// way.
func (s *apiServer) console(w http.ResponseWriter, r *http.Request) {
	v, err := resolveVM(s.data, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody{err.Error()})
		return
	}
	if vmPID(s.data, v.ID) == 0 {
		writeJSON(w, http.StatusConflict, errorBody{fmt.Sprintf(
			"vm %s is not running; its console output is in %s", v.ID, vmPath(s.data, v.ID, vmConsoleLog))})
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorBody{"the console cannot be streamed here"})
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{err.Error()})
		return
	}
	defer conn.Close()

	guest, err := net.Dial("unix", vmPath(s.data, v.ID, vmConsoleSock))
	if err != nil {
		_, _ = buf.WriteString("HTTP/1.1 500 Internal Server Error\r\n\r\n" + err.Error())
		_ = buf.Flush()
		return
	}
	defer guest.Close()

	if _, err := buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: dclient-console\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		return
	}
	if err := buf.Flush(); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(guest, buf); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, guest); done <- struct{}{} }()
	<-done
}

// --- wire types -------------------------------------------------------------

// event is one line of the NDJSON create stream: progress, then exactly one of
// a finished VM or an error.
type event struct {
	Log   string `json:"log,omitempty"`
	VM    *vm    `json:"vm,omitempty"`
	Error string `json:"error,omitempty"`
}

type vmStatus struct {
	VM
	PID int `json:"pid"`
}

// VM is embedded rather than aliased so the json shape stays the vm record plus
// the one piece of live state the client needs.
type VM = vm

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// --- listener ---------------------------------------------------------------

// listen creates the socket with the ownership and mode that make group
// membership the access control. A stale socket from a killed daemon is
// removed: nothing else is allowed to hold this path.
func listen(socket, group string) (net.Listener, error) {
	if err := os.MkdirAll(socketDir(socket), 0o755); err != nil {
		return nil, err
	}
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("could not listen on %s: %w", socket, err)
	}

	gid := -1
	if g, err := user.LookupGroup(group); err == nil {
		if n, err := strconv.Atoi(g.Gid); err == nil {
			gid = n
		}
	} else {
		log.Printf("group %q does not exist; the socket will be root-only (run `dclient install` to create it)", group)
	}
	if gid >= 0 {
		if err := os.Chown(socket, 0, gid); err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("could not chown %s to group %s: %w", socket, group, err)
		}
		if err := os.Chmod(socket, 0o660); err != nil {
			_ = ln.Close()
			return nil, err
		}
	} else if err := os.Chmod(socket, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// serveAPI runs until the context is cancelled.
func (s *apiServer) serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler: s.routes(),
		// No read timeout: the console holds a connection open indefinitely by
		// design, and creating a VM can take minutes.
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	log.Printf("api on %s", ln.Addr())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// socketUsable reports whether a daemon is listening and we may talk to it.
func socketUsable(socket string) bool {
	info, err := os.Stat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return false
	}
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		if errno, ok := err.(*net.OpError); ok {
			if se, ok := errno.Err.(*os.SyscallError); ok && se.Err == syscall.EACCES {
				log.Printf("a daemon is running but %s is not writable by you; join the %s group",
					socket, defaultGroup)
			}
		}
		return false
	}
	_ = conn.Close()
	return true
}
