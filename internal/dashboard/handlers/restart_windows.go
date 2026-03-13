package handlers

import (
	"log"
	"os"
	"os/exec"
)

// restartProcess starts the new binary as a child process and exits.
// Windows does not support syscall.Exec (execve), so we spawn + exit.
func restartProcess(execPath string) {
	args := os.Args
	log.Printf("self-update: restarting with %s %v", execPath, args[1:])

	cmd := exec.Command(execPath, args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		log.Printf("self-update: restart failed: %v", err)
		return
	}

	os.Exit(0)
}

// isRunningInDocker always returns false on Windows.
func isRunningInDocker() bool {
	return false
}
