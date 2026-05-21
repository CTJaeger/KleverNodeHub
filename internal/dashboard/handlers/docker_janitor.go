//go:build !windows

package handlers

import (
	"encoding/json"
	"log"
	"net/url"
	"strings"
	"time"
)

// janitorMaxRuntime caps the time SweepStaleFinalizeHelpers can spend blocking
// dashboard startup. A wedged Docker daemon could otherwise stall each remove
// call for up to the shared HTTP client's 5min timeout; with N stale helpers,
// startup could be unbounded. We give up and let the rest get cleaned on the
// next restart.
const janitorMaxRuntime = 30 * time.Second

// SweepStaleFinalizeHelpers removes leftover finalize-helper containers from
// past failed dashboard self-update attempts. The helpers are launched without
// AutoRemove so their logs survive for diagnosis; this sweep cleans them up
// on the next normal startup, after the operator has had a chance to inspect.
//
// Best-effort: never blocks the dashboard from starting for more than
// janitorMaxRuntime, never errors out. Called once from cmd/dashboard/main.go
// after the --self-update-finalize short-circuit and before normal init.
func SweepStaleFinalizeHelpers() {
	if !dockerSelfUpdateAvailable() {
		return // not in Docker, or socket not mounted — nothing to clean
	}

	client := newDockerSocketClient()

	// Scope the sweep to siblings of *this* container so a different dashboard
	// install on the same host doesn't get its helpers stomped.
	selfID, err := getSelfContainerID()
	if err != nil {
		return // can't determine own identity — bail rather than guess
	}
	info, err := client.inspectContainer(selfID)
	if err != nil {
		return
	}
	selfName := strings.TrimPrefix(info.Name, "/")
	if selfName == "" {
		return
	}

	helpers, err := client.listContainersByName(selfName + "-finalize-")
	if err != nil {
		log.Printf("janitor: list stale finalize helpers failed: %v", err)
		return
	}

	deadline := time.Now().Add(janitorMaxRuntime)
	for i, h := range helpers {
		if time.Now().After(deadline) {
			log.Printf("janitor: deadline reached after %d/%d helpers; remainder will be swept on next startup", i, len(helpers))
			return
		}
		// Skip live containers — paranoid guard in case a previous update is
		// somehow still in flight (shouldn't happen given the orchestrator
		// stops itself, but cheap to check).
		switch h.State {
		case "running", "restarting", "paused":
			continue
		}
		id := h.ID
		if id == "" {
			continue
		}
		log.Printf("janitor: removing stale finalize helper %s (%s)", short(id), h.State)
		if err := client.removeContainer(id); err != nil {
			log.Printf("janitor: remove %s failed: %v", short(id), err)
		}
	}
}

// containerSummary is the subset of Docker's /containers/json response we use.
type containerSummary struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	State string   `json:"State"`
}

// listContainersByName returns containers whose name contains the given
// substring (matches Docker's `name` filter behaviour), regardless of state.
func (d *dockerClient) listContainersByName(nameSubstring string) ([]containerSummary, error) {
	// json.Marshal so any character Docker accepts in the filter value
	// (including non-ASCII) is properly JSON-escaped — fmt %q produces Go
	// quoted strings (`\x..`, `\u....`) that aren't always valid JSON.
	filters, err := json.Marshal(map[string][]string{"name": {nameSubstring}})
	if err != nil {
		return nil, err
	}
	path := "/containers/json?all=true&filters=" + url.QueryEscape(string(filters))
	data, err := d.get(path)
	if err != nil {
		return nil, err
	}
	var result []containerSummary
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}
