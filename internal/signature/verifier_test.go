package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"

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
