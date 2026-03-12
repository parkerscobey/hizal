package auth

import (
	"strings"
	"testing"
)

func TestGenerateAPIKeyNormalizesPrefix(t *testing.T) {
	plaintext, _, err := GenerateAPIKey("  Test Key / Prod  ")
	if err != nil {
		t.Fatalf("GenerateAPIKey returned error: %v", err)
	}

	if !strings.HasPrefix(plaintext, "ctx_test-key-prod_") {
		t.Fatalf("plaintext = %q, want prefix %q", plaintext, "ctx_test-key-prod_")
	}
}

func TestGenerateAPIKeyFallsBackWhenPrefixIsEmpty(t *testing.T) {
	plaintext, _, err := GenerateAPIKey("   !!!   ")
	if err != nil {
		t.Fatalf("GenerateAPIKey returned error: %v", err)
	}

	if !strings.HasPrefix(plaintext, "ctx_key_") {
		t.Fatalf("plaintext = %q, want prefix %q", plaintext, "ctx_key_")
	}
}
