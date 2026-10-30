package httpapi

import (
	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"testing"
)

func TestOlderSettingsReceiptCannotLowerRunningSubtitleLimit(t *testing.T) {
	defer medialimits.ConfigureSubtitles(medialimits.DefaultSubtitleTracks, medialimits.DefaultSubtitleFileMiB)
	cache := &connectionPolicyCache{}
	current := operations.DefaultSettings()
	current.SubtitleTrackLimit = 256
	current.RemoteSignInPolicy = "owner-only"
	cache.set(operations.SettingsDocument{Revision: 3, Effective: current})
	old := current
	old.SubtitleTrackLimit = 32
	old.RemoteSignInPolicy = "off"
	cache.set(operations.SettingsDocument{Revision: 2, Effective: old})
	if medialimits.SubtitleTracks() != 256 || cache.setting.Load().revision != 3 || cache.setting.Load().remoteSignIn != "owner-only" {
		t.Fatal("older receipt replaced current runtime settings")
	}
}
