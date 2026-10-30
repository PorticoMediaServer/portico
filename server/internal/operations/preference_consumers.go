package operations

// Each field has an explicit owner. Client owners identify the published
// preference contract the frontend must consume; server owners enforce policy.
// A new registry field without an owner fails the registry contract test.
var preferenceConsumers = func() map[string]string {
	m := map[string]string{
		"music.qualityHome":                   "server.playback.delivery_policy",
		"music.qualityAway":                   "server.playback.delivery_policy",
		"music.qualityMobile":                 "server.playback.delivery_policy",
		"music.qualityDownloads":              "server.downloads.quality",
		"quality.downloads":                   "server.downloads.quality",
		"music.neverBoost":                    "client.audio_effects",
		"music.smoothTransitions":             "client.audio_effects",
		"music.dsdGainDb":                     "server.playback.audio_conversion",
		"playback.skipBackMs":                 "client.playback.controls",
		"playback.skipForwardMs":              "client.playback.controls",
		"playback.autoplayNext":               "client.playback.continuation",
		"playback.upNextCountdownMs":          "client.playback.continuation",
		"playback.startNextDuringCredits":     "client.playback.continuation",
		"playback.passoutProtection":          "server.playback.queue_completion",
		"playback.passoutAfterEpisodes":       "server.playback.queue_completion",
		"playback.introSkip":                  "client.playback.markers",
		"playback.creditsSkip":                "client.playback.markers",
		"playback.recapSkip":                  "client.playback.markers",
		"audiobooks.skipBackMs":               "client.playback.controls",
		"audiobooks.skipForwardMs":            "client.playback.controls",
		"audiobooks.resumeRewindMs":           "server.playback.audiobook_resume",
		"audiobooks.defaultSpeed":             "server.catalog.listening_preferences",
		"playback.sleepTimerMinutes":          "server.catalog.listening_preferences",
		"playback.defaultSpeed":               "client.playback.speed",
		"playback.startedThresholdPercent":    "server.catalog.personal_activity",
		"playback.playedThresholdPercent":     "server.catalog.personal_activity",
		"playback.preferredAudioLanguages":    "client.playback.tracks",
		"playback.preferredSubtitleLanguages": "client.playback.tracks",
		"playback.subtitleMode":               "client.playback.subtitles",
		"playback.subtitleSize":               "client.playback.subtitles",
		"playback.subtitleBackground":         "client.playback.subtitles",
		"playback.showSyncedLyrics":           "client.playback.lyrics",
		"delivery.directPlay":                 "server.playback.delivery_policy",
		"delivery.directStream":               "server.playback.delivery_policy",
		"delivery.transcode":                  "server.playback.delivery_policy",
		"music.audioNormalization":            "client.audio_effects",
		"music.crossfadeMs":                   "client.audio_effects",
		"music.gapless":                       "client.audio_effects",
		"privacy.pauseWatchHistory":           "server.catalog.personal_activity",
		"privacy.showActivityToMembers":       "server.catalog.recommendation_engine",
		"privacy.includeInWatchTogether":      "server.social.privacy",
		"search.rememberHistory":              "server.httpapi.search_history",
		"region.locale":                       "client.region",
		"region.hourCycle":                    "client.region",
		"home.rowOrder":                       "server.catalog.home_layout",
		"home.hiddenRowIds":                   "server.catalog.home_layout",
		"appearance.showBackdrops":            "client.appearance",
		"appearance.cardSizePercent":          "client.appearance",
		"notifications.badges":                "client.notifications",
	}
	for _, key := range []string{"playback.subtitleColor", "playback.subtitlePosition", "playback.subtitleFont", "playback.keepSubtitleStyling", "playback.useSystemCaptionStyle"} {
		m[key] = "client.subtitlePreferences"
	}
	for _, key := range []string{"playback.preferSdhSubtitles", "playback.forcedSubtitleFallback", "playback.useUntaggedForced", "playback.audioDescription", "playback.audioTrackChoice"} {
		m[key] = "server.playback.preference_selection"
	}
	for _, key := range []string{"appearance.motion", "appearance.textSize", "appearance.layout", "appearance.libraryView", "appearance.episodesLayout"} {
		m[key] = "client.appearancePreferences"
	}
	for _, key := range []string{"reading.theme", "reading.font", "reading.textSize", "reading.lineSpacing", "reading.margins", "reading.alignment", "reading.layout", "reading.twoPages", "reading.tapToTurn", "reading.hyphenation", "reading.highlightColour", "reading.pdfView", "comics.fit", "comics.direction", "comics.firstPageAlone"} {
		m[key] = "client.readingPreferences"
	}
	for _, key := range []string{"region.formats", "region.dateOrder", "region.firstDay", "region.sizeUnits"} {
		m[key] = "client.regionPreferences"
	}
	for _, network := range []string{"home", "remote", "mobile"} {
		m["quality."+network] = "server.playback.delivery_policy"
	}
	return m
}()

// AudioEffectPreferences is the playback renderer's settings object, derived
// only from the effective registry values. Clients apply it to their renderer
// and save edits with PATCH /v1/preferences rather than a second settings store.
type AudioEffectPreferences struct {
	NeverBoost        bool   `json:"neverBoost"`
	SmoothTransitions bool   `json:"smoothTransitions"`
	DSDGainDB         int    `json:"dsdGainDb"`
	Gapless           bool   `json:"gapless"`
	CrossfadeMs       int    `json:"crossfadeMs"`
	Normalization     string `json:"normalization"`
}

func (v PreferenceValues) AudioEffects() AudioEffectPreferences {
	return AudioEffectPreferences{NeverBoost: v.Bool("music.neverBoost"), SmoothTransitions: v.Bool("music.smoothTransitions"), DSDGainDB: v.Int("music.dsdGainDb"), Gapless: v.Bool("music.gapless"), CrossfadeMs: v.Int("music.crossfadeMs"), Normalization: v.Text("music.audioNormalization")}
}
