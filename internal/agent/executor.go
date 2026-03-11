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
			newID, err = e.docker.UpgradeContainerWithRollback(ctx, containerName, imageTag)
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
	case "node.provision":
		err = e.executeProvision(ctx, msg.Payload, result)
	case "config.list":
		err = e.executeConfigList(msg.Payload, result)
	case "config.read":
		err = e.executeConfigRead(msg.Payload, result)
	case "config.write":
		err = e.executeConfigWrite(msg.Payload, result)
	case "config.backup":
		err = e.executeConfigBackup(msg.Payload, result)
	case "config.backups":
		err = e.executeConfigBackups(msg.Payload, result)
	case "config.restore":
		err = e.executeConfigRestore(msg.Payload, result)
	case "node.logs":
		err = e.executeFetchLogs(ctx, msg.Payload, result)
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

// executeProvision handles the node.provision command.
func (e *Executor) executeProvision(ctx context.Context, payload any, result *models.CommandResult) error {
	p, ok := payload.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid provision payload")
	}

	req := &models.ProvisionRequest{
		ServerID: extractStringFromMap(p, "server_id"),
		NodeName: extractStringFromMap(p, "node_name"),
		Network:  extractStringFromMap(p, "network"),
		ImageTag: extractStringFromMap(p, "image_tag"),
	}
	if v, ok := p["port"].(float64); ok {
		req.Port = int(v)
	}
	if v, ok := p["generate_keys"].(bool); ok {
		req.GenerateKeys = v
	}
	if overrides, ok := p["config_overrides"].(map[string]any); ok {
		req.ConfigOverrides = make(map[string]string)
		for k, v := range overrides {
			if s, ok := v.(string); ok {
				req.ConfigOverrides[k] = s
			}
		}
	}

	jobID := fmt.Sprintf("prov-%d", time.Now().UnixNano())

	// Progress is reported via the result output for now
	// In future, this will be sent via WebSocket events
	provisioner := NewProvisioner(e.docker, req, jobID, nil)

	if err := provisioner.Run(ctx); err != nil {
		return err
	}

	result.Output = fmt.Sprintf("node %s provisioned successfully (job: %s)", req.NodeName, jobID)
	return nil
}

func (e *Executor) executeConfigList(payload any, result *models.CommandResult) error {
	dataDir := extractStringField(payload, "data_dir")
	if dataDir == "" {
		return fmt.Errorf("data_dir is required")
	}

	files, err := ListConfigFiles(dataDir)
	if err != nil {
		return err
	}

	jsonBytes, _ := json.Marshal(files)
	result.Output = string(jsonBytes)
	return nil
}

func (e *Executor) executeConfigRead(payload any, result *models.CommandResult) error {
	dataDir := extractStringField(payload, "data_dir")
	fileName := extractStringField(payload, "file_name")
	if dataDir == "" || fileName == "" {
		return fmt.Errorf("data_dir and file_name are required")
	}

	content, err := ReadConfigFile(dataDir, fileName)
	if err != nil {
		return err
	}

	result.Output = content
	return nil
}

func (e *Executor) executeConfigWrite(payload any, result *models.CommandResult) error {
	dataDir := extractStringField(payload, "data_dir")
	fileName := extractStringField(payload, "file_name")
	content := extractStringField(payload, "content")
	if dataDir == "" || fileName == "" {
		return fmt.Errorf("data_dir, file_name are required")
	}

	if err := WriteConfigFile(dataDir, fileName, content); err != nil {
		return err
	}

	result.Output = "written: " + fileName

	// Restart container if requested
	restartContainer := extractStringField(payload, "restart_container")
	if restartContainer != "" {
		ctx := context.Background()
		if err := e.docker.RestartContainer(ctx, restartContainer, defaultStopTimeout); err != nil {
			result.Output += " (restart failed: " + err.Error() + ")"
		} else {
			result.Output += " (container restarted)"
		}
	}

	return nil
}

func (e *Executor) executeConfigBackup(payload any, result *models.CommandResult) error {
	dataDir := extractStringField(payload, "data_dir")
	fileName := extractStringField(payload, "file_name")
	if dataDir == "" || fileName == "" {
		return fmt.Errorf("data_dir and file_name are required")
	}

	if err := BackupConfigFile(dataDir, fileName); err != nil {
		return err
	}

	result.Output = "backup created for: " + fileName
	return nil
}

func (e *Executor) executeConfigBackups(payload any, result *models.CommandResult) error {
	dataDir := extractStringField(payload, "data_dir")
	fileName := extractStringField(payload, "file_name")
	if dataDir == "" || fileName == "" {
		return fmt.Errorf("data_dir and file_name are required")
	}

	backups, err := ListConfigBackups(dataDir, fileName)
	if err != nil {
		return err
	}

	jsonBytes, _ := json.Marshal(backups)
	result.Output = string(jsonBytes)
	return nil
}

func (e *Executor) executeConfigRestore(payload any, result *models.CommandResult) error {
	dataDir := extractStringField(payload, "data_dir")
	backupName := extractStringField(payload, "backup_name")
	if dataDir == "" || backupName == "" {
		return fmt.Errorf("data_dir and backup_name are required")
	}

	if err := RestoreConfigBackup(dataDir, backupName); err != nil {
		return err
	}

	result.Output = "restored from: " + backupName
	return nil
}

func (e *Executor) executeFetchLogs(ctx context.Context, payload any, result *models.CommandResult) error {
	containerName := extractStringField(payload, "container_name")
	if containerName == "" {
		return fmt.Errorf("container_name is required")
	}

	tail := 100
	if v, ok := payload.(map[string]any); ok {
		if t, ok := v["tail"].(float64); ok && t > 0 {
			tail = int(t)
		}
	}

	var since int64
	if v, ok := payload.(map[string]any); ok {
		if s, ok := v["since"].(float64); ok && s > 0 {
			since = int64(s)
		}
	}

	lines, err := e.docker.FetchLogs(ctx, containerName, tail, since)
	if err != nil {
		return err
	}

	jsonBytes, _ := json.Marshal(lines)
	result.Output = string(jsonBytes)
	return nil
}

func extractStringFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
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
