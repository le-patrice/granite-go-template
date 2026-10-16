package acme

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"example.com/app/internal/platform/webhook"
)

const (
	// SignatureHeader carries "sha256=<hex HMAC-SHA256 of the raw body>".
	SignatureHeader = "X-Acme-Signature"

	signaturePrefix = "sha256="
	minSecretLength = 16
)

// Verifier checks Acme's HMAC-SHA256 webhook signature.
type Verifier struct {
	secret []byte
}

// NewVerifier returns a Verifier for secret, which must be at least 16 characters.
func NewVerifier(secret string) (*Verifier, error) {
	if len(secret) < minSecretLength {
		return nil, errors.New("acme: webhook secret must be at least 16 characters")
	}
	return &Verifier{secret: []byte(secret)}, nil
}

// Verify implements webhook.Verifier. The comparison is constant-time.
func (v *Verifier) Verify(body []byte, header http.Header) error {
	encoded, ok := strings.CutPrefix(header.Get(SignatureHeader), signaturePrefix)
	if !ok {
		return webhook.ErrInvalidSignature
	}
	got, err := hex.DecodeString(encoded)
	if err != nil {
		return webhook.ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return webhook.ErrInvalidSignature
	}
	return nil
}

// Sign returns the header value Acme would send for body. It is meant for
// tests and local tooling.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// WebhookSource returns the webhook.Source to register under "acme".
func WebhookSource(secret string) (webhook.Source, error) {
	verifier, err := NewVerifier(secret)
	if err != nil {
		return webhook.Source{}, err
	}
	return webhook.Source{Verifier: verifier, ExternalID: ExternalID}, nil
}
