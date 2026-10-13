package upgrade

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

const (
	// EnvInheritFD specifies the file descriptor number for the inherited listening socket.
	EnvInheritFD = "RELAY_INHERIT_FD"
	// EnvReadyFD specifies the file descriptor number for the readiness signaling pipe.
	EnvReadyFD = "RELAY_READY_FD"
	// EnvParentPID carries the parent process PID during upgrade handoff.
	EnvParentPID = "RELAY_PARENT_PID"
)

// GetListener returns an inherited net.Listener if passed by parent process,
// or binds a new TCP listener on the specified address during cold boot.
func GetListener(addr string) (net.Listener, bool, error) {
	fdStr := os.Getenv(EnvInheritFD)
	if fdStr == "" {
		ln, err := net.Listen("tcp", addr)
		return ln, false, err
	}

	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		return nil, false, fmt.Errorf("invalid %s value %q: %w", EnvInheritFD, fdStr, err)
	}

	// In Go, files passed via cmd.ExtraFiles are assigned file descriptors starting from 3.
	file := os.NewFile(uintptr(fd), "inherited-listener")
	if file == nil {
		return nil, false, fmt.Errorf("failed to open inherited file descriptor %d", fd)
	}
	// net.FileListener dups the file descriptor, so close our os.File wrapper to avoid leaking it ⚡.
	defer file.Close()

	ln, err := net.FileListener(file)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create listener from file descriptor %d: %w", fd, err)
	}

	return ln, true, nil
}

// SignalReady notifies the parent process via the readiness pipe that the child is up and serving.
// If the current process was not spawned as an upgrade child (EnvReadyFD unset), it returns nil.
func SignalReady() error {
	readyFDStr := os.Getenv(EnvReadyFD)
	if readyFDStr == "" {
		return nil
	}

	fd, err := strconv.Atoi(readyFDStr)
	if err != nil {
		return fmt.Errorf("invalid %s value %q: %w", EnvReadyFD, readyFDStr, err)
	}

	pipe := os.NewFile(uintptr(fd), "readiness-pipe")
	if pipe == nil {
		return fmt.Errorf("invalid readiness pipe file descriptor %d", fd)
	}
	defer pipe.Close()

	if _, err := pipe.Write([]byte("1\n")); err != nil {
		return fmt.Errorf("failed to write to readiness pipe: %w", err)
	}

	return nil
}
