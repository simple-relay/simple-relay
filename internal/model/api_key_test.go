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

	tests := []struct {
		name    string
		mutate  func(k *model.APIKey)
		wantErr string
	}{
		{
			name:    "empty id",
			mutate:  func(k *model.APIKey) { k.ID = "  " },
			wantErr: "id is required",
		},
		{
			name:    "empty name",
			mutate:  func(k *model.APIKey) { k.Name = "" },
			wantErr: "name is required",
		},
		{
			name:    "empty key_hash",
			mutate:  func(k *model.APIKey) { k.KeyHash = " " },
			wantErr: "key_hash is required",
		},
		{
			name:    "empty masked_key",
			mutate:  func(k *model.APIKey) { k.MaskedKey = "" },
			wantErr: "masked_key is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &model.APIKey{
				ID:        model.NewULID(),
				Name:      "cli-agent",
				KeyHash:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				MaskedKey: "sr_live_1234*****abcd",
				CreatedAt: now,
			}
			tt.mutate(k)
			err := k.Validate()
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("expected error %q, got: %v", tt.wantErr, err)
			}
		})
	}
}
