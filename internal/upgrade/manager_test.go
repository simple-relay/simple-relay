//go:build !windows

package upgrade_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/upgrade"
)

const EnvMockChildMode = "RELAY_TEST_MOCK_CHILD"

func TestMain(m *testing.M) {
	mode := os.Getenv(EnvMockChildMode)
	if mode != "" {
		switch mode {
		case "ready":
			if err := upgrade.SignalReady(); err != nil {
				os.Exit(2)
			}
			// Keep alive until killed by parent or test timeout
			time.Sleep(10 * time.Second)
			os.Exit(0)
		case "timeout":
			// Sleep without signaling ready
			time.Sleep(10 * time.Second)
			os.Exit(0)
		default:
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

func TestTriggerUpgrade_SuccessHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer ln.Close()

	t.Setenv(EnvMockChildMode, "ready")
	t.Setenv(upgrade.EnvUpgradeBinary, os.Args[0])

	// Setup mock NOTIFY_SOCKET to test sd_notify integration
	notifyPath := filepath.Join(t.TempDir(), "notify.sock")
	notifyLn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: notifyPath, Net: "unixgram"})
	if err != nil {
		t.Fatalf("failed to create mock NOTIFY_SOCKET: %v", err)
	}
	defer notifyLn.Close()
	t.Setenv("NOTIFY_SOCKET", notifyPath)

	readyCalled := make(chan struct{}, 1)
	mgr := upgrade.NewUpgradeManager(ln, func() {
		readyCalled <- struct{}{}
	}, nil)
	mgr.SetTimeout(5 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mgr.TriggerUpgrade(ctx); err != nil {
		t.Fatalf("TriggerUpgrade failed: %v", err)
	}

	childProc := mgr.ChildProcess()
	if childProc == nil {
		t.Fatalf("expected non-nil ChildProcess after successful upgrade")
	}
	t.Cleanup(func() {
		if childProc != nil {
			_ = childProc.Kill()
			_, _ = childProc.Wait()
		}
	})

	select {
	case <-readyCalled:
		// Success!
	case <-time.After(2 * time.Second):
		t.Fatalf("expected onReady callback to be called within timeout")
	}

	// Verify sd_notify sent MAINPID to NOTIFY_SOCKET
	_ = notifyLn.SetReadDeadline(time.Now().Add(1 * time.Second))
	buf := make([]byte, 64)
	n, _, err := notifyLn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("failed to read from NOTIFY_SOCKET: %v", err)
	}
	expectedNotify := fmt.Sprintf("MAINPID=%d\n", childProc.Pid)
	if string(buf[:n]) != expectedNotify {
		t.Fatalf("unexpected NOTIFY_SOCKET payload: got %q, want %q", string(buf[:n]), expectedNotify)
	}
}

func TestTriggerUpgrade_TimeoutAbort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer ln.Close()

	t.Setenv(EnvMockChildMode, "timeout")
	t.Setenv(upgrade.EnvUpgradeBinary, os.Args[0])

	readyCalled := false
	mgr := upgrade.NewUpgradeManager(ln, func() {
		readyCalled = true
	}, nil)
	mgr.SetTimeout(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = mgr.TriggerUpgrade(ctx)
	if err == nil {
		t.Fatalf("expected TriggerUpgrade to fail on timeout, got nil")
	}

	if readyCalled {
		t.Fatalf("onReady should not be called when upgrade times out")
	}

	childProc := mgr.ChildProcess()
	if childProc == nil {
		t.Fatalf("expected childProc to be tracked")
	}
	t.Cleanup(func() {
		if childProc != nil {
			_ = childProc.Kill()
			_, _ = childProc.Wait()
		}
	})
}

func TestUpgradeManager_StartSignalListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer ln.Close()

	t.Setenv(EnvMockChildMode, "ready")
	t.Setenv(upgrade.EnvUpgradeBinary, os.Args[0])

	readyCalled := make(chan struct{}, 1)
	mgr := upgrade.NewUpgradeManager(ln, func() {
		readyCalled <- struct{}{}
	}, nil)
	mgr.SetTimeout(5 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.StartSignalListener(ctx)

	// Send SIGUSR2 to self to trigger live upgrade
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR2); err != nil {
		t.Fatalf("failed to send SIGUSR2: %v", err)
	}

	select {
	case <-readyCalled:
		// Upgrade triggered by signal!
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for SIGUSR2 to trigger upgrade")
	}

	childProc := mgr.ChildProcess()
	t.Cleanup(func() {
		if childProc != nil {
			_ = childProc.Kill()
			_, _ = childProc.Wait()
		}
	})
}

func TestUpgradeManager_SetExecutable(t *testing.T) {
	mgr := upgrade.NewUpgradeManager(nil, nil, nil)
	mgr.SetExecutable("/usr/bin/custom-relay")
}
