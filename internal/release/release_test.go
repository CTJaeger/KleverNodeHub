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
	old := publicKey
	publicKey = pub
	t.Cleanup(func() { publicKey = old })
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

	publicKey = nil // binary built without a key
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
