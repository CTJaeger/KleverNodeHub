package dashboard

import (
	"testing"
)

func TestShouldFilterTag(t *testing.T) {
	filtered := []string{
		"dev-latest",
		"testnet-v1.0",
		"devnet-build",
		"alpine-latest",
		"val-only-v1",
		"v1.0-dev",
		"v2.0-testnet",
	}

	allowed := []string{
		"latest",
		"v0.60.0",
		"v1.7.16",
		"v1.7.15-rc1",
	}

	for _, tag := range filtered {
		if !shouldFilterTag(tag) {
			t.Errorf("shouldFilterTag(%q) = false, want true", tag)
		}
	}

	for _, tag := range allowed {
		if shouldFilterTag(tag) {
			t.Errorf("shouldFilterTag(%q) = true, want false", tag)
		}
	}
}

func TestNewTagCache(t *testing.T) {
	cache := NewTagCache()
	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
}

// Note: GetTags() with real Docker Hub is tested manually.
// Here we test cache logic only.
func TestTagCacheEmpty(t *testing.T) {
	cache := NewTagCache()
	// Cache is empty, so GetTags will try to fetch (which may fail in test env)
	// This is OK — the test verifies the code doesn't panic.
	_, _ = cache.GetTags()
}
