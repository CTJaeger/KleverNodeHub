package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/auth"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

func setupSecurityHandler(t *testing.T, creds []auth.PasskeyCredential) (*SecurityHandler, *dashboard.UpdateStore) {
	t.Helper()
	a, _ := setupAuthHandler(t)
	if len(creds) > 0 {
		wa, err := auth.NewWebAuthnManager(auth.WebAuthnConfig{
			RPDisplayName: "Test", RPID: "localhost", RPOrigins: []string{"https://localhost:9443"},
		}, creds)
		if err != nil {
			t.Fatal(err)
		}
		a.webauthn = wa
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	updates := dashboard.NewUpdateStore(t.TempDir())
	return NewSecurityHandler(a, store.NewServerStore(db), updates, nil), updates
}

func TestSecurityCheck_ReportsWithoutChangingAnything(t *testing.T) {
	creds := []auth.PasskeyCredential{
		{ID: "mine", Name: "Laptop", RegisteredAt: 100},
		{ID: "foreign", Name: "knh-test", RegisteredAt: 200},
	}
	h, updates := setupSecurityHandler(t, creds)
	if _, err := updates.Store("v0.9.0", "linux", "amd64", []byte("old")); err != nil {
		t.Fatal(err)
	}
	if _, err := updates.StoreRelease("v1.0.0", "linux", "amd64", []byte("bin"), "klever-agent-linux-amd64", []byte("sums"), []byte("sig")); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	h.HandleCheck(w, httptest.NewRequest(http.MethodGet, "/api/security/check", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var o securityOverview
	if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	if len(o.Passkeys) != 2 || o.StoredBinaries != 2 || o.UnsignedBinaries != 1 {
		t.Errorf("overview wrong: %+v", o)
	}
	warned := false
	for _, f := range o.Findings {
		warned = warned || f.Level == "warn"
	}
	if !warned {
		t.Error("the known attacker passkey name must be flagged")
	}

	// The check only reports: nothing is removed.
	if n := len(h.auth.webauthn.Credentials()); n != 2 {
		t.Errorf("passkeys changed by the check: %d left", n)
	}
	if n := len(updates.List()); n != 2 {
		t.Errorf("stored binaries changed by the check: %d left", n)
	}
}

func TestIsReleaseVersion(t *testing.T) {
	for v, want := range map[string]bool{"v0.3.91": true, "0.3.91": true, "v0.3.93-knh.1": true, "dev": false, "probe6": false, "v0.3.91-x": false, "v0.3.93-knh.": false, "": false} {
		if got := isReleaseVersion(v); got != want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestUnsignedStoredBinaryIsNotDelivered(t *testing.T) {
	h := &UpdateHandler{}
	signed := &dashboard.AgentBinaryInfo{Version: "v1.0.0", Signature: "sig"}
	unsigned := &dashboard.AgentBinaryInfo{Version: "v1.0.0"}
	if !h.deliverable(signed) || h.deliverable(unsigned) || h.deliverable(nil) {
		t.Error("only signed binaries may be delivered by default")
	}
	h.SetAllowUnsignedBinaries(true)
	if !h.deliverable(unsigned) {
		t.Error("--allow-unsigned-agent-binaries must allow unsigned binaries")
	}
}
