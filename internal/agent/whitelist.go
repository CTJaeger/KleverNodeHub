package agent

import (
	"fmt"
	"log"
	"regexp"
)

// AllowedCommands defines the set of commands the agent will execute.
var AllowedCommands = map[string]CommandSpec{
	"node.start":     {Description: "Start a Klever node container", RequiresContainer: true},
	"node.stop":      {Description: "Stop a Klever node container", RequiresContainer: true},
	"node.restart":   {Description: "Restart a Klever node container", RequiresContainer: true},
	"node.status":    {Description: "Get status of a Klever node container", RequiresContainer: true},
	"node.create":    {Description: "Create a new Klever node container", RequiresContainer: false},
	"node.remove":    {Description: "Remove a Klever node container", RequiresContainer: true},
	"node.upgrade":   {Description: "Upgrade a Klever node to a new image tag", RequiresContainer: true},
	"node.pull":      {Description: "Pull a Docker image", RequiresContainer: false},
	"node.provision": {Description: "Provision a new Klever node from scratch", RequiresContainer: false},
	"node.discovery": {Description: "Scan for existing Klever nodes", RequiresContainer: false},
}

// CommandSpec defines constraints for a whitelisted command.
type CommandSpec struct {
	Description       string
	RequiresContainer bool
}

// containerNamePattern restricts container names to safe characters.
// Allows: alphanumeric, hyphens, underscores, dots.
var containerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidateCommand checks if a command action is allowed and parameters are valid.
// Returns an error describing why the command was rejected, or nil if valid.
func ValidateCommand(action string, containerName string) error {
	spec, ok := AllowedCommands[action]
	if !ok {
		log.Printf("REJECTED command: %q (not in whitelist)", action)
		return fmt.Errorf("command not allowed: %q", action)
	}

	if spec.RequiresContainer {
		if containerName == "" {
			log.Printf("REJECTED command: %q (missing container name)", action)
			return fmt.Errorf("command %q requires a container name", action)
		}

		if !containerNamePattern.MatchString(containerName) {
			log.Printf("REJECTED command: %q container=%q (invalid name)", action, containerName)
			return fmt.Errorf("invalid container name: %q", containerName)
		}
	}

	log.Printf("ALLOWED command: %q container=%q", action, containerName)
	return nil
}
