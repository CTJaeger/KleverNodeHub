package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// AgentAuthMaxSkew is how far an agent's signed timestamp may differ from the
// dashboard clock before the WebSocket upgrade is rejected.
const AgentAuthMaxSkew = 5 * time.Minute

// agentAuthMessage is the exact byte string an agent signs when opening its
// WebSocket. Binding the server ID, a timestamp and a random nonce keeps a
// captured signature from being reused for another agent, after the skew
// window, or (with the dashboard's seen-nonce cache) at all.
func agentAuthMessage(serverID string, ts int64, nonce string) []byte {
	return []byte("kvh-agent-ws-v2|" + serverID + "|" + strconv.FormatInt(ts, 10) + "|" + nonce)
}

// SignAgentAuth signs the WebSocket-upgrade challenge with the agent's private
// key (issued at registration) and returns the base64 signature.
func SignAgentAuth(priv ed25519.PrivateKey, serverID string, ts int64, nonce string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, agentAuthMessage(serverID, ts, nonce)))
}

// NewAgentAuthNonce returns a fresh random nonce for one connection attempt.
func NewAgentAuthNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ErrAgentAuthSkew is returned (only for an otherwise valid signature) when the
// agent's clock is outside the allowed window.
var ErrAgentAuthSkew = errors.New("timestamp outside allowed window")

// VerifyAgentAuth checks a signature produced by SignAgentAuth against the
// agent's public key and enforces the timestamp skew window. The signature is
// checked first so skew details are only revealed to a legitimate key holder.
func VerifyAgentAuth(pub ed25519.PublicKey, serverID string, ts int64, nonce, sigB64 string, now time.Time) error {
	if nonce == "" || len(nonce) > 64 {
		return fmt.Errorf("invalid nonce")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("malformed signature")
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, agentAuthMessage(serverID, ts, nonce), sig) {
		return fmt.Errorf("signature mismatch")
	}
	skew := now.Sub(time.Unix(ts, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > AgentAuthMaxSkew {
		return fmt.Errorf("%w (clock skew %s)", ErrAgentAuthSkew, skew.Round(time.Second))
	}
	return nil
}
