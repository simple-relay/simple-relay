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
	snsCertURLPattern = regexp.MustCompile(`^https://sns\.[a-z0-9\-]+(\.amazonaws\.com|\.amazonaws\.com\.cn)/[^?#]+\.pem$`)
)

const (
	defaultCertTTL       = 24 * time.Hour
	maxCertSizeBytes     = 1024 * 1024 // 1 MB limit to prevent memory bloat
	maxCertCacheEntries  = 100
	certDownloadTimeout  = 3 * time.Second
)

type cachedCert struct {
	pubKey    *rsa.PublicKey
	expiresAt time.Time
}

type flightCall struct {
	wg  sync.WaitGroup
	val *rsa.PublicKey
	err error
}

// CertCache provides thread-safe in-memory caching of X.509 public keys with TTL.
type CertCache struct {
	mu       sync.RWMutex
	entries  map[string]cachedCert
	ttl      time.Duration
	client   *http.Client
	flightMu sync.Mutex
	flight   map[string]*flightCall
}

// NewCertCache creates an initialized CertCache.
func NewCertCache(ttl time.Duration, client *http.Client) *CertCache {
	if ttl <= 0 {
		ttl = defaultCertTTL
	}
	if client == nil {
		client = &http.Client{
			Timeout: certDownloadTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	} else if client.CheckRedirect == nil {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return &CertCache{
		entries: make(map[string]cachedCert),
		ttl:     ttl,
		client:  client,
		flight:  make(map[string]*flightCall),
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

	// Singleflight deduplication to avoid concurrent duplicate dials
	c.flightMu.Lock()
	if call, active := c.flight[certURL]; active {
		c.flightMu.Unlock()
		call.wg.Wait()
		return call.val, call.err
	}

	call := &flightCall{}
	call.wg.Add(1)
	c.flight[certURL] = call
	c.flightMu.Unlock()

	// Perform HTTP dial outside any locks
	pubKey, err := c.fetchAndParse(certURL)

	c.flightMu.Lock()
	delete(c.flight, certURL)
	call.val = pubKey
	call.err = err
	call.wg.Done()
	c.flightMu.Unlock()

	if err != nil {
		return nil, err
	}

	// Update cache under write lock with size bounds
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= maxCertCacheEntries {
		cleanTime := time.Now()
		for k, v := range c.entries {
			if v.expiresAt.Before(cleanTime) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= maxCertCacheEntries {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
	}

	c.entries[certURL] = cachedCert{
		pubKey:    pubKey,
		expiresAt: time.Now().Add(c.ttl),
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
