// Package scheduler implements background jobs for metrics maintenance.
package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

const (
	// Default retention periods
	defaultHotWindow       = 7 * 24 * time.Hour  // 7 days raw data
	defaultArchiveRetention = 90 * 24 * time.Hour // 90 days archived data
	defaultBucketSize      = 5 * time.Minute      // 5-minute aggregation

	// Job intervals
	decimationInterval     = 1 * time.Hour
	purgeInterval          = 24 * time.Hour
	systemCleanupInterval  = 6 * time.Hour
)

// Scheduler runs background maintenance jobs for metrics storage.
type Scheduler struct {
	metrics          *store.MetricsStore
	hotWindow        time.Duration
	archiveRetention time.Duration
	bucketSize       time.Duration
	cancel           context.CancelFunc
}

// Option configures the scheduler.
type Option func(*Scheduler)

// WithHotWindow sets the raw data retention window.
func WithHotWindow(d time.Duration) Option {
	return func(s *Scheduler) { s.hotWindow = d }
}

// WithArchiveRetention sets the archive data retention period.
func WithArchiveRetention(d time.Duration) Option {
	return func(s *Scheduler) { s.archiveRetention = d }
}

// WithBucketSize sets the aggregation bucket size.
func WithBucketSize(d time.Duration) Option {
	return func(s *Scheduler) { s.bucketSize = d }
}

// New creates a new scheduler.
func New(metrics *store.MetricsStore, opts ...Option) *Scheduler {
	s := &Scheduler{
		metrics:          metrics,
		hotWindow:        defaultHotWindow,
		archiveRetention: defaultArchiveRetention,
		bucketSize:       defaultBucketSize,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start launches background maintenance goroutines.
func (s *Scheduler) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	go s.runDecimation(ctx)
	go s.runPurge(ctx)
	go s.runSystemCleanup(ctx)

	log.Printf("scheduler started (hot=%s, archive=%s, bucket=%s)", s.hotWindow, s.archiveRetention, s.bucketSize)
}

// Stop gracefully shuts down all background jobs.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
		log.Println("scheduler stopped")
	}
}

func (s *Scheduler) runDecimation(ctx context.Context) {
	// Run once at startup to clean any backlog
	s.decimateOnce()

	ticker := time.NewTicker(decimationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.decimateOnce()
		}
	}
}

func (s *Scheduler) decimateOnce() {
	count, err := s.metrics.Decimate(s.hotWindow, s.bucketSize)
	if err != nil {
		log.Printf("decimation error: %v", err)
		return
	}
	if count > 0 {
		log.Printf("decimated %d raw metric rows into %s buckets", count, s.bucketSize)
	}
}

func (s *Scheduler) runPurge(ctx context.Context) {
	ticker := time.NewTicker(purgeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := s.metrics.Purge(s.archiveRetention)
			if err != nil {
				log.Printf("purge error: %v", err)
				continue
			}
			if count > 0 {
				log.Printf("purged %d archived metric rows older than %s", count, s.archiveRetention)
			}
		}
	}
}

func (s *Scheduler) runSystemCleanup(ctx context.Context) {
	ticker := time.NewTicker(systemCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := s.metrics.PurgeSystemMetrics(s.hotWindow)
			if err != nil {
				log.Printf("system metrics cleanup error: %v", err)
				continue
			}
			if count > 0 {
				log.Printf("purged %d system metric rows older than %s", count, s.hotWindow)
			}
		}
	}
}
