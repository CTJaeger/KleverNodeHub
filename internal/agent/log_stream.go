package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LogLine represents a single log line from a container.
type LogLine struct {
	Timestamp string `json:"timestamp,omitempty"`
	Stream    string `json:"stream"` // "stdout" or "stderr"
	Message   string `json:"message"`
}

// FetchLogs retrieves historical logs from a container.
func (d *DockerClient) FetchLogs(ctx context.Context, containerName string, tail int, since int64) ([]LogLine, error) {
	if tail <= 0 {
		tail = 100
	}
	if tail > 5000 {
		tail = 5000
	}

	// Check if the container uses TTY mode (raw output, no multiplexed headers)
	cj, err := d.InspectContainer(ctx, containerName)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	isTTY := cj.Config.Tty

	params := url.Values{
		"stdout":     {"true"},
		"stderr":     {"true"},
		"timestamps": {"true"},
		"tail":       {strconv.Itoa(tail)},
	}
	if since > 0 {
		params.Set("since", strconv.FormatInt(since, 10))
	}

	u := fmt.Sprintf("http://localhost/%s/containers/%s/logs?%s",
		d.apiVersion, url.PathEscape(containerName), params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch logs %s: %w", containerName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch logs %s: HTTP %d: %s", containerName, resp.StatusCode, string(body))
	}

	if isTTY {
		return parseRawLogStream(resp.Body)
	}
	return parseDockerLogStream(resp.Body)
}

// StreamLogs streams live logs from a container, calling onLine for each new line.
// It blocks until the context is cancelled or the stream ends.
func (d *DockerClient) StreamLogs(ctx context.Context, containerName string, since int64, onLine func(LogLine)) error {
	params := url.Values{
		"stdout":     {"true"},
		"stderr":     {"true"},
		"timestamps": {"true"},
		"follow":     {"true"},
		"tail":       {"0"},
	}
	if since > 0 {
		params.Set("since", strconv.FormatInt(since, 10))
	}

	u := fmt.Sprintf("http://localhost/%s/containers/%s/logs?%s",
		d.apiVersion, url.PathEscape(containerName), params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("stream logs %s: %w", containerName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stream logs %s: HTTP %d: %s", containerName, resp.StatusCode, string(body))
	}

	// Docker multiplexed stream: 8-byte header + payload
	// Header: [stream_type(1), 0, 0, 0, size(4 big-endian)]
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	header := make([]byte, 8)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, err := io.ReadFull(reader, header)
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read header: %w", err)
		}

		streamType := "stdout"
		if header[0] == 2 {
			streamType = "stderr"
		}

		size := int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
		if size <= 0 || size > 1<<20 {
			continue
		}

		payload := make([]byte, size)
		_, err = io.ReadFull(reader, payload)
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read payload: %w", err)
		}

		line := strings.TrimRight(string(payload), "\n\r")
		ts, msg := splitTimestamp(line)

		if onLine != nil {
			onLine(LogLine{
				Timestamp: ts,
				Stream:    streamType,
				Message:   msg,
			})
		}
	}
}

// parseDockerLogStream parses Docker multiplexed log output into LogLines.
func parseDockerLogStream(r io.Reader) ([]LogLine, error) {
	var lines []LogLine
	header := make([]byte, 8)
	reader := bufio.NewReaderSize(r, 64*1024)

	for {
		_, err := io.ReadFull(reader, header)
		if err != nil {
			if err == io.EOF {
				break
			}
			return lines, nil // Partial read is OK
		}

		streamType := "stdout"
		if header[0] == 2 {
			streamType = "stderr"
		}

		size := int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
		if size <= 0 || size > 1<<20 {
			continue
		}

		payload := make([]byte, size)
		_, err = io.ReadFull(reader, payload)
		if err != nil {
			break
		}

		line := strings.TrimRight(string(payload), "\n\r")
		ts, msg := splitTimestamp(line)
		lines = append(lines, LogLine{
			Timestamp: ts,
			Stream:    streamType,
			Message:   msg,
		})
	}

	return lines, nil
}

// parseRawLogStream parses raw (TTY mode) Docker log output into LogLines.
// TTY containers output plain text without the 8-byte multiplexed header.
func parseRawLogStream(r io.Reader) ([]LogLine, error) {
	var lines []LogLine
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		ts, msg := splitTimestamp(line)
		lines = append(lines, LogLine{
			Timestamp: ts,
			Stream:    "stdout",
			Message:   msg,
		})
	}
	return lines, nil
}

// splitTimestamp splits a Docker log line into timestamp and message.
// Docker timestamps look like "2026-03-12T10:00:00.123456789Z message here"
func splitTimestamp(line string) (string, string) {
	if len(line) < 31 {
		return "", line
	}
	// Check for ISO 8601 timestamp format
	if line[4] == '-' && line[7] == '-' && line[10] == 'T' {
		spaceIdx := strings.IndexByte(line, ' ')
		if spaceIdx > 20 {
			return line[:spaceIdx], line[spaceIdx+1:]
		}
	}
	return "", line
}

// LogStreamManager manages active log streams for containers.
type LogStreamManager struct {
	docker  *DockerClient
	streams map[string]context.CancelFunc // containerName -> cancel
}

// NewLogStreamManager creates a new log stream manager.
func NewLogStreamManager(docker *DockerClient) *LogStreamManager {
	return &LogStreamManager{
		docker:  docker,
		streams: make(map[string]context.CancelFunc),
	}
}

// StartStream starts streaming logs for a container.
// Only one stream per container is allowed.
func (m *LogStreamManager) StartStream(containerName string, onLine func(LogLine)) error {
	// Stop existing stream for this container
	m.StopStream(containerName)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	m.streams[containerName] = cancel

	go func() {
		defer cancel()
		defer delete(m.streams, containerName)
		_ = m.docker.StreamLogs(ctx, containerName, time.Now().Unix(), onLine)
	}()

	return nil
}

// StopStream stops streaming logs for a container.
func (m *LogStreamManager) StopStream(containerName string) {
	if cancel, ok := m.streams[containerName]; ok {
		cancel()
		delete(m.streams, containerName)
	}
}

// StopAll stops all active log streams.
func (m *LogStreamManager) StopAll() {
	for name, cancel := range m.streams {
		cancel()
		delete(m.streams, name)
	}
}

// ActiveStreams returns the number of active streams.
func (m *LogStreamManager) ActiveStreams() int {
	return len(m.streams)
}
