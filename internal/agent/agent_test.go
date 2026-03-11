package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

func TestNewAgent(t *testing.T) {
	a := New("/tmp/test-agent")
	if a == nil {
		t.Fatal("agent should not be nil")
	}
	if a.IsRegistered() {
		t.Error("new agent should not be registered")
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)

	a.config = &Config{
		ServerID:     "srv-1",
		DashboardURL: "https://dashboard.local:9443",
		CertPEM:      "fake-cert",
		KeyPEM:       "fake-key",
		CACertPEM:    "fake-ca",
		AgentPort:    9443,
	}

	if err := a.SaveConfig(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file exists with restricted permissions
	path := filepath.Join(dir, configFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("config file should not be empty")
	}

	// Load into new agent
	a2 := New(dir)
	if err := a2.LoadConfig(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if a2.config.ServerID != "srv-1" {
		t.Errorf("server_id = %q, want %q", a2.config.ServerID, "srv-1")
	}
	if a2.config.DashboardURL != "https://dashboard.local:9443" {
		t.Errorf("url = %q", a2.config.DashboardURL)
	}
	if !a2.IsRegistered() {
		t.Error("loaded agent should be registered")
	}
}

func TestLoadConfig_NotFound(t *testing.T) {
	dir := t.TempDir()
	a := New(dir)

	err := a.LoadConfig()
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestBuildAgentInfo(t *testing.T) {
	a := New("/tmp")
	a.config = &Config{}

	info := a.BuildAgentInfo()
	if info.Version != version.Version {
		t.Errorf("version = %q, want %q", info.Version, version.Version)
	}
	if info.OS == "" {
		t.Error("OS should not be empty")
	}
}

func TestBuildInfoMessage(t *testing.T) {
	a := New("/tmp")
	a.config = &Config{}

	msg := a.BuildInfoMessage()
	if msg.Action != "agent.info" {
		t.Errorf("action = %q, want %q", msg.Action, "agent.info")
	}
	if msg.Type != "event" {
		t.Errorf("type = %q, want %q", msg.Type, "event")
	}
	if msg.Timestamp == 0 {
		t.Error("timestamp should be set")
	}
}

func TestIsRegistered(t *testing.T) {
	a := New("/tmp")

	// No config
	if a.IsRegistered() {
		t.Error("no config = not registered")
	}

	// Config without cert
	a.config = &Config{ServerID: "srv-1"}
	if a.IsRegistered() {
		t.Error("no cert = not registered")
	}

	// Config with cert
	a.config.CertPEM = "some-cert"
	if !a.IsRegistered() {
		t.Error("with cert = registered")
	}
}
