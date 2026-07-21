package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

// DBResetProgressFunc receives status updates during a fast-reset.
type DBResetProgressFunc func(*models.DBResetProgress)

// ResetDBFastBootstrap deletes a node's local chain DB and restarts it with
// --start-in-epoch so it re-bootstraps from the latest epoch. This is the
// deliberate opposite of RestoreDB (which downloads the full-history snapshot):
// here we throw the local DB away and let the node fetch a fresh, epoch-aligned
// state on its own. It is for operators who do NOT need archival history.
//
// Only the db/ subtree is removed — config/, keys and validatorKey.pem are left
// untouched. The empty db/ is recreated with the container user's ownership
// (999:999) so the validator process can write into it; if the directory were
// left for Docker to create it would be owned by root and the node could not
// write to it.
func ResetDBFastBootstrap(ctx context.Context, docker *DockerClient, req *models.ResetDBRequest, onProgress DBResetProgressFunc) error {
	report := func(phase, msg string) {
		if onProgress != nil {
			onProgress(&models.DBResetProgress{
				ContainerName: req.ContainerName,
				Phase:         phase,
				Message:       msg,
			})
		}
	}

	if req.ContainerName == "" {
		return fmt.Errorf("container_name is required")
	}
	if req.DataDir == "" {
		return fmt.Errorf("data_dir is required")
	}
	dbDir := filepath.Join(req.DataDir, "db")

	// --- Stop the node so nothing is writing to the DB while we delete it ---
	report("stopping", "stopping node container")
	if err := docker.StopContainer(ctx, req.ContainerName, 30); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}

	// --- Delete the chain DB (config/keys untouched) ---
	report("clearing", "deleting local chain DB")
	if err := os.RemoveAll(dbDir); err != nil {
		// Try to bring the node back up rather than leave it stopped.
		_ = docker.StartContainer(ctx, req.ContainerName)
		return fmt.Errorf("delete chain db: %w", err)
	}
	// Recreate an empty db/ owned by the container user so the validator can
	// write into it (Docker would otherwise create it as root on start).
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		_ = docker.StartContainer(ctx, req.ContainerName)
		return fmt.Errorf("recreate db dir: %w", err)
	}
	if err := chownRecursive(dbDir, 999, 999); err != nil {
		log.Printf("db-reset %s: chown warning: %v", req.ContainerName, err)
	}

	// --- Recreate with --start-in-epoch and start ---
	// A plain restart would keep whatever command args the container was created
	// with (a main node may have none), so it could try to resume from the empty
	// DB. Recreate with --start-in-epoch so it fast-bootstraps from the latest
	// epoch instead.
	report("starting", "recreating node with fast-bootstrap and starting")
	if err := docker.RecreateWithStartInEpoch(ctx, req.ContainerName); err != nil {
		return fmt.Errorf("recreate/start container after reset: %w", err)
	}

	report("done", "chain DB cleared — node re-bootstrapping from latest epoch")
	return nil
}
