package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

type githubVerifier struct{}

// Verify validates GitHub webhook signatures formatted as sha256=<hex_digest>.
func (v *githubVerifier) Verify(header http.Header, secret string, rawBody []byte) error {
	if err := checkSecret(secret); err != nil {
		return err
	}

	sigHeader := strings.TrimSpace(header.Get("X-Hub-Signature-256"))
	if sigHeader == "" {
		// Fallback to X-Hub-Signature if X-Hub-Signature-256 is missing
		sigHeader = strings.TrimSpace(header.Get("X-Hub-Signature"))
		if sigHeader == "" {
			return ErrMissingSignatureHeader
		}
	}

	const prefix = "sha256="
	if !strings.HasPrefix(strings.ToLower(sigHeader), prefix) {
		return ErrInvalidSignatureFormat
	}

	hexDigest := strings.TrimSpace(sigHeader[len(prefix):])
	if len(hexDigest) != 64 {
		return ErrInvalidSignatureFormat
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	expectedHex := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(strings.ToLower(hexDigest)), []byte(expectedHex)) != 1 {
		return ErrSignatureMismatch
	}

	return nil
}
