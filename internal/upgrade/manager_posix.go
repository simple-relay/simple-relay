//go:build !windows

package upgrade

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type filer interface {
	File() (*os.File, error)
}

// TriggerUpgrade executes a live zero-downtime binary upgrade:
// 1. Duplicates listening socket file descriptor.
// 2. Creates anonymous readiness pipe.
// 3. Spawns child binary with socket (FD 3) and pipe write end (FD 4) passed via ExtraFiles.
// 4. Closes pipe write end in parent and waits for child readiness signal ("1\n") within timeout.
// 5. On readiness, updates systemd (if NOTIFY_SOCKET is present) and invokes onReady callback.
// If child crashes or times out, kills child process, cleans up, and returns error while parent continues serving.
func (m *UpgradeManager) TriggerUpgrade(ctx context.Context) error {
	if !m.upgrading.CompareAndSwap(false, true) {
		return fmt.Errorf("upgrade already in progress")
	}

	bin := m.executable
	if bin == "" {
		var err error
		bin, err = ResolveExecutable()
		if err != nil {
			m.upgrading.Store(false)
			return fmt.Errorf("resolve upgrade executable: %w", err)
		}
	}

	fl, ok := m.ln.(filer)
	if !ok {
		m.upgrading.Store(false)
		return fmt.Errorf("listener does not support file descriptor extraction")
	}

	lnFile, err := fl.File()
	if err != nil {
		m.upgrading.Store(false)
		return fmt.Errorf("duplicate listener file descriptor: %w", err)
	}
	defer lnFile.Close()

	pipeRead, pipeWrite, err := os.Pipe()
	if err != nil {
		m.upgrading.Store(false)
		return fmt.Errorf("create readiness pipe: %w", err)
	}
	defer pipeRead.Close()

	cmd := exec.Command(bin, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{lnFile, pipeWrite}

	// Filter out previous inherit/ready env vars before passing fresh ones to child
	var childEnv []string
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, EnvInheritFD+"=") &&
			!strings.HasPrefix(env, EnvReadyFD+"=") &&
			!strings.HasPrefix(env, EnvParentPID+"=") {
			childEnv = append(childEnv, env)
		}
	}
	childEnv = append(childEnv,
		fmt.Sprintf("%s=%d", EnvInheritFD, 3),
		fmt.Sprintf("%s=%d", EnvReadyFD, 4),
		fmt.Sprintf("%s=%d", EnvParentPID, os.Getpid()),
	)
	cmd.Env = childEnv

	m.logger.Info("spawning upgraded child process", "binary", bin)
	if err := cmd.Start(); err != nil {
		_ = pipeWrite.Close()
		m.upgrading.Store(false)
		return fmt.Errorf("start upgraded process: %w", err)
	}
	m.childProc = cmd.Process

	// Close parent's write end immediately so pipeRead will see EOF if child crashes prematurely ⚡
	_ = pipeWrite.Close()

	timeout := m.timeout
	if timeout <= 0 {
		if envVal := os.Getenv(EnvUpgradeTimeout); envVal != "" {
			if d, err := time.ParseDuration(envVal); err == nil && d > 0 {
				timeout = d
			}
		}
	}
	if timeout <= 0 {
		timeout = DefaultUpgradeTimeout
	}

	upgradeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	readyCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		n, err := pipeRead.Read(buf)
		if err != nil {
			readyCh <- fmt.Errorf("reading readiness pipe: %w", err)
			return
		}
		payload := strings.TrimSpace(string(buf[:n]))
		if payload != "1" {
			readyCh <- fmt.Errorf("unexpected readiness payload: %q", payload)
			return
		}
		readyCh <- nil
	}()

	select {
	case <-upgradeCtx.Done():
		m.logger.Error("upgraded child process timed out signaling readiness, aborting", "pid", cmd.Process.Pid, "err", upgradeCtx.Err())
		_ = cmd.Process.Kill()
		_ = pipeRead.Close()
		_ = cmd.Wait()
		m.upgrading.Store(false)
		return fmt.Errorf("upgrade timed out or canceled waiting for child readiness: %w", upgradeCtx.Err())

	case err := <-readyCh:
		if err != nil {
			m.logger.Error("child process failed readiness handshake, aborting", "pid", cmd.Process.Pid, "err", err)
			_ = cmd.Process.Kill()
			_ = pipeRead.Close()
			_ = cmd.Wait()
			m.upgrading.Store(false)
			return fmt.Errorf("child failed to signal readiness: %w", err)
		}
	}

	m.logger.Info("upgraded child process confirmed ready", "child_pid", cmd.Process.Pid)
	notifySystemd(cmd.Process.Pid, m.logger)

	if m.onReady != nil {
		m.onReady()
	}

	return nil
}

// StartSignalListener starts a background goroutine trapping syscall.SIGUSR2 to initiate live upgrades.
func (m *UpgradeManager) StartSignalListener(ctx context.Context) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR2)

	go func() {
		defer signal.Stop(sigCh)

		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-sigCh:
				m.logger.Info("received upgrade signal", "signal", sig.String())
				if err := m.TriggerUpgrade(ctx); err != nil {
					m.logger.Error("live binary upgrade failed", "err", err)
				}
			}
		}
	}()
}

func notifySystemd(pid int, logger *slog.Logger) {
	socketPath := os.Getenv("NOTIFY_SOCKET")
	if socketPath == "" {
		return
	}

	conn, err := net.Dial("unixgram", socketPath)
	if err != nil {
		logger.Warn("failed to dial NOTIFY_SOCKET", "socket", socketPath, "err", err)
		return
	}
	defer conn.Close()

	msg := fmt.Sprintf("MAINPID=%d\n", pid)
	if _, err := conn.Write([]byte(msg)); err != nil {
		logger.Warn("failed to send MAINPID to NOTIFY_SOCKET", "err", err)
	}
}
