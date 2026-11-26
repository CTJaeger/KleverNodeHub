// Package release verifies that a binary comes from an official release.
//
// CI hashes every release binary into checksums.txt (sha256sum format) and
// signs that file with the release Ed25519 key (release.sig, base64 of the raw
// 64-byte signature). The matching public key is compiled into every
// hub and agent binary, so nothing a hub sends — and nothing a hub admin can
// configure — can make an agent accept a binary that was not released.
package release

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// PublicKeyHex is the release signing public key of CTJaeger/KleverNodeHub
// (hex, 32 bytes). The private key exists only as the RELEASE_SIGNING_KEY
// secret of that repository's release workflow.
const PublicKeyHex = "09c49585f3d0c468847a0f5522f762f89782ab8b67a0b71760c339c78d7b32d5"

// SuccessorKeyHex is the release signing public key of Test0rMaik/KleverNodeHub,
// where development continues. Releases signed with it are accepted too, so
// dashboards and agents move there through a normal update.
const SuccessorKeyHex = "816dd6ccbf7e03bdf54e72fa38e2837ec05655605d828f16eed788f198a4a134"

// ChecksumsFile and SignatureFile are the release asset names. The signature
// name must not contain "checksums": dashboards up to v0.3.88 pick the first
// asset whose name does as their checksum file when they self-update.
const (
	ChecksumsFile = "checksums.txt"
	SignatureFile = "release.sig"
)

// ErrUnsigned means there is no signature to check against.
var ErrUnsigned = errors.New("release is not signed")

// publicKeys are the trusted release keys; a variable so tests can substitute
// their own key.
var publicKeys = trustedKeys(PublicKeyHex, SuccessorKeyHex)

func trustedKeys(hexKeys ...string) []ed25519.PublicKey {
	var keys []ed25519.PublicKey
	for _, h := range hexKeys {
		if k := mustParseKey(h); k != nil {
			keys = append(keys, k)
		}
	}
	return keys
}

func mustParseKey(h string) ed25519.PublicKey {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil // no key compiled in: every verification fails closed
	}
	return ed25519.PublicKey(b)
}

// VerifyChecksums checks the signature over a checksums.txt file.
func VerifyChecksums(checksums, sigB64 []byte) error {
	if len(bytes.TrimSpace(sigB64)) == 0 {
		return ErrUnsigned
	}
	if len(publicKeys) == 0 {
		return errors.New("no release public key compiled into this binary")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("malformed release signature")
	}
	for _, k := range publicKeys {
		if ed25519.Verify(k, checksums, sig) {
			return nil
		}
	}
	return errors.New("release signature does not match")
}

// ExpectedHash returns the sha256 listed for asset in a checksums.txt file.
func ExpectedHash(checksums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// sha256sum writes "<hash>  <name>" ("*<name>" in binary mode).
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s is not listed in the release checksums", asset)
}

// VerifyAsset checks that data is the release asset named asset: the checksums
// file must carry a valid release signature and list data's hash for asset.
func VerifyAsset(checksums, sigB64 []byte, asset string, data []byte) error {
	if err := VerifyChecksums(checksums, sigB64); err != nil {
		return err
	}
	want, err := ExpectedHash(checksums, asset)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("%s does not match the signed release checksum", asset)
	}
	return nil
}

// tagPrefix marks the line CI appends to checksums.txt before signing, so the
// release version is covered by the signature (sha256sum lines have 2 fields,
// this one 3, so asset lookups never match it).
const tagPrefix = "# release "

// Tag returns the release version recorded in a signed checksums.txt, or "".
func Tag(checksums []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		if t, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), tagPrefix); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// AgentAssetName is the release asset name of the agent for a platform.
func AgentAssetName(goos, goarch string) string {
	name := "klever-agent-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// HubAssetName is the release asset name of the dashboard for a platform.
func HubAssetName(goos, goarch string) string {
	name := "klever-node-hub-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// SwapPublicKeyForTest replaces the release key and returns a restore func.
// Only for tests in other packages; production code never calls it.
func SwapPublicKeyForTest(pub ed25519.PublicKey) (restore func()) {
	if !testing.Testing() {
		panic("release.SwapPublicKeyForTest called outside a test")
	}
	old := publicKeys
	publicKeys = []ed25519.PublicKey{pub}
	return func() { publicKeys = old }
}
