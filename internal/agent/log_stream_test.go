package agent

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSplitTimestamp(t *testing.T) {
	tests := []struct {
		input   string
		wantTS  string
		wantMsg string
	}{
		{"2026-03-12T10:00:00.123456789Z hello world", "2026-03-12T10:00:00.123456789Z", "hello world"},
		{"no timestamp here", "", "no timestamp here"},
		{"short", "", "short"},
		{"", "", ""},
	}

	for _, tt := range tests {
		ts, msg := splitTimestamp(tt.input)
		if ts != tt.wantTS || msg != tt.wantMsg {
			t.Errorf("splitTimestamp(%q) = (%q, %q), want (%q, %q)", tt.input, ts, msg, tt.wantTS, tt.wantMsg)
		}
	}
}

func TestParseDockerLogStream(t *testing.T) {
	// Build a mock Docker multiplexed log stream
	var buf bytes.Buffer

	// Write stdout line
	writeDockerLogFrame(&buf, 1, "2026-03-12T10:00:00.000000000Z [INFO] Node started\n")
	// Write stderr line
	writeDockerLogFrame(&buf, 2, "2026-03-12T10:00:01.000000000Z [ERROR] Something failed\n")
	// Write another stdout
	writeDockerLogFrame(&buf, 1, "2026-03-12T10:00:02.000000000Z [DEBUG] Processing block\n")

	lines, err := parseDockerLogStream(&buf)
	if err != nil {
		t.Fatalf("parseDockerLogStream: %v", err)
	}

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	if lines[0].Stream != "stdout" {
		t.Errorf("line[0].Stream = %q, want stdout", lines[0].Stream)
	}
	if lines[0].Timestamp != "2026-03-12T10:00:00.000000000Z" {
		t.Errorf("line[0].Timestamp = %q", lines[0].Timestamp)
	}
	if lines[0].Message != "[INFO] Node started" {
		t.Errorf("line[0].Message = %q", lines[0].Message)
	}

	if lines[1].Stream != "stderr" {
		t.Errorf("line[1].Stream = %q, want stderr", lines[1].Stream)
	}
	if lines[1].Message != "[ERROR] Something failed" {
		t.Errorf("line[1].Message = %q", lines[1].Message)
	}

	if lines[2].Message != "[DEBUG] Processing block" {
		t.Errorf("line[2].Message = %q", lines[2].Message)
	}
}

func TestParseDockerLogStream_Empty(t *testing.T) {
	var buf bytes.Buffer
	lines, err := parseDockerLogStream(&buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(lines))
	}
}

func TestLogStreamManager_StartStop(t *testing.T) {
	// Create a manager with a nil docker client (we won't actually stream)
	mgr := NewLogStreamManager(nil)

	if mgr.ActiveStreams() != 0 {
		t.Errorf("expected 0 active streams, got %d", mgr.ActiveStreams())
	}

	// StopStream on non-existent stream should not panic
	mgr.StopStream("klever-node1")

	// StopAll on empty should not panic
	mgr.StopAll()
}

// writeDockerLogFrame writes a Docker multiplexed log frame.
// streamType: 1=stdout, 2=stderr
func writeDockerLogFrame(buf *bytes.Buffer, streamType byte, msg string) {
	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:], uint32(len(msg)))
	buf.Write(header)
	buf.WriteString(msg)
}
