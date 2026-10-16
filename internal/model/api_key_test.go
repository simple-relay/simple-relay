package model_test

import (
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
)

func TestAPIKey_Validate(t *testing.T) {
	now := time.Now().UTC()
	key := &model.APIKey{
		ID:        model.NewULID(),
		Name:      "cli-agent",
		KeyHash:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		MaskedKey: "sr_live_1234*****abcd",
		CreatedAt: now,
	}
	if err := key.Validate(); err != nil {
		t.Fatalf("expected valid key, got: %v", err)
	}

	invalidKey := &model.APIKey{
		ID: "",
	}
	if err := invalidKey.Validate(); err == nil {
		t.Fatal("expected validation error on empty fields, got nil")
	}
}
