package model

import (
	"errors"
	"strings"
	"time"
)

// APIKey represents an administrative API key credential.
type APIKey struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	KeyHash   string     `json:"-"`
	MaskedKey string     `json:"masked_key"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// CreateKeyResponse is returned exclusively upon key creation to reveal the raw token once.
type CreateKeyResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	MaskedKey string    `json:"masked_key"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks essential constraints for an API key entity.
func (k *APIKey) Validate() error {
	if strings.TrimSpace(k.ID) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(k.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(k.KeyHash) == "" {
		return errors.New("key_hash is required")
	}
	if strings.TrimSpace(k.MaskedKey) == "" {
		return errors.New("masked_key is required")
	}
	return nil
}
