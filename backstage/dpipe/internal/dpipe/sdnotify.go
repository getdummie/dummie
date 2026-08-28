package dpipe

import (
	"fmt"
	"net"
	"os"
	"strings"
)

func Notify(state string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}
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

func NotifyReady() error { return Notify("READY=1") }

func NotifyMainPID(pid int) error {
	return Notify(fmt.Sprintf("MAINPID=%d\nREADY=1", pid))
}
