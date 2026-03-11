//go:build windows

package agent

import (
	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// collectDisk is a no-op on Windows (syscall.Statfs not available).
// Validators run on Linux; Windows support is for development only.
func (mc *MetricsCollector) collectDisk(m *models.SystemMetrics) {
	// No-op on Windows
}
