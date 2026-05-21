//go:build !windows

package handlers

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// isContainerID reports whether s looks like a Docker container ID — 12 to 64
// hex characters. Used to validate CLI input on the finalize-helper code path
// so a stray non-hex string can't drive Docker API calls against unrelated
// container IDs.
func isContainerID(s string) bool {
	if len(s) < 12 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// RunSelfUpdateFinalize is the entrypoint of the short-lived helper container
// spawned by dockerSelfUpdate. It runs inside a sidecar based on the new
// dashboard image and finishes the swap that the orchestrating (old) container
// could not finish itself:
//
//  1. Wait until the old (now-renamed) container is stopped.
//  2. Start the new replacement container — port(s) are free now.
//  3. Remove the old container.
//  4. Remove ourselves.
//
// Failure modes leave the system in a recoverable state: the old container is
// renamed with a `-old-<ts>` suffix and can be started again manually if needed.
func RunSelfUpdateFinalize(newID, oldID string) error {
	if newID == "" || oldID == "" {
		return fmt.Errorf("--self-update-finalize and --replaces are both required")
	}
	if !isContainerID(newID) || !isContainerID(oldID) {
		return fmt.Errorf("--self-update-finalize and --replaces must be Docker container IDs (12-64 hex chars)")
	}
	if newID == oldID {
		return fmt.Errorf("--self-update-finalize and --replaces must differ")
	}
	// Refuse to operate on ourselves. The helper detects its own ID via
	// hostname (12 short-form chars); newID/oldID from the orchestrator are
	// full 64-char IDs of which selfID is a prefix iff they refer to us.
	if selfID, err := getSelfContainerID(); err == nil && selfID != "" {
		if strings.HasPrefix(newID, selfID) || strings.HasPrefix(oldID, selfID) {
			return fmt.Errorf("self-update finalize: refusing to operate on own container %s", selfID)
		}
	}
	log.Printf("self-update finalize: waiting for old container %s to stop", short(oldID))

	client := newDockerSocketClient()

	// The old container is being stopped by its own goroutine; Docker's
	// graceful-stop timeout there is 10s, so 60s is a generous bound for us.
	if err := client.waitContainerStopped(oldID, 60*time.Second); err != nil {
		return fmt.Errorf("wait for old to stop: %w", err)
	}
	log.Printf("self-update finalize: old stopped — starting new container %s", short(newID))

	if err := client.startContainer(newID); err != nil {
		return fmt.Errorf("start new container: %w", err)
	}
	log.Printf("self-update finalize: new container started — removing old")

	if err := client.removeContainer(oldID); err != nil {
		// Not fatal: new dashboard is up, user just has a stopped backup to clean.
		log.Printf("self-update finalize: WARNING: remove old failed: %v (manual `docker rm` may be needed)", err)
	}

	// Self-remove. We can't wait for the response — the API call returning will
	// race against our own removal. Fire it off and let the runtime exit; Docker
	// will tear us down.
	selfID, err := getSelfContainerID()
	if err == nil {
		log.Printf("self-update finalize: done — removing self %s", short(selfID))
		go func() { _ = client.removeContainer(selfID) }()
		// Give the request a moment to hit the socket before we return and the
		// process exits. 500ms is plenty for a local Unix socket.
		time.Sleep(500 * time.Millisecond)
	} else {
		log.Printf("self-update finalize: WARNING: could not detect own container ID: %v (manual cleanup needed)", err)
	}

	return nil
}

func short(id string) string {
	if len(id) >= 12 {
		return id[:12]
	}
	return id
}
