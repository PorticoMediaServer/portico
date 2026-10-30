package playback

import "github.com/PorticoMediaServer/portico/server/internal/qualityladder"

// SavedMusicQualityPreference reads only a stored music choice. Transport is
// never inferred from locality or device class, and grants no extra authority.
func SavedMusicQualityPreference(values DeliveryPreferenceReader, class NetworkClass, transport string) string {
	if values == nil {
		return ""
	}
	key := "music.qualityAway"
	if NormalizeTransportClass(transport) == "cellular" {
		key = "music.qualityMobile"
	} else if class == NetworkLocal {
		key = "music.qualityHome"
	}
	if value := values.Text(key); value != "" {
		if _, ok := qualityladder.FindMusic(value); ok {
			return value
		}
	}
	return ""
}
