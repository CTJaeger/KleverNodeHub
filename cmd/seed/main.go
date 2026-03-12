// Command seed populates the dashboard database with realistic test data
// for UI development and validation. It creates servers, nodes, system metrics,
// node metrics, alert rules, and alert history — everything except login credentials.
//
// Usage:
//
//	go run ./cmd/seed [--data-dir ~/.klever-node-hub] [--clear]
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func main() {
	dataDir := flag.String("data-dir", defaultDataDir(), "Data directory containing dashboard.db")
	clear := flag.Bool("clear", false, "Clear existing seed data before inserting (preserves auth settings)")
	flag.Parse()

	dbPath := filepath.Join(*dataDir, "dashboard.db")
	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	serverStore := store.NewServerStore(db)
	nodeStore := store.NewNodeStore(db)
	metricsStore := store.NewMetricsStore(db)
	alertStore := store.NewAlertStore(db)

	if *clear {
		log.Println("clearing existing seed data...")
		clearData(db)
	}

	now := time.Now().Unix()

	// --- Servers ---
	servers := []models.Server{
		{
			ID:            "srv-hel-phantom",
			Name:          "Phantom",
			Hostname:      "phantom.klever.farm",
			IPAddress:     "10.10.1.5",
			PublicIP:      "89.167.73.246",
			Region:        "Helsinki, FI",
			OSInfo:        "Ubuntu 22.04.4 LTS (Jammy)",
			AgentVersion:  "0.5.1",
			Status:        "online",
			LastHeartbeat: now - 12,
			RegisteredAt:  now - 86400*92,
			UpdatedAt:     now - 12,
		},
		{
			ID:            "srv-nwk-titan",
			Name:          "Titan",
			Hostname:      "titan.klever.farm",
			IPAddress:     "10.10.2.5",
			PublicIP:      "45.33.32.156",
			Region:        "Newark, US",
			OSInfo:        "Debian 12.7 (Bookworm)",
			AgentVersion:  "0.5.1",
			Status:        "online",
			LastHeartbeat: now - 5,
			RegisteredAt:  now - 86400*78,
			UpdatedAt:     now - 5,
		},
		{
			ID:            "srv-sgp-nebula",
			Name:          "Nebula",
			Hostname:      "nebula.klever.farm",
			IPAddress:     "10.10.3.5",
			PublicIP:      "103.21.244.15",
			Region:        "Singapore, SG",
			OSInfo:        "Ubuntu 24.04 LTS (Noble)",
			AgentVersion:  "0.5.0",
			Status:        "online",
			LastHeartbeat: now - 18,
			RegisteredAt:  now - 86400*63,
			UpdatedAt:     now - 18,
		},
		{
			ID:            "srv-fra-eclipse",
			Name:          "Eclipse",
			Hostname:      "eclipse.klever.farm",
			IPAddress:     "10.10.4.5",
			PublicIP:      "185.199.108.42",
			Region:        "Frankfurt, DE",
			OSInfo:        "Rocky Linux 9.4",
			AgentVersion:  "0.5.1",
			Status:        "online",
			LastHeartbeat: now - 30,
			RegisteredAt:  now - 86400*55,
			UpdatedAt:     now - 30,
		},
		{
			ID:            "srv-sao-aurora",
			Name:          "Aurora",
			Hostname:      "aurora.klever.farm",
			IPAddress:     "10.10.5.5",
			PublicIP:      "177.71.160.89",
			Region:        "Sao Paulo, BR",
			OSInfo:        "Ubuntu 22.04.4 LTS (Jammy)",
			AgentVersion:  "0.4.8",
			Status:        "offline",
			LastHeartbeat: now - 14400,
			RegisteredAt:  now - 86400*120,
			UpdatedAt:     now - 14400,
		},
		{
			ID:            "srv-lon-horizon",
			Name:          "Horizon",
			Hostname:      "horizon.klever.farm",
			IPAddress:     "10.10.6.5",
			PublicIP:      "51.15.42.178",
			Region:        "London, UK",
			OSInfo:        "Debian 12.7 (Bookworm)",
			AgentVersion:  "0.5.1",
			Status:        "online",
			LastHeartbeat: now - 9,
			RegisteredAt:  now - 86400*40,
			UpdatedAt:     now - 9,
		},
	}

	log.Printf("creating %d servers...", len(servers))
	for i := range servers {
		if err := serverStore.Create(&servers[i]); err != nil {
			log.Printf("  server %s: %v (may already exist)", servers[i].ID, err)
		}
	}

	// --- Nodes ---
	type nodeSpec struct {
		node     models.Node
		blsKey   string
		imgTag   string
		nonce    float64
		syncing  float64
		cpuPct   float64
		memUsed  float64
		memLimit float64
	}

	nodeSpecs := []nodeSpec{
		// Phantom (Helsinki) — 3 validators, all healthy
		{node: models.Node{
			ID: "node-phantom-alpha", ServerID: "srv-hel-phantom", Name: "alpha",
			ContainerName: "klever-alpha", NodeType: "validator", RestAPIPort: 8080,
			DisplayName: "Alpha", DataDirectory: "/opt/klever/alpha", Status: "running",
		}, blsKey: fakeBLS("phantom-alpha"), imgTag: "v4.1.0",
			nonce: 29091835, cpuPct: 14.2, memUsed: 1610612736, memLimit: 4294967296},
		{node: models.Node{
			ID: "node-phantom-bravo", ServerID: "srv-hel-phantom", Name: "bravo",
			ContainerName: "klever-bravo", NodeType: "validator", RestAPIPort: 8081,
			DisplayName: "Bravo", DataDirectory: "/opt/klever/bravo", Status: "running",
		}, blsKey: fakeBLS("phantom-bravo"), imgTag: "v4.1.0",
			nonce: 29091830, cpuPct: 9.8, memUsed: 1476395008, memLimit: 4294967296},
		{node: models.Node{
			ID: "node-phantom-charlie", ServerID: "srv-hel-phantom", Name: "charlie",
			ContainerName: "klever-charlie", NodeType: "validator", RestAPIPort: 8082,
			DisplayName: "Charlie", DataDirectory: "/opt/klever/charlie", Status: "running",
		}, blsKey: fakeBLS("phantom-charlie"), imgTag: "v4.1.0",
			nonce: 29091828, cpuPct: 11.5, memUsed: 1543503872, memLimit: 4294967296},

		// Titan (Newark) — 4 validators + 1 observer, one syncing
		{node: models.Node{
			ID: "node-titan-alpha", ServerID: "srv-nwk-titan", Name: "alpha",
			ContainerName: "klever-alpha", NodeType: "validator", RestAPIPort: 8080,
			DisplayName: "Alpha", DataDirectory: "/opt/klever/alpha", Status: "running",
		}, blsKey: fakeBLS("titan-alpha"), imgTag: "v4.1.0",
			nonce: 29091840, cpuPct: 18.3, memUsed: 2362232012, memLimit: 8589934592},
		{node: models.Node{
			ID: "node-titan-bravo", ServerID: "srv-nwk-titan", Name: "bravo",
			ContainerName: "klever-bravo", NodeType: "validator", RestAPIPort: 8081,
			DisplayName: "Bravo", DataDirectory: "/opt/klever/bravo", Status: "running",
		}, blsKey: fakeBLS("titan-bravo"), imgTag: "v4.1.0",
			nonce: 29091838, cpuPct: 13.1, memUsed: 2147483648, memLimit: 8589934592},
		{node: models.Node{
			ID: "node-titan-charlie", ServerID: "srv-nwk-titan", Name: "charlie",
			ContainerName: "klever-charlie", NodeType: "validator", RestAPIPort: 8082,
			DisplayName: "Charlie", DataDirectory: "/opt/klever/charlie", Status: "syncing",
		}, blsKey: fakeBLS("titan-charlie"), imgTag: "v4.1.0",
			nonce: 29089500, syncing: 1, cpuPct: 52.6, memUsed: 3435973836, memLimit: 8589934592},
		{node: models.Node{
			ID: "node-titan-delta", ServerID: "srv-nwk-titan", Name: "delta",
			ContainerName: "klever-delta", NodeType: "validator", RestAPIPort: 8083,
			DisplayName: "Delta", DataDirectory: "/opt/klever/delta", Status: "running",
		}, blsKey: fakeBLS("titan-delta"), imgTag: "v4.0.9",
			nonce: 29091836, cpuPct: 10.9, memUsed: 1932735283, memLimit: 8589934592},
		{node: models.Node{
			ID: "node-titan-observer", ServerID: "srv-nwk-titan", Name: "observer",
			ContainerName: "klever-observer", NodeType: "observer", RedundancyLevel: 1,
			RestAPIPort: 8090, DisplayName: "Observer", DataDirectory: "/opt/klever/observer", Status: "running",
		}, blsKey: "", imgTag: "v4.1.0",
			nonce: 29091835, cpuPct: 4.7, memUsed: 805306368, memLimit: 4294967296},

		// Nebula (Singapore) — 2 validators
		{node: models.Node{
			ID: "node-nebula-alpha", ServerID: "srv-sgp-nebula", Name: "alpha",
			ContainerName: "klever-alpha", NodeType: "validator", RestAPIPort: 8080,
			DisplayName: "Alpha", DataDirectory: "/opt/klever/alpha", Status: "running",
		}, blsKey: fakeBLS("nebula-alpha"), imgTag: "v4.0.9",
			nonce: 29091820, cpuPct: 10.1, memUsed: 1288490189, memLimit: 4294967296},
		{node: models.Node{
			ID: "node-nebula-bravo", ServerID: "srv-sgp-nebula", Name: "bravo",
			ContainerName: "klever-bravo", NodeType: "validator", RestAPIPort: 8081,
			DisplayName: "Bravo", DataDirectory: "/opt/klever/bravo", Status: "running",
		}, blsKey: fakeBLS("nebula-bravo"), imgTag: "v4.0.9",
			nonce: 29091818, cpuPct: 8.6, memUsed: 1181116006, memLimit: 4294967296},

		// Eclipse (Frankfurt) — 2 validators, one stopped for maintenance
		{node: models.Node{
			ID: "node-eclipse-alpha", ServerID: "srv-fra-eclipse", Name: "alpha",
			ContainerName: "klever-alpha", NodeType: "validator", RestAPIPort: 8080,
			DisplayName: "Alpha", DataDirectory: "/opt/klever/alpha", Status: "running",
		}, blsKey: fakeBLS("eclipse-alpha"), imgTag: "v4.1.0",
			nonce: 29091832, cpuPct: 16.4, memUsed: 1717986918, memLimit: 4294967296},
		{node: models.Node{
			ID: "node-eclipse-bravo", ServerID: "srv-fra-eclipse", Name: "bravo",
			ContainerName: "klever-bravo", NodeType: "validator", RestAPIPort: 8081,
			DisplayName: "Bravo", DataDirectory: "/opt/klever/bravo", Status: "stopped",
		}, blsKey: fakeBLS("eclipse-bravo"), imgTag: "v4.0.9",
			nonce: 0, cpuPct: 0, memUsed: 0, memLimit: 4294967296},

		// Aurora (Sao Paulo) — 2 validators, server offline
		{node: models.Node{
			ID: "node-aurora-alpha", ServerID: "srv-sao-aurora", Name: "alpha",
			ContainerName: "klever-alpha", NodeType: "validator", RestAPIPort: 8080,
			DisplayName: "Alpha", DataDirectory: "/opt/klever/alpha", Status: "stopped",
		}, blsKey: fakeBLS("aurora-alpha"), imgTag: "v4.0.7",
			nonce: 0, cpuPct: 0, memUsed: 0, memLimit: 2147483648},
		{node: models.Node{
			ID: "node-aurora-bravo", ServerID: "srv-sao-aurora", Name: "bravo",
			ContainerName: "klever-bravo", NodeType: "validator", RestAPIPort: 8081,
			DisplayName: "Bravo", DataDirectory: "/opt/klever/bravo", Status: "stopped",
		}, blsKey: fakeBLS("aurora-bravo"), imgTag: "v4.0.7",
			nonce: 0, cpuPct: 0, memUsed: 0, memLimit: 2147483648},

		// Horizon (London) — 3 validators + 1 observer
		{node: models.Node{
			ID: "node-horizon-alpha", ServerID: "srv-lon-horizon", Name: "alpha",
			ContainerName: "klever-alpha", NodeType: "validator", RestAPIPort: 8080,
			DisplayName: "Alpha", DataDirectory: "/opt/klever/alpha", Status: "running",
		}, blsKey: fakeBLS("horizon-alpha"), imgTag: "v4.1.0",
			nonce: 29091839, cpuPct: 12.8, memUsed: 1825361100, memLimit: 4294967296},
		{node: models.Node{
			ID: "node-horizon-bravo", ServerID: "srv-lon-horizon", Name: "bravo",
			ContainerName: "klever-bravo", NodeType: "validator", RestAPIPort: 8081,
			DisplayName: "Bravo", DataDirectory: "/opt/klever/bravo", Status: "running",
		}, blsKey: fakeBLS("horizon-bravo"), imgTag: "v4.1.0",
			nonce: 29091837, cpuPct: 10.3, memUsed: 1610612736, memLimit: 4294967296},
		{node: models.Node{
			ID: "node-horizon-charlie", ServerID: "srv-lon-horizon", Name: "charlie",
			ContainerName: "klever-charlie", NodeType: "validator", RestAPIPort: 8082,
			DisplayName: "Charlie", DataDirectory: "/opt/klever/charlie", Status: "running",
		}, blsKey: fakeBLS("horizon-charlie"), imgTag: "v4.1.0",
			nonce: 29091834, cpuPct: 15.1, memUsed: 1932735283, memLimit: 4294967296},
		{node: models.Node{
			ID: "node-horizon-observer", ServerID: "srv-lon-horizon", Name: "observer",
			ContainerName: "klever-observer", NodeType: "observer", RedundancyLevel: 1,
			RestAPIPort: 8090, DisplayName: "Observer", DataDirectory: "/opt/klever/observer", Status: "running",
		}, blsKey: "", imgTag: "v4.1.0",
			nonce: 29091835, cpuPct: 3.9, memUsed: 644245094, memLimit: 2147483648},
	}

	log.Printf("creating %d nodes...", len(nodeSpecs))
	for _, ns := range nodeSpecs {
		n := ns.node
		n.BLSPublicKey = ns.blsKey
		n.DockerImageTag = ns.imgTag
		n.CreatedAt = now - 86400*15
		n.Metadata = map[string]any{
			"cpu_percent": ns.cpuPct,
			"mem_used":    ns.memUsed,
			"mem_limit":   ns.memLimit,
			"mem_percent": safePct(ns.memUsed, ns.memLimit),
		}
		if ns.nonce > 0 {
			n.Metadata["klv_nonce"] = ns.nonce
			n.Metadata["klv_is_syncing"] = ns.syncing
		}
		if err := nodeStore.Create(&n); err != nil {
			log.Printf("  node %s: %v (may already exist)", n.ID, err)
		}
	}

	// --- System Metrics (last 6 hours, every 30s) ---
	log.Println("generating system metrics (last 6 hours)...")
	type serverMetricProfile struct {
		serverID                               string
		baseCPU, baseMem, baseDisk, baseLoad   float64
		memTotal, memUsed, diskTotal, diskUsed uint64
	}
	profiles := []serverMetricProfile{
		// Phantom — 3 validators, moderate load, 8GB/100GB
		{"srv-hel-phantom", 22, 48, 41, 1.1, 8589934592, 4123168604, 107374182400, 44023414784},
		// Titan — 5 nodes (heavy), 16GB/200GB
		{"srv-nwk-titan", 38, 62, 54, 2.3, 17179869184, 10651630182, 214748364800, 115964116992},
		// Nebula — 2 validators, light, 8GB/100GB
		{"srv-sgp-nebula", 15, 36, 28, 0.7, 8589934592, 3092376453, 107374182400, 30064771072},
		// Eclipse — 1 running + 1 stopped, 8GB/100GB
		{"srv-fra-eclipse", 12, 32, 35, 0.5, 8589934592, 2748779069, 107374182400, 37580963840},
		// Aurora — offline, last metrics from 4h ago
		{"srv-sao-aurora", 0, 0, 52, 0, 4294967296, 0, 53687091200, 27917287424},
		// Horizon — 4 nodes, steady, 8GB/100GB
		{"srv-lon-horizon", 26, 52, 38, 1.5, 8589934592, 4456448409, 107374182400, 40802189312},
	}

	metricsStart := now - 6*3600
	for _, p := range profiles {
		for ts := metricsStart; ts <= now; ts += 30 {
			jitter := rand.Float64()*6 - 3 // +/-3%
			cpu := clamp(p.baseCPU+jitter+sinWave(ts, 3600, 5), 0, 100)
			mem := clamp(p.baseMem+jitter*0.5+sinWave(ts, 7200, 3), 0, 100)
			disk := clamp(p.baseDisk+float64(ts-metricsStart)/float64(6*3600)*2, 0, 100)
			load := math.Max(0, p.baseLoad+jitter*0.05)

			memUsed := uint64(float64(p.memTotal) * mem / 100)
			diskUsed := uint64(float64(p.diskTotal) * disk / 100)

			// Aurora: offline for last 4 hours
			if p.serverID == "srv-sao-aurora" && ts > now-14400 {
				cpu = 0
				mem = 0
				load = 0
				memUsed = 0
			}

			row := &store.SystemMetricsRow{
				CPUPercent:  cpu,
				MemPercent:  mem,
				MemTotal:    p.memTotal,
				MemUsed:     memUsed,
				DiskPercent: disk,
				DiskTotal:   p.diskTotal,
				DiskUsed:    diskUsed,
				LoadAvg1:    load,
				CollectedAt: ts,
			}
			if err := metricsStore.InsertSystemMetrics(p.serverID, row); err != nil {
				log.Printf("  system metrics %s @ %d: %v", p.serverID, ts, err)
				break
			}
		}
	}

	// --- Node Metrics (last 6 hours, every 60s) ---
	log.Println("generating node metrics (last 6 hours)...")
	type nodeMetricProfile struct {
		nodeID    string
		serverID  string
		baseNonce float64
		epoch     float64
		cpuLoad   float64
		memLoad   float64
		basePeers int
		syncing   bool
		online    bool
	}
	nodeProfiles := []nodeMetricProfile{
		// Phantom (Helsinki)
		{"node-phantom-alpha", "srv-hel-phantom", 29090000, 5395, 14, 38, 32, false, true},
		{"node-phantom-bravo", "srv-hel-phantom", 29090000, 5395, 10, 34, 28, false, true},
		{"node-phantom-charlie", "srv-hel-phantom", 29090000, 5395, 12, 36, 30, false, true},
		// Titan (Newark)
		{"node-titan-alpha", "srv-nwk-titan", 29090000, 5395, 18, 45, 38, false, true},
		{"node-titan-bravo", "srv-nwk-titan", 29090000, 5395, 13, 40, 35, false, true},
		{"node-titan-charlie", "srv-nwk-titan", 29089000, 5394, 52, 65, 22, true, true},
		{"node-titan-delta", "srv-nwk-titan", 29090000, 5395, 11, 38, 33, false, true},
		{"node-titan-observer", "srv-nwk-titan", 29090000, 5395, 5, 18, 40, false, true},
		// Nebula (Singapore)
		{"node-nebula-alpha", "srv-sgp-nebula", 29090000, 5395, 10, 30, 26, false, true},
		{"node-nebula-bravo", "srv-sgp-nebula", 29090000, 5395, 9, 28, 24, false, true},
		// Eclipse (Frankfurt) — only alpha is running
		{"node-eclipse-alpha", "srv-fra-eclipse", 29090000, 5395, 16, 40, 31, false, true},
		// Horizon (London)
		{"node-horizon-alpha", "srv-lon-horizon", 29090000, 5395, 13, 42, 35, false, true},
		{"node-horizon-bravo", "srv-lon-horizon", 29090000, 5395, 10, 38, 33, false, true},
		{"node-horizon-charlie", "srv-lon-horizon", 29090000, 5395, 15, 45, 30, false, true},
		{"node-horizon-observer", "srv-lon-horizon", 29090000, 5395, 4, 15, 42, false, true},
	}

	for _, np := range nodeProfiles {
		if !np.online {
			continue
		}
		for ts := metricsStart; ts <= now; ts += 60 {
			elapsed := float64(ts - metricsStart)
			nonce := np.baseNonce + elapsed*0.3
			epoch := np.epoch + elapsed/21600
			cpuLoad := clamp(np.cpuLoad+rand.Float64()*4-2+sinWave(ts, 1800, 3), 0, 100)
			memLoad := clamp(np.memLoad+rand.Float64()*6-3+sinWave(ts, 2400, 4), 0, 100)
			peers := float64(np.basePeers + rand.Intn(16) - 8)
			if peers < 3 {
				peers = 3
			}
			txProcessed := math.Floor(nonce * 1.15)
			netRecv := clamp(400000+rand.Float64()*300000+sinWave(ts, 900, 120000), 50000, 2500000)
			netSent := clamp(250000+rand.Float64()*200000+sinWave(ts, 1200, 90000), 30000, 1800000)

			metrics := map[string]float64{
				"klv_nonce":                      math.Floor(nonce),
				"klv_epoch_number":               math.Floor(epoch),
				"klv_cpu_load_percent":           cpuLoad,
				"klv_mem_load_percent":           memLoad,
				"klv_num_connected_peers":        peers,
				"klv_num_transactions_processed": txProcessed,
				"klv_network_recv_bps":           netRecv,
				"klv_network_sent_bps":           netSent,
				"klv_is_syncing":                 0,
			}

			if np.syncing {
				metrics["klv_is_syncing"] = 1
			}

			if err := metricsStore.InsertNodeMetrics(np.nodeID, np.serverID, metrics, ts); err != nil {
				log.Printf("  node metrics %s @ %d: %v", np.nodeID, ts, err)
				break
			}
		}
	}

	// --- Alert Rules ---
	log.Println("creating custom alert rules...")
	rules := []*store.AlertRule{
		{
			ID: "rule-high-cpu", Name: "Sustained High CPU", Enabled: true,
			MetricName: "klv_cpu_load_percent", Condition: "gt", Threshold: 80,
			DurationSec: 300, Severity: "warning", NodeFilter: "*", CooldownMin: 15,
		},
		{
			ID: "rule-disk-critical", Name: "Disk Space Critical", Enabled: true,
			MetricName: "disk_percent", Condition: "gt", Threshold: 90,
			DurationSec: 60, Severity: "critical", NodeFilter: "*", CooldownMin: 30,
		},
		{
			ID: "rule-low-peers", Name: "Low Peer Count", Enabled: true,
			MetricName: "klv_num_connected_peers", Condition: "lt", Threshold: 10,
			DurationSec: 600, Severity: "warning", NodeFilter: "*", CooldownMin: 60,
		},
		{
			ID: "rule-mem-warning", Name: "Memory Usage Warning", Enabled: true,
			MetricName: "klv_mem_load_percent", Condition: "gt", Threshold: 85,
			DurationSec: 180, Severity: "warning", NodeFilter: "*", CooldownMin: 20,
		},
	}
	for _, r := range rules {
		if err := alertStore.CreateRule(r); err != nil {
			log.Printf("  rule %s: %v (may already exist)", r.ID, err)
		}
	}

	// --- Alert History ---
	log.Println("creating alert history...")
	alerts := []*store.AlertRecord{
		{
			ID: "alert-001", RuleID: "rule-high-cpu", RuleName: "Sustained High CPU",
			NodeID: "node-titan-charlie", ServerID: "srv-nwk-titan",
			Severity: "warning", State: "firing",
			Message: "klv_cpu_load_percent = 87.3% on Titan/Charlie (threshold: 80%)",
			FiredAt: now - 1800, CreatedAt: now - 1800,
		},
		{
			ID: "alert-002", RuleID: "rule-high-cpu", RuleName: "Sustained High CPU",
			NodeID: "node-titan-alpha", ServerID: "srv-nwk-titan",
			Severity: "warning", State: "resolved",
			Message: "klv_cpu_load_percent = 82.1% on Titan/Alpha (threshold: 80%)",
			FiredAt: now - 86400, ResolvedAt: now - 85200,
			CreatedAt: now - 86400,
		},
		{
			ID: "alert-003", RuleID: "rule-disk-critical", RuleName: "Disk Space Critical",
			NodeID: "", ServerID: "srv-nwk-titan",
			Severity: "critical", State: "resolved",
			Message: "disk_percent = 91.2% on Titan (threshold: 90%)",
			FiredAt: now - 172800, ResolvedAt: now - 172000,
			CreatedAt: now - 172800,
		},
		{
			ID: "alert-004", RuleID: "rule-low-peers", RuleName: "Low Peer Count",
			NodeID: "node-nebula-bravo", ServerID: "srv-sgp-nebula",
			Severity: "warning", State: "resolved",
			Message: "klv_num_connected_peers = 5 on Nebula/Bravo (threshold: 10)",
			FiredAt: now - 259200, ResolvedAt: now - 258000,
			CreatedAt: now - 259200,
		},
		{
			ID: "alert-005", RuleID: "builtin-nonce-stall", RuleName: "Nonce Stall",
			NodeID: "node-aurora-alpha", ServerID: "srv-sao-aurora",
			Severity: "critical", State: "resolved",
			Message: "nonce stalled at 29088000 for 600s on Aurora/Alpha",
			FiredAt: now - 14400, ResolvedAt: now - 13800,
			CreatedAt: now - 14400,
		},
		{
			ID: "alert-006", RuleID: "rule-mem-warning", RuleName: "Memory Usage Warning",
			NodeID: "node-titan-charlie", ServerID: "srv-nwk-titan",
			Severity: "warning", State: "firing",
			Message: "klv_mem_load_percent = 88.5% on Titan/Charlie (threshold: 85%)",
			FiredAt: now - 900, CreatedAt: now - 900,
		},
		{
			ID: "alert-007", RuleID: "rule-high-cpu", RuleName: "Sustained High CPU",
			NodeID: "node-horizon-charlie", ServerID: "srv-lon-horizon",
			Severity: "warning", State: "resolved",
			Message: "klv_cpu_load_percent = 81.7% on Horizon/Charlie (threshold: 80%)",
			FiredAt: now - 43200, ResolvedAt: now - 42600,
			CreatedAt: now - 43200,
		},
		{
			ID: "alert-008", RuleID: "rule-low-peers", RuleName: "Low Peer Count",
			NodeID: "node-eclipse-alpha", ServerID: "srv-fra-eclipse",
			Severity: "warning", State: "resolved",
			Message: "klv_num_connected_peers = 7 on Eclipse/Alpha (threshold: 10)",
			FiredAt: now - 345600, ResolvedAt: now - 344400,
			CreatedAt: now - 345600,
		},
	}
	for _, a := range alerts {
		if err := alertStore.CreateAlert(a); err != nil {
			log.Printf("  alert %s: %v (may already exist)", a.ID, err)
		}
	}

	log.Println("seed complete!")
	log.Printf("  %d servers", len(servers))
	log.Printf("  %d nodes", len(nodeSpecs))
	log.Printf("  ~%d system metric points per server (6h @ 30s)", 6*3600/30)
	log.Printf("  ~%d node metric points per node (6h @ 60s)", 6*3600/60)
	log.Printf("  %d custom alert rules", len(rules))
	log.Printf("  %d alert history entries", len(alerts))
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".klever-node-hub"
	}
	return filepath.Join(home, ".klever-node-hub")
}

func clearData(db *store.DB) {
	tables := []string{
		"alerts", "alert_rules", "metrics_recent", "metrics_archive",
		"system_metrics", "nodes", "servers",
	}
	for _, t := range tables {
		if _, err := db.SQL().Exec("DELETE FROM " + t); err != nil {
			log.Printf("  clear %s: %v", t, err)
		}
	}
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func sinWave(ts int64, periodSec int, amplitude float64) float64 {
	return amplitude * math.Sin(2*math.Pi*float64(ts)/float64(periodSec))
}

func safePct(used, limit float64) float64 {
	if limit == 0 {
		return 0
	}
	return used / limit * 100
}

func fakeBLS(seed string) string {
	h := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%x", h)
}
