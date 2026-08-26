package dpipe

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// Notify sends a state line to systemd's notification socket. It is a no-op
// when NOTIFY_SOCKET is unset, which is every case except running under a
// Type=notify unit -- so callers do not have to know how they were started.
//
// Errors are returned rather than logged because the one caller that cannot
// ignore them is the upgrade path: if systemd is not told that the main pid
// moved, it goes on watching the process that is about to drain and exit, and
// will call the unit dead while the new one is serving.
func Notify(state string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}
	// A leading '@' is systemd's spelling of an abstract socket, which Go writes
	// as a leading NUL.
	if strings.HasPrefix(addr, "@") {
		addr = "\x00" + addr[1:]
	}

	c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("notify %s: %w", addr, err)
	}
	defer c.Close()

	if _, err := c.Write([]byte(state)); err != nil {
		return fmt.Errorf("notify %s: %w", addr, err)
	}
	return nil
}

// NotifyReady says the process is up and serving.
func NotifyReady() error { return Notify("READY=1") }

// NotifyMainPID hands systemd the pid of the process that has just adopted the
// listeners, so the unit follows the handover instead of the process leaving it.
//
// READY=1 rides along in the same datagram: the two are one statement -- "this
// pid is the service now, and it is already serving" -- and sending them
// separately would leave a window where systemd has retargeted onto a process it
// does not yet consider ready.
func NotifyMainPID(pid int) error {
	return Notify(fmt.Sprintf("MAINPID=%d\nREADY=1", pid))
}
