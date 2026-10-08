package model

import (
	"sync"
	"testing"
	"unicode"
)

func TestNewULID(t *testing.T) {
	id := NewULID()
	if len(id) != 26 {
		t.Fatalf("expected ULID length 26, got %d (%s)", len(id), id)
	}

	for _, r := range id {
		if unicode.IsLetter(r) && !unicode.IsLower(r) {
			t.Fatalf("expected lowercase ULID, found uppercase letter in: %s", id)
		}
	}

	if !IsValidULID(id) {
		t.Fatalf("expected valid ULID, got false for: %s", id)
	}
}

func TestNewULID_MonotonicAndUnique(t *testing.T) {
	const count = 1000
	ids := make([]string, count)

	for i := 0; i < count; i++ {
		ids[i] = NewULID()
	}

	for i := 1; i < count; i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("expected monotonic increasing ULID, but %s <= %s at index %d", ids[i], ids[i-1], i)
		}
	}
}

func TestNewULID_Concurrent(t *testing.T) {
	const goroutines = 20
	const perGoroutine = 50

	var wg sync.WaitGroup
	generated := make([][]string, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			localIDs := make([]string, perGoroutine)
			for i := 0; i < perGoroutine; i++ {
				localIDs[i] = NewULID()
			}
			generated[idx] = localIDs
		}(g)
	}

	wg.Wait()

	seen := make(map[string]struct{}, goroutines*perGoroutine)
	for _, group := range generated {
		for _, id := range group {
			if _, exists := seen[id]; exists {
				t.Fatalf("duplicate ULID detected in concurrent test: %s", id)
			}
			seen[id] = struct{}{}
		}
	}
}
