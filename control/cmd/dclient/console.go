package main

import (
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

const detachKey = 0x1d

func attachConsole(socket string) error {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("could not reach the console at %s: %w", socket, err)
	}
	defer conn.Close()
	return proxyConsole(conn, conn)
}

func proxyConsole(out io.Writer, in io.Reader) error {
	restore, err := makeRaw(int(os.Stdin.Fd()))
	if err == nil {
		defer restore()
		fmt.Fprint(os.Stderr, "attached; ctrl-] to detach\r\n")
	} else {
		fmt.Fprintln(os.Stderr, "attached; stdin is not a terminal")
	}

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(os.Stdout, in)
		done <- err
	}()
	go func() {
		done <- copyUntilDetach(out, os.Stdin)
	}()

	return <-done
}

func copyUntilDetach(dst io.Writer, src io.Reader) error {
	var b [1]byte
	for {
		n, err := src.Read(b[:])
		if n > 0 {
			if b[0] == detachKey {
				return nil
			}
			if _, err := dst.Write(b[:n]); err != nil {
				return err
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func makeRaw(fd int) (func(), error) {
	prev, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	raw := *prev
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, prev) }, nil
}
