package live

import (
	"math"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// LiveSettings are the server-wide Live TV defaults, used by the next source refresh and
// by the running tuner as it bounds its rewind stretch and retries a dropped source.
type LiveSettings struct {
	ImportImages     bool `json:"importImages"`
	DiscoveryEnabled bool `json:"discoveryEnabled"`
	RewindMinutes    int  `json:"rewindMinutes"`
	RetrySeconds     int  `json:"retrySeconds"`
	GuideDays        int  `json:"guideDays"`
}

var Document = settings.Define("live", LiveSettings{ImportImages: true, DiscoveryEnabled: true, RewindMinutes: 30, RetrySeconds: 60, GuideDays: 7}, func(v *LiveSettings) error {
	if v.RewindMinutes < 10 || v.RewindMinutes > 120 {
		return &settings.Invalid{Field: "rewindMinutes", Message: "Choose 10 to 120 minutes."}
	}
	if v.RetrySeconds < 0 || int64(v.RetrySeconds) > math.MaxInt64/int64(time.Second) {
		return &settings.Invalid{Field: "retrySeconds", Message: "Give a retry time of 0 or more seconds."}
	}
	if !slices.Contains([]int{3, 7, 14}, v.GuideDays) {
		return &settings.Invalid{Field: "guideDays", Message: "Choose 3, 7 or 14 days."}
	}
	return nil
})

func (v LiveSettings) Tuning() Tuning {
	return Tuning{Stretch: time.Duration(v.RewindMinutes) * time.Minute, MaxBytes: DefaultTuning.MaxBytes, Retry: time.Duration(v.RetrySeconds) * time.Second}
}
