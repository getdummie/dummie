package intproxy

import (
	"encoding/json"
	"io"
	"os"
	"time"

	"intproxy/internal/integration"
)

// UsageMarker starts every usage line, which is how a log shipper tells them
// apart from the rest of this process's output on the same stream.
const UsageMarker = "intproxyusage"

type usageLine struct {
	Timestamp   string `json:"ts"`
	Meter       string `json:"meter"`
	Integration string `json:"integration"`
	Status      int    `json:"status"`
	DurationMS  int64  `json:"duration_ms"`
	Model       string `json:"model"`
	Input       int64  `json:"input_tokens"`
	Output      int64  `json:"output_tokens"`
	CacheRead   int64  `json:"cache_read_tokens"`
	CacheWrite  int64  `json:"cache_write_tokens"`
}

func (s *Server) recordUsage(ig, meter string, status int, took time.Duration, u integration.Usage) {
	model := u.Model
	if len(model) > 128 {
		model = model[:128]
	}
	b, err := json.Marshal(usageLine{
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Meter:       meter,
		Integration: ig,
		Status:      status,
		DurationMS:  took.Milliseconds(),
		Model:       model,
		Input:       u.Input,
		Output:      u.Output,
		CacheRead:   u.CacheRead,
		CacheWrite:  u.CacheWrite,
	})
	if err != nil {
		return
	}
	line := append([]byte(UsageMarker+" "), b...)
	line = append(line, '\n')

	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if _, err := s.usageOut().Write(line); err != nil {
		s.log.Warn("could not record usage", "integration", ig, "error", err)
	}
}

func (s *Server) usageOut() io.Writer {
	if s.usage != nil {
		return s.usage
	}
	return os.Stdout
}
