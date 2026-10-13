package upgrade

import (
	"log/slog"
	"net"
	"os"
	"sync/atomic"
	"time"
)

const (
	// DefaultUpgradeTimeout is the default duration to wait for child process readiness.
	DefaultUpgradeTimeout = 15 * time.Second
	// EnvUpgradeTimeout specifies the environment variable to configure child readiness timeout.
	EnvUpgradeTimeout = "RELAY_UPGRADE_TIMEOUT"
)

// UpgradeManager coordinates live binary upgrades and socket handoff.
type UpgradeManager struct {
	ln        net.Listener
	onReady   func()
	logger    *slog.Logger
	upgrading atomic.Bool

	// Testing and runtime overrides
	executable string
	timeout    time.Duration
	childProc  *os.Process
}

// NewUpgradeManager initializes a new UpgradeManager.
func NewUpgradeManager(ln net.Listener, onReady func(), logger *slog.Logger) *UpgradeManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &UpgradeManager{
		ln:      ln,
		onReady: onReady,
		logger:  logger,
	}
}

// SetExecutable overrides the executable path to launch (primarily for testing).
func (m *UpgradeManager) SetExecutable(path string) {
	m.executable = path
}

// SetTimeout overrides the timeout for child process readiness (primarily for testing).
func (m *UpgradeManager) SetTimeout(d time.Duration) {
	m.timeout = d
}

// ChildProcess returns the child process created by the most recent upgrade attempt.
func (m *UpgradeManager) ChildProcess() *os.Process {
	return m.childProc
}
