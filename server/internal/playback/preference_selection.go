package playback

import "sort"

// Selection policies rank tracks within the language already chosen, keeping
// explicit and carried session choices ahead of saved defaults. They never
// change source facts or force a conversion merely by relabelling a track.
func chooseAudioWithPolicy(tracks []SourceAudio, explicit int, carried string, languages []string, originalLanguage, policy string, preferDescription bool, client ClientProfile) (*SourceAudio, string) {
	chosen, reason := chooseAudio(tracks, explicit, carried, languages, originalLanguage)
	// Only a language match has format ties. Unknown-original and exhausted-list
	// fallbacks keep the exact file default or first track selected above.
	if chosen == nil || reason != "preferred_language" && reason != "original_language" {
		return chosen, reason
	}
	ranked := make([]SourceAudio, 0, len(tracks))
	for _, track := range tracks {
		if !track.Commentary && languageKey(track.Language) == languageKey(chosen.Language) {
			ranked = append(ranked, track)
		}
	}
	canPlay := func(track *SourceAudio) bool {
		for _, transport := range client.Transports {
			if len(audioRejections(track, client, transport)) == 0 {
				return true
			}
		}
		return false
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := &ranked[i], &ranked[j]
		// AudioDescription is populated from the source disposition, not a
		// title-string heuristic. Unknown facts keep the ordinary ordering.
		if a.AudioDescription != b.AudioDescription {
			return a.AudioDescription == preferDescription
		}
		if policy == "stereo" && (a.Channels == 2) != (b.Channels == 2) {
			return a.Channels == 2
		}
		if policy == "best" && canPlay(a) != canPlay(b) {
			return canPlay(a)
		}
		if policy == "channels" || policy == "best" {
			return a.Channels > b.Channels
		}
		if a.Default != b.Default {
			return a.Default
		}
		return false
	})
	if len(ranked) > 0 {
		for i := range tracks {
			if tracks[i].Index == ranked[0].Index {
				return &tracks[i], reason
			}
		}
	}
	return chosen, reason
}
