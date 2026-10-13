//go:build windows

package upgrade

import (
	"context"
	"errors"
)

// TriggerUpgrade returns an error on Windows as live socket handoff is unsupported on Windows.
func (m *UpgradeManager) TriggerUpgrade(ctx context.Context) error {
	m.logger.Warn("live zero-downtime upgrades via socket handoff are not supported on Windows; please restart the service.")
	return errors.New("live zero-downtime upgrade is not supported on Windows")
}

// StartSignalListener is a no-op on Windows.
func (m *UpgradeManager) StartSignalListener(ctx context.Context) {
	m.logger.Debug("signal listener for live upgrade is not supported on Windows")
}
