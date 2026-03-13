package notify

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// PushSubscription represents a Web Push subscription from the browser.
type PushSubscription struct {
	Endpoint string            `json:"endpoint"`
	Keys     PushSubscriptionKeys `json:"keys"`
}

// PushSubscriptionKeys holds the client keys for push encryption.
type PushSubscriptionKeys struct {
	P256dh string `json:"p256dh"` // base64url-encoded client public key
	Auth   string `json:"auth"`   // base64url-encoded auth secret (16 bytes)
}

// WebPushChannel sends notifications via Web Push (RFC 8291).
type WebPushChannel struct {
	vapid         *VAPIDKeys
	subscriptions []PushSubscription
	subject       string // mailto: or https: URL for VAPID
	httpClient    *http.Client
}

// NewWebPushChannel creates a new Web Push notification channel.
func NewWebPushChannel(vapid *VAPIDKeys, subject string) *WebPushChannel {
	return &WebPushChannel{
		vapid:   vapid,
		subject: subject,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetSubscriptions replaces all subscriptions.
func (w *WebPushChannel) SetSubscriptions(subs []PushSubscription) {
	w.subscriptions = subs
}

// AddSubscription adds a subscription.
func (w *WebPushChannel) AddSubscription(sub PushSubscription) {
	// Deduplicate by endpoint
	for i, existing := range w.subscriptions {
		if existing.Endpoint == sub.Endpoint {
			w.subscriptions[i] = sub
			return
		}
	}
	w.subscriptions = append(w.subscriptions, sub)
}

// RemoveSubscription removes a subscription by endpoint.
func (w *WebPushChannel) RemoveSubscription(endpoint string) {
	for i, sub := range w.subscriptions {
		if sub.Endpoint == endpoint {
			w.subscriptions = append(w.subscriptions[:i], w.subscriptions[i+1:]...)
			return
		}
	}
}

func (w *WebPushChannel) Name() string { return "webpush" }

func (w *WebPushChannel) Validate() error {
	if w.vapid == nil {
		return fmt.Errorf("VAPID keys not configured")
	}
	return nil
}

func (w *WebPushChannel) Send(alert *Alert) error {
	if err := w.Validate(); err != nil {
		return err
	}

	if len(w.subscriptions) == 0 {
		return nil // No subscribers — not an error
	}

	icon := "ℹ️"
	switch alert.Severity {
	case SeverityWarning:
		icon = "⚠️"
	case SeverityCritical:
		icon = "🚨"
	}

	payload, _ := json.Marshal(map[string]string{
		"title": icon + " " + alert.Title,
		"body":  alert.Message,
		"tag":   alert.Source,
	})

	var lastErr error
	for _, sub := range w.subscriptions {
		if err := w.sendToSubscription(sub, payload); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (w *WebPushChannel) sendToSubscription(sub PushSubscription, payload []byte) error {
	// Decode client keys
	clientPubBytes, err := base64.RawURLEncoding.DecodeString(sub.Keys.P256dh)
	if err != nil {
		return fmt.Errorf("decode p256dh: %w", err)
	}
	authSecret, err := base64.RawURLEncoding.DecodeString(sub.Keys.Auth)
	if err != nil {
		return fmt.Errorf("decode auth: %w", err)
	}

	// Encrypt payload per RFC 8291 (aes128gcm)
	encrypted, err := encryptPayload(w.vapid.PrivateKey, clientPubBytes, authSecret, payload)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	// Build VAPID authorization header
	vapidHeader, err := buildVAPIDAuth(w.vapid, sub.Endpoint, w.subject)
	if err != nil {
		return fmt.Errorf("vapid auth: %w", err)
	}

	req, err := http.NewRequest("POST", sub.Endpoint, bytes.NewReader(encrypted))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Authorization", vapidHeader)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("push send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusGone {
		// Subscription expired — remove it
		w.RemoveSubscription(sub.Endpoint)
		return nil
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("push endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// encryptPayload encrypts a Web Push payload per RFC 8291 (aes128gcm content encoding).
func encryptPayload(serverPriv *ecdsa.PrivateKey, clientPubBytes, authSecret, plaintext []byte) ([]byte, error) {
	// Generate ephemeral ECDH key pair
	curve := ecdh.P256()
	ephPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	ephPub := ephPriv.PublicKey()

	// Convert client public key to ecdh
	clientPub, err := curve.NewPublicKey(clientPubBytes)
	if err != nil {
		return nil, fmt.Errorf("parse client public key: %w", err)
	}

	// ECDH shared secret
	sharedSecret, err := ephPriv.ECDH(clientPub)
	if err != nil {
		return nil, fmt.Errorf("ECDH: %w", err)
	}

	// PRK = HKDF-Extract(auth_secret, ecdh_secret)
	prkReader := hkdf.New(sha256.New, sharedSecret, authSecret, []byte("WebPush: info\x00"+string(clientPubBytes)+string(ephPub.Bytes())))
	ikm := make([]byte, 32)
	if _, err := io.ReadFull(prkReader, ikm); err != nil {
		return nil, err
	}

	// Generate 16-byte salt
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	// Derive content encryption key (CEK) and nonce
	cekInfo := []byte("Content-Encoding: aes128gcm\x00")
	nonceInfo := []byte("Content-Encoding: nonce\x00")

	cek := make([]byte, 16)
	cekReader := hkdf.New(sha256.New, ikm, salt, cekInfo)
	if _, err := io.ReadFull(cekReader, cek); err != nil {
		return nil, err
	}

	nonce := make([]byte, 12)
	nonceReader := hkdf.New(sha256.New, ikm, salt, nonceInfo)
	if _, err := io.ReadFull(nonceReader, nonce); err != nil {
		return nil, err
	}

	// Pad plaintext: append 0x02 delimiter (RFC 8188)
	padded := append(plaintext, 2)

	// AES-128-GCM encrypt
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, padded, nil)

	// Build aes128gcm header: salt(16) + rs(4) + idlen(1) + keyid(65) + ciphertext
	ephPubBytes := ephPub.Bytes()
	var header bytes.Buffer
	header.Write(salt)
	rs := make([]byte, 4)
	binary.BigEndian.PutUint32(rs, 4096)
	header.Write(rs)
	header.WriteByte(byte(len(ephPubBytes)))
	header.Write(ephPubBytes)
	header.Write(ciphertext)

	return header.Bytes(), nil
}

// buildVAPIDAuth creates a VAPID Authorization header (RFC 8292).
func buildVAPIDAuth(vapid *VAPIDKeys, endpoint, subject string) (string, error) {
	// Extract audience (origin) from endpoint
	parts := strings.SplitN(endpoint, "/", 4)
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid endpoint URL")
	}
	audience := parts[0] + "//" + parts[2]

	now := time.Now()
	claims := map[string]any{
		"aud": audience,
		"exp": now.Add(12 * time.Hour).Unix(),
		"sub": subject,
	}

	// JWT header
	headerJSON, _ := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64

	// Sign with ECDSA P-256
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, vapid.PrivateKey, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	// Encode r and s as fixed-size 32-byte big-endian (IEEE P1363 format)
	sig := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):64], sBytes)

	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	jwt := signingInput + "." + sigB64

	return "vapid t=" + jwt + ", k=" + vapid.PublicKey, nil
}

// Subscriptions returns the current list of push subscriptions.
func (w *WebPushChannel) Subscriptions() []PushSubscription {
	return w.subscriptions
}

// SubscriptionCount returns the number of active subscriptions.
func (w *WebPushChannel) SubscriptionCount() int {
	return len(w.subscriptions)
}

// PublicKey returns the VAPID public key for the frontend.
func (w *WebPushChannel) PublicKey() string {
	if w.vapid == nil {
		return ""
	}
	return w.vapid.PublicKey
}
