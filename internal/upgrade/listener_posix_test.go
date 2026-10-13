//go:build !windows

package upgrade_test

import (
	"net"
	"strconv"
	"testing"

	"github.com/simple-relay/simple-relay/internal/upgrade"
)

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

	// Use actual descriptor integer directly without clobbering FD 3 in test process ⚡
	t.Setenv(upgrade.EnvInheritFD, strconv.Itoa(int(f.Fd())))

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
