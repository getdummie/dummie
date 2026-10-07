package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, OpText, []byte(`{"t":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&buf, OpBinary, nil); err != nil {
		t.Fatal(err)
	}
	op, p, err := ReadFrame(&buf)
	if err != nil || op != OpText || string(p) != `{"t":"hello"}` {
		t.Fatalf("got %d %q %v", op, p, err)
	}
	op, p, err = ReadFrame(&buf)
	if err != nil || op != OpBinary || len(p) != 0 {
		t.Fatalf("got %d %q %v", op, p, err)
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	r := bytes.NewReader([]byte{OpText, 0xff, 0xff, 0xff, 0xff})
	if _, _, err := ReadFrame(r); err == nil {
		t.Fatal("an oversize frame was accepted")
	}
}

func writeSession(t *testing.T, lines ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "--work--")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "2024_abc.jsonl")
	if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSummarize(t *testing.T) {
	huge := `{"type":"message","message":{"role":"user","content":"` + strings.Repeat("x", maxScanLine*2) + `"}}`
	f := writeSession(t,
		`{"type":"session","version":3,"id":"abc","timestamp":"2024-12-03T14:00:00.000Z","cwd":"/work"}`,
		huge,
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"fix the login   bug"}]}}`,
		`{"type":"message","message":{"role":"user","content":"second"}}`,
		`{"type":"session_info","name":"Auth"}`,
	)
	s, err := summarize(f)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "abc" || s.Cwd != "/work" || s.Name != "Auth" {
		t.Fatalf("header or name wrong: %+v", s)
	}
	// The oversize first message is skipped, so the title is the next one.
	if s.Title != "fix the login bug" {
		t.Fatalf("title = %q", s.Title)
	}
}

func TestSummarizeRejectsNonSession(t *testing.T) {
	f := writeSession(t, `{"type":"message"}`)
	if _, err := summarize(f); err == nil {
		t.Fatal("a file without a session header was accepted")
	}
}

func TestReadLineKeepsUnicodeSeparators(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("a\u2028b\u2029c\nd"))
	line, _ := readLine(br)
	if string(line) != "a\u2028b\u2029c" {
		t.Fatalf("line = %q", line)
	}
}

func TestWriteProvidersKeepsUserEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	user := `{"providers":{"ollama":{"baseUrl":"http://localhost:11434/v1"},"dummie-chatgpt":{"baseUrl":"old"}},"other":1}`
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeProviders(path, map[string]*piProvider{
		providerChat:      {BaseURL: "https://llm.int.example.com/v1", API: "openai-completions", APIKey: "x", Models: []piModel{{ID: "zai/glm-4.6"}}},
		providerResponses: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Providers map[string]json.RawMessage `json:"providers"`
		Other     int                        `json:"other"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Providers["ollama"]; !ok || doc.Other != 1 {
		t.Fatalf("user content lost: %s", b)
	}
	if _, ok := doc.Providers[providerResponses]; ok {
		t.Fatalf("a provider with no models was kept: %s", b)
	}
	if _, ok := doc.Providers[providerChat]; !ok {
		t.Fatalf("the chat provider was not written: %s", b)
	}
}

func TestWriteProvidersLeavesBrokenFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeProviders(path, map[string]*piProvider{providerChat: nil}); err == nil {
		t.Fatal("a broken models.json was overwritten")
	}
	if b, _ := os.ReadFile(path); string(b) != "{nope" {
		t.Fatalf("file changed to %q", b)
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd": "passwd",
		".bashrc":          "bashrc",
		"my photo (1).png": "my_photo_1_.png",
		"":                 "file",
	} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOwnsUpload(t *testing.T) {
	d := &daemon{home: "/home/u"}
	if !d.ownsUpload("/home/u/.dummie/uploads/2024-01-01/1-a.png") {
		t.Error("an upload was refused")
	}
	for _, p := range []string{"/etc/shadow", "/home/u/.dummie/uploads/../../.ssh/id_ed25519", "/home/u/.dummie/uploads"} {
		if d.ownsUpload(p) {
			t.Errorf("%s was accepted as an upload", p)
		}
	}
}

func TestFindSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", root)
	dir := filepath.Join(root, "--work--")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "2026-10-06T07-35-05-582Z_01a1-77e4.jsonl")
	if err := os.WriteFile(f, []byte(`{"type":"session","id":"01a1-77e4","cwd":"/work"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &daemon{procs: map[string]*piProc{"/live.jsonl": {id: "live-1"}}}

	if got, err := d.findSession("01a1-77e4"); err != nil || got != f {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := d.findSession("live-1"); err != nil || got != "/live.jsonl" {
		t.Fatalf("a running session was not found: %q, %v", got, err)
	}
	for _, bad := range []string{"*", "../x", "", "nope"} {
		if _, err := d.findSession(bad); err == nil {
			t.Errorf("%q resolved to a session", bad)
		}
	}
}

func TestResolveInStaysInside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveIn(root, "src/a.go"); err != nil {
		t.Errorf("a plain path was refused: %v", err)
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	if got, err := resolveIn(root, "../../etc/passwd"); err != nil || !strings.HasPrefix(got, realRoot) {
		// ".." is clamped to the root rather than refused.
		t.Errorf("../ resolved to %q, %v", got, err)
	}
	for _, bad := range []string{"escape/x", "link", ""} {
		if _, err := resolveIn(root, bad); err == nil {
			t.Errorf("%q was allowed", bad)
		}
	}
}

func TestWriteEditableConflicts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := readEditable(root, "a.txt")
	if err != nil || f.Content != "one" || !f.Exists {
		t.Fatalf("read %+v, %v", f, err)
	}
	// The agent edits it underneath the editor.
	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeEditable(root, "a.txt", f.Hash, false, []byte("mine")); !errors.Is(err, errConflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if _, err := writeEditable(root, "a.txt", f.Hash, true, []byte("mine")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if string(b) != "mine" || fi.Mode().Perm() != 0o600 {
		t.Fatalf("got %q mode %v", b, fi.Mode().Perm())
	}
	if _, err := writeEditable(root, "new.txt", "", false, []byte("x")); err != nil {
		t.Fatalf("a new file was refused: %v", err)
	}
}

func TestReadEditableRefusesBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "b.bin"), []byte{1, 0, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEditable(root, "b.bin"); err == nil {
		t.Fatal("a binary file was opened for editing")
	}
}

func TestRetireOthers(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "agent-dev-1.sock")
	ln, err := net.Listen("unix", other)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, p, _ := ReadFrame(c)
		got <- string(p)
	}()

	stale := filepath.Join(dir, "agent-dev-2.sock")
	if l, err := net.Listen("unix", stale); err == nil {
		l.(*net.UnixListener).SetUnlinkOnClose(false)
		_ = l.Close()
	}
	own := socketPath(dir, "dev-3")
	if err := os.WriteFile(own, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	retireOthers(dir, "dev-3")
	if m := <-got; m != `{"t":"retire"}` {
		t.Fatalf("the other daemon got %q", m)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a socket nobody listens on was left behind")
	}
	if _, err := os.Stat(own); err != nil {
		t.Error("retireOthers touched this binary's own socket")
	}
}
