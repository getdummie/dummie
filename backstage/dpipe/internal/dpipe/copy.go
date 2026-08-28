package dpipe

import (
	"io"
	"net"
	"sync"
)

type closeWriter interface{ CloseWrite() error }

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
	if cw, ok := dst.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}
