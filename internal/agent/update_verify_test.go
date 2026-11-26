package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"runtime"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/release"
	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

func TestVerifyUpdateBinary(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	defer release.SwapPublicKeyForTest(pub)()

	bin := []byte("signed agent")
	hubBin := []byte("signed dashboard")
	line := func(name string, data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:]) + "  " + name + "\n"
	}
	signedFor := func(tag string) map[string]any {
		sums := line(release.AgentAssetName(runtime.GOOS, runtime.GOARCH), bin) +
			line(release.HubAssetName(runtime.GOOS, runtime.GOARCH), hubBin)
		if tag != "" {
			sums += "# release " + tag + "\n"
		}
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums)))
		return map[string]any{"release_checksums": sums, "release_signature": sig}
	}
	signed := signedFor("v0.3.89")

	e := &Executor{}
	if err := e.verifyUpdateBinary(signed, bin); err != nil {
		t.Fatalf("signed agent release rejected: %v", err)
	}
	if err := e.verifyUpdateBinary(signed, []byte("malicious")); err == nil {
		t.Error("tampered binary accepted")
	}
	if err := e.verifyUpdateBinary(signed, hubBin); err == nil {
		t.Error("signed dashboard binary accepted as agent update")
	}
	if err := e.verifyUpdateBinary(map[string]any{}, []byte("malicious")); err == nil {
		t.Error("unsigned binary accepted without local opt-in")
	}

	if err := e.verifyUpdateBinary(signedFor(""), bin); err == nil {
		t.Error("signed checksums without a release version accepted")
	}

	defer func(v string) { version.Version = v }(version.Version)
	version.Version = "v0.3.90"
	if err := e.verifyUpdateBinary(signed, bin); err == nil {
		t.Error("downgrade to an older signed release accepted")
	}
	if err := e.verifyUpdateBinary(signedFor("v0.3.90"), bin); err != nil {
		t.Errorf("reinstall of the same version rejected: %v", err)
	}

	e.SetAllowUnsignedUpdates(true)
	if err := e.verifyUpdateBinary(map[string]any{}, []byte("custom build")); err != nil {
		t.Errorf("local opt-in ignored: %v", err)
	}
}
