package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func withTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := publicKeys
	publicKeys = []ed25519.PublicKey{pub}
	t.Cleanup(func() { publicKeys = old })
	return priv
}

func checksumsFor(name string, data []byte) []byte {
	sum := sha256.Sum256(data)
	return []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n" +
		"0000000000000000000000000000000000000000000000000000000000000000  other\n")
}

func TestVerifyAsset(t *testing.T) {
	priv := withTestKey(t)
	bin := []byte("agent binary")
	sums := checksumsFor("klever-agent-linux-amd64", bin)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)))

	if err := VerifyAsset(sums, sig, "klever-agent-linux-amd64", bin); err != nil {
		t.Fatalf("valid release rejected: %v", err)
	}
	if err := VerifyAsset(sums, sig, "klever-agent-linux-amd64", []byte("evil")); err == nil {
		t.Error("tampered binary accepted")
	}
	if err := VerifyAsset(sums, sig, "klever-agent-linux-arm64", bin); err == nil {
		t.Error("binary accepted under an unlisted asset name")
	}

	forged := checksumsFor("klever-agent-linux-amd64", []byte("evil"))
	if err := VerifyAsset(forged, sig, "klever-agent-linux-amd64", []byte("evil")); err == nil {
		t.Error("checksums file changed after signing was accepted")
	}

	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	otherSig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, forged)))
	if err := VerifyAsset(forged, otherSig, "klever-agent-linux-amd64", []byte("evil")); err == nil {
		t.Error("signature by a foreign key accepted")
	}
}

func TestVerifyChecksums_UnsignedAndNoKey(t *testing.T) {
	withTestKey(t)
	if err := VerifyChecksums([]byte("x"), nil); err != ErrUnsigned {
		t.Errorf("missing signature: err = %v, want ErrUnsigned", err)
	}

	publicKeys = nil // binary built without a key
	if err := VerifyChecksums([]byte("x"), []byte("c2ln")); err == nil {
		t.Error("verification must fail closed without a compiled-in key")
	}
}

func TestExpectedHash_BinaryModeName(t *testing.T) {
	sums := []byte("ABCDEF  *klever-agent-windows-amd64.exe\n")
	got, err := ExpectedHash(sums, "klever-agent-windows-amd64.exe")
	if err != nil || got != "abcdef" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestAssetNames(t *testing.T) {
	if got := AgentAssetName("windows", "arm64"); got != "klever-agent-windows-arm64.exe" {
		t.Errorf("AgentAssetName = %q", got)
	}
	if got := HubAssetName("linux", "amd64"); got != "klever-node-hub-linux-amd64" {
		t.Errorf("HubAssetName = %q", got)
	}
}

// Releases of both repositories are accepted; anything else is not.
func TestVerifyChecksums_TrustsBothRepositories(t *testing.T) {
	if len(publicKeys) != 2 {
		t.Fatalf("expected 2 trusted keys, got %d", len(publicKeys))
	}
	if hex.EncodeToString(publicKeys[0]) != PublicKeyHex || hex.EncodeToString(publicKeys[1]) != SuccessorKeyHex {
		t.Fatal("trusted keys are not the two release keys")
	}

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sums := []byte("abc  klever-agent-linux-amd64\n# release v1\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums))
	if err := VerifyChecksums(sums, []byte(sig)); err == nil {
		t.Error("signature of an unknown key accepted")
	}

	defer func(k []ed25519.PublicKey) { publicKeys = k }(publicKeys)
	publicKeys = []ed25519.PublicKey{mustParseKey(PublicKeyHex), pub}
	if err := VerifyChecksums(sums, []byte(sig)); err != nil {
		t.Errorf("signature of the second trusted key rejected: %v", err)
	}
}
