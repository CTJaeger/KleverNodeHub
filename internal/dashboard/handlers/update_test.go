package handlers

import (
	"testing"
)

func TestParseOSArch(t *testing.T) {
	tests := []struct {
		input    string
		wantOS   string
		wantArch string
	}{
		{"linux/amd64", "linux", "amd64"},
		{"darwin/arm64", "darwin", "arm64"},
		{"windows/amd64", "windows", "amd64"},
		{"", "linux", "amd64"},        // default
		{"unknown", "linux", "amd64"}, // no slash → default
	}

	for _, tt := range tests {
		os, arch := ParseOSArch(tt.input)
		if os != tt.wantOS || arch != tt.wantArch {
			t.Errorf("ParseOSArch(%q) = (%q, %q), want (%q, %q)", tt.input, os, arch, tt.wantOS, tt.wantArch)
		}
	}
}
