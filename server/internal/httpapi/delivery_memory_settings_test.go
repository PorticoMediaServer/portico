package httpapi

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestDeliveryMemorySettingsFollowOwnerPreference(t *testing.T) {
	s := operations.DefaultSettings()
	for _, location := range []string{"memory", "disk"} {
		for _, cap := range operations.MemoryCaps {
			s.TranscodeLocation, s.MemoryCap = location, cap
			got := deliveryFromSettings(transcodeSettings(s), 0).Normalized()
			if got.TranscodeLocation != location || got.MemoryCap != cap {
				t.Fatalf("owner preference %s/%s became %s/%s", location, cap, got.TranscodeLocation, got.MemoryCap)
			}
		}
	}
}
