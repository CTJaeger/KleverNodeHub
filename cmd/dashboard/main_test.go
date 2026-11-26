package main

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func newTestSettings(t *testing.T) *store.SettingsStore {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store.NewSettingsStore(db)
}

// A signing key stored by a version up to v0.3.88 is replaced exactly once,
// then kept across restarts.
func TestLoadOrCreateJWTKey_RotatesLegacyKeyOnce(t *testing.T) {
	settings := newTestSettings(t)

	legacy := bytes.Repeat([]byte{0xAB}, 32)
	if err := settings.Set("jwt_signing_key", hex.EncodeToString(legacy)); err != nil {
		t.Fatal(err)
	}

	first, err := loadOrCreateJWTKey(settings)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, legacy) {
		t.Fatal("legacy signing key was kept")
	}
	if len(first) < 32 {
		t.Fatalf("new key too short: %d bytes", len(first))
	}

	second, err := loadOrCreateJWTKey(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("signing key changed again on the next start: sessions would end on every restart")
	}
}

func TestLoadOrCreateJWTKey_FreshInstallKeepsKey(t *testing.T) {
	settings := newTestSettings(t)

	first, err := loadOrCreateJWTKey(settings)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateJWTKey(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("fresh install: signing key not stable across restarts")
	}
}
