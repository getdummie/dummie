package main

import (
  "crypto/rand"
  "crypto/sha256"
  "encoding/base64"
  "encoding/hex"
  "time"

  "github.com/golang-jwt/jwt/v5"
  "github.com/google/uuid"

  "control/internal/db"
)

// newAccessToken mints a short-lived HS256 JWT carrying the user's identity.
func newAccessToken(u db.User, secret string, ttl time.Duration) (string, error) {
  now := time.Now()
  claims := jwt.MapClaims{
    "sub":       uuid.UUID(u.ID.Bytes).String(),
    "username":  u.Username,
    "user_type": u.UserType,
    "iat":       now.Unix(),
    "exp":       now.Add(ttl).Unix(),
  }
  tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
  return tok.SignedString([]byte(secret))
}

// newRefreshToken returns a high-entropy opaque token (raw, to hand to the client).
func newRefreshToken() (string, error) {
  b := make([]byte, 32)
  if _, err := rand.Read(b); err != nil {
    return "", err
  }
  return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashRefresh is the value we persist: we never store the raw refresh token.
func hashRefresh(raw string) string {
  sum := sha256.Sum256([]byte(raw))
  return hex.EncodeToString(sum[:])
}
