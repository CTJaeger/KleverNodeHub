package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunSecurityCheck(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	want := map[string]bool{
		mk("node1/config/probe_result_1.json"): true,
		mk("node1/config/probe_status.json"):   true,
		mk(".knh_probe_abc"):                   true,
	}
	mk("node1/config/config.toml")
	mk("node1/config/probe_notes.txt")
	mk("node1/db/probe_result_ignored.json") // chain DB dirs are skipped
	mk("a/b/c/d/e/f/g/probe_result_deep.json")

	defer func(r []string) { securityCheckRoots = r }(securityCheckRoots)
	securityCheckRoots = []string{root}

	rep := RunSecurityCheck(context.Background())
	if len(rep.Files) != len(want) {
		t.Fatalf("found %v, want %d files", rep.Files, len(want))
	}
	for _, f := range rep.Files {
		if !want[f] {
			t.Errorf("unexpected finding %s", f)
		}
	}
	if rep.ExeSHA256 == "" || rep.GOOS == "" || rep.Version == "" {
		t.Errorf("report incomplete: %+v", rep)
	}
}
