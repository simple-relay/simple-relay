package version

import (
	"strings"
	"testing"
)

func TestVersionDefaults(t *testing.T) {
	if Version == "" {
		t.Errorf("expected non-empty Version")
	}
	if GitCommit == "" {
		t.Errorf("expected non-empty GitCommit")
	}
	if BuildDate == "" {
		t.Errorf("expected non-empty BuildDate")
	}
}

func TestUserAgent(t *testing.T) {
	ua := UserAgent()
	expectedPrefix := "simple-relay/"
	if !strings.HasPrefix(ua, expectedPrefix) {
		t.Errorf("expected UserAgent %q to have prefix %q", ua, expectedPrefix)
	}
}
