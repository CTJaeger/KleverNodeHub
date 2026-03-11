//go:build !windows

package agent

import (
	"syscall"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// collectDisk uses syscall.Statfs to check disk usage on Unix systems.
func (mc *MetricsCollector) collectDisk(m *models.SystemMetrics) {
	for _, path := range mc.diskPaths {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil {
			continue
		}

		total := stat.Blocks * uint64(stat.Bsize)
		free := stat.Bavail * uint64(stat.Bsize)
		used := total - free

		// Use the largest disk found
		if total > m.DiskTotal {
			m.DiskTotal = total
			m.DiskUsed = used
			if total > 0 {
				m.DiskPercent = float64(used) / float64(total) * 100.0
			}
		}
	}
}
