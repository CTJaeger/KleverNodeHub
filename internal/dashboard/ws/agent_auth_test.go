package ws

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/crypto"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func TestAuthenticateAgent(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	serverStore := store.NewServerStore(db)

	ca, err := crypto.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := crypto.GenerateEd25519KeyPair()
	certPEM, err := ca.IssueAgentCertificate(pub, "srv-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := serverStore.Create(&models.Server{ID: "srv-1", Name: "n", Hostname: "h", IPAddress: "1.1.1.1", Status: "online", Certificate: certPEM}); err != nil {
		t.Fatal(err)
	}
	srv, _ := serverStore.GetByID("srv-1")
	h := &AgentHandler{serverStore: serverStore}

	signed := func(ts int64) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/ws/agent?server_id=srv-1", nil)
		r.Header.Set("X-Agent-Timestamp", strconv.FormatInt(ts, 10))
		nonce, _ := crypto.NewAgentAuthNonce()
		r.Header.Set("X-Agent-Nonce", nonce)
		r.Header.Set("X-Agent-Signature", crypto.SignAgentAuth(priv, "srv-1", ts, nonce))
		return r
	}
	unsigned := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/ws/agent?server_id=srv-1", nil) }

	// Legacy agent (never signed) is still accepted.
	if !h.authenticateAgent(unsigned(), srv) {
		t.Fatal("legacy unsigned agent rejected before it ever signed")
	}
	// Signature from a different key is rejected.
	_, otherPriv, _ := crypto.GenerateEd25519KeyPair()
	bad := httptest.NewRequest(http.MethodGet, "/ws/agent?server_id=srv-1", nil)
	now := time.Now().Unix()
	bad.Header.Set("X-Agent-Timestamp", strconv.FormatInt(now, 10))
	bad.Header.Set("X-Agent-Nonce", "n1")
	bad.Header.Set("X-Agent-Signature", crypto.SignAgentAuth(otherPriv, "srv-1", now, "n1"))
	if h.authenticateAgent(bad, srv) {
		t.Fatal("signature from wrong key accepted")
	}
	// Valid signature accepted and flips the server to signed-only.
	if !h.authenticateAgent(signed(now), srv) {
		t.Fatal("valid signature rejected")
	}
	if req, _ := serverStore.RequiresSignedAuth("srv-1"); !req {
		t.Fatal("server not marked as requiring signed auth")
	}
	// Now an impersonator that only knows the server_id is rejected.
	if h.authenticateAgent(unsigned(), srv) {
		t.Fatal("unsigned upgrade accepted after server switched to signed auth")
	}
	// Stale signature rejected.
	if h.authenticateAgent(signed(now-3600), srv) {
		t.Fatal("stale signature accepted")
	}

	// Replay of an already-used signed request is rejected.
	replay := signed(now)
	if !h.authenticateAgent(replay, srv) {
		t.Fatal("fresh signed request rejected")
	}
	if h.authenticateAgent(replay, srv) {
		t.Fatal("replayed request (same nonce) accepted")
	}
}

func TestAuthenticateAgent_OperatorRequiresSigned(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ss := store.NewServerStore(db)
	_ = ss.Create(&models.Server{ID: "srv-9", Name: "n", Hostname: "h", IPAddress: "1.1.1.1", Status: "online"})
	srv, _ := ss.GetByID("srv-9")
	h := &AgentHandler{serverStore: ss}
	h.SetRequireSignedAuth(func() bool { return true })
	if h.authenticateAgent(httptest.NewRequest(http.MethodGet, "/ws/agent?server_id=srv-9", nil), srv) {
		t.Fatal("unsigned upgrade accepted although operator requires signed auth")
	}
}
