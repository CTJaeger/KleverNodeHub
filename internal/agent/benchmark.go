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
	Timestamp int64              `json:"timestamp"`
	Score     int                `json:"score"`     // 0-1000
	MaxScore  int                `json:"max_score"` // 1000
	Grade     string             `json:"grade"`     // "A", "B", "C", etc.
	Verdict   string             `json:"verdict"`   // "Excellent — production-ready..."
	Sections  []BenchmarkSection `json:"sections"`
	RawOutput string             `json:"raw_output"`
}

// BenchmarkSection is one category in the benchmark report.
type BenchmarkSection struct {
	Name     string  `json:"name"`      // "GOROUTINE SCALABILITY", "DISK I/O", etc.
	Status   string  `json:"status"`    // "PASS", "WARN", "FAIL"
	Score    int     `json:"score"`     // section score
	MaxScore int     `json:"max_score"` // section max
	Percent  float64 `json:"percent"`   // score percentage
}

const (
	benchmarkContainerName = "klever-benchmark-run"
	benchmarkImageTag      = "v1.7.16-0-gcf9f612c" // first build with benchmark binary
)

// BenchmarkProgress is called during benchmark execution to report progress.
type BenchmarkProgress func(step, total int, status string)

// RunBenchmark runs the klever-go benchmark tool in a one-shot Docker container.
func (d *DockerClient) RunBenchmark(ctx context.Context, onProgress BenchmarkProgress) (*BenchmarkResult, error) {
	benchDir := "/tmp/klever-benchmark"
	report := func(step int, status string) {
		if onProgress != nil {
			onProgress(step, 8, status)
		}
	}

	// 1. Create benchmark directory with permissions for container user (UID 999)
	report(1, "Preparing benchmark directory...")
	if err := os.MkdirAll(benchDir, 0777); err != nil {
		return nil, fmt.Errorf("create benchmark dir: %w", err)
	}
	_ = os.Chown(benchDir, 999, 999)

	// 2. Remove leftover container from previous run
	_ = d.RemoveContainer(ctx, benchmarkContainerName, true)

	// 3. Pull the benchmark image
	report(2, "Pulling benchmark image...")
	benchImage := kleverImage + ":" + benchmarkImageTag
	if err := d.PullImage(ctx, benchImage); err != nil {
		return nil, fmt.Errorf("pull benchmark image: %w", err)
	}

	// 4. Create one-shot benchmark container
	// Entrypoint overrides the image's entrypoint.sh
	body := containerCreateBody{
		Image:      benchImage,
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
	report(3, "Starting benchmark container...")
	if err := d.StartContainer(ctx, benchmarkContainerName); err != nil {
		_ = d.RemoveContainer(ctx, benchmarkContainerName, true)
		return nil, fmt.Errorf("start benchmark container: %w", err)
	}

	// 6. Stream logs to detect progress while waiting for exit
	report(4, "Running benchmarks...")
	go d.streamBenchmarkProgress(ctx, benchmarkContainerName, onProgress)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := d.waitForExit(waitCtx, benchmarkContainerName); err != nil {
		_ = d.RemoveContainer(ctx, benchmarkContainerName, true)
		_ = os.RemoveAll(benchDir)
		return nil, fmt.Errorf("benchmark did not finish: %w", err)
	}

	// 7. Check exit code
	report(7, "Reading results...")
	cj, err := d.InspectContainer(ctx, benchmarkContainerName)
	if err == nil && cj.State.ExitCode != 0 {
		// Read logs for error details
		errOutput, _ := d.readContainerLogs(ctx, benchmarkContainerName)
		_ = d.RemoveContainer(ctx, benchmarkContainerName, true)
		_ = os.RemoveAll(benchDir)
		// Extract last meaningful line as error message
		errMsg := lastNonEmptyLine(errOutput)
		if errMsg == "" {
			errMsg = fmt.Sprintf("exit code %d", cj.State.ExitCode)
		}
		return nil, fmt.Errorf("benchmark failed: %s", errMsg)
	}

	// 8. Read logs
	rawOutput, logErr := d.readContainerLogs(ctx, benchmarkContainerName)
	if logErr != nil {
		_ = d.RemoveContainer(ctx, benchmarkContainerName, true)
		_ = os.RemoveAll(benchDir)
		return nil, fmt.Errorf("read benchmark logs: %w", logErr)
	}

	// 9. Cleanup
	report(8, "Cleaning up...")
	_ = d.RemoveContainer(ctx, benchmarkContainerName, true)
	_ = os.RemoveAll(benchDir)

	// 10. Parse results
	result := parseBenchmarkOutput(rawOutput)
	return result, nil
}

// streamBenchmarkProgress follows the container logs and sends progress events
// for each "Running..." line the benchmark outputs.
func (d *DockerClient) streamBenchmarkProgress(ctx context.Context, containerName string, onProgress BenchmarkProgress) {
	if onProgress == nil {
		return
	}

	params := url.Values{
		"stdout": {"true"},
		"stderr": {"true"},
		"follow": {"true"},
		"tail":   {"0"},
	}

	u := fmt.Sprintf("http://localhost/%s/containers/%s/logs?%s",
		d.apiVersion, url.PathEscape(containerName), params.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Map benchmark test names to step numbers
	testSteps := []struct {
		keyword string
		step    int
		name    string
	}{
		{"network benchmark", 4, "Running network benchmark..."},
		{"KV store benchmark", 4, "Running KV store benchmark..."},
		{"goroutine scalability", 5, "Running CPU scalability benchmark..."},
		{"disk I/O benchmark", 5, "Running disk I/O benchmark..."},
		{"memory benchmark", 6, "Running memory benchmark..."},
		{"big-number benchmark", 6, "Running big-number / FPU benchmark..."},
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			// Strip Docker multiplex headers (8-byte prefix per frame)
			cleaned := stripDockerLogHeaders(buf[:n])
			if cleaned != "" {
				chunk = cleaned
			}
			for _, ts := range testSteps {
				if strings.Contains(strings.ToLower(chunk), strings.ToLower(ts.keyword)) {
					onProgress(ts.step, 8, ts.name)
				}
			}
		}
		if readErr != nil {
			return
		}
	}
}

// waitForExit polls container status until it stops running or the context times out.
func (d *DockerClient) waitForExit(ctx context.Context, containerName string) error {
	for {
		status, err := d.GetContainerStatus(ctx, containerName)
		if err != nil {
			return fmt.Errorf("get status: %w", err)
		}
		// GetContainerStatus returns "running" or "stopped"
		if status != "running" {
			return nil
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

	return stripDockerLogHeaders(raw), nil
}

// stripDockerLogHeaders removes the 8-byte multiplexed stream headers.
func stripDockerLogHeaders(data []byte) string {
	var out strings.Builder
	for len(data) >= 8 {
		frameSize := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		data = data[8:]
		if frameSize > len(data) {
			frameSize = len(data)
		}
		out.Write(data[:frameSize])
		data = data[frameSize:]
	}
	if out.Len() == 0 {
		return string(data)
	}
	return out.String()
}

// lastNonEmptyLine returns the last non-empty line from text.
func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

// --- Parsing ---

var (
	scorePattern   = regexp.MustCompile(`SCORE\s*:\s*(\d+)\s*/\s*(\d+)\s+Grade:\s*(\S+)\s+(.+)`)
	sectionPattern = regexp.MustCompile(`(?i)^\s*([\w\s/()]+?)\s+(\d+)\s*/\s*(\d+)\s+\[`)
)

func parseBenchmarkOutput(raw string) *BenchmarkResult {
	result := &BenchmarkResult{
		Timestamp: time.Now().Unix(),
		RawOutput: raw,
		MaxScore:  1000,
	}

	// Parse SCORE line
	if m := scorePattern.FindStringSubmatch(raw); m != nil {
		result.Score, _ = strconv.Atoi(m[1])
		result.MaxScore, _ = strconv.Atoi(m[2])
		result.Grade = m[3]
		result.Verdict = strings.TrimSpace(m[4])
	}

	// Parse section scores from the summary block at the bottom
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		m := sectionPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		score, _ := strconv.Atoi(m[2])
		maxScore, _ := strconv.Atoi(m[3])
		var pct float64
		if maxScore > 0 {
			pct = float64(score) / float64(maxScore) * 100
		}

		// Determine status from the section headers in the report
		name := strings.TrimSpace(m[1])
		status := sectionStatus(raw, name)

		result.Sections = append(result.Sections, BenchmarkSection{
			Name:     name,
			Status:   status,
			Score:    score,
			MaxScore: maxScore,
			Percent:  pct,
		})
	}

	return result
}

// sectionStatus finds the PASS/WARN/FAIL status for a section from the full report.
func sectionStatus(raw, sectionName string) string {
	// Map summary names to header names in the report
	headerNames := map[string][]string{
		"Goroutine (CPU)": {"GOROUTINE"},
		"Disk I/O":        {"DISK I/O"},
		"Network":         {"NETWORK"},
		"KV Store":        {"KV STORE"},
		"Memory":          {"MEMORY"},
		"BigNum / FPU":    {"BIG NUMBER"},
	}

	patterns, ok := headerNames[sectionName]
	if !ok {
		patterns = []string{strings.ToUpper(sectionName)}
	}

	for _, pat := range patterns {
		idx := strings.Index(strings.ToUpper(raw), pat)
		if idx < 0 {
			continue
		}
		// Look for [OK] PASS, [!!] WARN, [XX] FAIL near the header
		snippet := raw[idx:min(idx+200, len(raw))]
		if strings.Contains(snippet, "FAIL") {
			return "FAIL"
		}
		if strings.Contains(snippet, "WARN") {
			return "WARN"
		}
		if strings.Contains(snippet, "PASS") {
			return "PASS"
		}
	}
	return "PASS"
}
