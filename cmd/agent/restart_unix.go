//go:build !windows

package main

import (
	"log"
	"os"
	"syscall"
)

// restartAgent replaces the current process with the updated binary using execve.
// This preserves PID, nohup protection, and systemd tracking.
func restartAgent() {
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("restart: cannot determine executable: %v", err)
		os.Exit(1)
	}
	log.Printf("restarting agent: execve %s %v", execPath, os.Args[1:])
	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
		log.Printf("restart: execve failed: %v — exiting for systemd/supervisor restart", err)
		os.Exit(1)
	}
}
