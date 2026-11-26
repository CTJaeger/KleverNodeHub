package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

// securityCheckRoots are the places searched for files left behind by known
// intrusions. A variable so tests can point it at a temp dir.
var securityCheckRoots = []string{"/tmp", "/var/tmp", "/root", "/home", "/opt", "/srv", "/mnt", "/data", "/etc"}

const (
	securityCheckMaxDepth   = 6
	securityCheckMaxEntries = 300000
	securityCheckMaxFiles   = 50
)

// securityCheckSkip names directories that are large and never hold the files
// looked for (chain databases, container layers).
var securityCheckSkip = map[string]bool{"db": true, "overlay2": true, "containers": true, "node_modules": true, ".git": true, "proc": true, "sys": true}

// isAttackerFile matches the file names known from the November 2026 intrusions.
func isAttackerFile(name string) bool {
	if strings.HasPrefix(name, ".knh_probe_") {
		return true
	}
	return strings.HasSuffix(name, ".json") && (strings.HasPrefix(name, "probe_result") || strings.HasPrefix(name, "probe_status"))
}

// RunSecurityCheck looks for known attacker files and reports the checksum of
// the running agent program. Read-only.
func RunSecurityCheck(ctx context.Context) *models.SecurityCheckReport {
	rep := &models.SecurityCheckReport{
		Files:   []string{},
		Version: version.Version,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
	if exe, err := os.Executable(); err == nil {
		rep.ExeSHA256 = fileSHA256(exe)
	}

	visited := 0
	for _, root := range securityCheckRoots {
		rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if ctx.Err() != nil || visited >= securityCheckMaxEntries || len(rep.Files) >= securityCheckMaxFiles {
				rep.Truncated = true
				return filepath.SkipAll
			}
			visited++
			if d.IsDir() {
				depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
				if path != root && (securityCheckSkip[d.Name()] || depth >= securityCheckMaxDepth) {
					return filepath.SkipDir
				}
				return nil
			}
			if isAttackerFile(d.Name()) {
				rep.Files = append(rep.Files, path)
			}
			return nil
		})
	}
	return rep
}

func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
