package store

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// DataPoint is a single time-series data point.
type DataPoint struct {
	Value       float64 `json:"value"`
	CollectedAt int64   `json:"collected_at"`
}

// AggregatedPoint is a decimated time-series data point with min/max/avg.
type AggregatedPoint struct {
	AvgValue    float64 `json:"avg_value"`
	MinValue    float64 `json:"min_value"`
	MaxValue    float64 `json:"max_value"`
	SampleCount int     `json:"sample_count"`
	BucketStart int64   `json:"bucket_start"`
	BucketEnd   int64   `json:"bucket_end"`
}

// SystemMetricsRow represents a row from the system_metrics table.
type SystemMetricsRow struct {
	ServerID    string  `json:"server_id"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemPercent  float64 `json:"mem_percent"`
	DiskPercent float64 `json:"disk_percent"`
	LoadAvg1    float64 `json:"load_avg_1"`
	CollectedAt int64   `json:"collected_at"`
}

// MetricsStore handles persistence for node and system metrics.
type MetricsStore struct {
	db *DB
	mu sync.Mutex // batch insert serialization
}

// NewMetricsStore creates a new metrics store.
func NewMetricsStore(db *DB) *MetricsStore {
	return &MetricsStore{db: db}
}

// InsertNodeMetrics inserts raw node metrics into metrics_recent.
// Only numeric values are stored; string metrics are skipped.
func (s *MetricsStore) InsertNodeMetrics(nodeID, serverID string, metrics map[string]float64, ts int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO metrics_recent (node_id, server_id, metric_name, metric_value, collected_at) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for name, value := range metrics {
		if _, err := stmt.Exec(nodeID, serverID, name, value, ts); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert %s: %w", name, err)
		}
	}

	return tx.Commit()
}

// InsertSystemMetrics inserts a system metrics snapshot.
func (s *MetricsStore) InsertSystemMetrics(serverID string, cpuPercent, memPercent, diskPercent, loadAvg1 float64, ts int64) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	_, err := s.db.db.Exec(`INSERT INTO system_metrics (server_id, cpu_percent, mem_percent, disk_percent, load_avg_1, collected_at) VALUES (?, ?, ?, ?, ?, ?)`,
		serverID, cpuPercent, memPercent, diskPercent, loadAvg1, ts)
	if err != nil {
		return fmt.Errorf("insert system metrics: %w", err)
	}
	return nil
}

// QueryRecent returns raw data points from metrics_recent for a specific metric.
func (s *MetricsStore) QueryRecent(nodeID, metricName string, from, to int64) ([]DataPoint, error) {
	rows, err := s.db.db.Query(
		`SELECT metric_value, collected_at FROM metrics_recent
		 WHERE node_id = ? AND metric_name = ? AND collected_at >= ? AND collected_at <= ?
		 ORDER BY collected_at ASC`,
		nodeID, metricName, from, to)
	if err != nil {
		return nil, fmt.Errorf("query recent: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var points []DataPoint
	for rows.Next() {
		var p DataPoint
		if err := rows.Scan(&p.Value, &p.CollectedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// QueryArchive returns aggregated data points from metrics_archive.
func (s *MetricsStore) QueryArchive(nodeID, metricName string, from, to int64) ([]AggregatedPoint, error) {
	rows, err := s.db.db.Query(
		`SELECT avg_value, min_value, max_value, sample_count, bucket_start, bucket_end
		 FROM metrics_archive
		 WHERE node_id = ? AND metric_name = ? AND bucket_start >= ? AND bucket_end <= ?
		 ORDER BY bucket_start ASC`,
		nodeID, metricName, from, to)
	if err != nil {
		return nil, fmt.Errorf("query archive: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var points []AggregatedPoint
	for rows.Next() {
		var p AggregatedPoint
		if err := rows.Scan(&p.AvgValue, &p.MinValue, &p.MaxValue, &p.SampleCount, &p.BucketStart, &p.BucketEnd); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// QuerySystemMetrics returns system metrics for a server within a time range.
func (s *MetricsStore) QuerySystemMetrics(serverID string, from, to int64) ([]SystemMetricsRow, error) {
	rows, err := s.db.db.Query(
		`SELECT server_id, cpu_percent, mem_percent, disk_percent, load_avg_1, collected_at
		 FROM system_metrics
		 WHERE server_id = ? AND collected_at >= ? AND collected_at <= ?
		 ORDER BY collected_at ASC`,
		serverID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query system metrics: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []SystemMetricsRow
	for rows.Next() {
		var r SystemMetricsRow
		if err := rows.Scan(&r.ServerID, &r.CPUPercent, &r.MemPercent, &r.DiskPercent, &r.LoadAvg1, &r.CollectedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// Decimate aggregates raw metrics older than the cutoff into 5-minute buckets
// in metrics_archive, then deletes the aggregated raw rows.
// Returns the number of raw rows decimated.
func (s *MetricsStore) Decimate(olderThan time.Duration, bucketSize time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-olderThan).Unix()
	bucketSecs := int64(bucketSize.Seconds())

	tx, err := s.db.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}

	// Aggregate into buckets
	_, err = tx.Exec(`
		INSERT INTO metrics_archive (node_id, server_id, metric_name, avg_value, min_value, max_value, sample_count, bucket_start, bucket_end)
		SELECT node_id, server_id, metric_name,
			AVG(metric_value), MIN(metric_value), MAX(metric_value), COUNT(*),
			(collected_at / ?) * ?,
			(collected_at / ?) * ? + ?
		FROM metrics_recent
		WHERE collected_at < ?
		GROUP BY node_id, server_id, metric_name, (collected_at / ?) * ?
	`, bucketSecs, bucketSecs, bucketSecs, bucketSecs, bucketSecs, cutoff, bucketSecs, bucketSecs)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("aggregate: %w", err)
	}

	// Delete decimated raw rows
	result, err := tx.Exec(`DELETE FROM metrics_recent WHERE collected_at < ?`, cutoff)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("delete raw: %w", err)
	}

	count, _ := result.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return count, nil
}

// Purge deletes archived metrics older than the given duration.
// Returns the number of rows deleted.
func (s *MetricsStore) Purge(olderThan time.Duration) (int64, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	cutoff := time.Now().Add(-olderThan).Unix()

	result, err := s.db.db.Exec(`DELETE FROM metrics_archive WHERE bucket_end < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge archive: %w", err)
	}

	count, _ := result.RowsAffected()
	return count, nil
}

// PurgeSystemMetrics deletes system metrics older than the given duration.
func (s *MetricsStore) PurgeSystemMetrics(olderThan time.Duration) (int64, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	cutoff := time.Now().Add(-olderThan).Unix()

	result, err := s.db.db.Exec(`DELETE FROM system_metrics WHERE collected_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge system metrics: %w", err)
	}

	count, _ := result.RowsAffected()
	return count, nil
}

// QueryAutoResolution automatically selects metrics_recent or metrics_archive
// based on the requested time range. If the range starts within the hot window
// (7 days), it uses recent data; otherwise archive data.
func (s *MetricsStore) QueryAutoResolution(nodeID, metricName string, from, to int64, hotWindow time.Duration) (any, error) {
	cutoff := time.Now().Add(-hotWindow).Unix()

	if from >= cutoff {
		// Entirely within hot window — use raw data
		return s.QueryRecent(nodeID, metricName, from, to)
	}

	if to < cutoff {
		// Entirely in cold storage — use archive
		return s.QueryArchive(nodeID, metricName, from, to)
	}

	// Spans both — merge archive + recent
	archivePoints, err := s.QueryArchive(nodeID, metricName, from, cutoff)
	if err != nil {
		return nil, err
	}

	recentPoints, err := s.QueryRecent(nodeID, metricName, cutoff, to)
	if err != nil {
		return nil, err
	}

	// Return a combined result
	type MergedResult struct {
		Archive []AggregatedPoint `json:"archive"`
		Recent  []DataPoint       `json:"recent"`
	}
	return &MergedResult{Archive: archivePoints, Recent: recentPoints}, nil
}

// ExtractNumericMetrics extracts numeric values from a map[string]any,
// converting JSON number types to float64.
func ExtractNumericMetrics(raw map[string]any) map[string]float64 {
	result := make(map[string]float64, len(raw))
	for k, v := range raw {
		switch val := v.(type) {
		case float64:
			result[k] = val
		case int:
			result[k] = float64(val)
		case int64:
			result[k] = float64(val)
			// String metrics are intentionally skipped
		}
	}
	return result
}

// scanner is a common interface for *sql.Row and *sql.Rows.
type metricsScanner interface {
	Scan(dest ...any) error
}

// Ensure both types implement the scanner interface.
var (
	_ metricsScanner = (*sql.Row)(nil)
	_ metricsScanner = (*sql.Rows)(nil)
)
