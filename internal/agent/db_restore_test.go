package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		0:                       "0 B",
		512:                     "512 B",
		1024:                    "1.0 KiB",
		1536:                    "1.5 KiB",
		1024 * 1024:             "1.0 MiB",
		43 * 1024 * 1024 * 1024: "43.0 GiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	if got := dirSize(filepath.Join(dir, "does-not-exist")); got != 0 {
		t.Errorf("dirSize(missing) = %d, want 0", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), make([]byte, 100), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b"), make([]byte, 50), 0644); err != nil {
		t.Fatal(err)
	}
	if got := dirSize(dir); got != 150 {
		t.Errorf("dirSize = %d, want 150", got)
	}
}

// makeTarGz builds a gzipped tar with the given file paths (each with trivial
// content) for exercising the extractor.
func makeTarGz(t *testing.T, paths []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, p := range paths {
		content := []byte("x")
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: 0644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

func TestDownloadAndExtractDB_OnlyDBEntries(t *testing.T) {
	// Archive contains db/ entries AND a config/ entry that must be ignored,
	// proving a stray config in the snapshot can't clobber live configuration.
	archive := makeTarGz(t, []string{
		"db/1/shard.dat",
		"db/2/shard.dat",
		"config/config.toml", // must NOT be extracted
	})
	dest := t.TempDir()

	if err := extractDBStream(bytes.NewReader(archive), dest); err != nil {
		t.Fatalf("extract: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "db", "1", "shard.dat")); err != nil {
		t.Errorf("expected db/1/shard.dat extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "db", "2", "shard.dat")); err != nil {
		t.Errorf("expected db/2/shard.dat extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "config", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("config/config.toml must NOT be extracted, stat err = %v", err)
	}
}

func TestDownloadAndExtractDB_RejectsNoDBEntries(t *testing.T) {
	archive := makeTarGz(t, []string{"config/config.toml", "wallet/key.pem"})
	dest := t.TempDir()
	if err := extractDBStream(bytes.NewReader(archive), dest); err == nil {
		t.Error("expected error when archive has no db/ entries, got nil")
	}
}
