package model

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	ulidMu      sync.Mutex
	ulidEntropy = ulid.Monotonic(rand.Reader, 0)
)

// NewULID generates a monotonic, lowercase ULID string.
// Monotonic ULIDs ensure strict chronological sorting and prevent SQLite B-Tree index fragmentation.
func NewULID() string {
	ulidMu.Lock()
	defer ulidMu.Unlock()

	id := ulid.MustNew(ulid.Timestamp(time.Now()), ulidEntropy)
	return strings.ToLower(id.String())
}

// IsValidULID checks whether the given string is a valid ULID representation.
func IsValidULID(s string) bool {
	_, err := ulid.Parse(s)
	return err == nil
}
