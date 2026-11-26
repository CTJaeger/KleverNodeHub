// Command releasesign creates the release signing key and signs checksums.txt.
//
//	releasesign keygen <private-key-file>   write a new key (0600), print the public key
//	releasesign sign <checksums.txt>        sign with $RELEASE_SIGNING_KEY, write release.sig next to it
//
// The private key is the base64 of the 32-byte Ed25519 seed. It belongs only in
// the RELEASE_SIGNING_KEY secret of the release workflow; the printed public
// key goes into internal/release.PublicKeyHex.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CTJaeger/KleverNodeHub/internal/release"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: releasesign keygen <private-key-file> | sign <checksums.txt>")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2])
	case "sign":
		err = sign(os.Args[2])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasesign:", err)
		os.Exit(1)
	}
}

func keygen(path string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err // never overwrite an existing key
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("private key written to %s (put it into the RELEASE_SIGNING_KEY secret, then delete the file)\n", path)
	fmt.Printf("public key (internal/release.PublicKeyHex): %s\n", hex.EncodeToString(pub))
	spki, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return err
	}
	fmt.Printf("public key (scripts/install-agent.sh RELEASE_PUBKEY_B64): %s\n", base64.StdEncoding.EncodeToString(spki))
	return nil
}

func sign(path string) error {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("RELEASE_SIGNING_KEY")))
	if err != nil || len(seed) != ed25519.SeedSize {
		return fmt.Errorf("RELEASE_SIGNING_KEY is missing or not a base64 Ed25519 seed")
	}
	priv := ed25519.NewKeyFromSeed(seed)

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))

	// Refuse to publish a signature this build's own verifier would reject:
	// catches a secret that doesn't match the compiled-in public key.
	if err := release.VerifyChecksums(data, []byte(sig)); err != nil {
		return fmt.Errorf("signature does not verify against internal/release.PublicKeyHex: %w", err)
	}
	return os.WriteFile(filepath.Join(filepath.Dir(path), release.SignatureFile), []byte(sig+"\n"), 0o644)
}
