package httpapi

import (
	"context"
	"github.com/PorticoMediaServer/portico/apikit"
)

// Keep one typed owner settings contract, including conversion validation and
// the network policy callback. Both consume the same operations document.
func registerConnectionSettings(registry *apikit.Registry, d Dependencies) error {
	return registerConversionOwnerSettings(registry, d)
}

// Apply the latest committed document after either settings entry point. The
// response may be a historical receipt; runtime state must never follow it.
func (d Dependencies) applyLiveSettings(ctx context.Context) error {
	if d.connectionPolicy != nil {
		d.connectionPolicy.applyMu.Lock()
		defer d.connectionPolicy.applyMu.Unlock()
	}
	current, err := d.settingsDocument(ctx)
	if err != nil {
		return err
	}
	if d.connectionPolicy != nil {
		d.connectionPolicy.setLocked(current)
	}
	if d.Access.Advertiser != nil {
		d.Access.Advertiser.Apply(current.Effective.LANDiscoveryEnabled)
	}
	if d.Access.DLNA != nil {
		d.Access.DLNA.Apply(current.Effective.DLNAEnabled)
	}
	if d.Administration != nil {
		d.Administration.ApplyBackgroundPolicy(ctx)
	}
	if d.SettingsApplied != nil {
		d.SettingsApplied(ctx)
	}
	return nil
}
