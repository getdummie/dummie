package dpipe

import (
	"io"
	"net"
	"sync"
)

// closeWriter is implemented by TCP, Unix and TLS connections.
type closeWriter interface{ CloseWrite() error }

// Pipe copies bytes in both directions until both halves are done, propagating
// half-close so a backend sees EOF when the client stops writing.
//
// On Linux io.Copy between two TCP sockets uses splice(2); when one side is a
// *tls.Conn the copy is necessarily a userspace one.
func Pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); copyHalf(b, a) }()
	go func() { defer wg.Done(); copyHalf(a, b) }()
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

func copyHalf(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	// Half-close so the peer sees EOF; Pipe closes both conns once both halves
	// are done. Connections without CloseWrite are simply closed by Pipe.
	if cw, ok := dst.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}
