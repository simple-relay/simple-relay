package signature

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

func TestForProvider(t *testing.T) {
	tests := []struct {
		provider model.SignatureProvider
		wantNil  bool
		wantErr  error
	}{
		{provider: model.SignatureProviderNone, wantNil: true, wantErr: nil},
		{provider: "", wantNil: true, wantErr: nil},
		{provider: model.SignatureProviderStripe, wantNil: false, wantErr: nil},
		{provider: model.SignatureProviderGitHub, wantNil: false, wantErr: nil},
		{provider: model.SignatureProviderHMACSHA256, wantNil: false, wantErr: nil},
		{provider: model.SignatureProviderEd25519, wantNil: false, wantErr: nil},
		{provider: model.SignatureProviderRSASHA256, wantNil: false, wantErr: nil},
		{provider: "unknown", wantNil: true, wantErr: ErrUnsupportedProvider},
	}

	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			v, err := ForProvider(tt.provider)
			if err != tt.wantErr {
				t.Fatalf("ForProvider(%s) error = %v, wantErr = %v", tt.provider, err, tt.wantErr)
			}
			if (v == nil) != tt.wantNil {
				t.Fatalf("ForProvider(%s) nil = %v, wantNil = %v", tt.provider, v == nil, tt.wantNil)
			}
		})
	}
}

func TestStripeVerifier(t *testing.T) {
	secret := "whsec_test_secret_12345"
	rawBody := []byte(`{"id":"evt_123","object":"event","type":"payment_intent.succeeded"}`)
	timestamp := "1614295982"

	// Generate valid HMAC
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(rawBody)
	validSig := hex.EncodeToString(mac.Sum(nil))

	v := &stripeVerifier{}

	t.Run("Valid single v1 signature", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("Stripe-Signature", fmt.Sprintf("t=%s,v1=%s", timestamp, validSig))
		if err := v.Verify(hdr, secret, rawBody); err != nil {
			t.Fatalf("expected valid verification, got: %v", err)
		}
	})

	t.Run("Valid multiple v1 signatures during secret rotation", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("Stripe-Signature", fmt.Sprintf("t=%s,v1=deadbeefdeadbeef,v1=%s,v0=something", timestamp, validSig))
		if err := v.Verify(hdr, secret, rawBody); err != nil {
			t.Fatalf("expected valid verification with multiple v1s, got: %v", err)
		}
	})

	t.Run("Signature mismatch", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("Stripe-Signature", fmt.Sprintf("t=%s,v1=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", timestamp))
		if err := v.Verify(hdr, secret, rawBody); err != ErrSignatureMismatch {
			t.Fatalf("expected ErrSignatureMismatch, got: %v", err)
		}
	})

	t.Run("Missing Stripe-Signature header", func(t *testing.T) {
		hdr := http.Header{}
		if err := v.Verify(hdr, secret, rawBody); err != ErrMissingSignatureHeader {
			t.Fatalf("expected ErrMissingSignatureHeader, got: %v", err)
		}
	})

	t.Run("Malformed header - missing timestamp", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("Stripe-Signature", fmt.Sprintf("v1=%s", validSig))
		if err := v.Verify(hdr, secret, rawBody); err != ErrInvalidSignatureFormat {
			t.Fatalf("expected ErrInvalidSignatureFormat, got: %v", err)
		}
	})

	t.Run("Malformed header - missing v1", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("Stripe-Signature", fmt.Sprintf("t=%s", timestamp))
		if err := v.Verify(hdr, secret, rawBody); err != ErrInvalidSignatureFormat {
			t.Fatalf("expected ErrInvalidSignatureFormat, got: %v", err)
		}
	})

	t.Run("Missing secret", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("Stripe-Signature", fmt.Sprintf("t=%s,v1=%s", timestamp, validSig))
		if err := v.Verify(hdr, "", rawBody); err != ErrMissingSecret {
			t.Fatalf("expected ErrMissingSecret, got: %v", err)
		}
	})
}

func TestGitHubVerifier(t *testing.T) {
	secret := "gh_webhook_secret_999"
	rawBody := []byte(`{"action":"opened","issue":{"number":42}}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	validSig := hex.EncodeToString(mac.Sum(nil))

	v := &githubVerifier{}

	t.Run("Valid X-Hub-Signature-256", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Hub-Signature-256", "sha256="+validSig)
		if err := v.Verify(hdr, secret, rawBody); err != nil {
			t.Fatalf("expected valid verification, got: %v", err)
		}
	})

	t.Run("Valid fallback X-Hub-Signature", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Hub-Signature", "sha256="+validSig)
		if err := v.Verify(hdr, secret, rawBody); err != nil {
			t.Fatalf("expected valid fallback verification, got: %v", err)
		}
	})

	t.Run("Signature mismatch", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Hub-Signature-256", "sha256=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
		if err := v.Verify(hdr, secret, rawBody); err != ErrSignatureMismatch {
			t.Fatalf("expected ErrSignatureMismatch, got: %v", err)
		}
	})

	t.Run("Missing prefix sha256=", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Hub-Signature-256", validSig)
		if err := v.Verify(hdr, secret, rawBody); err != ErrInvalidSignatureFormat {
			t.Fatalf("expected ErrInvalidSignatureFormat, got: %v", err)
		}
	})

	t.Run("Invalid hex digest length", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Hub-Signature-256", "sha256=1234")
		if err := v.Verify(hdr, secret, rawBody); err != ErrInvalidSignatureFormat {
			t.Fatalf("expected ErrInvalidSignatureFormat, got: %v", err)
		}
	})

	t.Run("Missing header", func(t *testing.T) {
		hdr := http.Header{}
		if err := v.Verify(hdr, secret, rawBody); err != ErrMissingSignatureHeader {
			t.Fatalf("expected ErrMissingSignatureHeader, got: %v", err)
		}
	})

	t.Run("Missing secret", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Hub-Signature-256", "sha256="+validSig)
		if err := v.Verify(hdr, "  ", rawBody); err != ErrMissingSecret {
			t.Fatalf("expected ErrMissingSecret, got: %v", err)
		}
	})
}

func TestGenericHMACVerifier(t *testing.T) {
	secret := "generic_super_secret"
	rawBody := []byte(`{"user_id":"u_123","event":"order_paid"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	rawMac := mac.Sum(nil)
	hexSig := hex.EncodeToString(rawMac)
	b64StdSig := base64.StdEncoding.EncodeToString(rawMac)
	b64URLSig := base64.URLEncoding.EncodeToString(rawMac)

	v := &genericHMACVerifier{}

	t.Run("Valid hex with sha256= prefix on X-Signature", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Signature", "sha256="+hexSig)
		if err := v.Verify(hdr, secret, rawBody); err != nil {
			t.Fatalf("expected valid verification, got: %v", err)
		}
	})

	t.Run("Valid hex without prefix on X-Signature-SHA256", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Signature-SHA256", hexSig)
		if err := v.Verify(hdr, secret, rawBody); err != nil {
			t.Fatalf("expected valid verification, got: %v", err)
		}
	})

	t.Run("Valid Base64 standard encoding", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Signature", b64StdSig)
		if err := v.Verify(hdr, secret, rawBody); err != nil {
			t.Fatalf("expected valid verification, got: %v", err)
		}
	})

	t.Run("Valid Base64 URL-safe encoding", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Signature", b64URLSig)
		if err := v.Verify(hdr, secret, rawBody); err != nil {
			t.Fatalf("expected valid verification, got: %v", err)
		}
	})

	t.Run("Header priority order", func(t *testing.T) {
		hdr := http.Header{}
		// X-Signature is checked first; give it invalid signature, give X-Signature-SHA256 valid
		hdr.Set("X-Signature", "sha256=0000000000000000000000000000000000000000000000000000000000000000")
		hdr.Set("X-Signature-SHA256", hexSig)
		if err := v.Verify(hdr, secret, rawBody); err != ErrSignatureMismatch {
			t.Fatalf("expected X-Signature to be evaluated first and mismatch, got: %v", err)
		}
	})

	t.Run("Invalid signature format", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Signature", "not-hex-and-not-valid-base64-length!")
		if err := v.Verify(hdr, secret, rawBody); err != ErrInvalidSignatureFormat {
			t.Fatalf("expected ErrInvalidSignatureFormat, got: %v", err)
		}
	})

	t.Run("Missing signature header", func(t *testing.T) {
		hdr := http.Header{}
		if err := v.Verify(hdr, secret, rawBody); err != ErrMissingSignatureHeader {
			t.Fatalf("expected ErrMissingSignatureHeader, got: %v", err)
		}
	})

	t.Run("Missing secret", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Signature", hexSig)
		if err := v.Verify(hdr, "", rawBody); err != ErrMissingSecret {
			t.Fatalf("expected ErrMissingSecret, got: %v", err)
		}
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func generateTestCertPEM(t *testing.T, privKey *rsa.PrivateKey) []byte {
	t.Helper()
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Amazon Web Services"},
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
}

func TestEd25519Verifier(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}

	pubKeyHex := hex.EncodeToString(pubKey)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)

	pubDER, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		t.Fatalf("failed to marshal ed25519 public key: %v", err)
	}
	pubKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	rawBody := []byte(`{"event":"user.created","id":"usr_123"}`)
	v := &ed25519Verifier{}

	t.Run("Standard Webhooks valid signature (Hex public key)", func(t *testing.T) {
		msgID := "msg_p12345"
		timestamp := "1710000000"
		signedPayload := fmt.Sprintf("%s.%s.%s", msgID, timestamp, string(rawBody))
		sig := ed25519.Sign(privKey, []byte(signedPayload))
		sigB64 := base64.StdEncoding.EncodeToString(sig)

		hdr := http.Header{}
		hdr.Set("webhook-id", msgID)
		hdr.Set("webhook-timestamp", timestamp)
		hdr.Set("webhook-signature", fmt.Sprintf("v1a,%s", sigB64))

		if err := v.Verify(hdr, pubKeyHex, rawBody); err != nil {
			t.Fatalf("expected valid standard webhooks verification, got: %v", err)
		}
	})

	t.Run("Standard Webhooks with signature rotation (Base64 public key)", func(t *testing.T) {
		msgID := "msg_rot"
		timestamp := "1710000005"
		signedPayload := fmt.Sprintf("%s.%s.%s", msgID, timestamp, string(rawBody))
		sig := ed25519.Sign(privKey, []byte(signedPayload))
		sigB64 := base64.StdEncoding.EncodeToString(sig)

		hdr := http.Header{}
		hdr.Set("webhook-id", msgID)
		hdr.Set("webhook-timestamp", timestamp)
		dummySigB64 := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		hdr.Set("webhook-signature", fmt.Sprintf("v1a,%s v1a,%s", dummySigB64, sigB64))

		if err := v.Verify(hdr, pubKeyB64, rawBody); err != nil {
			t.Fatalf("expected valid rotation verification, got: %v", err)
		}
	})

	t.Run("Standard Webhooks missing timestamp or id", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("webhook-id", "msg_test")
		hdr.Set("webhook-signature", "v1a,someSig")
		if err := v.Verify(hdr, pubKeyHex, rawBody); err != ErrInvalidSignatureFormat {
			t.Fatalf("expected ErrInvalidSignatureFormat, got: %v", err)
		}
	})

	t.Run("Standard Webhooks signature mismatch", func(t *testing.T) {
		dummySigB64 := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		hdr := http.Header{}
		hdr.Set("webhook-id", "msg_test")
		hdr.Set("webhook-timestamp", "1710000000")
		hdr.Set("webhook-signature", fmt.Sprintf("v1a,%s", dummySigB64))
		if err := v.Verify(hdr, pubKeyHex, rawBody); err != ErrSignatureMismatch {
			t.Fatalf("expected ErrSignatureMismatch, got: %v", err)
		}
	})

	t.Run("Standard Webhooks invalid non-numeric timestamp", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("webhook-id", "msg_test")
		hdr.Set("webhook-timestamp", "not-a-number")
		hdr.Set("webhook-signature", "v1a,someSig")
		if err := v.Verify(hdr, pubKeyHex, rawBody); err != ErrInvalidSignatureFormat {
			t.Fatalf("expected ErrInvalidSignatureFormat, got: %v", err)
		}
	})

	t.Run("Standard Webhooks candidates capped at 5", func(t *testing.T) {
		msgID := "msg_cap"
		timestamp := "1710000010"
		signedPayload := fmt.Sprintf("%s.%s.%s", msgID, timestamp, string(rawBody))
		sig := ed25519.Sign(privKey, []byte(signedPayload))
		sigB64 := base64.StdEncoding.EncodeToString(sig)
		dummySigB64 := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))

		// 5 dummy candidates + valid candidate at index 6
		sigHeaderVal := fmt.Sprintf("v1a,%s v1a,%s v1a,%s v1a,%s v1a,%s v1a,%s",
			dummySigB64, dummySigB64, dummySigB64, dummySigB64, dummySigB64, sigB64)

		hdr := http.Header{}
		hdr.Set("webhook-id", msgID)
		hdr.Set("webhook-timestamp", timestamp)
		hdr.Set("webhook-signature", sigHeaderVal)

		if err := v.Verify(hdr, pubKeyHex, rawBody); err != ErrSignatureMismatch {
			t.Fatalf("expected ErrSignatureMismatch due to candidate capping, got: %v", err)
		}
	})

	t.Run("Generic Ed25519 with PEM public key", func(t *testing.T) {
		sig := ed25519.Sign(privKey, rawBody)
		sigB64 := base64.StdEncoding.EncodeToString(sig)

		hdr := http.Header{}
		hdr.Set("X-Signature-Ed25519", sigB64)

		if err := v.Verify(hdr, pubKeyPEM, rawBody); err != nil {
			t.Fatalf("expected valid generic ed25519 verification, got: %v", err)
		}
	})

	t.Run("Generic Ed25519 with prefix ed25519= and Hex signature", func(t *testing.T) {
		sig := ed25519.Sign(privKey, rawBody)
		sigHex := hex.EncodeToString(sig)

		hdr := http.Header{}
		hdr.Set("X-Signature", fmt.Sprintf("ed25519=%s", sigHex))

		if err := v.Verify(hdr, pubKeyHex, rawBody); err != nil {
			t.Fatalf("expected valid generic ed25519 verification, got: %v", err)
		}
	})

	t.Run("Generic Ed25519 missing header", func(t *testing.T) {
		hdr := http.Header{}
		if err := v.Verify(hdr, pubKeyHex, rawBody); err != ErrMissingSignatureHeader {
			t.Fatalf("expected ErrMissingSignatureHeader, got: %v", err)
		}
	})

	t.Run("Invalid public key", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Signature", "some-sig")
		if err := v.Verify(hdr, "not-a-valid-pubkey", rawBody); err != ErrInvalidPublicKey {
			t.Fatalf("expected ErrInvalidPublicKey, got: %v", err)
		}
	})
}

func TestRSAVerifier(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	certPEM := generateTestCertPEM(t, privKey)

	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal rsa public key: %v", err)
	}
	pubKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	// Setup custom cert cache for mock SNS download
	validSNSCertURL := "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem"
	fetchCount := 0
	mockClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			fetchCount++
			if req.URL.String() == validSNSCertURL {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(certPEM)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		}),
	}
	cache := NewCertCache(time.Hour, mockClient)
	v := &rsaVerifier{certCache: cache}

	t.Run("AWS SNS Notification with SignatureVersion 2 (SHA-256) and dynamic cert caching", func(t *testing.T) {
		msg := snsMessage{
			Type:             "Notification",
			Message:          "Hello AWS SNS",
			MessageId:        "msg-sns-12345",
			Timestamp:        "2026-10-14T12:00:00.000Z",
			TopicArn:         "arn:aws:sns:us-east-1:123456789012:test-topic",
			SigningCertURL:   validSNSCertURL,
			SignatureVersion: "2",
		}
		canonicalStr := buildSNSCanonicalString(&msg)
		h := sha256.Sum256([]byte(canonicalStr))
		sigBytes, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, h[:])
		if err != nil {
			t.Fatalf("failed to sign sns: %v", err)
		}
		msg.Signature = base64.StdEncoding.EncodeToString(sigBytes)
		rawBody, _ := json.Marshal(msg)

		hdr := http.Header{}
		// First call: cache miss, fetches cert
		if err := v.Verify(hdr, "", rawBody); err != nil {
			t.Fatalf("expected valid AWS SNS verification, got: %v", err)
		}
		if fetchCount != 1 {
			t.Fatalf("expected 1 fetch, got %d", fetchCount)
		}

		// Second call: cache hit, should NOT fetch again
		if err := v.Verify(hdr, "", rawBody); err != nil {
			t.Fatalf("expected valid cached AWS SNS verification, got: %v", err)
		}
		if fetchCount != 1 {
			t.Fatalf("expected cache hit with 1 fetch, got %d", fetchCount)
		}
	})

	t.Run("AWS SNS with SignatureVersion 1 (SHA-1)", func(t *testing.T) {
		msg := snsMessage{
			Type:             "Notification",
			Message:          "Hello AWS SNS v1",
			MessageId:        "msg-sns-v1-999",
			Timestamp:        "2026-10-14T12:05:00.000Z",
			TopicArn:         "arn:aws:sns:us-east-1:123456789012:test-topic",
			SigningCertURL:   validSNSCertURL,
			SignatureVersion: "1",
		}
		canonicalStr := buildSNSCanonicalString(&msg)
		h := sha1.Sum([]byte(canonicalStr))
		sigBytes, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA1, h[:])
		if err != nil {
			t.Fatalf("failed to sign sns v1: %v", err)
		}
		msg.Signature = base64.StdEncoding.EncodeToString(sigBytes)
		rawBody, _ := json.Marshal(msg)

		hdr := http.Header{}
		if err := v.Verify(hdr, "", rawBody); err != nil {
			t.Fatalf("expected valid AWS SNS v1 verification, got: %v", err)
		}
	})

	t.Run("AWS SNS with untrusted cert URL", func(t *testing.T) {
		msg := snsMessage{
			Type:             "Notification",
			Message:          "Hello Hacker",
			MessageId:        "msg-fake",
			Timestamp:        "2026-10-14T12:00:00.000Z",
			TopicArn:         "arn:aws:sns:us-east-1:123456789012:test-topic",
			SigningCertURL:   "https://evil-server.com/cert.pem",
			Signature:        "AAAA",
			SignatureVersion: "2",
		}
		rawBody, _ := json.Marshal(msg)
		hdr := http.Header{}
		if err := v.Verify(hdr, "", rawBody); err == nil {
			t.Fatal("expected failure on untrusted cert URL")
		}
	})

	t.Run("AWS SNS with static public key override (bypasses cert download)", func(t *testing.T) {
		msg := snsMessage{
			Type:             "Notification",
			Message:          "Hello Override",
			MessageId:        "msg-override",
			Timestamp:        "2026-10-14T12:10:00.000Z",
			TopicArn:         "arn:aws:sns:us-east-1:123456789012:test-topic",
			SigningCertURL:   "https://non-existent-or-unreachable.example.com",
			SignatureVersion: "2",
		}
		canonicalStr := buildSNSCanonicalString(&msg)
		h := sha256.Sum256([]byte(canonicalStr))
		sigBytes, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, h[:])
		if err != nil {
			t.Fatalf("failed to sign sns: %v", err)
		}
		msg.Signature = base64.StdEncoding.EncodeToString(sigBytes)
		rawBody, _ := json.Marshal(msg)

		hdr := http.Header{}
		// Uses static public key in secret, completely ignores SigningCertURL
		if err := v.Verify(hdr, pubKeyPEM, rawBody); err != nil {
			t.Fatalf("expected valid verification with static public key override, got: %v", err)
		}
	})

	t.Run("Generic RSA over raw body with X-Signature-RSA", func(t *testing.T) {
		rawPayload := []byte(`{"event":"generic.rsa.test"}`)
		h := sha256.Sum256(rawPayload)
		sigBytes, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, h[:])
		if err != nil {
			t.Fatalf("failed to sign raw body: %v", err)
		}
		sigB64 := base64.StdEncoding.EncodeToString(sigBytes)

		hdr := http.Header{}
		hdr.Set("X-Signature-RSA", fmt.Sprintf("rsa-sha256=%s", sigB64))

		if err := v.Verify(hdr, pubKeyPEM, rawPayload); err != nil {
			t.Fatalf("expected valid generic rsa verification, got: %v", err)
		}
	})

	t.Run("Generic RSA signature mismatch on tampered body", func(t *testing.T) {
		rawPayload := []byte(`{"event":"generic.rsa.test"}`)
		h := sha256.Sum256(rawPayload)
		sigBytes, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, h[:])
		if err != nil {
			t.Fatalf("failed to sign raw body: %v", err)
		}
		sigB64 := base64.StdEncoding.EncodeToString(sigBytes)

		hdr := http.Header{}
		hdr.Set("X-Signature-RSA", sigB64)

		tamperedPayload := []byte(`{"event":"tampered"}`)
		if err := v.Verify(hdr, pubKeyPEM, tamperedPayload); err != ErrSignatureMismatch {
			t.Fatalf("expected ErrSignatureMismatch, got: %v", err)
		}
	})

	t.Run("AWS SNS with invalid timestamp format", func(t *testing.T) {
		msg := snsMessage{
			Type:             "Notification",
			Message:          "Hello AWS SNS",
			MessageId:        "msg-sns-bad-time",
			Timestamp:        "invalid-timestamp-format",
			TopicArn:         "arn:aws:sns:us-east-1:123456789012:test-topic",
			SigningCertURL:   validSNSCertURL,
			Signature:        "AAAA",
			SignatureVersion: "2",
		}
		rawBody, _ := json.Marshal(msg)
		hdr := http.Header{}
		if err := v.Verify(hdr, "", rawBody); err != ErrInvalidSignatureFormat {
			t.Fatalf("expected ErrInvalidSignatureFormat for invalid timestamp, got: %v", err)
		}
	})

	t.Run("Generic RSA with Base64 signature length check", func(t *testing.T) {
		rawPayload := []byte(`{"event":"generic.rsa.b64check"}`)
		h := sha256.Sum256(rawPayload)
		sigBytes, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, h[:])
		if err != nil {
			t.Fatalf("failed to sign raw body: %v", err)
		}
		sigB64 := base64.StdEncoding.EncodeToString(sigBytes)

		hdr := http.Header{}
		hdr.Set("X-Signature", sigB64)

		if err := v.Verify(hdr, pubKeyPEM, rawPayload); err != nil {
			t.Fatalf("expected valid verification for Base64 signature, got: %v", err)
		}
	})
}

func TestCertCache_Hardening(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}
	certPEM := generateTestCertPEM(t, privKey)
	validSNSCertURL := "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-valid.pem"

	t.Run("Singleflight deduplication on concurrent fetches", func(t *testing.T) {
		var fetchCount int64
		mockClient := &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				atomic.AddInt64(&fetchCount, 1)
				time.Sleep(30 * time.Millisecond)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(certPEM)),
					Header:     make(http.Header),
				}, nil
			}),
		}
		cache := NewCertCache(time.Hour, mockClient)

		var wg sync.WaitGroup
		concurrency := 10
		errorsCh := make(chan error, concurrency)

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pk, err := cache.GetOrFetch(validSNSCertURL)
				if err != nil {
					errorsCh <- err
					return
				}
				if pk == nil {
					errorsCh <- errors.New("nil public key returned")
				}
			}()
		}

		wg.Wait()
		close(errorsCh)

		for err := range errorsCh {
			t.Fatalf("concurrent GetOrFetch returned error: %v", err)
		}

		if count := atomic.LoadInt64(&fetchCount); count != 1 {
			t.Fatalf("expected singleflight to execute exactly 1 HTTP dial, got %d", count)
		}
	})

	t.Run("SSRF URL whitelist and query parameter rejection", func(t *testing.T) {
		cache := NewCertCache(time.Hour, nil)

		tests := []string{
			"https://sns.us-east-1.amazonaws.com/cert.pem?leak=1",
			"https://sns.us-east-1.amazonaws.com/cert.pem#fragment",
			"https://sns.us-east-1.amazonaws.com/cert.txt",
			"http://sns.us-east-1.amazonaws.com/cert.pem",
			"https://evil-server.com/cert.pem",
			"https://sns.us-east-1.amazonaws.com.evil.com/cert.pem",
		}

		for _, url := range tests {
			_, err := cache.GetOrFetch(url)
			if err != ErrUntrustedCertURL {
				t.Fatalf("expected ErrUntrustedCertURL for %s, got: %v", url, err)
			}
		}
	})

	t.Run("Redirect blocking", func(t *testing.T) {
		redirectURL := "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-redirect.pem"
		mockClient := &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusFound,
					Header: http.Header{
						"Location": []string{"http://169.254.169.254/metadata"},
					},
					Body: io.NopCloser(bytes.NewReader(nil)),
				}, nil
			}),
		}
		cache := NewCertCache(time.Hour, mockClient)
		_, err := cache.GetOrFetch(redirectURL)
		if err == nil {
			t.Fatal("expected error on HTTP 302 redirect, got nil")
		}
	})
}
