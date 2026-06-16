package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// dbBackupSources maps a network to the official Klever FullNode chain-DB
// snapshot. These are the same archives documented by Klever
// (`curl -k <url> | tar -xz -C ./node`) and are tens of GB.
var dbBackupSources = map[string]string{
	"mainnet": "https://backup.mainnet.klever.org/kleverchain.mainnet.latest.tar.gz",
	"testnet": "https://backup.testnet.klever.org/kleverchain.testnet.latest.tar.gz",
}

// extractedSizeFactor is how much larger we assume the extracted DB is versus
// the compressed archive. Blockchain DBs compress modestly; 2x is a safe
// preflight margin so we never start a restore that can't fit.
const extractedSizeFactor = 2

// DBRestoreProgressFunc receives progress updates during a restore.
type DBRestoreProgressFunc func(p *models.DBRestoreProgress)

// RestoreDB replaces a node's chain DB with the official Klever FullNode
// snapshot. Flow: preflight disk check -> stop node -> rotate old db aside
// (or delete if space is tight) -> stream-download + extract only the db/
// entries -> fix permissions -> start node. On failure before the new DB is
// in place, the old DB is rolled back.
//
// It streams the archive straight through gzip+tar (never staging the tens of
// GB on disk) and only extracts entries under db/, so a stray config/ in the
// archive can't clobber the node's existing configuration.
func RestoreDB(ctx context.Context, docker *DockerClient, req *models.RestoreDBRequest, onProgress DBRestoreProgressFunc) error {
	report := func(phase string, pct int, msg string) {
		if onProgress != nil {
			onProgress(&models.DBRestoreProgress{
				ContainerName: req.ContainerName,
				Phase:         phase,
				Percent:       pct,
				Message:       msg,
			})
		}
	}

	url, ok := dbBackupSources[req.Network]
	if !ok {
		url = dbBackupSources["mainnet"]
	}
	if req.DataDir == "" {
		return fmt.Errorf("data_dir is required")
	}
	dbDir := filepath.Join(req.DataDir, "db")
	oldDir := filepath.Join(req.DataDir, "db.old")

	// --- Preflight: how big is the archive, and do we have room? ---
	report("preflight", 0, "checking archive size and free disk space")
	archiveSize, err := remoteContentLength(ctx, url)
	if err != nil {
		return fmt.Errorf("preflight: cannot reach backup server: %w", err)
	}
	needExtracted := archiveSize * extractedSizeFactor
	free, err := freeDiskSpace(req.DataDir)
	if err != nil {
		return fmt.Errorf("preflight: cannot check free space on %s: %w", req.DataDir, err)
	}
	oldSize := dirSize(dbDir) // 0 if it doesn't exist

	// Decide rotation strategy: keep the old DB if both fit (safe rollback),
	// otherwise delete it first to make room (best-effort, no rollback).
	keepOld := free >= needExtracted+oldSize
	if !keepOld && free < needExtracted {
		return fmt.Errorf("preflight: not enough disk space — need ~%s for the extracted DB but only %s is free on %s",
			humanBytes(needExtracted), humanBytes(free), req.DataDir)
	}
	log.Printf("db-restore %s: archive=%s extracted~%s free=%s oldDB=%s keepOld=%v",
		req.ContainerName, humanBytes(archiveSize), humanBytes(needExtracted), humanBytes(free), humanBytes(oldSize), keepOld)

	// --- Stop the node ---
	report("stopping", 0, "stopping node container")
	if err := docker.StopContainer(ctx, req.ContainerName, 30); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}

	// --- Rotate the old DB out of the way ---
	_ = os.RemoveAll(oldDir) // clear any leftover from a previous run
	rolledBack := false
	if oldSize > 0 {
		if keepOld {
			if err := os.Rename(dbDir, oldDir); err != nil {
				_ = docker.StartContainer(ctx, req.ContainerName)
				return fmt.Errorf("rotate old db aside: %w", err)
			}
		} else {
			report("preflight", 0, "removing old DB to free space (no rollback possible)")
			if err := os.RemoveAll(dbDir); err != nil {
				_ = docker.StartContainer(ctx, req.ContainerName)
				return fmt.Errorf("remove old db: %w", err)
			}
		}
	}
	// rollbackOldDB restores the previous DB if we kept it and something fails.
	rollbackOldDB := func() {
		if keepOld && oldSize > 0 && !rolledBack {
			_ = os.RemoveAll(dbDir)
			_ = os.Rename(oldDir, dbDir)
			rolledBack = true
		}
	}

	if err := os.MkdirAll(dbDir, 0755); err != nil {
		rollbackOldDB()
		_ = docker.StartContainer(ctx, req.ContainerName)
		return fmt.Errorf("create db dir: %w", err)
	}

	// --- Download + extract only db/ entries ---
	report("downloading", 0, fmt.Sprintf("downloading %s snapshot (%s)", req.Network, humanBytes(archiveSize)))
	if err := downloadAndExtractDB(ctx, url, req.DataDir, archiveSize, func(pct int) {
		report("downloading", pct, "")
	}); err != nil {
		rollbackOldDB()
		_ = docker.StartContainer(ctx, req.ContainerName)
		return fmt.Errorf("download/extract db: %w", err)
	}

	// --- Permissions to match the container user ---
	report("permissions", 100, "setting ownership")
	if err := chownRecursive(dbDir, 999, 999); err != nil {
		log.Printf("db-restore %s: chown warning: %v", req.ContainerName, err)
	}

	// --- Start the node back up ---
	report("starting", 100, "starting node container")
	if err := docker.StartContainer(ctx, req.ContainerName); err != nil {
		return fmt.Errorf("start container after restore: %w", err)
	}

	// Success — drop the kept-aside old DB.
	if keepOld && oldSize > 0 {
		_ = os.RemoveAll(oldDir)
	}
	report("done", 100, "chain DB restored")
	return nil
}

// downloadAndExtractDB streams the gzip+tar archive and writes only entries
// under db/ into destDir, reporting download progress against totalBytes.
func downloadAndExtractDB(ctx context.Context, url, destDir string, totalBytes uint64, onPct func(int)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	// Count bytes off the wire (the compressed stream) for progress.
	counted := &countingReader{r: resp.Body, total: totalBytes, onPct: onPct}
	if err := extractDBStream(counted, destDir); err != nil {
		return err
	}
	onPct(100)
	return nil
}

// extractDBStream reads a gzip+tar stream and writes only entries under db/
// into destDir. A stray config/ or wallet/ in the snapshot is ignored so a
// restore never overwrites live configuration or keys. Returns an error if the
// archive contains no db/ entries (unexpected layout).
func extractDBStream(r io.Reader, destDir string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gr.Close() }()

	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("resolve dest: %w", err)
	}

	tr := tar.NewReader(gr)
	var extractedAny bool
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// Only restore the db/ subtree. The official archive extracts into the
		// node dir (-C ./node) and contains db/...; anything else (e.g. config)
		// is intentionally ignored so we never overwrite live configuration.
		name := filepath.Clean(header.Name)
		if name != "db" && !strings.HasPrefix(name, "db"+string(filepath.Separator)) {
			continue
		}

		target := filepath.Join(destDir, name)
		// Traversal guard: resolved target must stay inside destDir.
		absTarget, err := filepath.Abs(target)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(absDest, absTarget)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", target, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil { //nolint:gosec // size bounded by remote archive, streamed to disk
				_ = f.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			_ = f.Close()
			extractedAny = true
		}
	}
	if !extractedAny {
		return fmt.Errorf("archive contained no db/ entries — unexpected snapshot layout")
	}
	return nil
}

// remoteContentLength does a HEAD request to learn the archive size for the
// preflight check. Falls back to a GET if HEAD isn't supported.
func remoteContentLength(ctx context.Context, url string) (uint64, error) {
	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK && resp.ContentLength > 0 {
		return uint64(resp.ContentLength), nil
	}
	// Fall back to a ranged GET to read Content-Range / Content-Length.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	getReq.Header.Set("Range", "bytes=0-0")
	gResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		return 0, err
	}
	defer func() { _ = gResp.Body.Close() }()
	if cr := gResp.Header.Get("Content-Range"); cr != "" {
		// Format: "bytes 0-0/123456789"
		if idx := strings.LastIndex(cr, "/"); idx >= 0 {
			var total uint64
			if _, err := fmt.Sscanf(cr[idx+1:], "%d", &total); err == nil && total > 0 {
				return total, nil
			}
		}
	}
	return 0, fmt.Errorf("could not determine archive size")
}

// countingReader reports download progress as a percentage of total.
type countingReader struct {
	r        io.Reader
	total    uint64
	read     uint64
	lastPct  int
	lastEmit time.Time
	onPct    func(int)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += uint64(n)
	if c.total > 0 && c.onPct != nil {
		pct := int(c.read * 100 / c.total)
		if pct > 100 {
			pct = 100
		}
		// Emit at most ~once per second and only on change, to avoid flooding.
		if pct != c.lastPct && time.Since(c.lastEmit) > time.Second {
			c.lastPct = pct
			c.lastEmit = time.Now()
			c.onPct(pct)
		}
	}
	return n, err
}

// dirSize returns the total size of a directory tree, or 0 if it doesn't exist.
func dirSize(dir string) uint64 {
	var total uint64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += uint64(info.Size())
		}
		return nil
	})
	return total
}

// humanBytes formats a byte count as a human-readable string.
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
