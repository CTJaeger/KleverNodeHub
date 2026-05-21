package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/CTJaeger/KleverNodeHub/internal/agent"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

const (
	heartbeatInterval   = 30 * time.Second
	discoveryInterval   = 30 * time.Second
	nodeMetricsInterval = 15 * time.Second
	reconnectBaseDelay  = 1 * time.Second
	reconnectMaxDelay   = 60 * time.Second

	// How often to re-detect the agent's public IP. The dashboard updates the
	// stored IP from any heartbeat that carries a changed value, so this only
	// affects how stale a recently-changed IP (DHCP renewal, VPN flap) can be.
	publicIPRefreshInterval = 15 * time.Minute

	// Maximum time to wait for queued result/progress messages to flush before
	// restartAgent() exec's the new binary, so the dashboard sees the outcome
	// of the agent.update / agent.restart command it just sent.
	restartDrainTimeout = 3 * time.Second

	// Application-level keepalive. Detects half-open connections that TCP
	// alone wouldn't notice (NAT idle timeout, silent intermediary drop).
	pingInterval = 25 * time.Second
	pingTimeout  = 10 * time.Second

	// If a connection held for at least this long, treat the next disconnect
	// as a fresh failure and reset the reconnect backoff to the base delay.
	connStableThreshold = 5 * time.Minute

	// Discovery slower than this gets logged at WARN level so a busy Docker
	// daemon is visible in agent logs.
	discoverySlowThreshold = 5 * time.Second
)

func main() {
	info := version.Get()
	fmt.Printf("Klever Node Hub - Agent %s (%s)\n", info.Version, info.GitCommit)

	// CLI flags
	configDir := flag.String("config-dir", defaultConfigDir(), "Config directory")
	dashboardURL := flag.String("dashboard-url", "", "Dashboard URL for registration (e.g. https://192.168.1.10:9443)")
	registerToken := flag.String("register-token", "", "One-time registration token")
	dockerSocket := flag.String("docker-socket", "/var/run/docker.sock", "Docker socket path")
	flag.Parse()

	// Ensure config directory exists
	if err := os.MkdirAll(*configDir, 0700); err != nil {
		log.Fatalf("create config dir: %v", err)
	}

	// --- Agent config ---
	ag := agent.New(*configDir)
	if err := ag.LoadConfig(); err != nil {
		// Not registered yet
		if *registerToken == "" || *dashboardURL == "" {
			log.Fatal("agent not registered. Use --dashboard-url and --register-token to register")
		}

		log.Printf("registering with dashboard at %s...", *dashboardURL)
		if err := ag.Register(*dashboardURL, *registerToken); err != nil {
			log.Fatalf("registration failed: %v", err)
		}
		log.Println("registration successful")
	}

	config := ag.Config()
	if config == nil || config.ServerID == "" {
		log.Fatal("invalid agent config: missing server ID")
	}
	log.Printf("server ID: %s", config.ServerID)
	log.Printf("dashboard: %s", config.DashboardURL)

	// --- Executor ---
	executor := agent.NewExecutor(*dockerSocket)

	// --- Metrics collector ---
	metricsCollector := agent.NewMetricsCollector(nil)

	// --- Node metrics collector ---
	nodeMetrics := agent.NewNodeMetricsCollector(
		agent.WithPollInterval(nodeMetricsInterval),
	)

	// --- Graceful shutdown ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("received %s, shutting down...", sig)
		cancel()
	}()

	// --- Detect public IP ---
	// Stored in an atomic so the refresher goroutine and the per-connection
	// goroutines below can update / read it without a mutex.
	var publicIPAtomic atomic.Value
	publicIPAtomic.Store(agent.DetectPublicIP(ctx))
	if ip := publicIPAtomic.Load().(string); ip != "" {
		log.Printf("public IP: %s", ip)
	}

	// Refresh the public IP periodically so a DHCP renewal or VPN flap
	// surfaces in the next heartbeat rather than waiting for a restart.
	go refreshPublicIP(ctx, &publicIPAtomic, publicIPRefreshInterval)

	publicIPGetter := func() string {
		v, _ := publicIPAtomic.Load().(string)
		return v
	}

	// --- WebSocket connection loop with auto-reconnect ---
	wsURL := buildWSURL(config.DashboardURL, config.ServerID)
	delay := reconnectBaseDelay

	for ctx.Err() == nil {

		log.Printf("connecting to %s...", wsURL)
		connectStart := time.Now()
		err := runAgentLoop(ctx, wsURL, ag, executor, metricsCollector, nodeMetrics, *dockerSocket, publicIPGetter)
		connectedFor := time.Since(connectStart)
		if ctx.Err() != nil {
			break
		}

		// A connection that held for a meaningful duration shouldn't be
		// punished by the previous failure's backoff state. Reset so a
		// late-life disconnect reconnects quickly.
		if connectedFor >= connStableThreshold {
			delay = reconnectBaseDelay
		}

		log.Printf("connection lost after %s: %v — reconnecting in %s",
			connectedFor.Round(time.Second), err, delay)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}

		// Exponential backoff
		delay = delay * 2
		if delay > reconnectMaxDelay {
			delay = reconnectMaxDelay
		}
	}

	log.Println("agent stopped")
}

// runAgentLoop connects to the dashboard and runs the message pump until disconnected.
//
// publicIPGetter is invoked when each outbound message is built so a public-IP
// change during the lifetime of the connection is picked up on the next
// heartbeat without needing to drop and reconnect.
func runAgentLoop(ctx context.Context, wsURL string, ag *agent.Agent, executor *agent.Executor, metrics *agent.MetricsCollector, nodeMetrics *agent.NodeMetricsCollector, dockerSocket string, publicIPGetter func() string) error {
	// Create a loop-scoped context so all goroutines (poller, sender) stop on disconnect
	loopCtx, loopCancel := context.WithCancel(ctx)
	defer loopCancel()

	dialCtx, dialCancel := context.WithTimeout(loopCtx, 15*time.Second)
	defer dialCancel()

	tlsConfig, err := ag.TLSConfig()
	if err != nil {
		return fmt.Errorf("TLS config: %w", err)
	}

	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "closing") }()
	conn.SetReadLimit(50 << 20) // 50 MB — agent binaries are ~10-15 MB base64-encoded

	log.Println("connected to dashboard")

	// Send agent info on connect
	infoMsg := ag.BuildInfoMessage(publicIPGetter())
	if err := writeMessage(loopCtx, conn, infoMsg); err != nil {
		return fmt.Errorf("send agent info: %w", err)
	}

	// Heartbeat ticker
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	// Ping ticker — application-level keepalive so half-open connections
	// are detected within ~pingInterval rather than waiting for the next
	// outbound message to fail.
	pingTicker := time.NewTicker(pingInterval)
	defer pingTicker.Stop()

	// Result channel for async command execution
	resultCh := make(chan *models.Message, 16)

	// Progress events channel — used by long-running commands (benchmark)
	progressCh := make(chan *models.Message, 16)

	// Discovery trigger channel (e.g. after provisioning)
	discoverNow := make(chan struct{}, 1)

	// Discovery message channel — produced by runDiscoveryLoop, drained by the writer.
	// Buffered so a slow writer can't backpressure the discovery goroutine and stall
	// it inside the Docker call.
	discoveryMsgCh := make(chan *models.Message, 4)

	// Node metrics channels
	nodeMetricsCh := make(chan *models.Message, 32)
	nodeStallCh := make(chan *models.Message, 8)

	// Start node metrics poller (uses loopCtx so it stops on disconnect)
	go nodeMetrics.RunPoller(loopCtx, ag.Config().ServerID, nodeMetricsCh, nodeStallCh)

	// Discovery is the agent's most expensive operation (up to 30s of Docker
	// inspect/stats calls per cycle). Run it on its own goroutine so a slow
	// Docker daemon can never block heartbeats — the writer just drains the
	// finished message off discoveryMsgCh like any other event.
	go runDiscoveryLoop(loopCtx, ag, nodeMetrics, dockerSocket, discoverNow, discoveryMsgCh)

	// Heartbeat + discovery dispatch + node metrics writer
	go func() {
		// Cancelling loopCtx on writer exit propagates the disconnect to the
		// read loop and to all the other goroutines (poller, discovery).
		defer loopCancel()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-heartbeatTicker.C:
				hbStart := time.Now()
				hb := &models.Message{
					ID:     fmt.Sprintf("hb-%d", time.Now().UnixNano()),
					Type:   "event",
					Action: "agent.heartbeat",
					Payload: &models.HeartbeatPayload{
						Timestamp: time.Now().Unix(),
						Metrics:   metrics.Collect(),
						PublicIP:  publicIPGetter(),
					},
					Timestamp: time.Now().Unix(),
				}
				if err := writeMessage(loopCtx, conn, hb); err != nil {
					log.Printf("send heartbeat: %v", err)
					return
				}
				if elapsed := time.Since(hbStart); elapsed > 2*time.Second {
					log.Printf("heartbeat write took %s (slow link?)", elapsed.Round(time.Millisecond))
				}
			case <-pingTicker.C:
				pingCtx, cancel := context.WithTimeout(loopCtx, pingTimeout)
				err := conn.Ping(pingCtx)
				cancel()
				if err != nil {
					log.Printf("ping failed: %v — closing connection", err)
					return
				}
			case msg := <-discoveryMsgCh:
				if err := writeMessage(loopCtx, conn, msg); err != nil {
					log.Printf("send discovery: %v", err)
					return
				}
			case msg := <-nodeMetricsCh:
				if err := writeMessage(loopCtx, conn, msg); err != nil {
					log.Printf("send node metrics: %v", err)
					return
				}
			case msg := <-nodeStallCh:
				log.Printf("ALERT: nonce stall detected — %s", msg.ID)
				if err := writeMessage(loopCtx, conn, msg); err != nil {
					log.Printf("send stall alert: %v", err)
					return
				}
			case msg := <-progressCh:
				if err := writeMessage(loopCtx, conn, msg); err != nil {
					log.Printf("send progress: %v", err)
					return
				}
			case msg := <-resultCh:
				if err := writeMessage(loopCtx, conn, msg); err != nil {
					log.Printf("send result: %v", err)
					return
				}
			}
		}
	}()

	// Read loop: receive commands from dashboard
	for {
		_, data, err := conn.Read(loopCtx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var msg models.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("invalid message: %v", err)
			continue
		}

		if msg.Type == "command" {
			// Per-command progress callback — sends events to dashboard.
			// Defined per-command (not as an Executor field) so concurrent
			// commands can't race when assigning the callback.
			onProgress := func(action string, payload map[string]any) {
				progressMsg := &models.Message{
					ID:        fmt.Sprintf("progress-%d", time.Now().UnixNano()),
					Type:      "event",
					Action:    action,
					Payload:   payload,
					Timestamp: time.Now().Unix(),
				}
				select {
				case progressCh <- progressMsg:
				default:
				}
			}
			// Execute command asynchronously
			go func(m models.Message) {
				result := executor.Execute(&m, onProgress)
				resultMsg := agent.BuildResultMessage(result)
				select {
				case resultCh <- resultMsg:
				case <-loopCtx.Done():
				}
				// Trigger immediate discovery after provisioning
				if m.Action == "node.provision" && result.Success {
					select {
					case discoverNow <- struct{}{}:
					default:
					}
				}
				// Self-restart after a successful agent update or an explicit restart command.
				// Wait for the queued result (and any in-flight progress events) to
				// actually leave the socket before exec'ing the new binary — otherwise
				// the dashboard never sees the outcome of the command that triggered
				// the restart.
				if (m.Action == "agent.update" || m.Action == "agent.restart") && result.Success {
					drainBeforeRestart(resultCh, progressCh, restartDrainTimeout)
					restartAgent()
				}
			}(msg)
		}
	}
}

// refreshPublicIP periodically re-detects the agent's public IP and updates
// the supplied atomic. Logs only when the value actually changes, so a
// stable network produces no noise.
func refreshPublicIP(ctx context.Context, store *atomic.Value, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ip := agent.DetectPublicIP(ctx)
			if ip == "" {
				continue
			}
			old, _ := store.Load().(string)
			if ip != old {
				log.Printf("public IP changed: %q -> %q", old, ip)
				store.Store(ip)
			}
		}
	}
}

// runDiscoveryLoop owns the periodic Docker discovery. It runs RunDiscovery
// (which can block on Docker for up to its internal 30s context) off the
// writer's critical path and emits the finished agent.discovery message
// on out, where the writer drains it like any other event.
//
// Running this on a dedicated goroutine is what stops a slow Docker daemon
// from starving heartbeats and tripping the dashboard's offline alert.
func runDiscoveryLoop(
	ctx context.Context,
	ag *agent.Agent,
	nodeMetrics *agent.NodeMetricsCollector,
	dockerSocket string,
	discoverNow <-chan struct{},
	out chan<- *models.Message,
) {
	runOnce := func(reason string) {
		start := time.Now()
		report := ag.RunDiscovery(dockerSocket)
		elapsed := time.Since(start)
		if elapsed >= discoverySlowThreshold {
			log.Printf("discovery (%s): %d nodes in %s (slow — Docker daemon busy?)",
				reason, len(report.Nodes), elapsed.Round(time.Millisecond))
		} else {
			log.Printf("discovery (%s): %d nodes in %s",
				reason, len(report.Nodes), elapsed.Round(time.Millisecond))
		}
		nodeMetrics.UpdateNodes(report)
		msg := ag.BuildDiscoveryMessage(report)
		select {
		case out <- msg:
		case <-ctx.Done():
		}
	}

	// Initial discovery on connect
	runOnce("initial")

	ticker := time.NewTicker(discoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce("periodic")
		case <-discoverNow:
			runOnce("triggered")
		}
	}
}

// drainBeforeRestart waits for outbound result/progress queues to empty so the
// final command result reaches the dashboard before exec replaces the agent.
// Returns when the channels are empty (plus a small settle window for the
// writer's in-flight conn.Write to flush) or after maxWait, whichever comes
// first. len(chan)==0 only tells us nothing is queued; the settle covers the
// writer being mid-iteration.
func drainBeforeRestart(resultCh, progressCh chan *models.Message, maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if len(resultCh) == 0 && len(progressCh) == 0 {
			time.Sleep(200 * time.Millisecond)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	log.Printf("restart: drain timed out after %s, executing anyway", maxWait)
}

// writeMessage serializes and sends a message over WebSocket.
func writeMessage(ctx context.Context, conn *websocket.Conn, msg *models.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

// buildWSURL constructs the WebSocket URL from the dashboard URL.
func buildWSURL(dashboardURL, serverID string) string {
	// Convert https:// to wss://, http:// to ws://
	wsURL := dashboardURL
	if len(wsURL) > 8 && wsURL[:8] == "https://" {
		wsURL = "wss://" + wsURL[8:]
	} else if len(wsURL) > 7 && wsURL[:7] == "http://" {
		wsURL = "ws://" + wsURL[7:]
	}
	return wsURL + "/ws/agent?server_id=" + serverID
}

// defaultConfigDir returns the default config directory path.
func defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".klever-agent"
	}
	return filepath.Join(home, ".klever-agent")
}
