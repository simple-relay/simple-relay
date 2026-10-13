//go:build windows

package upgrade_test

import (
	"context"
	"net"
	"testing"

	"github.com/simple-relay/simple-relay/internal/upgrade"
)

func TestTriggerUpgrade_WindowsUnsupported(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer ln.Close()

	mgr := upgrade.NewUpgradeManager(ln, nil, nil)
	err = mgr.TriggerUpgrade(context.Background())
	if err == nil {
		t.Fatalf("expected error on Windows, got nil")
	}

	// Verify StartSignalListener does not panic
	mgr.StartSignalListener(context.Background())
}
