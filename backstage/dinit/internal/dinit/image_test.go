package dinit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadImageConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "image.json")
	body := `{"user":"appuser","entrypoint":["/bin/sh","-c"],"cmd":["exec marimo edit"],"env":["PORT=8080","HOST=0.0.0.0"]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	c := readImageConfig(p)
	if c.User != "appuser" {
		t.Errorf("user = %q, want appuser", c.User)
	}
	want := []string{"/bin/sh", "-c", "exec marimo edit"}
	if got := c.argv(); !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestReadImageConfigWithoutAFile(t *testing.T) {
	c := readImageConfig(filepath.Join(t.TempDir(), "missing.json"))
	if c.User != "" || len(c.argv()) != 0 || len(c.Env) != 0 {
		t.Errorf("a missing config yielded %+v", c)
	}
}

func TestReadImageConfigThatIsNotJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "image.json")
	if err := os.WriteFile(p, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := readImageConfig(p); c.User != "" || len(c.argv()) != 0 {
		t.Errorf("a corrupt config yielded %+v, want the zero value", c)
	}
}

func TestArgvIsEntrypointThenCmd(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entrypoint []string
		cmd        []string
		want       []string
	}{
		{"both", []string{"/app"}, []string{"--serve"}, []string{"/app", "--serve"}},
		{"entrypoint only", []string{"/app"}, nil, []string{"/app"}},
		{"cmd only", nil, []string{"/app", "--serve"}, []string{"/app", "--serve"}},
		{"neither", nil, nil, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := imageConfig{Entrypoint: tc.entrypoint, Cmd: tc.cmd}
			if got := c.argv(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDedupEnvKeepsTheLastAssignment(t *testing.T) {
	got := dedupEnv([]string{"PATH=/a", "PORT=1", "TERM=dumb", "PORT=2"})
	want := []string{"PATH=/a", "TERM=dumb", "PORT=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dedupEnv = %q, want %q", got, want)
	}
}

func TestEnvPutsTheImagePathOverTheBuiltInOne(t *testing.T) {
	c := imageConfig{Env: []string{"PATH=/opt/venv/bin", "PORT=8080"}}
	env := c.env(user{name: "appuser", uid: 1000, gid: 1000, home: "/home/appuser"})

	var paths []string
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			paths = append(paths, e)
		}
	}
	if len(paths) != 1 || paths[0] != "PATH=/opt/venv/bin" {
		t.Errorf("PATH entries = %q, want just the image's", paths)
	}
	for _, want := range []string{"PORT=8080", "HOME=/home/appuser", "USER=appuser", "LOGNAME=appuser"} {
		if !contains(env, want) {
			t.Errorf("env is missing %q: %q", want, env)
		}
	}
}

func TestEnvLetsAnOverrideWin(t *testing.T) {
	c := imageConfig{Env: []string{"TERM=dumb"}}
	env := c.env(user{name: "root", home: "/root"}, "TERM=xterm")
	if !contains(env, "TERM=xterm") || contains(env, "TERM=dumb") {
		t.Errorf("the override did not win: %q", env)
	}
}

func TestDefaultUserFallsBackToRoot(t *testing.T) {
	if u := (imageConfig{}).defaultUser(); u.name != "root" || u.uid != 0 {
		t.Errorf("an image with no user yielded %+v, want root", u)
	}
}

func TestShellQuote(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain", "'plain'"},
		{"a b", "'a b'"},
		{"$HOME", "'$HOME'"},
		{"it's", `'it'\''s'`},
		{"a`b`", "'a`b`'"},
		{`a"b`, `'a"b'`},
	} {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestResolveUser(t *testing.T) {
	dir := t.TempDir()
	passwd := filepath.Join(dir, "passwd")
	group := filepath.Join(dir, "group")
	if err := os.WriteFile(passwd, []byte(
		"root:x:0:0:root:/root:/bin/bash\n"+
			"appuser:x:1000:1000::/home/appuser:/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(group, []byte("root:x:0:\nappgroup:x:2000:\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		spec string
		want user
	}{
		{"appuser", user{name: "appuser", uid: 1000, gid: 1000, home: "/home/appuser", shell: "/bin/sh"}},
		{"1000", user{name: "appuser", uid: 1000, gid: 1000, home: "/home/appuser", shell: "/bin/sh"}},
		{"appuser:appgroup", user{name: "appuser", uid: 1000, gid: 2000, home: "/home/appuser", shell: "/bin/sh"}},
		{"1000:2000", user{name: "appuser", uid: 1000, gid: 2000, home: "/home/appuser", shell: "/bin/sh"}},
		// A uid with no passwd entry is still a valid user to run as: a
		// scratch image has no /etc/passwd at all.
		{"1500", user{name: "1500", uid: 1500, gid: 1500}},
		{"1500:2000", user{name: "1500", uid: 1500, gid: 2000}},
	} {
		got, ok := resolveUserIn(passwd, group, tc.spec)
		if !ok {
			t.Errorf("resolveUser(%q) did not resolve", tc.spec)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveUser(%q) = %+v, want %+v", tc.spec, got, tc.want)
		}
	}

	for _, spec := range []string{"", "nosuchuser", "appuser:nosuchgroup", ":1000"} {
		if _, ok := resolveUserIn(passwd, group, spec); ok {
			t.Errorf("resolveUser(%q) resolved, want a refusal", spec)
		}
	}
}

func TestResolveArgv0SearchesTheImagePath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sh := filepath.Join(bin, "sh")
	if err := os.WriteFile(sh, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	notExec := filepath.Join(bin, "data")
	if err := os.WriteFile(notExec, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=/nonexistent:" + bin}

	// A bare name is what an image's entrypoint almost always is, and ForkExec
	// would fail on it with ENOENT if it were passed through unresolved.
	got, err := resolveArgv0("sh", env)
	if err != nil {
		t.Fatalf("resolveArgv0(sh) = %v, want %s", err, sh)
	}
	if got != sh {
		t.Errorf("resolveArgv0(sh) = %s, want %s", got, sh)
	}

	if got, err := resolveArgv0(sh, env); err != nil || got != sh {
		t.Errorf("an absolute path did not pass through: %s, %v", got, err)
	}

	for _, bad := range []string{"", "nosuchbinary", "data", filepath.Join(bin, "nosuchbinary"), notExec} {
		if _, err := resolveArgv0(bad, env); err == nil {
			t.Errorf("resolveArgv0(%q) resolved, want a refusal", bad)
		}
	}
}

func TestResolveArgv0UsesTheImagePathNotDinits(t *testing.T) {
	// The image's PATH is the one its binaries were installed against; dinit's
	// own is only a fallback for images that declare none.
	if _, err := resolveArgv0("sh", []string{"PATH="}); err == nil {
		t.Error("an empty PATH still resolved a bare name")
	}
}

func TestEnvValueTakesTheLastAssignment(t *testing.T) {
	env := []string{"PATH=/first", "PORT=1", "PATH=/last"}
	if got := envValue(env, "PATH"); got != "/last" {
		t.Errorf("envValue(PATH) = %q, want /last", got)
	}
	if got := envValue(env, "MISSING"); got != "" {
		t.Errorf("envValue(MISSING) = %q, want empty", got)
	}
}

func contains(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}
