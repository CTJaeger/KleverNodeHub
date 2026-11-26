package agent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// systemDirs are host locations a node data directory must never be in. The
// hub picks data_dir for config writes, key operations and container mounts,
// so without this a hub admin session could aim those at the host system.
var systemDirs = []string{
	"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32",
	"/proc", "/run", "/sbin", "/sys", "/usr", "/var/run", "/var/lib/docker",
	"/var/lib/containerd", "/var/snap", "/snap",
}

const dockerVolumesDir = "/var/lib/docker/volumes"

// ValidateDataDir rejects data directories the hub must not point the agent at:
// relative or unclean paths, ':' (Docker bind syntax), system locations and SSH
// directories. If allowedRoots is set (agent.json allowed_data_roots), the
// directory must also lie under one of them.
func ValidateDataDir(dir string, allowedRoots []string) error {
	if runtime.GOOS == "windows" {
		return nil // container mounts and the paths below are Unix-specific
	}
	if err := validateDataDirPath(dir, allowedRoots); err != nil {
		return err
	}
	// A symlinked data dir (or parent) must land somewhere allowed too.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		if err := validateDataDirPath(resolved, allowedRoots); err != nil {
			return fmt.Errorf("data_dir %q resolves to a disallowed location: %w", dir, err)
		}
	}
	return nil
}

func validateDataDirPath(dir string, allowedRoots []string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("data_dir must be a clean absolute path: %q", dir)
	}
	if dir == "/" || strings.ContainsAny(dir, ":\n") {
		return fmt.Errorf("data_dir not allowed: %q", dir)
	}
	// Nodes on named Docker volumes live in /var/lib/docker/volumes/<name>/_data.
	rel := strings.Split(strings.TrimPrefix(dir, dockerVolumesDir+"/"), "/")
	volume := strings.HasPrefix(dir, dockerVolumesDir+"/") && len(rel) >= 2 && rel[1] == "_data"
	for _, sys := range systemDirs {
		if volume && sys == "/var/lib/docker" {
			continue
		}
		if dir == sys || strings.HasPrefix(dir, sys+"/") {
			return fmt.Errorf("data_dir inside system directory %s not allowed: %q", sys, dir)
		}
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == ".ssh" {
			return fmt.Errorf("data_dir inside an SSH directory not allowed: %q", dir)
		}
	}
	if len(allowedRoots) == 0 {
		return nil
	}
	for _, root := range allowedRoots {
		root = filepath.Clean(root)
		if dir == root || strings.HasPrefix(dir, strings.TrimSuffix(root, "/")+"/") {
			return nil
		}
	}
	return fmt.Errorf("data_dir %q is outside allowed_data_roots", dir)
}
