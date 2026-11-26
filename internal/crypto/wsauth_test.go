package crypto

import (
	"testing"
	"time"
)

func TestAgentAuth_SignVerify(t *testing.T) {
	pub, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, _ := GenerateEd25519KeyPair()
	now := time.Now()
	ts := now.Unix()
	nonce, _ := NewAgentAuthNonce()
	sig := SignAgentAuth(priv, "srv-1", ts, nonce)

	if err := VerifyAgentAuth(pub, "srv-1", ts, nonce, sig, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if VerifyAgentAuth(pub, "srv-2", ts, nonce, sig, now) == nil {
		t.Fatal("signature accepted for a different server ID")
	}
	if VerifyAgentAuth(otherPub, "srv-1", ts, nonce, sig, now) == nil {
		t.Fatal("signature accepted for a different key")
	}
	if VerifyAgentAuth(pub, "srv-1", ts, nonce, sig, now.Add(AgentAuthMaxSkew+time.Minute)) == nil {
		t.Fatal("stale timestamp accepted")
	}
	if VerifyAgentAuth(pub, "srv-1", ts, nonce, sig, now.Add(-AgentAuthMaxSkew-time.Minute)) == nil {
		t.Fatal("future timestamp accepted")
	}
	if VerifyAgentAuth(pub, "srv-1", ts, "other-nonce", sig, now) == nil {
		t.Fatal("signature accepted with a different nonce")
	}
	if VerifyAgentAuth(pub, "srv-1", ts, nonce, "!!notbase64", now) == nil {
		t.Fatal("garbage signature accepted")
	}
}
