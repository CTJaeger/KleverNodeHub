package alerting

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/notify"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// Regression detector tuning. These are deliberately conservative: it is
// far better to stay silent than to spam node operators with a warning
// they cannot act on. A version change is judged at most once.
const (
	// regressionMetric is the Klever node metric we compare before/after.
	regressionMetric = "klv_block_process_duration_ms"

	// regressionCheckInterval is how often the detector scans for pending
	// version changes to evaluate.
	regressionCheckInterval = 15 * time.Minute

	// regressionMinAgeSec: a version change must be at least this old before
	// we judge it — gives the new version time to settle past initial sync
	// and cold-start noise.
	regressionMinAgeSec = 12 * 3600

	// regressionMaxAgeSec: changes older than this are never evaluated (e.g.
	// after a long dashboard downtime) — the hot metrics are likely gone.
	regressionMaxAgeSec = 7 * 24 * 3600

	// regressionBaselineWindowSec: how far back before the change we sample
	// the baseline performance.
	regressionBaselineWindowSec = 24 * 3600

	// regressionMinPoints: minimum data points required in *each* window for
	// a meaningful median. Below this, we skip and wait for more data.
	regressionMinPoints = 20

	// regressionRatio / regressionAbsMs: a regression is only flagged when
	// the post-upgrade median is both >=1.5x the baseline AND at least
	// 30ms higher in absolute terms — so 2ms->3ms never alarms.
	regressionRatio = 1.5
	regressionAbsMs = 30.0
)

// RegressionDetector watches for node version changes and, once enough
// post-upgrade data has accumulated, compares block-processing performance
// before and after the change. If the new version is meaningfully slower it
// raises a single warning alert. It never repeats: each version change is
// marked evaluated after it has been judged.
type RegressionDetector struct {
	versionStore *store.VersionHistoryStore
	metricsStore *store.MetricsStore
	nodeStore    *store.NodeStore
	alertStore   *store.AlertStore
	notifier     *notify.Manager
	cancel       context.CancelFunc
	interval     time.Duration
	idCounter    int64
}

// NewRegressionDetector creates a new RegressionDetector.
func NewRegressionDetector(
	versionStore *store.VersionHistoryStore,
	metricsStore *store.MetricsStore,
	nodeStore *store.NodeStore,
	alertStore *store.AlertStore,
	notifier *notify.Manager,
) *RegressionDetector {
	return &RegressionDetector{
		versionStore: versionStore,
		metricsStore: metricsStore,
		nodeStore:    nodeStore,
		alertStore:   alertStore,
		notifier:     notifier,
		interval:     regressionCheckInterval,
	}
}

// Start launches the detection loop.
func (d *RegressionDetector) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	go d.run(ctx)
	log.Printf("regression detector started (interval=%s)", d.interval)
}

// Stop halts the detection loop.
func (d *RegressionDetector) Stop() {
	if d.cancel != nil {
		d.cancel()
		log.Println("regression detector stopped")
	}
}

func (d *RegressionDetector) run(ctx context.Context) {
	// Initial check after a short delay so the dashboard finishes startup.
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	d.check()

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.check()
		}
	}
}

// check evaluates all pending version changes that are old enough to judge.
func (d *RegressionDetector) check() {
	now := time.Now().Unix()
	pending, err := d.versionStore.PendingEvaluations(now, regressionMinAgeSec, regressionMaxAgeSec)
	if err != nil {
		log.Printf("regression detector: list pending: %v", err)
		return
	}

	for _, change := range pending {
		d.evaluateChange(change, now)
	}
}

// evaluateChange judges a single version change. It marks the change
// evaluated once a verdict is reached; if there is not yet enough data it
// leaves it pending so a later run can try again.
func (d *RegressionDetector) evaluateChange(change store.VersionChange, now int64) {
	baselineFrom := change.DetectedAt - regressionBaselineWindowSec
	baselineTo := change.DetectedAt
	postFrom := change.DetectedAt
	postTo := now

	baseline, err := d.metricsStore.QueryRecent(change.NodeID, regressionMetric, baselineFrom, baselineTo)
	if err != nil {
		log.Printf("regression detector: query baseline for %s: %v", change.NodeID, err)
		return
	}
	post, err := d.metricsStore.QueryRecent(change.NodeID, regressionMetric, postFrom, postTo)
	if err != nil {
		log.Printf("regression detector: query post for %s: %v", change.NodeID, err)
		return
	}

	// Not enough data yet — leave pending, a later run will retry until the
	// change ages out of the maxAge window.
	if len(baseline) < regressionMinPoints || len(post) < regressionMinPoints {
		return
	}

	baselineMedian := median(baseline)
	postMedian := median(post)

	regressed := postMedian >= baselineMedian*regressionRatio &&
		(postMedian-baselineMedian) >= regressionAbsMs

	if regressed {
		d.raiseAlert(change, baselineMedian, postMedian)
	} else {
		log.Printf("regression detector: node %s upgrade to %s OK (median %.0fms -> %.0fms)",
			change.NodeID, change.Version, baselineMedian, postMedian)
	}

	// Verdict reached — never judge this change again.
	if err := d.versionStore.MarkEvaluated(change.ID); err != nil {
		log.Printf("regression detector: mark evaluated %d: %v", change.ID, err)
	}
}

// raiseAlert creates a single warning alert for a confirmed regression.
func (d *RegressionDetector) raiseAlert(change store.VersionChange, baselineMedian, postMedian float64) {
	now := time.Now()

	nodeName := change.NodeID
	prevVersion := ""
	if node, err := d.nodeStore.GetByID(change.NodeID); err == nil && node != nil {
		if node.DisplayName != "" {
			nodeName = node.DisplayName
		} else if node.ContainerName != "" {
			nodeName = node.ContainerName
		}
	}
	if prev, err := d.versionStore.PreviousChange(change); err == nil && prev != nil {
		prevVersion = prev.Version
	}

	pctChange := 0.0
	if baselineMedian > 0 {
		pctChange = (postMedian - baselineMedian) / baselineMedian * 100
	}

	var msg string
	if prevVersion != "" {
		msg = fmt.Sprintf(
			"node:%s — block processing median rose from %.0fms to %.0fms (+%.0f%%) after upgrade %s -> %s",
			nodeName, baselineMedian, postMedian, pctChange, prevVersion, change.Version)
	} else {
		msg = fmt.Sprintf(
			"node:%s — block processing median rose from %.0fms to %.0fms (+%.0f%%) after upgrade to %s",
			nodeName, baselineMedian, postMedian, pctChange, change.Version)
	}

	d.idCounter++
	record := &store.AlertRecord{
		ID:         fmt.Sprintf("regression-%d-%d", now.UnixNano(), d.idCounter),
		RuleID:     "builtin-version-regression",
		RuleName:   "Version Performance Regression",
		NodeID:     change.NodeID,
		ServerID:   change.ServerID,
		Severity:   notify.SeverityWarning,
		State:      "firing",
		Message:    msg,
		FiredAt:    now.Unix(),
		NotifiedAt: now.Unix(),
		CreatedAt:  now.Unix(),
	}
	if err := d.alertStore.CreateAlert(record); err != nil {
		log.Printf("regression detector: create alert: %v", err)
		return
	}

	d.notifier.Send(&notify.Alert{
		Title:     "warning: Version Performance Regression",
		Message:   msg,
		Severity:  notify.SeverityWarning,
		Source:    fmt.Sprintf("node:%s", nodeName),
		AlertType: "version",
		Time:      now.Unix(),
	})

	log.Printf("regression detector: ALERT — %s", msg)
}

// median returns the 50th-percentile value of the data points. Using the
// median (not the mean) means a handful of cold-start spikes barely move
// the result.
func median(points []store.DataPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.Value
	}
	sort.Float64s(values)
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}
