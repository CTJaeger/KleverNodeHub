//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
)

// restartAgent starts a new agent process and exits the current one.
// Windows cannot execve over a running process.
func restartAgent() {
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("restart: cannot determine executable: %v", err)
		os.Exit(1)
	}
	log.Printf("restarting agent: %s %v", execPath, os.Args[1:])
	cmd := exec.Command(execPath, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("restart: failed to start new process: %v", err)
	}
	os.Exit(0)
}
