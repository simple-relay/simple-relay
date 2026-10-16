package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

// GenerateRawKey creates a new random API key with sr_live_ prefix and returns the raw key,
// its corresponding model.APIKey entity with SHA-256 hash and masked representation.
func GenerateRawKey(name string) (string, *model.APIKey, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate secure random bytes: %w", err)
	}

	rawKey := "sr_live_" + hex.EncodeToString(bytes)
	keyHash := HashKey(rawKey)
	maskedKey := MaskKey(rawKey)

	apiKey := &model.APIKey{
		ID:        model.NewULID(),
		Name:      name,
		KeyHash:   keyHash,
		MaskedKey: maskedKey,
		CreatedAt: time.Now().UTC(),
	}

	return rawKey, apiKey, nil
}

// HashKey computes the hex-encoded SHA-256 hash of a raw API key token.
func HashKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

// MaskKey formats a raw API key token as AAA*****BBBB to safely display as a hint.
func MaskKey(rawKey string) string {
	if len(rawKey) < 15 {
		return "*****"
	}
	return rawKey[:11] + "*****" + rawKey[len(rawKey)-4:]
}
