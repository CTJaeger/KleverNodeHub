//go:build !windows

package handlers

import (
	"strings"
	"testing"
)

func TestReplaceImageTag(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		newTag  string
		want    string
		wantErr bool
	}{
		{"simple", "ctjaeger/klever-node-hub:v0.3.40", "v0.3.42", "ctjaeger/klever-node-hub:v0.3.42", false},
		{"latest_to_version", "ctjaeger/klever-node-hub:latest", "v0.3.68", "ctjaeger/klever-node-hub:v0.3.68", false},
		{"no_tag", "ctjaeger/klever-node-hub", "v0.3.68", "ctjaeger/klever-node-hub:v0.3.68", false},
		{"registry_port_with_tag", "registry.local:5000/foo:v1", "v2", "registry.local:5000/foo:v2", false},
		{"registry_port_no_tag", "registry.local:5000/foo", "v2", "registry.local:5000/foo:v2", false},
		{"digest_pinned_rejected", "ctjaeger/klever-node-hub@sha256:abc123", "v0.3.68", "", true},
		{"library_image", "alpine:3.20", "3.21", "alpine:3.21", false},
		{"library_image_no_tag", "alpine", "3.21", "alpine:3.21", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceImageTag(tt.image, tt.newTag)
			if (err != nil) != tt.wantErr {
				t.Fatalf("replaceImageTag(%q, %q) error = %v, wantErr %v", tt.image, tt.newTag, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("replaceImageTag(%q, %q) = %q, want %q", tt.image, tt.newTag, got, tt.want)
			}
		})
	}
}

func TestParseImagePullStream(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "success_stream",
			body: `{"status":"Pulling from ctjaeger/klever-node-hub","id":"v0.3.68"}
{"status":"Pulling fs layer","progressDetail":{},"id":"abc"}
{"status":"Status: Downloaded newer image for ctjaeger/klever-node-hub:v0.3.68"}`,
			wantErr: false,
		},
		{
			name:      "error_detail",
			body:      `{"errorDetail":{"message":"manifest for ctjaeger/klever-node-hub:v9 not found"},"error":"manifest for ctjaeger/klever-node-hub:v9 not found"}`,
			wantErr:   true,
			errSubstr: "manifest for",
		},
		{
			name:      "error_field_only",
			body:      `{"error":"pull access denied"}`,
			wantErr:   true,
			errSubstr: "pull access denied",
		},
		{
			name:    "empty_body",
			body:    ``,
			wantErr: false,
		},
		{
			name: "error_mid_stream",
			body: `{"status":"Pulling from foo"}
{"status":"Pulling fs layer","id":"abc"}
{"errorDetail":{"message":"toomanyrequests"},"error":"toomanyrequests"}`,
			wantErr:   true,
			errSubstr: "toomanyrequests",
		},
		{
			// Stream cut mid-object (daemon died, network blip). We return nil
			// and rely on the subsequent createContainer to surface "no such image"
			// — documented behaviour, kept for visibility.
			name:    "truncated_mid_object",
			body:    `{"status":"Pulling from foo"}` + "\n" + `{"status":"Downl`,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseImagePullStream(strings.NewReader(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseImagePullStream(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("parseImagePullStream(%q) error = %v, want substring %q", tt.name, err, tt.errSubstr)
			}
		})
	}
}

func TestLooksLikeContainerID(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"short_hex_12", "abc123def456", true},
		{"full_64_hex", strings.Repeat("a", 64), true},
		{"too_short_11", "abc123def45", false},
		{"empty", "", false},
		// Docker container IDs are lowercase only; reject mixed-case to avoid
		// treating hostname overrides that happen to be hex-like as valid IDs.
		{"uppercase_rejected", "ABC123DEF456", false},
		{"mixed_case_rejected", "abc123DEF456", false},
		{"contains_nonhex", "abc123def45g", false},
		{"hostname_style", "klever-node-hub", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeContainerID(tt.s); got != tt.want {
				t.Errorf("looksLikeContainerID(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestCgroupContainerIDRegex(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     string
	}{
		{
			name: "cgroup_v1_docker",
			contents: `12:pids:/docker/abc123def4567890abc123def4567890abc123def4567890abc123def45678901
11:memory:/docker/abc123def4567890abc123def4567890abc123def4567890abc123def45678901`,
			want: "abc123def4567890abc123def4567890abc123def4567890abc123def4567890",
		},
		{
			name:     "cgroup_v2_systemd_scope",
			contents: `0::/system.slice/docker-1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff1234.scope`,
			want:     "1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff",
		},
		{
			name:     "no_container_id",
			contents: `0::/user.slice/user-1000.slice/session-2.scope`,
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := cgroupContainerIDRe.FindString(tt.contents)
			if tt.want == "" {
				if match != "" {
					t.Errorf("cgroupContainerIDRe.FindString(...) = %q, want no match", match)
				}
				return
			}
			if match == "" {
				t.Errorf("cgroupContainerIDRe.FindString(...) returned no match, want %q", tt.want)
				return
			}
			if len(match) != 64 {
				t.Errorf("matched ID is %d chars, want 64: %q", len(match), match)
			}
		})
	}
}

func TestIsContainerID(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"valid_short_12", "abc123def456", true},
		{"valid_full_64", strings.Repeat("a", 64), true},
		{"too_short_11", "abc123def45", false},
		{"too_long_65", strings.Repeat("a", 65), false},
		{"empty", "", false},
		{"nonhex_char", "abc123def45g", false},
		{"injection_attempt_flag", "--rm", false},
		{"injection_attempt_path", "../../etc", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isContainerID(tt.s); got != tt.want {
				t.Errorf("isContainerID(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}
