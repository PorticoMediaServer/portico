package qualityladder

// MusicPreset is independent of video. CD quality is a fidelity ceiling, not
// a fictitious bitrate; zero numeric bitrate never means unbounded video.
type MusicPreset struct {
	ID            string  `json:"id"`
	AudioKbps     int     `json:"audioKbps"`
	DataMBPerHour float64 `json:"dataMBPerHour"`
}

func Music() []MusicPreset {
	return []MusicPreset{{ID: "original"}, {ID: "cd-quality"}, {ID: "320", AudioKbps: 320, DataMBPerHour: 144}, {ID: "96", AudioKbps: 96, DataMBPerHour: 43.2}}
}
func MusicIDs() []string {
	out := []string{}
	for _, p := range Music() {
		out = append(out, p.ID)
	}
	return out
}
func FindMusic(id string) (MusicPreset, bool) {
	for _, p := range Music() {
		if p.ID == id {
			return p, true
		}
	}
	return LegacyMusic(id)
}

// LegacyMusic preserves a stored explicit pre-ladder choice without offering
// it as a new menu choice or silently promoting it to another bitrate.
func LegacyMusic(id string) (MusicPreset, bool) {
	switch id {
	case "256":
		return MusicPreset{ID: id, AudioKbps: 256}, true
	case "192":
		return MusicPreset{ID: id, AudioKbps: 192}, true
	case "128":
		return MusicPreset{ID: id, AudioKbps: 128}, true
	}
	return MusicPreset{}, false
}
