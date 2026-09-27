package main

import (
	"bytes"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestLLMSealerRoundTripAndBinding(t *testing.T) {
	s, err := newLLMSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	owner := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}

	blob, err := s.seal("zai-secret", llmKeyAAD("zai", owner))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("zai-secret")) {
		t.Fatal("the key is readable in the sealed blob")
	}
	got, err := s.open(blob, llmKeyAAD("zai", owner))
	if err != nil || got != "zai-secret" {
		t.Fatalf("open = %q, %v", got, err)
	}

	// A user's blob moved onto the global row must not become everyone's key.
	if _, err := s.open(blob, llmKeyAAD("zai", pgtype.UUID{})); err == nil {
		t.Fatal("a blob opened under another row's binding")
	}
}
