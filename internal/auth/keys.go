package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var keyPrefixSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

// GenerateAPIKey generates a new API key for an org.
// Returns (plaintext, sha256hex) — the hash is stored in the DB.
// We use SHA-256 of a 32-byte random key for deterministic lookup.
// 32 bytes of entropy = 256 bits, equivalent security to bcrypt for random tokens.
func GenerateAPIKey(orgSlug string) (plaintext, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("failed to generate key bytes: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	plaintext = fmt.Sprintf("ctx_%s_%s", normalizeKeyPrefix(orgSlug), encoded)
	hash = sha256Hex(plaintext)
	return plaintext, hash, nil
}

// HashKey returns the SHA-256 hex of a key for DB lookup.
func HashKey(plaintext string) string {
	return sha256Hex(plaintext)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func normalizeKeyPrefix(s string) string {
	normalized := strings.ToLower(strings.TrimSpace(s))
	normalized = keyPrefixSanitizer.ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, "-")
	if normalized == "" {
		return "key"
	}
	return normalized
}
