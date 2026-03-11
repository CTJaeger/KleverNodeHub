package agent

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// MetricsCollector collects system-level metrics from the host.
type MetricsCollector struct {
	// procDir allows overriding /proc for testing
	procDir string
	// diskPaths are the filesystem paths to check for disk usage
	diskPaths []string
	// prevCPU stores the previous CPU stats for calculating delta
	prevCPU *cpuStats
}

// cpuStats stores raw CPU counters from /proc/stat.
type cpuStats struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c *cpuStats) total() uint64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

func (c *cpuStats) active() uint64 {
	return c.total() - c.idle - c.iowait
}

// NewMetricsCollector creates a new metrics collector.
// diskPaths specifies which filesystem paths to monitor for disk usage.
func NewMetricsCollector(diskPaths []string) *MetricsCollector {
	if len(diskPaths) == 0 {
		diskPaths = []string{"/"}
	}
	return &MetricsCollector{
		procDir:   "/proc",
		diskPaths: diskPaths,
	}
}

// Collect gathers current system metrics.
func (mc *MetricsCollector) Collect() *models.SystemMetrics {
	m := &models.SystemMetrics{
		CollectedAt: time.Now().Unix(),
	}

	m.CPUPercent = mc.collectCPU()
	mc.collectMemory(m)
	mc.collectDisk(m)
	mc.collectLoadAvg(m)

	return m
}

// collectCPU reads CPU usage from /proc/stat (Linux) or falls back to runtime (other OS).
func (mc *MetricsCollector) collectCPU() float64 {
	if runtime.GOOS != "linux" {
		return 0 // Fallback: no /proc on macOS/Windows
	}

	stats, err := mc.readCPUStats()
	if err != nil {
		return 0
	}

	if mc.prevCPU == nil {
		mc.prevCPU = stats
		return 0 // Need two samples for delta
	}

	prev := mc.prevCPU
	mc.prevCPU = stats

	totalDelta := stats.total() - prev.total()
	if totalDelta == 0 {
		return 0
	}

	activeDelta := stats.active() - prev.active()
	return float64(activeDelta) / float64(totalDelta) * 100.0
}

// readCPUStats parses /proc/stat for aggregate CPU counters.
func (mc *MetricsCollector) readCPUStats() (*cpuStats, error) {
	f, err := os.Open(mc.procDir + "/stat")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 8 {
			return nil, fmt.Errorf("unexpected /proc/stat cpu line: %s", line)
		}

		parse := func(s string) uint64 {
			v, _ := strconv.ParseUint(s, 10, 64)
			return v
		}

		stats := &cpuStats{
			user:    parse(fields[1]),
			nice:    parse(fields[2]),
			system:  parse(fields[3]),
			idle:    parse(fields[4]),
			iowait:  parse(fields[5]),
			irq:     parse(fields[6]),
			softirq: parse(fields[7]),
		}
		if len(fields) > 8 {
			stats.steal = parse(fields[8])
		}

		return stats, nil
	}

	return nil, fmt.Errorf("cpu line not found in /proc/stat")
}

// collectMemory reads memory info from /proc/meminfo (Linux) or falls back.
func (mc *MetricsCollector) collectMemory(m *models.SystemMetrics) {
	if runtime.GOOS != "linux" {
		// Fallback: use Go runtime stats
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		m.MemUsed = mem.Sys
		return
	}

	f, err := os.Open(mc.procDir + "/meminfo")
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	var memTotal, memAvailable uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			memTotal = parseKBValue(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			memAvailable = parseKBValue(line)
		}
	}

	m.MemTotal = memTotal * 1024 // Convert KB to bytes
	if memTotal > 0 {
		m.MemUsed = (memTotal - memAvailable) * 1024
		m.MemPercent = float64(memTotal-memAvailable) / float64(memTotal) * 100.0
	}
}

// collectDisk is implemented in metrics_disk_unix.go and metrics_disk_windows.go

// collectLoadAvg reads load average from /proc/loadavg (Linux only).
func (mc *MetricsCollector) collectLoadAvg(m *models.SystemMetrics) {
	if runtime.GOOS != "linux" {
		return
	}

	data, err := os.ReadFile(mc.procDir + "/loadavg")
	if err != nil {
		return
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return
	}

	m.LoadAvg1, _ = strconv.ParseFloat(fields[0], 64)
	m.LoadAvg5, _ = strconv.ParseFloat(fields[1], 64)
	m.LoadAvg15, _ = strconv.ParseFloat(fields[2], 64)
}

// parseKBValue extracts the numeric KB value from a /proc/meminfo line.
// Example: "MemTotal:       16384000 kB" -> 16384000
func parseKBValue(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[1], 10, 64)
	return v
}
