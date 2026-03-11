package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewMetricsCollector(t *testing.T) {
	mc := NewMetricsCollector(nil)
	if mc == nil {
		t.Fatal("expected non-nil collector")
	}
	if len(mc.diskPaths) == 0 {
		t.Error("expected default disk paths")
	}
}

func TestNewMetricsCollector_CustomPaths(t *testing.T) {
	mc := NewMetricsCollector([]string{"/data", "/opt"})
	if len(mc.diskPaths) != 2 {
		t.Errorf("diskPaths = %d, want 2", len(mc.diskPaths))
	}
}

func TestCollect_Basic(t *testing.T) {
	mc := NewMetricsCollector(nil)
	m := mc.Collect()
	if m == nil {
		t.Fatal("expected non-nil metrics")
	}
	if m.CollectedAt == 0 {
		t.Error("CollectedAt should be set")
	}
}

func TestCollect_TwoSamples(t *testing.T) {
	mc := NewMetricsCollector(nil)
	// First sample initializes prevCPU
	_ = mc.Collect()
	// Second sample should calculate delta
	m := mc.Collect()
	if m == nil {
		t.Fatal("expected non-nil metrics")
	}
	// On non-Linux, CPU will be 0 — that's expected
}

func TestReadCPUStats_MockProc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping /proc test on non-Linux")
	}

	// Create mock /proc/stat
	dir := t.TempDir()
	statContent := "cpu  1000 200 300 5000 100 50 25 10 0 0\ncpu0  500 100 150 2500 50 25 12 5 0 0\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(statContent), 0644); err != nil {
		t.Fatalf("write mock stat: %v", err)
	}

	mc := &MetricsCollector{procDir: dir}
	stats, err := mc.readCPUStats()
	if err != nil {
		t.Fatalf("readCPUStats: %v", err)
	}

	if stats.user != 1000 {
		t.Errorf("user = %d, want 1000", stats.user)
	}
	if stats.idle != 5000 {
		t.Errorf("idle = %d, want 5000", stats.idle)
	}
	if stats.system != 300 {
		t.Errorf("system = %d, want 300", stats.system)
	}
	if stats.steal != 10 {
		t.Errorf("steal = %d, want 10", stats.steal)
	}
}

func TestCPUPercent_MockProc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping /proc test on non-Linux")
	}

	dir := t.TempDir()

	// First sample
	stat1 := "cpu  1000 200 300 5000 100 50 25 10 0 0\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat1), 0644); err != nil {
		t.Fatalf("write stat1: %v", err)
	}
	mc := &MetricsCollector{procDir: dir, diskPaths: []string{dir}}
	cpu1 := mc.collectCPU()
	if cpu1 != 0 {
		t.Errorf("first sample should be 0, got %f", cpu1)
	}

	// Second sample with more active time
	stat2 := "cpu  1200 200 400 5100 100 50 25 10 0 0\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat2), 0644); err != nil {
		t.Fatalf("write stat2: %v", err)
	}
	cpu2 := mc.collectCPU()

	// Total delta: (1200+200+400+5100+100+50+25+10) - (1000+200+300+5000+100+50+25+10) = 7085-6685 = 400
	// Active delta: (1200+200+400+100+50+25+10) - (1000+200+300+100+50+25+10) = 1985-1685 = 300
	// CPU% = 300/400 * 100 = 75%
	expected := 75.0
	if cpu2 < expected-1 || cpu2 > expected+1 {
		t.Errorf("cpu = %f, want ~%f", cpu2, expected)
	}
}

func TestCollectMemory_MockProc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping /proc test on non-Linux")
	}

	dir := t.TempDir()
	meminfo := `MemTotal:       16384000 kB
MemFree:         2000000 kB
MemAvailable:    8000000 kB
Buffers:          500000 kB
Cached:          5000000 kB
`
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(meminfo), 0644); err != nil {
		t.Fatalf("write meminfo: %v", err)
	}

	mc := &MetricsCollector{procDir: dir, diskPaths: []string{dir}}
	m := mc.Collect()

	expectedTotal := uint64(16384000 * 1024)
	if m.MemTotal != expectedTotal {
		t.Errorf("MemTotal = %d, want %d", m.MemTotal, expectedTotal)
	}

	expectedUsed := uint64((16384000 - 8000000) * 1024)
	if m.MemUsed != expectedUsed {
		t.Errorf("MemUsed = %d, want %d", m.MemUsed, expectedUsed)
	}

	expectedPercent := float64(16384000-8000000) / float64(16384000) * 100.0
	if m.MemPercent < expectedPercent-1 || m.MemPercent > expectedPercent+1 {
		t.Errorf("MemPercent = %f, want ~%f", m.MemPercent, expectedPercent)
	}
}

func TestCollectLoadAvg_MockProc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping /proc test on non-Linux")
	}

	dir := t.TempDir()
	loadavg := "1.25 2.50 3.75 2/150 12345\n"
	if err := os.WriteFile(filepath.Join(dir, "loadavg"), []byte(loadavg), 0644); err != nil {
		t.Fatalf("write loadavg: %v", err)
	}

	// Also need /proc/stat and /proc/meminfo for Collect()
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte("cpu  1000 200 300 5000 100 50 25 10 0 0\n"), 0644); err != nil {
		t.Fatalf("write stat: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte("MemTotal: 16384000 kB\nMemAvailable: 8000000 kB\n"), 0644); err != nil {
		t.Fatalf("write meminfo: %v", err)
	}

	mc := &MetricsCollector{procDir: dir, diskPaths: []string{dir}}
	m := mc.Collect()

	if m.LoadAvg1 != 1.25 {
		t.Errorf("LoadAvg1 = %f, want 1.25", m.LoadAvg1)
	}
	if m.LoadAvg5 != 2.50 {
		t.Errorf("LoadAvg5 = %f, want 2.50", m.LoadAvg5)
	}
	if m.LoadAvg15 != 3.75 {
		t.Errorf("LoadAvg15 = %f, want 3.75", m.LoadAvg15)
	}
}

func TestParseKBValue(t *testing.T) {
	tests := []struct {
		line string
		want uint64
	}{
		{"MemTotal:       16384000 kB", 16384000},
		{"MemAvailable:    8000000 kB", 8000000},
		{"Buffers:          500000 kB", 500000},
		{"", 0},
		{"BadLine", 0},
	}

	for _, tt := range tests {
		got := parseKBValue(tt.line)
		if got != tt.want {
			t.Errorf("parseKBValue(%q) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

func TestCPUStats_TotalAndActive(t *testing.T) {
	s := &cpuStats{
		user: 1000, nice: 200, system: 300, idle: 5000,
		iowait: 100, irq: 50, softirq: 25, steal: 10,
	}

	expectedTotal := uint64(1000 + 200 + 300 + 5000 + 100 + 50 + 25 + 10)
	if s.total() != expectedTotal {
		t.Errorf("total() = %d, want %d", s.total(), expectedTotal)
	}

	expectedActive := expectedTotal - 5000 - 100 // total - idle - iowait
	if s.active() != expectedActive {
		t.Errorf("active() = %d, want %d", s.active(), expectedActive)
	}
}
