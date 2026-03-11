package agent

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const validatorKeyFile = "validatorKey.pem"

// ExtractBLSPublicKey reads a validatorKey.pem from the config directory
// and extracts the BLS public key (hex-encoded).
//
// The PEM file contains the private key in a custom format used by Klever.
// The public key is extracted from the last 96 bytes of the decoded key data
// (BLS12-381 public key = 96 bytes).
func ExtractBLSPublicKey(configDir string) (string, error) {
	path := filepath.Join(configDir, validatorKeyFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read validator key: %w", err)
	}

	return parseBLSPublicKeyFromPEM(string(data))
}

// parseBLSPublicKeyFromPEM parses the BLS public key from PEM content.
// Klever's validatorKey.pem has the format:
//
//	-----BEGIN PRIVATE KEY for <hex-public-key>-----
//	<base64-encoded-private-key>
//	-----END PRIVATE KEY for <hex-public-key>-----
//
// The public key is in the PEM header/footer.
func parseBLSPublicKeyFromPEM(pemData string) (string, error) {
	lines := strings.Split(strings.TrimSpace(pemData), "\n")
	if len(lines) == 0 {
		return "", fmt.Errorf("empty PEM data")
	}

	// Look for the BEGIN line with the public key
	beginLine := strings.TrimSpace(lines[0])
	const prefix = "-----BEGIN PRIVATE KEY for "
	const suffix = "-----"

	if !strings.HasPrefix(beginLine, prefix) || !strings.HasSuffix(beginLine, suffix) {
		return "", fmt.Errorf("unexpected PEM header: %s", beginLine)
	}

	// Extract hex public key from between prefix and suffix
	hexKey := beginLine[len(prefix) : len(beginLine)-len(suffix)]
	hexKey = strings.TrimSpace(hexKey)

	if len(hexKey) == 0 {
		return "", fmt.Errorf("empty public key in PEM header")
	}

	// Validate it's valid hex
	if _, err := hex.DecodeString(hexKey); err != nil {
		return "", fmt.Errorf("invalid hex in PEM header: %w", err)
	}

	return hexKey, nil
}
