package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

func TestValidateDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	ok := []string{"/opt/klever-node", "/root/klever-node1", "/home/node/kn", "/var/lib/klever/node", "/var/lib/docker/volumes/kn1/_data"}
	bad := []string{"", "relative/dir", "/", "/etc", "/etc/cron.d", "/usr/local/bin", "/opt/../etc",
		"/opt/x/", "/opt/x:/host", "/root/.ssh", "/home/u/.ssh/keys", "/var/lib/docker/volumes", "/var/lib/docker/volumes/x", "/var/lib/docker/volumes/x/other", "/var/lib/docker/overlay2/x/merged"}
	for _, d := range ok {
		if err := ValidateDataDir(d, nil); err != nil {
			t.Errorf("%q rejected: %v", d, err)
		}
	}
	for _, d := range bad {
		if err := ValidateDataDir(d, nil); err == nil {
			t.Errorf("%q accepted", d)
		}
	}

	roots := []string{"/opt/klever"}
	if err := ValidateDataDir("/opt/klever/node1", roots); err != nil {
		t.Errorf("dir under allowed root rejected: %v", err)
	}
	if err := ValidateDataDir("/opt/kleverx", roots); err == nil {
		t.Error("sibling with shared prefix accepted")
	}
	if err := ValidateDataDir("/root/node", roots); err == nil {
		t.Error("dir outside allowed roots accepted")
	}
}

func TestProvisionPreflight_RejectsEscapingNodeName(t *testing.T) {
	for _, name := range []string{"../../root/.ssh", "", "a/b", ".hidden"} {
		p := NewProvisioner(nil, &models.ProvisionRequest{NodeName: name, Network: "mainnet"}, "job", nil)
		if err := p.stepPreflight(context.Background()); err == nil {
			t.Errorf("node name %q accepted", name)
		}
	}
}

func TestValidateDataDir_ResolvesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	link := filepath.Join(t.TempDir(), "node")
	if err := os.Symlink("/etc", link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := ValidateDataDir(link, nil); err == nil {
		t.Error("data_dir symlinked to /etc accepted")
	}
}
