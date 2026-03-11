package agent

import (
	"testing"
)

func TestValidateCommand_Allowed(t *testing.T) {
	tests := []struct {
		action    string
		container string
	}{
		{"node.start", "klever-node1"},
		{"node.stop", "klever-backup"},
		{"node.restart", "my_node.v2"},
		{"node.status", "node-1"},
	}

	for _, tt := range tests {
		if err := ValidateCommand(tt.action, tt.container); err != nil {
			t.Errorf("ValidateCommand(%q, %q) = %v, want nil", tt.action, tt.container, err)
		}
	}
}

func TestValidateCommand_Rejected(t *testing.T) {
	tests := []struct {
		action    string
		container string
		desc      string
	}{
		{"node.delete", "klever-node1", "unknown command"},
		{"system.exec", "klever-node1", "dangerous command"},
		{"node.start", "", "missing container"},
		{"node.start", "../escape", "path traversal"},
		{"node.start", "name with spaces", "spaces in name"},
		{"node.stop", "-flag", "starts with dash"},
	}

	for _, tt := range tests {
		if err := ValidateCommand(tt.action, tt.container); err == nil {
			t.Errorf("ValidateCommand(%q, %q) = nil, want error (%s)", tt.action, tt.container, tt.desc)
		}
	}
}

func TestContainerNamePattern(t *testing.T) {
	valid := []string{"klever-node1", "node_1", "a", "MyNode.v2", "test123"}
	invalid := []string{"", "-start", "../etc", "name space", ";cmd", "|pipe"}

	for _, name := range valid {
		if !containerNamePattern.MatchString(name) {
			t.Errorf("pattern should match %q", name)
		}
	}
	for _, name := range invalid {
		if containerNamePattern.MatchString(name) {
			t.Errorf("pattern should not match %q", name)
		}
	}
}
