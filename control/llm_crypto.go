package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// llmSealer keeps llm api keys encrypted at rest. They cannot be hashed like
// our own tokens: the broker has to hand the plaintext to intproxy.
type llmSealer struct {
	aead cipher.AEAD
}

// loadLLMSealer returns nil when no key is configured, which turns the llm
// endpoints into 503s rather than refusing to start.
func loadLLMSealer() *llmSealer {
	raw := strings.TrimSpace(os.Getenv("LLM_KEY_ENCRYPTION_KEY"))
	if raw == "" {
		log.Printf("LLM_KEY_ENCRYPTION_KEY is not set; llm keys cannot be stored or used")
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		log.Fatal("LLM_KEY_ENCRYPTION_KEY must be 32 bytes, base64 encoded")
	}
	s, err := newLLMSealer(key)
	if err != nil {
		log.Fatalf("could not set up llm key encryption: %v", err)
	}
	return s
}

func newLLMSealer(key []byte) (*llmSealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &llmSealer{aead: aead}, nil
}

// llmKeyAAD binds a sealed key to its row, so a blob copied onto another
// owner or provider does not decrypt.
func llmKeyAAD(provider string, owner pgtype.UUID) []byte {
	who := "global"
	if owner.Valid {
		who = domainIDString(owner)
	}
	return []byte(provider + "|" + who)
}

func (s *llmSealer) seal(plain string, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(plain), aad), nil
}

func (s *llmSealer) open(blob, aad []byte) (string, error) {
	n := s.aead.NonceSize()
	if len(blob) < n {
		return "", errors.New("sealed llm key is truncated")
	}
	plain, err := s.aead.Open(nil, blob[:n], blob[n:], aad)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
