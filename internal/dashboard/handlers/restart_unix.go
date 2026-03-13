//go:build !windows

package handlers

import (
	"log"
	"os"
	"syscall"
)

// restartProcess replaces the current process with the new binary using execve.
// This preserves the PID, nohup protection, and systemd tracking.
func restartProcess(execPath string) {
	args := os.Args
	log.Printf("self-update: execve %s %v", execPath, args[1:])

	if err := syscall.Exec(execPath, args, os.Environ()); err != nil {
		log.Printf("self-update: execve failed: %v", err)
	}
}

// isRunningInDocker checks if the process is running inside a Docker container.
func isRunningInDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}
