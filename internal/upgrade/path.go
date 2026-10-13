package upgrade

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	// EnvUpgradeBinary specifies an explicit binary executable path to spawn during live upgrade.
	EnvUpgradeBinary = "RELAY_UPGRADE_BINARY"
)

// executableFunc allows mocking os.Executable in tests.
var executableFunc = os.Executable

// ResolveExecutable resolves the binary to execute for live upgrade.
// Resolution order:
// 1. RELAY_UPGRADE_BINARY environment variable (explicit operator override)
// 2. exec.LookPath(os.Args[0]) (resolves command from $PATH)
// 3. os.Executable() (self-resolution via /proc/self/exe or platform equivalent)
// Any trailing " (deleted)" suffix is trimmed (handles unlinked/replaced binaries on Linux).
func ResolveExecutable() (string, error) {
	if bin := os.Getenv(EnvUpgradeBinary); bin != "" {
		return cleanExecutablePath(bin), nil
	}

	if len(os.Args) > 0 && os.Args[0] != "" {
		if path, err := exec.LookPath(os.Args[0]); err == nil {
			cleaned := cleanExecutablePath(path)
			if _, statErr := os.Stat(cleaned); statErr == nil {
				return cleaned, nil
			}
		}
	}

	execPath, err := executableFunc()
	if err != nil {
		return "", fmt.Errorf("failed to determine executable path: %w", err)
	}

	return cleanExecutablePath(execPath), nil
}

func cleanExecutablePath(path string) string {
	return strings.TrimSuffix(strings.TrimSpace(path), " (deleted)")
}
