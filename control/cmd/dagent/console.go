package main

import (
  "fmt"
  "io"
  "net"
  "os"

  "golang.org/x/sys/unix"
)

// detachKey is ctrl-] , the same escape telnet and docker attach use.
const detachKey = 0x1d

// attachConsole proxies the terminal to the guest's serial port. Everything the
// guest writes is already being recorded to console.log by qemu, so detaching
// loses nothing.
func attachConsole(socket string) error {
  conn, err := net.Dial("unix", socket)
  if err != nil {
    return fmt.Errorf("could not reach the console at %s: %w", socket, err)
  }
  defer conn.Close()
  return proxyConsole(conn, conn)
}

// proxyConsole is the terminal half, shared by the direct path and the daemon
// client -- the transport differs, the terminal handling does not.
func proxyConsole(out io.Writer, in io.Reader) error {
  restore, err := makeRaw(int(os.Stdin.Fd()))
  if err == nil {
    defer restore()
    fmt.Fprint(os.Stderr, "attached; ctrl-] to detach\r\n")
  } else {
    // Not a terminal (a pipe, or output being captured). Still useful, just
    // line-buffered and without the escape.
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

// copyUntilDetach forwards bytes to the guest and returns when the detach key
// is seen. It is byte-at-a-time on purpose: in raw mode the terminal is
// unbuffered, and interactive typing is not a throughput problem.
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

// makeRaw puts the terminal in raw mode so keystrokes reach the guest as typed:
// no local echo, no line buffering, and ctrl-c goes to the guest rather than
// killing us.
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
