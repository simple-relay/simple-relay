package signature

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	// ErrInvalidRSAPublicKey is returned when the configured RSA public key cannot be parsed.
	ErrInvalidRSAPublicKey = errors.New("invalid rsa public key")

	rsaCandidateHeaders = []string{
		"X-Signature-RSA",
		"X-Signature",
	}
)

type rsaVerifier struct {
	certCache *CertCache
}

type snsMessage struct {
	Type             string `json:"Type"`
	Message          string `json:"Message"`
	MessageId        string `json:"MessageId"`
	Subject          string `json:"Subject,omitempty"`
	SubscribeURL     string `json:"SubscribeURL,omitempty"`
	Timestamp        string `json:"Timestamp"`
	Token            string `json:"Token,omitempty"`
	TopicArn         string `json:"TopicArn"`
	SigningCertURL   string `json:"SigningCertURL"`
	Signature        string `json:"Signature"`
	SignatureVersion string `json:"SignatureVersion"`
}

// Verify validates RSA-SHA256 webhook signatures for AWS SNS and Generic RSA.
func (v *rsaVerifier) Verify(header http.Header, secret string, rawBody []byte) error {
	// Case 1: Check if this is an AWS SNS JSON message
	var msg snsMessage
	if err := json.Unmarshal(rawBody, &msg); err == nil && msg.SigningCertURL != "" && msg.Signature != "" {
		return v.verifyAWSSNS(&msg, secret)
	}

	// Case 2: Generic RSA over raw body
	return v.verifyGenericRSA(header, secret, rawBody)
}

func (v *rsaVerifier) verifyAWSSNS(msg *snsMessage, secret string) error {
	var (
		pubKey *rsa.PublicKey
		err    error
	)

	// Static public key override from route configuration
	if strings.TrimSpace(secret) != "" {
		pubKey, err = parseRSAPublicKey(secret)
		if err != nil {
			return err
		}
	} else {
		// Fetch with URL whitelist and in-memory TTL caching
		cache := v.certCache
		if cache == nil {
			cache = defaultCertCache
		}
		pubKey, err = cache.GetOrFetch(msg.SigningCertURL)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSignatureFormat, err)
		}
	}

	canonicalStr := buildSNSCanonicalString(msg)
	if canonicalStr == "" {
		return ErrInvalidSignatureFormat
	}

	sigBytes, err := base64.StdEncoding.DecodeString(msg.Signature)
	if err != nil {
		return ErrInvalidSignatureFormat
	}

	// Dynamic hash branching based on SignatureVersion
	var (
		hashFunc  crypto.Hash
		hashBytes []byte
	)

	if msg.SignatureVersion == "1" {
		hashFunc = crypto.SHA1
		h := sha1.Sum([]byte(canonicalStr))
		hashBytes = h[:]
	} else {
		hashFunc = crypto.SHA256
		h := sha256.Sum256([]byte(canonicalStr))
		hashBytes = h[:]
	}

	if err := rsa.VerifyPKCS1v15(pubKey, hashFunc, hashBytes, sigBytes); err != nil {
		return ErrSignatureMismatch
	}

	return nil
}

func buildSNSCanonicalString(msg *snsMessage) string {
	var b strings.Builder

	switch msg.Type {
	case "Notification":
		b.WriteString("Message\n")
		b.WriteString(msg.Message)
		b.WriteString("\n")

		b.WriteString("MessageId\n")
		b.WriteString(msg.MessageId)
		b.WriteString("\n")

		if msg.Subject != "" {
			b.WriteString("Subject\n")
			b.WriteString(msg.Subject)
			b.WriteString("\n")
		}

		b.WriteString("Timestamp\n")
		b.WriteString(msg.Timestamp)
		b.WriteString("\n")

		b.WriteString("TopicArn\n")
		b.WriteString(msg.TopicArn)
		b.WriteString("\n")

		b.WriteString("Type\n")
		b.WriteString(msg.Type)
		b.WriteString("\n")

	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		b.WriteString("Message\n")
		b.WriteString(msg.Message)
		b.WriteString("\n")

		b.WriteString("MessageId\n")
		b.WriteString(msg.MessageId)
		b.WriteString("\n")

		b.WriteString("SubscribeURL\n")
		b.WriteString(msg.SubscribeURL)
		b.WriteString("\n")

		b.WriteString("Timestamp\n")
		b.WriteString(msg.Timestamp)
		b.WriteString("\n")

		b.WriteString("Token\n")
		b.WriteString(msg.Token)
		b.WriteString("\n")

		b.WriteString("TopicArn\n")
		b.WriteString(msg.TopicArn)
		b.WriteString("\n")

		b.WriteString("Type\n")
		b.WriteString(msg.Type)
		b.WriteString("\n")

	default:
		return ""
	}

	return b.String()
}

func (v *rsaVerifier) verifyGenericRSA(header http.Header, secret string, rawBody []byte) error {
	if err := checkSecret(secret); err != nil {
		return err
	}

	pubKey, err := parseRSAPublicKey(secret)
	if err != nil {
		return err
	}

	var sigHeader string
	for _, h := range rsaCandidateHeaders {
		val := strings.TrimSpace(header.Get(h))
		if val != "" {
			sigHeader = val
			break
		}
	}

	if sigHeader == "" {
		return ErrMissingSignatureHeader
	}

	// Strip optional "sha256=" or "rsa-sha256=" prefix
	lower := strings.ToLower(sigHeader)
	if strings.HasPrefix(lower, "sha256=") {
		sigHeader = strings.TrimSpace(sigHeader[len("sha256="):])
	} else if strings.HasPrefix(lower, "rsa-sha256=") {
		sigHeader = strings.TrimSpace(sigHeader[len("rsa-sha256="):])
	}

	var sigBytes []byte

	// Try Hex
	if b, err := hex.DecodeString(sigHeader); err == nil {
		sigBytes = b
	}

	// Try Base64
	if sigBytes == nil {
		for _, enc := range []*base64.Encoding{
			base64.StdEncoding,
			base64.URLEncoding,
			base64.RawStdEncoding,
			base64.RawURLEncoding,
		} {
			if b, err := enc.DecodeString(sigHeader); err == nil {
				sigBytes = b
				break
			}
		}
	}

	if sigBytes == nil {
		return ErrInvalidSignatureFormat
	}

	h := sha256.Sum256(rawBody)
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, h[:], sigBytes); err != nil {
		return ErrSignatureMismatch
	}

	return nil
}

func parseRSAPublicKey(secret string) (*rsa.PublicKey, error) {
	trimmed := strings.TrimSpace(secret)
	block, _ := pem.Decode([]byte(trimmed))
	var derBytes []byte
	if block != nil {
		derBytes = block.Bytes
	} else {
		// Attempt raw base64 DER
		b, err := base64.StdEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, ErrInvalidRSAPublicKey
		}
		derBytes = b
	}

	// Try PKIX
	if pub, err := x509.ParsePKIXPublicKey(derBytes); err == nil {
		if rsaKey, ok := pub.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}

	// Try PKCS#1
	if rsaKey, err := x509.ParsePKCS1PublicKey(derBytes); err == nil {
		return rsaKey, nil
	}

	// Try parsing full X.509 certificate
	if cert, err := x509.ParseCertificate(derBytes); err == nil {
		if rsaKey, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}

	return nil, ErrInvalidRSAPublicKey
}
