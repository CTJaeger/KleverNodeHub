package dashboard

import (
	"os"
	"testing"
)

func TestUpdateStoreStoreAndGet(t *testing.T) {
	dir := t.TempDir()
	s := NewUpdateStore(dir)

	data := []byte("fake binary content")
	info, err := s.Store("v0.2.0", "linux", "amd64", data)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if info.Version != "v0.2.0" {
		t.Errorf("version = %q, want v0.2.0", info.Version)
	}
	if info.Size != int64(len(data)) {
		t.Errorf("size = %d, want %d", info.Size, len(data))
	}
	if info.Checksum == "" {
		t.Error("expected non-empty checksum")
	}

	got := s.Get("linux", "amd64")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Version != "v0.2.0" {
		t.Errorf("Get version = %q, want v0.2.0", got.Version)
	}
}

func TestUpdateStoreGetBinary(t *testing.T) {
	dir := t.TempDir()
	s := NewUpdateStore(dir)

	data := []byte("agent binary bytes")
	_, err := s.Store("v1.0.0", "linux", "arm64", data)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	readData, info, err := s.GetBinary("linux", "arm64")
	if err != nil {
		t.Fatalf("GetBinary: %v", err)
	}
	if string(readData) != string(data) {
		t.Error("binary data mismatch")
	}
	if info.Version != "v1.0.0" {
		t.Errorf("version = %q, want v1.0.0", info.Version)
	}
}

func TestUpdateStoreGetBinary_NotFound(t *testing.T) {
	dir := t.TempDir()
	s := NewUpdateStore(dir)

	_, _, err := s.GetBinary("linux", "amd64")
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestUpdateStoreList(t *testing.T) {
	dir := t.TempDir()
	s := NewUpdateStore(dir)

	if list := s.List(); len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}

	_, _ = s.Store("v1.0.0", "linux", "amd64", []byte("bin1"))
	_, _ = s.Store("v1.0.0", "linux", "arm64", []byte("bin2"))

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 binaries, got %d", len(list))
	}
}

func TestUpdateStoreLatestVersion(t *testing.T) {
	dir := t.TempDir()
	s := NewUpdateStore(dir)

	if v := s.LatestVersion(); v != "" {
		t.Errorf("expected empty latest version, got %q", v)
	}

	_, _ = s.Store("v0.1.0", "linux", "amd64", []byte("old"))

	// Backdate the first entry so the next store is clearly newer
	s.mu.Lock()
	if info := s.binaries["linux/amd64"]; info != nil {
		info.UploadedAt -= 10
	}
	s.mu.Unlock()

	_, _ = s.Store("v0.2.0", "darwin", "arm64", []byte("new"))

	v := s.LatestVersion()
	if v != "v0.2.0" {
		t.Errorf("latest = %q, want v0.2.0", v)
	}
}

func TestUpdateStoreOverwrite(t *testing.T) {
	dir := t.TempDir()
	s := NewUpdateStore(dir)

	_, _ = s.Store("v0.1.0", "linux", "amd64", []byte("old"))
	_, _ = s.Store("v0.2.0", "linux", "amd64", []byte("new binary"))

	info := s.Get("linux", "amd64")
	if info.Version != "v0.2.0" {
		t.Errorf("version = %q, want v0.2.0", info.Version)
	}

	data, _, err := s.GetBinary("linux", "amd64")
	if err != nil {
		t.Fatalf("GetBinary: %v", err)
	}
	if string(data) != "new binary" {
		t.Errorf("data = %q, want 'new binary'", string(data))
	}
}

func TestUpdateStorePersistence(t *testing.T) {
	dir := t.TempDir()

	// Store a binary
	s1 := NewUpdateStore(dir)
	_, _ = s1.Store("v1.0.0", "linux", "amd64", []byte("persisted binary"))

	// Create a new store from same dir — should load index
	s2 := NewUpdateStore(dir)
	info := s2.Get("linux", "amd64")
	if info == nil {
		t.Fatal("expected loaded binary info")
	}
	if info.Version != "v1.0.0" {
		t.Errorf("version = %q, want v1.0.0", info.Version)
	}

	// Verify binary data is readable
	data, _, err := s2.GetBinary("linux", "amd64")
	if err != nil {
		t.Fatalf("GetBinary after reload: %v", err)
	}
	if string(data) != "persisted binary" {
		t.Error("binary data mismatch after reload")
	}
}

func TestUpdateStoreChecksum(t *testing.T) {
	dir := t.TempDir()
	s := NewUpdateStore(dir)

	data := []byte("checksum test data")
	info, _ := s.Store("v1.0.0", "linux", "amd64", data)

	expected := sha256Hex(data)
	if info.Checksum != expected {
		t.Errorf("checksum = %q, want %q", info.Checksum, expected)
	}
}

func TestUpdateStoreIndexFile(t *testing.T) {
	dir := t.TempDir()
	s := NewUpdateStore(dir)

	_, _ = s.Store("v1.0.0", "linux", "amd64", []byte("test"))

	// Verify index.json exists
	indexPath := s.dataDir + "/index.json"
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Error("index.json not created")
	}
}
