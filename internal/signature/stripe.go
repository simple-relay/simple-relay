package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

type stripeVerifier struct{}

// Verify validates Stripe webhook signatures formatted as t=<timestamp>,v1=<sig>.
func (v *stripeVerifier) Verify(header http.Header, secret string, rawBody []byte) error {
	if err := checkSecret(secret); err != nil {
		return err
	}

	sigHeader := strings.TrimSpace(header.Get("Stripe-Signature"))
	if sigHeader == "" {
		return ErrMissingSignatureHeader
	}

	var timestamp string
	var signatures []string

	items := strings.Split(sigHeader, ",")
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "t":
			timestamp = val
		case "v1":
			if val != "" {
				signatures = append(signatures, val)
			}
		}
	}

	if timestamp == "" || len(signatures) == 0 {
		return ErrInvalidSignatureFormat
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(rawBody)
	expectedHex := hex.EncodeToString(mac.Sum(nil))

	expectedBytes := []byte(expectedHex)
	matched := false
	for _, sig := range signatures {
		if subtle.ConstantTimeCompare([]byte(sig), expectedBytes) == 1 {
			matched = true
			break
		}
	}

	if !matched {
		return ErrSignatureMismatch
	}

	return nil
}
