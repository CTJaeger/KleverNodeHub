package auth

import (
	"strings"
	"testing"
)

func TestGenerateCodes(t *testing.T) {
	rm := NewRecoveryManager(nil)

	plaintext, hashed, err := rm.GenerateCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if len(plaintext) != recoveryCodeCount {
		t.Errorf("got %d codes, want %d", len(plaintext), recoveryCodeCount)
	}
	if len(hashed) != recoveryCodeCount {
		t.Errorf("got %d hashes, want %d", len(hashed), recoveryCodeCount)
	}

	// Check format: XXXX-XXXX-XXXX
	for i, code := range plaintext {
		parts := strings.Split(code, "-")
		if len(parts) != 3 {
			t.Errorf("code %d: expected 3 parts, got %d: %q", i, len(parts), code)
			continue
		}
		for _, part := range parts {
			if len(part) != 4 {
				t.Errorf("code %d: part %q should be 4 chars", i, part)
			}
		}
	}

	// All codes should be unique
	seen := make(map[string]bool)
	for _, code := range plaintext {
		if seen[code] {
			t.Errorf("duplicate code: %s", code)
		}
		seen[code] = true
	}
}

func TestVerifyValidCode(t *testing.T) {
	rm := NewRecoveryManager(nil)

	plaintext, _, err := rm.GenerateCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Each code should verify exactly once
	for i, code := range plaintext {
		if !rm.Verify(code) {
			t.Errorf("code %d should be valid", i)
		}
		// Second use should fail
		if rm.Verify(code) {
			t.Errorf("code %d should not verify twice", i)
		}
	}
}

func TestVerifyInvalidCode(t *testing.T) {
	rm := NewRecoveryManager(nil)

	_, _, err := rm.GenerateCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if rm.Verify("AAAA-BBBB-CCCC") {
		t.Error("invalid code should not verify")
	}
}

func TestVerifyNormalization(t *testing.T) {
	rm := NewRecoveryManager(nil)

	plaintext, _, err := rm.GenerateCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	code := plaintext[0]
	// Should work without dashes
	noDashes := strings.ReplaceAll(code, "-", "")
	if !rm.Verify(noDashes) {
		t.Error("code without dashes should verify")
	}
}

func TestVerifyCaseInsensitive(t *testing.T) {
	rm := NewRecoveryManager(nil)

	plaintext, _, err := rm.GenerateCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	code := plaintext[0]
	lower := strings.ToLower(code)
	if !rm.Verify(lower) {
		t.Error("lowercase code should verify")
	}
}

func TestRemaining(t *testing.T) {
	rm := NewRecoveryManager(nil)

	plaintext, _, err := rm.GenerateCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if rm.Remaining() != recoveryCodeCount {
		t.Errorf("remaining = %d, want %d", rm.Remaining(), recoveryCodeCount)
	}

	rm.Verify(plaintext[0])
	if rm.Remaining() != recoveryCodeCount-1 {
		t.Errorf("remaining = %d, want %d", rm.Remaining(), recoveryCodeCount-1)
	}
}

func TestRegenerate(t *testing.T) {
	rm := NewRecoveryManager(nil)

	plaintext1, _, err := rm.GenerateCodes()
	if err != nil {
		t.Fatalf("generate 1: %v", err)
	}

	// Use one code
	rm.Verify(plaintext1[0])

	// Regenerate
	plaintext2, _, err := rm.GenerateCodes()
	if err != nil {
		t.Fatalf("generate 2: %v", err)
	}

	// Old codes should no longer work
	if rm.Verify(plaintext1[1]) {
		t.Error("old code should not verify after regeneration")
	}

	// New codes should work
	if !rm.Verify(plaintext2[0]) {
		t.Error("new code should verify")
	}

	if rm.Remaining() != recoveryCodeCount-1 {
		t.Errorf("remaining = %d, want %d", rm.Remaining(), recoveryCodeCount-1)
	}
}

func TestCodesReturnsCopy(t *testing.T) {
	rm := NewRecoveryManager(nil)

	_, _, err := rm.GenerateCodes()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	codes1 := rm.Codes()
	codes2 := rm.Codes()

	// Modifying the returned slice should not affect the manager
	codes1[0].Used = true
	if rm.Remaining() != recoveryCodeCount {
		t.Error("modifying returned codes should not affect manager")
	}

	// Both copies should be independent
	if &codes1[0] == &codes2[0] {
		t.Error("Codes() should return independent copies")
	}
}
