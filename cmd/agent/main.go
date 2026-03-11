package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"nhooyr.io/websocket"

	"github.com/CTJaeger/KleverNodeHub/internal/agent"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

const (
	heartbeatInterval  = 30 * time.Second
	discoveryInterval  = 5 * time.Minute
	reconnectBaseDelay = 1 * time.Second
	reconnectMaxDelay  = 60 * time.Second
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

	// --- WebSocket connection loop with auto-reconnect ---
	wsURL := buildWSURL(config.DashboardURL, config.ServerID)
	delay := reconnectBaseDelay

	for {
		if ctx.Err() != nil {
			break
		}

		log.Printf("connecting to %s...", wsURL)
		err := runAgentLoop(ctx, wsURL, ag, executor, *dockerSocket)
		if ctx.Err() != nil {
			break
		}

		log.Printf("connection lost: %v — reconnecting in %s", err, delay)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			break
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
func runAgentLoop(ctx context.Context, wsURL string, ag *agent.Agent, executor *agent.Executor, dockerSocket string) error {
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	defer dialCancel()

	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		// TODO: Add mTLS client certificate from agent config
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "closing") }()

	log.Println("connected to dashboard")

	// Send agent info on connect
	infoMsg := ag.BuildInfoMessage()
	if err := writeMessage(ctx, conn, infoMsg); err != nil {
		return fmt.Errorf("send agent info: %w", err)
	}

	// Run initial discovery
	go func() {
		report := ag.RunDiscovery(dockerSocket)
		discoveryMsg := ag.BuildDiscoveryMessage(report)
		if err := writeMessage(ctx, conn, discoveryMsg); err != nil {
			log.Printf("send discovery: %v", err)
		}
		log.Printf("initial discovery: %d nodes found", len(report.Nodes))
	}()

	// Heartbeat ticker
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	// Discovery ticker
	discoveryTicker := time.NewTicker(discoveryInterval)
	defer discoveryTicker.Stop()

	// Result channel for async command execution
	resultCh := make(chan *models.Message, 16)

	// Heartbeat + discovery sender
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeatTicker.C:
				hb := &models.Message{
					ID:     fmt.Sprintf("hb-%d", time.Now().UnixNano()),
					Type:   "event",
					Action: "agent.heartbeat",
					Payload: &models.HeartbeatPayload{
						Timestamp: time.Now().Unix(),
					},
					Timestamp: time.Now().Unix(),
				}
				if err := writeMessage(ctx, conn, hb); err != nil {
					log.Printf("send heartbeat: %v", err)
					return
				}
			case <-discoveryTicker.C:
				report := ag.RunDiscovery(dockerSocket)
				discoveryMsg := ag.BuildDiscoveryMessage(report)
				if err := writeMessage(ctx, conn, discoveryMsg); err != nil {
					log.Printf("send discovery: %v", err)
					return
				}
			case msg := <-resultCh:
				if err := writeMessage(ctx, conn, msg); err != nil {
					log.Printf("send result: %v", err)
					return
				}
			}
		}
	}()

	// Read loop: receive commands from dashboard
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var msg models.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("invalid message: %v", err)
			continue
		}

		if msg.Type == "command" {
			// Execute command asynchronously
			go func(m models.Message) {
				result := executor.Execute(&m)
				resultMsg := agent.BuildResultMessage(result)
				select {
				case resultCh <- resultMsg:
				case <-ctx.Done():
				}
			}(msg)
		}
	}
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
