package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BenchmarkResult holds the parsed results of a server hardware benchmark.
type BenchmarkResult struct {
	Timestamp int64           `json:"timestamp"`
	Overall   string          `json:"overall"` // "PASS", "WARN", "FAIL"
	Tests     []BenchmarkTest `json:"tests"`
	RawOutput string          `json:"raw_output"`
}

// BenchmarkTest is a single benchmark test category.
type BenchmarkTest struct {
	Name    string `json:"name"`    // "Disk I/O", "Network", "CPU", "Memory", "KV Store"
	Status  string `json:"status"`  // "PASS", "WARN", "FAIL"
	Details string `json:"details"` // "459 MB/s write, 6029 random IOPS (NVMe performing well)"
}

const benchmarkContainerName = "klever-benchmark-run"

// RunBenchmark runs the klever-go benchmark tool in a one-shot Docker container.
// Steps: create benchmark dir → create container → start → wait → read logs → cleanup.
func (d *DockerClient) RunBenchmark(ctx context.Context) (*BenchmarkResult, error) {
	benchDir := "/tmp/klever-benchmark"

	// 1. Create benchmark directory with correct ownership
	if err := os.MkdirAll(benchDir, 0755); err != nil {
		return nil, fmt.Errorf("create benchmark dir: %w", err)
	}
	if err := os.Chmod(benchDir, 0777); err != nil {
		return nil, fmt.Errorf("chmod benchmark dir: %w", err)
	}

	// 2. Remove leftover container from previous run (if any)
	_ = d.RemoveContainer(ctx, benchmarkContainerName, true)

	// 3. Pull latest klever-go image
	if err := d.PullImage(ctx, kleverImage+":latest"); err != nil {
		return nil, fmt.Errorf("pull benchmark image: %w", err)
	}

	// 4. Create one-shot benchmark container
	body := containerCreateBody{
		Image:      kleverImage + ":latest",
		User:       "999:999",
		Entrypoint: []string{"/usr/local/bin/benchmark"},
		Cmd:        []string{"--disk-dir", "/opt/klever-blockchain/benchmark"},
		HostConfig: hostConfigBody{
			Binds: []string{
				benchDir + ":/opt/klever-blockchain/benchmark",
			},
			NetworkMode: "host",
		},
		Labels: map[string]string{
			"managed-by": "klever-node-hub",
			"purpose":    "benchmark",
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal benchmark config: %w", err)
	}

	createURL := fmt.Sprintf("http://localhost/%s/containers/create?name=%s",
		d.apiVersion, url.QueryEscape(benchmarkContainerName))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create benchmark container: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create benchmark container: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var createResp containerCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return nil, fmt.Errorf("decode create response: %w", err)
	}

	// 5. Start the container
	if err := d.StartContainer(ctx, benchmarkContainerName); err != nil {
		_ = d.RemoveContainer(ctx, benchmarkContainerName, true)
		return nil, fmt.Errorf("start benchmark container: %w", err)
	}

	// 6. Wait for container to finish (poll status every 2s, max 5 min)
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := d.waitForExit(waitCtx, benchmarkContainerName); err != nil {
		_ = d.RemoveContainer(ctx, benchmarkContainerName, true)
		return nil, fmt.Errorf("benchmark did not finish: %w", err)
	}

	// 7. Read logs
	rawOutput, err := d.readContainerLogs(ctx, benchmarkContainerName)
	if err != nil {
		_ = d.RemoveContainer(ctx, benchmarkContainerName, true)
		return nil, fmt.Errorf("read benchmark logs: %w", err)
	}

	// 8. Cleanup
	_ = d.RemoveContainer(ctx, benchmarkContainerName, true)
	_ = os.RemoveAll(benchDir)

	// 9. Parse results
	result := parseBenchmarkOutput(rawOutput)
	return result, nil
}

// waitForExit polls container status until it exits or the context times out.
func (d *DockerClient) waitForExit(ctx context.Context, containerName string) error {
	for {
		status, err := d.GetContainerStatus(ctx, containerName)
		if err != nil {
			return fmt.Errorf("get status: %w", err)
		}
		if strings.Contains(status, "exited") || strings.Contains(status, "dead") || strings.Contains(status, "created") {
			// "created" after running means it finished (some Docker versions)
			if !strings.Contains(status, "running") {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for benchmark to complete")
		case <-time.After(2 * time.Second):
			continue
		}
	}
}

// readContainerLogs reads all stdout/stderr from a (stopped) container.
func (d *DockerClient) readContainerLogs(ctx context.Context, containerName string) (string, error) {
	params := url.Values{
		"stdout": {"true"},
		"stderr": {"true"},
		"tail":   {"500"},
	}

	u := fmt.Sprintf("http://localhost/%s/containers/%s/logs?%s",
		d.apiVersion, url.PathEscape(containerName), params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch logs: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("fetch logs: HTTP %d: %s", resp.StatusCode, string(body))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}

	// Docker multiplexed stream: strip 8-byte headers from each frame
	return stripDockerLogHeaders(raw), nil
}

// stripDockerLogHeaders removes the 8-byte multiplexed stream headers
// that Docker prepends to each log line when TTY is disabled.
func stripDockerLogHeaders(data []byte) string {
	var out strings.Builder
	for len(data) >= 8 {
		// Bytes 0: stream type (1=stdout, 2=stderr)
		// Bytes 4-7: big-endian uint32 frame size
		frameSize := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		data = data[8:]
		if frameSize > len(data) {
			frameSize = len(data)
		}
		out.Write(data[:frameSize])
		data = data[frameSize:]
	}
	// If no frames were found (TTY mode), return raw
	if out.Len() == 0 {
		return string(data)
	}
	return out.String()
}

// parseBenchmarkOutput parses the text output of the klever benchmark tool.
var benchLinePattern = regexp.MustCompile(`(?i)^\s*-?\s*([\w\s/]+?):\s*(PASS|WARN|FAIL)\s*[—–-]\s*(.+)$`)

func parseBenchmarkOutput(raw string) *BenchmarkResult {
	result := &BenchmarkResult{
		Timestamp: time.Now().Unix(),
		Overall:   "PASS",
		RawOutput: raw,
	}

	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		m := benchLinePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		test := BenchmarkTest{
			Name:    strings.TrimSpace(m[1]),
			Status:  strings.ToUpper(m[2]),
			Details: strings.TrimSpace(m[3]),
		}
		result.Tests = append(result.Tests, test)

		// Overall = worst status
		if test.Status == "FAIL" {
			result.Overall = "FAIL"
		} else if test.Status == "WARN" && result.Overall != "FAIL" {
			result.Overall = "WARN"
		}
	}

	return result
}

// FormatBenchmarkScore returns a human-readable score from benchmark results.
func FormatBenchmarkScore(r *BenchmarkResult) string {
	if len(r.Tests) == 0 {
		return "No results"
	}

	passed := 0
	for _, t := range r.Tests {
		if t.Status == "PASS" {
			passed++
		}
	}
	return strconv.Itoa(passed) + "/" + strconv.Itoa(len(r.Tests)) + " passed"
}
