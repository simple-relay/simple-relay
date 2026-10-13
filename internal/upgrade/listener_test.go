package upgrade_test

import (
	"io"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"

	"github.com/simple-relay/simple-relay/internal/upgrade"
)

func TestGetListener_ColdBoot(t *testing.T) {
	t.Setenv(upgrade.EnvInheritFD, "")

	ln, inherited, err := upgrade.GetListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error on cold boot: %v", err)
	}
	defer ln.Close()

	if inherited {
		t.Fatalf("expected inherited=false on cold boot, got true")
	}

	if ln.Addr() == nil {
		t.Fatalf("expected active listener with non-nil address")
	}
}

func TestGetListener_Inherited(t *testing.T) {
	// 1. Create a parent listener to simulate existing active socket
	origLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create initial listener: %v", err)
	}
	defer origLn.Close()

	tcpLn, ok := origLn.(*net.TCPListener)
	if !ok {
		t.Fatalf("expected *net.TCPListener, got %T", origLn)
	}

	// 2. Extract underlying file descriptor
	f, err := tcpLn.File()
	if err != nil {
		t.Fatalf("failed to extract file descriptor: %v", err)
	}
	defer f.Close()

	// Duplicate to FD 3 as specified by convention for ExtraFiles[0]
	targetFD := 3
	if err := syscall.Dup2(int(f.Fd()), targetFD); err != nil {
		t.Fatalf("failed to dup file descriptor to fd %d: %v", targetFD, err)
	}

	t.Setenv(upgrade.EnvInheritFD, strconv.Itoa(targetFD))

	// 3. Acquire listener through upgrade package
	inheritedLn, inherited, err := upgrade.GetListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("unexpected error getting inherited listener: %v", err)
	}
	defer inheritedLn.Close()

	if !inherited {
		t.Fatalf("expected inherited=true, got false")
	}

	if inheritedLn.Addr().String() != origLn.Addr().String() {
		t.Fatalf("listener address mismatch: got %s, want %s", inheritedLn.Addr().String(), origLn.Addr().String())
	}
}

func TestGetListener_InvalidFD(t *testing.T) {
	t.Setenv(upgrade.EnvInheritFD, "not-an-int")

	_, _, err := upgrade.GetListener("127.0.0.1:0")
	if err == nil {
		t.Fatalf("expected error for malformed %s, got nil", upgrade.EnvInheritFD)
	}
}

func TestSignalReady_NotInherited(t *testing.T) {
	t.Setenv(upgrade.EnvReadyFD, "")

	if err := upgrade.SignalReady(); err != nil {
		t.Fatalf("expected nil error when %s is not set, got: %v", upgrade.EnvReadyFD, err)
	}
}

func TestSignalReady_Inherited(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()

	t.Setenv(upgrade.EnvReadyFD, strconv.Itoa(int(w.Fd())))

	if err := upgrade.SignalReady(); err != nil {
		t.Fatalf("SignalReady failed: %v", err)
	}

	// Read from pipe reader and verify the payload sent by SignalReady
	payload, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read from readiness pipe: %v", err)
	}

	expected := "1\n"
	if string(payload) != expected {
		t.Fatalf("unexpected payload in readiness pipe: got %q, want %q", string(payload), expected)
	}
}

func TestSignalReady_InvalidFD(t *testing.T) {
	t.Setenv(upgrade.EnvReadyFD, "invalid-fd")

	if err := upgrade.SignalReady(); err == nil {
		t.Fatalf("expected error for malformed %s, got nil", upgrade.EnvReadyFD)
	}
}
