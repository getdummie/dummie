package dinit

import (
	"os"
	"strconv"
	"strings"
)

// The guest's own /etc/passwd is the only account database dinit reads: no nss,
// no libc, so an image using ldap or sssd is out of scope.
type user struct {
	name  string
	uid   int
	gid   int
	home  string
	shell string
}

const (
	passwdPath = "/etc/passwd"
	groupPath  = "/etc/group"
)

func lookupUser(name string) (user, bool) {
	return lookupUserIn(passwdPath, name)
}

// resolveUser reads an image's User field, which is any of the forms docker
// accepts: a name, a numeric uid, or either with a group after a colon. A
// numeric id needs no entry in /etc/passwd -- a scratch image has none -- so it
// resolves to that id with no home and no shell.
func resolveUser(spec string) (user, bool) {
	return resolveUserIn(passwdPath, groupPath, spec)
}

func resolveUserIn(passwd, group, spec string) (user, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return user{}, false
	}
	name, groupSpec, hasGroup := strings.Cut(spec, ":")
	if name == "" {
		return user{}, false
	}

	u, ok := lookupUserIn(passwd, name)
	if !ok {
		uid, err := strconv.Atoi(name)
		if err != nil || uid < 0 {
			return user{}, false
		}
		if byID, found := lookupUserByUIDIn(passwd, uid); found {
			u = byID
		} else {
			u = user{name: name, uid: uid, gid: uid}
		}
	}

	if hasGroup {
		gid, ok := resolveGroupIn(group, groupSpec)
		if !ok {
			return user{}, false
		}
		u.gid = gid
	}
	return u, true
}

func lookupUserByUIDIn(path string, uid int) (user, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return user{}, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		if id, err := strconv.Atoi(f[2]); err != nil || id != uid {
			continue
		}
		gid, err := strconv.Atoi(f[3])
		if err != nil {
			continue
		}
		return user{name: f[0], uid: uid, gid: gid, home: f[5], shell: f[6]}, true
	}
	return user{}, false
}

func resolveGroupIn(path, spec string) (int, bool) {
	if spec == "" {
		return 0, false
	}
	if gid, err := strconv.Atoi(spec); err == nil && gid >= 0 {
		return gid, true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 || f[0] != spec {
			continue
		}
		gid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		return gid, true
	}
	return 0, false
}

func lookupUserIn(path, name string) (user, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return user{}, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 || f[0] != name {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		gid, err := strconv.Atoi(f[3])
		if err != nil {
			continue
		}
		return user{name: name, uid: uid, gid: gid, home: f[5], shell: f[6]}, true
	}
	return user{}, false
}
