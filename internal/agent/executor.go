package agent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

const (
	defaultCommandTimeout = 60 * time.Second
	defaultStopTimeout    = 30 // seconds for graceful stop
)

// Executor handles incoming commands from the dashboard.
type Executor struct {
	docker *DockerClient
}

// NewExecutor creates a new command executor.
func NewExecutor(dockerSocket string) *Executor {
	return &Executor{
		docker: NewDockerClient(dockerSocket),
	}
}

// NewExecutorWithClient creates an executor with a specific Docker client (for testing).
func NewExecutorWithClient(client *DockerClient) *Executor {
	return &Executor{docker: client}
}

// Execute processes a command message and returns a result.
func (e *Executor) Execute(msg *models.Message) *models.CommandResult {
	result := &models.CommandResult{
		CommandID: msg.ID,
	}

	// Extract container name from payload
	containerName := extractContainerName(msg.Payload)

	// Validate against whitelist
	if err := ValidateCommand(msg.Action, containerName); err != nil {
		result.Error = err.Error()
		return result
	}

	// Execute with timeout
	ctx, cancel := context.WithTimeout(context.Background(), defaultCommandTimeout)
	defer cancel()

	var err error
	switch msg.Action {
	case "node.start":
		err = e.docker.StartContainer(ctx, containerName)
	case "node.stop":
		err = e.docker.StopContainer(ctx, containerName, defaultStopTimeout)
	case "node.restart":
		err = e.docker.RestartContainer(ctx, containerName, defaultStopTimeout)
	case "node.status":
		var status string
		status, err = e.docker.GetContainerStatus(ctx, containerName)
		if err == nil {
			result.Output = status
		}
	default:
		err = fmt.Errorf("unhandled command: %s", msg.Action)
	}

	if err != nil {
		result.Error = err.Error()
		log.Printf("command %s failed: %v", msg.Action, err)
		return result
	}

	result.Success = true
	log.Printf("command %s completed: container=%s", msg.Action, containerName)

	// Get status after operation (except for status command which already has it)
	if msg.Action != "node.status" {
		if status, err := e.docker.GetContainerStatus(ctx, containerName); err == nil {
			result.Output = status
		}
	}

	return result
}

// BuildResultMessage wraps a CommandResult in a Message envelope.
func BuildResultMessage(result *models.CommandResult) *models.Message {
	return &models.Message{
		ID:        fmt.Sprintf("result-%d", time.Now().UnixNano()),
		Type:      "response",
		Action:    "command.result",
		Payload:   result,
		Timestamp: time.Now().Unix(),
	}
}

// extractContainerName extracts the container_name from the command payload.
func extractContainerName(payload any) string {
	if payload == nil {
		return ""
	}

	switch p := payload.(type) {
	case map[string]any:
		if name, ok := p["container_name"].(string); ok {
			return name
		}
	case map[string]string:
		if name, ok := p["container_name"]; ok {
			return name
		}
	}

	return ""
}
