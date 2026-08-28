package httpsniff

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
)

var (
	ErrTooLarge = errors.New("httpsniff: header block too large")
	ErrMalformed = errors.New("httpsniff: malformed request")
	ErrNoHost = errors.New("httpsniff: missing host")
)

var terminator = []byte("\r\n\r\n")

const DefaultMaxBytes = 65536

func ReadHeaderBlock(r io.Reader, max int) ([]byte, string, error) {
	if max <= 0 {
		max = DefaultMaxBytes
	}
	buf := make([]byte, 0, 2048)
	tmp := make([]byte, 2048)
	searched := 0
	end := -1

	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			from := searched - (len(terminator) - 1)
			if from < 0 {
				from = 0
			}
			if i := bytes.Index(buf[from:], terminator); i >= 0 {
				end = from + i + len(terminator)
			}
			searched = len(buf)
		}
		if end >= 0 {
			break
		}
		if len(buf) > max {
			return buf, "", ErrTooLarge
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf, "", ErrMalformed
			}
			return buf, "", err
		}
	}

	if end > max {
		return buf, "", ErrTooLarge
	}
	host, err := ParseHost(buf[:end])
	return buf, host, err
}

func ParseHost(header []byte) (string, error) {
	lines := strings.Split(strings.TrimRight(string(header), "\r\n"), "\r\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", ErrMalformed
	}

	parts := strings.Fields(lines[0])
	if len(parts) < 3 || !strings.HasPrefix(parts[2], "HTTP/1.") {
		return "", ErrMalformed
	}
	if h := authorityFromTarget(parts[1]); h != "" {
		return normalizeHost(h), nil
	}

	for _, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(name), "host") {
			continue
		}
		if h := normalizeHost(strings.TrimSpace(value)); h != "" {
			return h, nil
		}
	}
	return "", ErrNoHost
}

func authorityFromTarget(target string) string {
	i := strings.Index(target, "://")
	if i < 0 {
		return ""
	}
	rest := target[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	return rest
}

func NormalizeHost(h string) string { return normalizeHost(h) }

func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(h, ".")
	h = strings.Trim(h, "[]")
	return strings.ToLower(h)
}
