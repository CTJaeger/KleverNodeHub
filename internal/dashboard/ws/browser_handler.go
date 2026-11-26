package ws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// BrowserHandler handles WebSocket connections from browser clients.
type BrowserHandler struct {
	hub            *Hub
	originPatterns []string
}

// SetOriginPatterns lists the hosts (besides the request's own Host) whose
// pages may open the browser WebSocket, e.g. the public --domain behind a
// reverse proxy that rewrites Host.
func (h *BrowserHandler) SetOriginPatterns(p []string) { h.originPatterns = p }

// NewBrowserHandler creates a new WebSocket handler for browser connections.
func NewBrowserHandler(hub *Hub) *BrowserHandler {
	return &BrowserHandler{hub: hub}
}

// HandleUpgrade upgrades an HTTP connection to WebSocket for browser clients.
func (h *BrowserHandler) HandleUpgrade(w http.ResponseWriter, r *http.Request) {
	// Origin is checked (cross-site pages must not ride the session cookie);
	// same-host origins are always allowed by the library.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.originPatterns,
	})
	if err != nil {
		log.Printf("browser websocket upgrade failed: %v", err)
		return
	}

	clientID := generateClientID()
	browserConn := h.hub.RegisterBrowser(clientID)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Write loop: send events from hub to browser
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-browserConn.SendCh:
				if !ok {
					return
				}
				writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, data)
				writeCancel()
				if err != nil {
					log.Printf("browser websocket write error for %s: %v", clientID, err)
					return
				}
			}
		}
	}()

	// Read loop: keep connection alive, handle pings
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("browser websocket read error for %s: %v", clientID, err)
			}
			break
		}
	}

	h.hub.UnregisterBrowser(clientID)
	_ = conn.Close(websocket.StatusNormalClosure, "closing")
}

func generateClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "browser-" + hex.EncodeToString(b)
}
