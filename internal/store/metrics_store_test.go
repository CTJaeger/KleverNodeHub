package store

import (
	"path/filepath"
	"testing"
	"time"
)

func setupMetricsStore(t *testing.T) *MetricsStore {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewMetricsStore(db)
}

func TestInsertAndQueryNodeMetrics(t *testing.T) {
	s := setupMetricsStore(t)

	metrics := map[string]float64{
		"klv_nonce":            29091835,
		"klv_cpu_load_percent": 1.0,
		"klv_epoch_number":     5397,
	}

	ts := time.Now().Unix()
	if err := s.InsertNodeMetrics("node-1", "server-1", metrics, ts); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Query back
	points, err := s.QueryRecent("node-1", "klv_nonce", ts-1, ts+1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("points = %d, want 1", len(points))
	}
	if points[0].Value != 29091835 {
		t.Errorf("value = %f, want 29091835", points[0].Value)
	}
}

func TestInsertAndQuerySystemMetrics(t *testing.T) {
	s := setupMetricsStore(t)

	ts := time.Now().Unix()
	if err := s.InsertSystemMetrics("server-1", &SystemMetricsRow{
		CPUPercent: 45.5, MemPercent: 62.3, MemTotal: 8000000000, MemUsed: 4984000000,
		DiskPercent: 78.1, DiskTotal: 100000000000, DiskUsed: 78100000000, LoadAvg1: 1.25, CollectedAt: ts,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := s.QuerySystemMetrics("server-1", ts-1, ts+1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].CPUPercent != 45.5 {
		t.Errorf("cpu = %f, want 45.5", rows[0].CPUPercent)
	}
	if rows[0].LoadAvg1 != 1.25 {
		t.Errorf("load = %f, want 1.25", rows[0].LoadAvg1)
	}
}

func TestQueryRecent_Empty(t *testing.T) {
	s := setupMetricsStore(t)
	points, err := s.QueryRecent("nonexistent", "klv_nonce", 0, time.Now().Unix())
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("points = %d, want 0", len(points))
	}
}

func TestQueryRecent_TimeRange(t *testing.T) {
	s := setupMetricsStore(t)

	base := int64(1000000)
	for i := int64(0); i < 5; i++ {
		m := map[string]float64{"klv_nonce": float64(100 + i)}
		if err := s.InsertNodeMetrics("node-1", "server-1", m, base+i*10); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// Query middle range
	points, err := s.QueryRecent("node-1", "klv_nonce", base+15, base+35)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 2 {
		t.Errorf("points = %d, want 2 (ts 20 and 30)", len(points))
	}
}

func TestDecimate(t *testing.T) {
	s := setupMetricsStore(t)

	// Insert data that is "old" (10 days ago)
	oldTs := time.Now().Add(-10 * 24 * time.Hour).Unix()
	for i := 0; i < 10; i++ {
		m := map[string]float64{"klv_nonce": float64(100 + i)}
		if err := s.InsertNodeMetrics("node-1", "server-1", m, oldTs+int64(i*60)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// Insert recent data (should NOT be decimated)
	recentTs := time.Now().Unix()
	m := map[string]float64{"klv_nonce": float64(200)}
	if err := s.InsertNodeMetrics("node-1", "server-1", m, recentTs); err != nil {
		t.Fatalf("insert recent: %v", err)
	}

	// Decimate: aggregate data older than 7 days into 5-min buckets
	count, err := s.Decimate(7*24*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("decimate: %v", err)
	}
	if count != 10 {
		t.Errorf("decimated = %d, want 10", count)
	}

	// Recent data should still exist
	points, err := s.QueryRecent("node-1", "klv_nonce", recentTs-1, recentTs+1)
	if err != nil {
		t.Fatalf("query recent: %v", err)
	}
	if len(points) != 1 {
		t.Errorf("recent points = %d, want 1", len(points))
	}

	// Archive should have data
	archivePoints, err := s.QueryArchive("node-1", "klv_nonce", oldTs-300, oldTs+600)
	if err != nil {
		t.Fatalf("query archive: %v", err)
	}
	if len(archivePoints) == 0 {
		t.Error("expected archive data after decimation")
	}

	// Verify aggregate values
	if archivePoints[0].SampleCount == 0 {
		t.Error("expected non-zero sample count")
	}
	if archivePoints[0].MinValue > archivePoints[0].MaxValue {
		t.Error("min > max")
	}
}

// TestPurgeSystemMetrics_Batched inserts more rows than one purge batch and
// verifies the batched delete removes them all (loop terminates, no leftovers).
func TestPurgeSystemMetrics_Batched(t *testing.T) {
	s := setupMetricsStore(t)

	oldTs := time.Now().Add(-100 * 24 * time.Hour).Unix()
	const rows = purgeBatch + 500 // force more than one batch
	for i := 0; i < rows; i++ {
		m := &SystemMetricsRow{CPUPercent: 1, CollectedAt: oldTs + int64(i)}
		if err := s.InsertSystemMetrics("server-1", m); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	n, err := s.PurgeSystemMetrics(90 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != rows {
		t.Errorf("purged = %d, want %d", n, rows)
	}

	left, err := s.QuerySystemMetrics("server-1", oldTs-1, oldTs+int64(rows)+1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("rows left after batched purge = %d, want 0", len(left))
	}
}

// TestDecimate_MultipleSlices covers data spanning more than one decimation
// slice (sliceBuckets * bucketSize), exercising the chunked path: every old row
// must be aggregated and removed from metrics_recent across slices.
func TestDecimate_MultipleSlices(t *testing.T) {
	s := setupMetricsStore(t)

	bucket := 5 * time.Minute
	// Spread old rows across ~4 hours (well beyond one 6-bucket=30-min slice),
	// one sample every 5 minutes so each falls in its own bucket.
	base := time.Now().Add(-10 * 24 * time.Hour).Unix()
	const samples = 48 // 4 hours / 5 min
	for i := 0; i < samples; i++ {
		m := map[string]float64{"klv_nonce": float64(i)}
		if err := s.InsertNodeMetrics("node-1", "server-1", m, base+int64(i)*int64(bucket.Seconds())); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	count, err := s.Decimate(7*24*time.Hour, bucket)
	if err != nil {
		t.Fatalf("decimate: %v", err)
	}
	if count != samples {
		t.Errorf("decimated = %d, want %d (all old rows across slices)", count, samples)
	}

	// metrics_recent must be empty of the old rows.
	pts, err := s.QueryRecent("node-1", "klv_nonce", base-60, base+int64(samples)*int64(bucket.Seconds())+60)
	if err != nil {
		t.Fatalf("query recent: %v", err)
	}
	if len(pts) != 0 {
		t.Errorf("recent rows left after decimation = %d, want 0", len(pts))
	}

	// Archive should hold one bucket per sample (each sample in its own 5-min bucket).
	arc, err := s.QueryArchive("node-1", "klv_nonce", base-300, base+int64(samples)*int64(bucket.Seconds())+300)
	if err != nil {
		t.Fatalf("query archive: %v", err)
	}
	if len(arc) != samples {
		t.Errorf("archive buckets = %d, want %d", len(arc), samples)
	}
}

func TestPurge(t *testing.T) {
	s := setupMetricsStore(t)

	// First decimate some old data to create archive rows
	oldTs := time.Now().Add(-100 * 24 * time.Hour).Unix()
	for i := 0; i < 5; i++ {
		m := map[string]float64{"klv_nonce": float64(100 + i)}
		if err := s.InsertNodeMetrics("node-1", "server-1", m, oldTs+int64(i*60)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	_, err := s.Decimate(1*time.Hour, 5*time.Minute)
	if err != nil {
		t.Fatalf("decimate: %v", err)
	}

	// Purge archives older than 90 days
	count, err := s.Purge(90 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if count == 0 {
		t.Error("expected purge to remove old archive data")
	}
}

func TestPurgeSystemMetrics(t *testing.T) {
	s := setupMetricsStore(t)

	oldTs := time.Now().Add(-10 * 24 * time.Hour).Unix()
	if err := s.InsertSystemMetrics("server-1", &SystemMetricsRow{
		CPUPercent: 50, MemPercent: 60, DiskPercent: 70, LoadAvg1: 1.0, CollectedAt: oldTs,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	count, err := s.PurgeSystemMetrics(7 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if count != 1 {
		t.Errorf("purged = %d, want 1", count)
	}
}

func TestExtractNumericMetrics(t *testing.T) {
	raw := map[string]any{
		"klv_nonce":            float64(29091835),
		"klv_cpu_load_percent": float64(1),
		"klv_consensus_state":  "not in consensus group", // string — should be skipped
		"klv_app_version":      "v1.7.15",                // string — should be skipped
		"klv_is_syncing":       float64(0),
	}

	numeric := ExtractNumericMetrics(raw)
	if len(numeric) != 3 {
		t.Errorf("numeric count = %d, want 3", len(numeric))
	}
	if numeric["klv_nonce"] != 29091835 {
		t.Errorf("klv_nonce = %f, want 29091835", numeric["klv_nonce"])
	}
	if _, ok := numeric["klv_consensus_state"]; ok {
		t.Error("string metric should not be in numeric map")
	}
}

func TestQueryAutoResolution_RecentOnly(t *testing.T) {
	s := setupMetricsStore(t)

	ts := time.Now().Unix()
	m := map[string]float64{"klv_nonce": float64(100)}
	if err := s.InsertNodeMetrics("node-1", "server-1", m, ts); err != nil {
		t.Fatalf("insert: %v", err)
	}

	result, err := s.QueryAutoResolution("node-1", "klv_nonce", ts-1, ts+1, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	// Should return recent data points
	points, ok := result.([]DataPoint)
	if !ok {
		t.Fatalf("expected []DataPoint, got %T", result)
	}
	if len(points) != 1 {
		t.Errorf("points = %d, want 1", len(points))
	}
}

func TestBatchInsert_LargeSet(t *testing.T) {
	s := setupMetricsStore(t)

	// Simulate a realistic node metrics payload (76 numeric metrics)
	metrics := make(map[string]float64, 76)
	for i := 0; i < 76; i++ {
		metrics["klv_metric_"+string(rune('a'+i%26))+string(rune('0'+i/26))] = float64(i * 100)
	}

	ts := time.Now().Unix()
	if err := s.InsertNodeMetrics("node-1", "server-1", metrics, ts); err != nil {
		t.Fatalf("insert 76 metrics: %v", err)
	}

	// Verify one of them
	points, err := s.QueryRecent("node-1", "klv_metric_a0", ts-1, ts+1)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 1 {
		t.Errorf("points = %d, want 1", len(points))
	}
}
