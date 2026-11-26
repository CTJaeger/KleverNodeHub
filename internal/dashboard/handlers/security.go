package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/release"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

// SecurityHandler serves the security check. It only reads and reports: what
// to do about a finding is the operator's decision, the dashboard changes
// nothing on its own.
type SecurityHandler struct {
	auth    *AuthHandler
	servers *store.ServerStore
	updates *dashboard.UpdateStore
	hub     *ws.Hub
}

// NewSecurityHandler creates a new SecurityHandler.
func NewSecurityHandler(auth *AuthHandler, servers *store.ServerStore, updates *dashboard.UpdateStore, hub *ws.Hub) *SecurityHandler {
	return &SecurityHandler{auth: auth, servers: servers, updates: updates, hub: hub}
}

var releaseVersionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-knh\.\d+)?$`)

// isReleaseVersion reports whether an agent version looks like a release tag.
// Anything else (dev, a custom label) was not installed from a release.
func isReleaseVersion(v string) bool { return releaseVersionPattern.MatchString(v) }

type securityPasskey struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RegisteredAt int64  `json:"registered_at"`
	SignCount    uint32 `json:"sign_count"`
}

type securityServer struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Hostname       string `json:"hostname"`
	IPAddress      string `json:"ip_address"`
	AgentVersion   string `json:"agent_version"`
	ReleaseVersion bool   `json:"release_version"`
	RegisteredAt   int64  `json:"registered_at"`
	Online         bool   `json:"online"`
}

type securityFinding struct {
	Level string `json:"level"` // "warn" or "info"
	Text  string `json:"text"`
}

type securityOverview struct {
	Passkeys         []securityPasskey `json:"passkeys"`
	KleverAddress    string            `json:"klever_address"`
	HasPassword      bool              `json:"has_password"`
	Servers          []securityServer  `json:"servers"`
	StoredBinaries   int               `json:"stored_binaries"`
	UnsignedBinaries int               `json:"unsigned_binaries"`
	Findings         []securityFinding `json:"findings"`
}

func (h *SecurityHandler) overview() (*securityOverview, error) {
	o := &securityOverview{
		Passkeys:    []securityPasskey{},
		Servers:     []securityServer{},
		Findings:    []securityFinding{},
		HasPassword: h.auth.password.HasPassword(),
	}
	for _, c := range h.auth.webauthn.Credentials() {
		o.Passkeys = append(o.Passkeys, securityPasskey{ID: c.ID, Name: c.Name, RegisteredAt: c.RegisteredAt, SignCount: c.SignCount})
		if strings.EqualFold(c.Name, "knh-test") {
			o.Findings = append(o.Findings, securityFinding{Level: "warn", Text: fmt.Sprintf("Passkey %q matches a name used in known intrusions", c.Name)})
		}
	}
	if h.auth.klever != nil {
		o.KleverAddress = h.auth.klever.Address()
	}

	servers, err := h.servers.List()
	if err != nil {
		return nil, err
	}
	for _, s := range servers {
		name := s.DisplayName
		if name == "" {
			name = s.Name
		}
		rel := s.AgentVersion == "" || isReleaseVersion(s.AgentVersion)
		o.Servers = append(o.Servers, securityServer{
			ID: s.ID, Name: name, Hostname: s.Hostname, IPAddress: s.IPAddress,
			AgentVersion: s.AgentVersion, ReleaseVersion: rel, RegisteredAt: s.RegisteredAt,
			Online: h.hub != nil && h.hub.IsConnected(s.ID),
		})
		if !rel {
			o.Findings = append(o.Findings, securityFinding{Level: "warn", Text: fmt.Sprintf("Server %q reports agent version %q, which is not a release", name, s.AgentVersion)})
		}
	}

	if h.updates != nil {
		for _, b := range h.updates.List() {
			o.StoredBinaries++
			if !b.Signed() {
				o.UnsignedBinaries++
			}
		}
	}
	if o.UnsignedBinaries > 0 {
		o.Findings = append(o.Findings, securityFinding{Level: "info", Text: fmt.Sprintf("%d stored agent binaries are not from a signed release and are not handed to agents", o.UnsignedBinaries)})
	}
	return o, nil
}

// HandleCheck handles GET /api/security/check
func (h *SecurityHandler) HandleCheck(w http.ResponseWriter, _ *http.Request) {
	o, err := h.overview()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, o)
}

type serverCheckResult struct {
	ServerID string   `json:"server_id"`
	Name     string   `json:"name"`
	Status   string   `json:"status"` // ok, warn, offline, unsupported, error
	Files    []string `json:"files,omitempty"`
	Binary   string   `json:"binary,omitempty"` // verified, mismatch, unverifiable
	Detail   string   `json:"detail,omitempty"`
}

// HandleCheckServers handles POST /api/security/check/servers
// Asks every connected agent to look for known attacker files and report the
// checksum of its own program, which is compared with the signed release.
// Read-only. An agent that was replaced can lie: a clean answer is not proof.
func (h *SecurityHandler) HandleCheckServers(w http.ResponseWriter, _ *http.Request) {
	servers, err := h.servers.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	results := make([]serverCheckResult, len(servers))
	client := &http.Client{Timeout: 30 * time.Second}
	var sumsMu sync.Mutex
	sumsCache := map[string][]byte{}
	signedSums := func(tag string) []byte {
		sumsMu.Lock()
		defer sumsMu.Unlock()
		if s, ok := sumsCache[tag]; ok {
			return s
		}
		s, _, err := fetchSignedChecksums(client, tag)
		if err != nil {
			s = nil
		}
		sumsCache[tag] = s
		return s
	}

	var wg sync.WaitGroup
	for i := range servers {
		srv := servers[i]
		name := srv.DisplayName
		if name == "" {
			name = srv.Name
		}
		res := &results[i]
		res.ServerID, res.Name = srv.ID, name

		if h.hub == nil || !h.hub.IsConnected(srv.ID) {
			res.Status = "offline"
			continue
		}
		if !h.hub.AgentHasCapability(srv.ID, models.CapabilitySecurityCheck) {
			res.Status = "unsupported"
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := &models.Message{
				ID:        fmt.Sprintf("cmd-security-check-%s-%d", srv.ID, time.Now().UnixNano()),
				Type:      "command",
				Action:    "security.check",
				Payload:   map[string]string{},
				Timestamp: time.Now().Unix(),
			}
			out, err := h.hub.SendCommand(srv.ID, msg, 3*time.Minute)
			if err != nil || out == nil || !out.Success {
				res.Status = "error"
				if err != nil {
					res.Detail = err.Error()
				} else if out != nil {
					res.Detail = out.Error
				}
				return
			}
			var rep models.SecurityCheckReport
			if err := json.Unmarshal([]byte(out.Output), &rep); err != nil {
				res.Status, res.Detail = "error", "unreadable report"
				return
			}
			res.Files = rep.Files
			res.Binary = "unverifiable"
			if isReleaseVersion(rep.Version) {
				if sums := signedSums(rep.Version); sums != nil {
					want, err := release.ExpectedHash(sums, release.AgentAssetName(rep.GOOS, rep.GOARCH))
					if err == nil && strings.EqualFold(want, rep.ExeSHA256) {
						res.Binary = "verified"
					} else if err == nil {
						res.Binary = "mismatch"
					}
				}
			}
			res.Status = "ok"
			if len(rep.Files) > 0 || res.Binary == "mismatch" {
				res.Status = "warn"
			}
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"servers": results})
}
