package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
)

type genericHMACVerifier struct{}

// candidateHeaders lists header keys inspected for generic HMAC signatures in priority order.
var candidateHeaders = []string{
	"X-Signature",
	"X-Hub-Signature-256",
	"X-Signature-SHA256",
}

// Verify validates generic HMAC-SHA256 signatures, auto-detecting hex or base64 encoding.
func (v *genericHMACVerifier) Verify(header http.Header, secret string, rawBody []byte) error {
	if err := checkSecret(secret); err != nil {
		return err
	}

	var sigHeader string
	for _, h := range candidateHeaders {
		val := strings.TrimSpace(header.Get(h))
		if val != "" {
			sigHeader = val
			break
		}
	}

	if sigHeader == "" {
		return ErrMissingSignatureHeader
	}

	// Strip optional "sha256=" or "sha-256=" prefix if present
	lower := strings.ToLower(sigHeader)
	if strings.HasPrefix(lower, "sha256=") {
		sigHeader = strings.TrimSpace(sigHeader[len("sha256="):])
	} else if strings.HasPrefix(lower, "sha-256=") {
		sigHeader = strings.TrimSpace(sigHeader[len("sha-256="):])
	}

	if sigHeader == "" {
		return ErrInvalidSignatureFormat
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	expectedBytes := mac.Sum(nil)

	// Attempt 1: Hex decoding (64 hex characters -> 32 bytes)
	if len(sigHeader) == 64 {
		decodedHex, err := hex.DecodeString(sigHeader)
		if err == nil && len(decodedHex) == 32 {
			if subtle.ConstantTimeCompare(expectedBytes, decodedHex) == 1 {
				return nil
			}
			return ErrSignatureMismatch
		}
	}

	// Attempt 2: Base64 decoding (32 bytes expected)
	var decodedB64 []byte
	var b64Err error

	if b, err := base64.StdEncoding.DecodeString(sigHeader); err == nil {
		decodedB64 = b
	} else if b, err := base64.URLEncoding.DecodeString(sigHeader); err == nil {
		decodedB64 = b
	} else if b, err := base64.RawStdEncoding.DecodeString(sigHeader); err == nil {
		decodedB64 = b
	} else if b, err := base64.RawURLEncoding.DecodeString(sigHeader); err == nil {
		decodedB64 = b
	} else {
		b64Err = err
	}

	if b64Err == nil && len(decodedB64) == 32 {
		if subtle.ConstantTimeCompare(expectedBytes, decodedB64) == 1 {
			return nil
		}
		return ErrSignatureMismatch
	}

	return ErrInvalidSignatureFormat
}
