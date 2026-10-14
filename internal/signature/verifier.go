package signature

import (
	"errors"
	"net/http"
	"strings"

	"github.com/simple-relay/simple-relay/internal/model"
)

var (
	// ErrMissingSignatureHeader is returned when expected signature headers are absent.
	ErrMissingSignatureHeader = errors.New("missing signature header")

	// ErrInvalidSignatureFormat is returned when signature format/structure is malformed.
	ErrInvalidSignatureFormat = errors.New("invalid signature format")

	// ErrSignatureMismatch is returned when cryptographic signature does not match payload.
	ErrSignatureMismatch = errors.New("signature mismatch")

	// ErrUnsupportedProvider is returned when provider is not recognized.
	ErrUnsupportedProvider = errors.New("unsupported signature provider")

	// ErrMissingSecret is returned when the route's signature secret is empty.
	ErrMissingSecret = errors.New("signature secret is required")
)

// Verifier defines the contract for verifying webhook payload authenticity.
type Verifier interface {
	Verify(header http.Header, secret string, rawBody []byte) error
}

var (
	defaultStripeVerifier      = &stripeVerifier{}
	defaultGitHubVerifier      = &githubVerifier{}
	defaultGenericHMACVerifier = &genericHMACVerifier{}
	defaultEd25519Verifier     = &ed25519Verifier{}
	defaultRSAVerifier         = &rsaVerifier{certCache: defaultCertCache}
)

// ForProvider returns the Verifier implementation corresponding to the given provider.
func ForProvider(provider model.SignatureProvider) (Verifier, error) {
	switch provider {
	case model.SignatureProviderNone, "":
		return nil, nil
	case model.SignatureProviderStripe:
		return defaultStripeVerifier, nil
	case model.SignatureProviderGitHub:
		return defaultGitHubVerifier, nil
	case model.SignatureProviderHMACSHA256:
		return defaultGenericHMACVerifier, nil
	case model.SignatureProviderEd25519:
		return defaultEd25519Verifier, nil
	case model.SignatureProviderRSASHA256:
		return defaultRSAVerifier, nil
	default:
		return nil, ErrUnsupportedProvider
	}
}

func checkSecret(secret string) error {
	if strings.TrimSpace(secret) == "" {
		return ErrMissingSecret
	}
	return nil
}
