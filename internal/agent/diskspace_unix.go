//go:build !windows

package agent

import "syscall"

// freeDiskSpace returns the number of bytes available to an unprivileged
// process on the filesystem containing path. Used for the DB-restore
// preflight so we don't fill the disk with a tens-of-GB extraction.
func freeDiskSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
