package agent

import (
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

func TestNewProvisioner(t *testing.T) {
	req := &models.ProvisionRequest{
		ServerID: "srv-1",
		NodeName: "test-node",
		Network:  "mainnet",
		ImageTag: "latest",
		Port:     8080,
	}

	p := NewProvisioner(nil, req, "job-1", nil)
	if p == nil {
		t.Fatal("expected non-nil provisioner")
	}
	if len(p.steps) != 7 {
		t.Errorf("steps = %d, want 7", len(p.steps))
	}
	if p.nodeDir == "" {
		t.Error("expected nodeDir to be set")
	}
}

func TestProvisionerStepNames(t *testing.T) {
	req := &models.ProvisionRequest{
		NodeName: "test-node",
		Network:  "mainnet",
	}
	p := NewProvisioner(nil, req, "job-1", nil)

	expected := []string{
		"Pre-flight checks",
		"Pull Docker image",
		"Create directory structure",
		"Download configuration",
		"Create container",
		"Start container",
		"Verify node",
	}

	for i, step := range p.steps {
		if step.Name != expected[i] {
			t.Errorf("step %d: got %q, want %q", i, step.Name, expected[i])
		}
	}
}

func TestReplaceConfigValue(t *testing.T) {
	content := `[GeneralSettings]
  NodeDisplayName = "default"
  Identity = ""
  Port = "8080"
`
	result := replaceConfigValue(content, "NodeDisplayName", "MyValidator")
	if result == content {
		t.Error("expected content to change")
	}
	expected := `  NodeDisplayName = "MyValidator"`
	if !contains(result, expected) {
		t.Errorf("result does not contain %q:\n%s", expected, result)
	}
}

func TestReplaceConfigValue_NoMatch(t *testing.T) {
	content := "SomeOtherKey = value\n"
	result := replaceConfigValue(content, "NodeDisplayName", "test")
	if result != content {
		t.Error("should not modify content when key not found")
	}
}

func TestProvisionerProgressCallback(t *testing.T) {
	var received []models.ProvisionProgress

	req := &models.ProvisionRequest{
		NodeName: "test-node",
		Network:  "mainnet",
		ServerID: "srv-1",
	}

	p := NewProvisioner(nil, req, "job-1", func(progress *models.ProvisionProgress) {
		received = append(received, *progress)
	})

	// Test reportProgress directly
	p.reportProgress(1, 7, "Test Step", "running", "")
	p.reportProgress(1, 7, "Test Step", "completed", "")

	if len(received) != 2 {
		t.Fatalf("received %d progress events, want 2", len(received))
	}
	if received[0].Status != "running" {
		t.Errorf("status = %q, want running", received[0].Status)
	}
	if received[1].Status != "completed" {
		t.Errorf("status = %q, want completed", received[1].Status)
	}
	if received[0].JobID != "job-1" {
		t.Errorf("jobID = %q, want job-1", received[0].JobID)
	}
}

// contains is defined in executor_test.go
