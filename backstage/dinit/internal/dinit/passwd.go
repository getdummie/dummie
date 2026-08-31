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

const passwdPath = "/etc/passwd"

func lookupUser(name string) (user, bool) {
	return lookupUserIn(passwdPath, name)
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
