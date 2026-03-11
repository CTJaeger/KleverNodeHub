package agent

import (
	"context"
	"encoding/json"
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
	case "node.create":
		err = e.executeCreate(ctx, msg.Payload, result)
	case "node.remove":
		err = e.docker.RemoveContainer(ctx, containerName, false)
	case "node.upgrade":
		imageTag := extractStringField(msg.Payload, "image_tag")
		if imageTag == "" {
			err = fmt.Errorf("image_tag is required for upgrade")
		} else {
			var newID string
			newID, err = e.docker.UpgradeContainer(ctx, containerName, imageTag)
			if err == nil {
				result.Output = "upgraded to " + imageTag + " (container: " + newID[:12] + ")"
			}
		}
	case "node.pull":
		image := extractStringField(msg.Payload, "image")
		if image == "" {
			err = fmt.Errorf("image is required for pull")
		} else {
			err = e.docker.PullImage(ctx, image)
			if err == nil {
				result.Output = "pulled " + image
			}
		}
	case "node.discovery":
		nodes, discErr := e.docker.DiscoverNodes(ctx)
		if discErr != nil {
			err = discErr
		} else {
			jsonBytes, _ := json.Marshal(nodes)
			result.Output = string(jsonBytes)
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

	// Get status after lifecycle operations
	if msg.Action == "node.start" || msg.Action == "node.stop" || msg.Action == "node.restart" {
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

// executeCreate handles the node.create command.
func (e *Executor) executeCreate(ctx context.Context, payload any, result *models.CommandResult) error {
	cfg := extractContainerConfig(payload)
	if cfg == nil {
		return fmt.Errorf("invalid container configuration")
	}

	// Ensure data directories exist
	if err := EnsureDataDirs(cfg.DataDir); err != nil {
		return fmt.Errorf("create data dirs: %w", err)
	}

	// Pull image first
	image := fmt.Sprintf("%s:%s", kleverImage, cfg.ImageTag)
	if err := e.docker.PullImage(ctx, image); err != nil {
		return fmt.Errorf("pull image: %w", err)
	}

	// Create container
	containerID, err := e.docker.CreateContainer(ctx, cfg)
	if err != nil {
		return err
	}

	// Start container
	if err := e.docker.StartContainer(ctx, containerID); err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	result.Output = "created and started: " + containerID[:12]
	return nil
}

// extractContainerConfig parses a ContainerConfig from the payload.
func extractContainerConfig(payload any) *ContainerConfig {
	p, ok := payload.(map[string]any)
	if !ok {
		return nil
	}

	cfg := &ContainerConfig{}
	if v, ok := p["name"].(string); ok {
		cfg.Name = v
	}
	if v, ok := p["image_tag"].(string); ok {
		cfg.ImageTag = v
	}
	if v, ok := p["data_dir"].(string); ok {
		cfg.DataDir = v
	}
	if v, ok := p["rest_api_port"].(float64); ok {
		cfg.RestAPIPort = int(v)
	}
	if v, ok := p["display_name"].(string); ok {
		cfg.DisplayName = v
	}
	if v, ok := p["redundancy_level"].(float64); ok {
		cfg.RedundancyLevel = int(v)
	}

	return cfg
}

// extractStringField extracts a string field from the payload.
func extractStringField(payload any, field string) string {
	if p, ok := payload.(map[string]any); ok {
		if v, ok := p[field].(string); ok {
			return v
		}
	}
	if p, ok := payload.(map[string]string); ok {
		return p[field]
	}
	return ""
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
