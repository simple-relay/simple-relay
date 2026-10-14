package signature

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

var (
	// ErrUntrustedCertURL is returned when certificate URL fails hostname/protocol whitelist checks.
	ErrUntrustedCertURL = errors.New("untrusted certificate url")

	// ErrCertDownloadFailed is returned when downloading the certificate fails.
	ErrCertDownloadFailed = errors.New("failed to download certificate")

	// ErrInvalidCertificate is returned when the certificate cannot be parsed as a valid RSA cert.
	ErrInvalidCertificate = errors.New("invalid rsa certificate")

	// snsCertURLPattern strictly enforces https and official AWS SNS domain format.
	snsCertURLPattern = regexp.MustCompile(`^https://sns\.[a-z0-9\-]+\.amazonaws\.com/`)
)

const (
	defaultCertTTL      = 24 * time.Hour
	maxCertSizeBytes    = 1024 * 1024 // 1 MB limit to prevent memory bloat
	certDownloadTimeout = 3 * time.Second
)

type cachedCert struct {
	pubKey    *rsa.PublicKey
	expiresAt time.Time
}

// CertCache provides thread-safe in-memory caching of X.509 public keys with TTL.
type CertCache struct {
	mu      sync.RWMutex
	entries map[string]cachedCert
	ttl     time.Duration
	client  *http.Client
}

// NewCertCache creates an initialized CertCache.
func NewCertCache(ttl time.Duration, client *http.Client) *CertCache {
	if ttl <= 0 {
		ttl = defaultCertTTL
	}
	if client == nil {
		client = &http.Client{
			Timeout: certDownloadTimeout,
		}
	}
	return &CertCache{
		entries: make(map[string]cachedCert),
		ttl:     ttl,
		client:  client,
	}
}

var defaultCertCache = NewCertCache(defaultCertTTL, nil)

// GetOrFetch returns the cached RSA public key or downloads and caches it if missing/expired.
func (c *CertCache) GetOrFetch(certURL string) (*rsa.PublicKey, error) {
	if !snsCertURLPattern.MatchString(certURL) {
		return nil, ErrUntrustedCertURL
	}

	now := time.Now()

	// Fast read path
	c.mu.RLock()
	entry, ok := c.entries[certURL]
	if ok && entry.expiresAt.After(now) {
		c.mu.RUnlock()
		return entry.pubKey, nil
	}
	c.mu.RUnlock()

	// Slow write path
	c.mu.Lock()
	defer c.mu.Unlock()

	// Recheck after acquiring exclusive lock
	entry, ok = c.entries[certURL]
	if ok && entry.expiresAt.After(now) {
		return entry.pubKey, nil
	}

	pubKey, err := c.fetchAndParse(certURL)
	if err != nil {
		return nil, err
	}

	c.entries[certURL] = cachedCert{
		pubKey:    pubKey,
		expiresAt: now.Add(c.ttl),
	}

	return pubKey, nil
}

func (c *CertCache) fetchAndParse(certURL string) (*rsa.PublicKey, error) {
	resp, err := c.client.Get(certURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCertDownloadFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrCertDownloadFailed, resp.StatusCode)
	}

	certBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxCertSizeBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCertDownloadFailed, err)
	}

	block, _ := pem.Decode(certBytes)
	if block == nil {
		return nil, ErrInvalidCertificate
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
	}

	pubKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: not an rsa public key", ErrInvalidCertificate)
	}

	return pubKey, nil
}
