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
	s, err := summarize(f, statOf(t, f))
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
	if _, err := summarize(f, statOf(t, f)); err == nil {
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
	if got, err := findPiSession("01a1-77e4"); err != nil || got != f {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"*", "../x", "", "nope"} {
		if _, err := findPiSession(bad); err == nil {
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

func TestReadRawChunkWalksTheFile(t *testing.T) {
	root := t.TempDir()
	want := bytes.Repeat([]byte{0, 1, 2}, rawChunk/2)
	if err := os.WriteFile(filepath.Join(root, "a.bin"), want, 0o600); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for {
		b, size, err := readRawChunk(root, "a.bin", int64(len(got)))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, b...)
		if int64(len(got)) >= size {
			break
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("read %d bytes, want %d", len(got), len(want))
	}
	if _, _, err := readRawChunk(root, "a.bin", int64(len(want))+1); err == nil {
		t.Fatal("an offset past the end was accepted")
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

func statOf(t *testing.T, f string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func writeLines(t *testing.T, name string, lines ...string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSummarizeClaude(t *testing.T) {
	f := writeLines(t, "7f0c.jsonl",
		`{"type":"summary","summary":"Login fix"}`,
		`{"type":"user","isMeta":true,"cwd":"/work","message":{"role":"user","content":"Caveat"}}`,
		`{"type":"user","isSidechain":true,"cwd":"/work","message":{"role":"user","content":"sub"}}`,
		`{"type":"user","cwd":"/work","timestamp":"2026-10-01T00:00:00Z","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"user","cwd":"/work","message":{"role":"user","content":[{"type":"text","text":"fix the login"}]}}`,
	)
	s, err := summarizeClaude(f, statOf(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "7f0c" || s.Cwd != "/work" || s.Name != "Login fix" || s.Title != "fix the login" {
		t.Fatalf("got %+v", s)
	}
	empty := writeLines(t, "x.jsonl", `{"type":"summary","summary":"only"}`)
	if _, err := summarizeClaude(empty, statOf(t, empty)); err == nil {
		t.Fatal("a session without a prompt was listed")
	}
}

func TestSummarizeCodex(t *testing.T) {
	meta := `{"type":"session_meta","payload":{"id":"0199","timestamp":"2026-10-01T00:00:00Z","cwd":"/work","instructions":"` +
		strings.Repeat("x", maxScanLine*2) + `"}}`
	f := writeLines(t, "rollout-2026-10-01T00-00-00-0199.jsonl", meta,
		`{"type":"event_msg","payload":{"type":"user_message","message":"<environment_context>"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"add tests"}}`,
	)
	s, err := summarizeCodex(f, statOf(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "0199" || s.Cwd != "/work" || s.Title != "add tests" {
		t.Fatalf("got %+v", s)
	}
	bad := writeLines(t, "rollout-x.jsonl", `{"type":"event_msg"}`)
	if _, err := summarizeCodex(bad, statOf(t, bad)); err == nil {
		t.Fatal("a file without session_meta was listed")
	}
}

func TestSummarizeGemini(t *testing.T) {
	f := writeLines(t, "session-1.json",
		`{"sessionId":"g1","startTime":"2026-10-01T00:00:00Z","messages":[{"type":"gemini","content":"hi"},{"type":"user","content":"write docs"}]}`)
	s, err := summarizeGemini(f, statOf(t, f), "/work")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "g1" || s.Cwd != "/work" || s.Title != "write docs" {
		t.Fatalf("got %+v", s)
	}
}

func TestProxyModelsSplitByApi(t *testing.T) {
	px := proxyInfo{Models: []llmModel{{ID: "zai/glm-4.6", OwnedBy: "zai"}, {ID: "chatgpt/gpt-5", OwnedBy: "chatgpt"}}}
	if got := px.chatModels(); len(got) != 1 || got[0] != "zai/glm-4.6" {
		t.Errorf("chat models = %v", got)
	}
	if got := px.responsesModels(); len(got) != 1 || got[0] != "chatgpt/gpt-5" {
		t.Errorf("responses models = %v", got)
	}
	if got := pickModel(&modelRef{ID: "nope"}, px.chatModels()); got != "zai/glm-4.6" {
		t.Errorf("an unknown model was kept: %q", got)
	}
}

func TestNewUUID(t *testing.T) {
	u := newUUID()
	if len(u) != 36 || u[14] != '4' || !strings.ContainsRune("89ab", rune(u[19])) {
		t.Fatalf("%q is not a v4 uuid", u)
	}
}

func TestSummarizeAllCachesUntilChanged(t *testing.T) {
	f := writeSession(t, `{"type":"session","id":"c1","cwd":"/a"}`)
	calls := 0
	fn := func(file string, fi os.FileInfo) (sessionSummary, error) {
		calls++
		return summarize(file, fi)
	}
	summarizeAll("test", []string{f}, fn)
	got := summarizeAll("test", []string{f}, fn)
	if calls != 1 || len(got) != 1 || got[0].Key != "test:c1" {
		t.Fatalf("calls = %d, got %+v", calls, got)
	}
	if err := os.WriteFile(f, []byte(`{"type":"session","id":"c1","cwd":"/b"}`+"\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := summarizeAll("test", []string{f}, fn); calls != 2 || got[0].Cwd != "/b" {
		t.Fatalf("a changed file was not read again: calls = %d, got %+v", calls, got)
	}
}

func TestWithUserBins(t *testing.T) {
	got := withUserBins("/home/u", "/home/u/.local/bin:/usr/bin")
	want := "/home/u/.bun/bin:/home/u/.opencode/bin:/home/u/.npm-global/bin:/home/u/.local/bin:/usr/bin"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := withUserBins("/home/u", ""); !strings.HasSuffix(got, "/home/u/.npm-global/bin") {
		t.Fatalf("an empty PATH left a trailing separator: %q", got)
	}
}
