package signature

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	// ErrInvalidPublicKey is returned when the configured public key is malformed.
	ErrInvalidPublicKey = errors.New("invalid ed25519 public key")

	ed25519CandidateHeaders = []string{
		"X-Signature-Ed25519",
		"X-Signature",
		"X-Ed25519-Signature",
	}
)

type ed25519Verifier struct{}

// Verify validates Ed25519 webhook signatures for Standard Webhooks and Generic Ed25519.
func (v *ed25519Verifier) Verify(header http.Header, secret string, rawBody []byte) error {
	if err := checkSecret(secret); err != nil {
		return err
	}

	pubKey, err := parseEd25519PublicKey(secret)
	if err != nil {
		return err
	}

	// Path A: Standard Webhooks (Svix / Clerk / Resend)
	standardSig := strings.TrimSpace(header.Get("webhook-signature"))
	if standardSig != "" {
		return verifyStandardWebhooksEd25519(header, pubKey, rawBody, standardSig)
	}

	// Path B: Generic Ed25519 on raw body
	return verifyGenericEd25519(header, pubKey, rawBody)
}

func parseEd25519PublicKey(secret string) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(secret)

	// Attempt 1: PEM format
	if strings.Contains(trimmed, "BEGIN PUBLIC KEY") {
		block, _ := pem.Decode([]byte(trimmed))
		if block != nil {
			parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err == nil {
				if pk, ok := parsed.(ed25519.PublicKey); ok && len(pk) == ed25519.PublicKeySize {
					return pk, nil
				}
			}
		}
	}

	// Attempt 2: Hex (64 hex characters -> 32 bytes)
	if len(trimmed) == ed25519.PublicKeySize*2 {
		if b, err := hex.DecodeString(trimmed); err == nil && len(b) == ed25519.PublicKeySize {
			return ed25519.PublicKey(b), nil
		}
	}

	// Attempt 3: Base64 (Standard or URL-safe, padded or unpadded)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(trimmed); err == nil && len(b) == ed25519.PublicKeySize {
			return ed25519.PublicKey(b), nil
		}
	}

	return nil, ErrInvalidPublicKey
}

func verifyStandardWebhooksEd25519(header http.Header, pubKey ed25519.PublicKey, rawBody []byte, sigHeader string) error {
	msgID := strings.TrimSpace(header.Get("webhook-id"))
	timestamp := strings.TrimSpace(header.Get("webhook-timestamp"))
	if msgID == "" || timestamp == "" {
		return ErrInvalidSignatureFormat
	}

	signedPayload := fmt.Sprintf("%s.%s.%s", msgID, timestamp, string(rawBody))
	signedBytes := []byte(signedPayload)

	// Signatures are space-separated: "v1a,sig1 v1,sig2"
	tokens := strings.Fields(sigHeader)
	var candidates [][]byte

	for _, token := range tokens {
		parts := strings.SplitN(token, ",", 2)
		if len(parts) == 2 && parts[0] == "v1a" {
			sigCandidate := parts[1]
			for _, enc := range []*base64.Encoding{
				base64.StdEncoding,
				base64.URLEncoding,
				base64.RawStdEncoding,
				base64.RawURLEncoding,
			} {
				if b, err := enc.DecodeString(sigCandidate); err == nil && len(b) == ed25519.SignatureSize {
					candidates = append(candidates, b)
					break
				}
			}
		}
	}

	if len(candidates) == 0 {
		return ErrInvalidSignatureFormat
	}

	for _, sig := range candidates {
		if ed25519.Verify(pubKey, signedBytes, sig) {
			return nil
		}
	}

	return ErrSignatureMismatch
}

func verifyGenericEd25519(header http.Header, pubKey ed25519.PublicKey, rawBody []byte) error {
	var sigHeader string
	for _, h := range ed25519CandidateHeaders {
		val := strings.TrimSpace(header.Get(h))
		if val != "" {
			sigHeader = val
			break
		}
	}

	if sigHeader == "" {
		return ErrMissingSignatureHeader
	}

	// Strip optional "ed25519=" prefix
	lower := strings.ToLower(sigHeader)
	if strings.HasPrefix(lower, "ed25519=") {
		sigHeader = strings.TrimSpace(sigHeader[len("ed25519="):])
	}

	if sigHeader == "" {
		return ErrInvalidSignatureFormat
	}

	var sigBytes []byte

	// Try Hex (128 hex chars -> 64 bytes)
	if len(sigHeader) == ed25519.SignatureSize*2 {
		if b, err := hex.DecodeString(sigHeader); err == nil && len(b) == ed25519.SignatureSize {
			sigBytes = b
		}
	}

	// Try Base64
	if sigBytes == nil {
		for _, enc := range []*base64.Encoding{
			base64.StdEncoding,
			base64.URLEncoding,
			base64.RawStdEncoding,
			base64.RawURLEncoding,
		} {
			if b, err := enc.DecodeString(sigHeader); err == nil && len(b) == ed25519.SignatureSize {
				sigBytes = b
				break
			}
		}
	}

	if len(sigBytes) != ed25519.SignatureSize {
		return ErrInvalidSignatureFormat
	}

	if ed25519.Verify(pubKey, rawBody, sigBytes) {
		return nil
	}

	return ErrSignatureMismatch
}
