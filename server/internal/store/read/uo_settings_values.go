package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Closed ordinary-settings vocabulary for LOCAL DRAFT 611/612. Security,
// account authority, credentials, trust networks/proxies are excluded.
type UOSettingKey string

const (
	UOSettingPerAccountCap               UOSettingKey = "perAccountCap"
	UOSettingServerCap                   UOSettingKey = "serverCap"
	UOSettingRemoteBitrateLimitKbps      UOSettingKey = "remoteBitrateLimitKbps"
	UOSettingConnectionLimit             UOSettingKey = "connectionLimit"
	UOSettingAdvertiseUrl                UOSettingKey = "advertiseUrl"
	UOSettingUploadCapacityKbps          UOSettingKey = "uploadCapacityKbps"
	UOSettingPausedSessionTimeoutMinutes UOSettingKey = "pausedSessionTimeoutMinutes"
	UOSettingAdvertisedInterface         UOSettingKey = "advertisedInterface"
	UOSettingLanDiscoveryEnabled         UOSettingKey = "lanDiscoveryEnabled"

	UOSettingLogRetention                       UOSettingKey = "logRetention"
	UOSettingName                               UOSettingKey = "name"
	UOSettingDownloadMaxQuality                 UOSettingKey = "downloadMaxQuality"
	UOSettingAnalysisStorageGiB                 UOSettingKey = "analysisStorageGiB"
	UOSettingAnalysisStageStorageMiB            UOSettingKey = "analysisStageStorageMiB"
	UOSettingSubtitleTrackLimit                 UOSettingKey = "subtitleTrackLimit"
	UOSettingSubtitleFileLimitMiB               UOSettingKey = "subtitleFileLimitMiB"
	UOSettingThrottleBufferMs                   UOSettingKey = "throttleBufferMs"
	UOSettingPlayedRetentionMs                  UOSettingKey = "playedRetentionMs"
	UOSettingSessionStorageMiB                  UOSettingKey = "sessionStorageMiB"
	UOSettingMaxConcurrentSessions              UOSettingKey = "maxConcurrentSessions"
	UOSettingMaxHardwareSessions                UOSettingKey = "maxHardwareSessions"
	UOSettingMaxSoftwareSessions                UOSettingKey = "maxSoftwareSessions"
	UOSettingMaxBackgroundSessions              UOSettingKey = "maxBackgroundSessions"
	UOSettingDiagnosticDays                     UOSettingKey = "diagnosticDays"
	UOSettingNotificationDays                   UOSettingKey = "notificationDays"
	UOSettingJobDays                            UOSettingKey = "jobDays"
	UOSettingLibraryMissingTitlesDays           UOSettingKey = "library.missingTitlesDays"
	UOSettingSegmentsShortestEpisode            UOSettingKey = "segments.shortestEpisode"
	UOSettingLogDetailMinutes                   UOSettingKey = "logDetailMinutes"
	UOSettingWebhookTimeoutSeconds              UOSettingKey = "webhookTimeoutSeconds"
	UOSettingWebhookQueueSize                   UOSettingKey = "webhookQueueSize"
	UOSettingWebhookLogSize                     UOSettingKey = "webhookLogSize"
	UOSettingHardwareDecode                     UOSettingKey = "hardwareDecode"
	UOSettingHdrToneMapping                     UOSettingKey = "hdrToneMapping"
	UOSettingDirectStreamRemux                  UOSettingKey = "directStreamRemux"
	UOSettingRecommendationsTitleDataset        UOSettingKey = "recommendations.titleDataset"
	UOSettingHomeCommunityActivityEnabled       UOSettingKey = "home.communityActivityEnabled"
	UOSettingSegmentsUseChapterNames            UOSettingKey = "segments.useChapterNames"
	UOSettingHardwareBackend                    UOSettingKey = "hardwareBackend"
	UOSettingHdrToneMappingAlgorithm            UOSettingKey = "hdrToneMappingAlgorithm"
	UOSettingX264Preset                         UOSettingKey = "x264Preset"
	UOSettingPlanningPolicy                     UOSettingKey = "planningPolicy"
	UOSettingTranscodeLocation                  UOSettingKey = "transcodeLocation"
	UOSettingMemoryCap                          UOSettingKey = "memoryCap"
	UOSettingUpdatesReleaseChannel              UOSettingKey = "updates.releaseChannel"
	UOSettingLibraryMissingTitles               UOSettingKey = "library.missingTitles"
	UOSettingLibrarySegmentDetection            UOSettingKey = "library.segmentDetection"
	UOSettingSegmentsIntroWindow                UOSettingKey = "segments.introWindow"
	UOSettingSegmentsCreditsWindow              UOSettingKey = "segments.creditsWindow"
	UOSettingSegmentsFilmCreditsWindow          UOSettingKey = "segments.filmCreditsWindow"
	UOSettingSegmentsTvMode                     UOSettingKey = "segments.tvMode"
	UOSettingSegmentsAnimeMode                  UOSettingKey = "segments.animeMode"
	UOSettingSegmentsMovieMode                  UOSettingKey = "segments.movieMode"
	UOSettingLogLevel                           UOSettingKey = "logLevel"
	UOSettingConversionMode                     UOSettingKey = "conversionMode"
	UOSettingOutputFormat                       UOSettingKey = "outputFormat"
	UOSettingMusicAwayQuality                   UOSettingKey = "musicAwayQuality"
	UOSettingWebhookRetries                     UOSettingKey = "webhookRetries"
	UOSettingHardwareDevice                     UOSettingKey = "hardwareDevice"
	UOSettingHardwareSelection                  UOSettingKey = "hardwareSelection"
	UOSettingTemporaryDirectory                 UOSettingKey = "temporaryDirectory"
	UOSettingFfmpegPath                         UOSettingKey = "ffmpegPath"
	UOSettingUpdatesFeedUrl                     UOSettingKey = "updates.feedUrl"
	UOSettingSegmentsShortestFilm               UOSettingKey = "segments.shortestFilm"
	UOSettingSegmentsViewerConfidencePercent    UOSettingKey = "segments.viewerConfidencePercent"
	UOSettingSegmentsAutomaticConfidencePercent UOSettingKey = "segments.automaticConfidencePercent"
	UOSettingAccessUrls                         UOSettingKey = "accessUrls"
)

type uoSettingSpec struct {
	key       UOSettingKey
	def       UOValue
	low, high float64
	ranged    bool
	options   []string
	numbers   []float64
}

var uoSettingSpecs = []uoSettingSpec{
	{key: UOSettingPerAccountCap, def: UOValue{Kind: UOInteger}, low: 0, high: 1000000, ranged: true},
	{key: UOSettingServerCap, def: UOValue{Kind: UOInteger}, low: 0, high: 1000000, ranged: true},
	{key: UOSettingRemoteBitrateLimitKbps, def: UOValue{Kind: UOInteger}, low: 0, high: 200000, ranged: true},
	{key: UOSettingConnectionLimit, def: UOValue{Kind: UOInteger}, low: 0, high: 65536, ranged: true},
	{key: UOSettingAdvertiseUrl, def: UOValue{Kind: UOText}},
	{key: UOSettingUploadCapacityKbps, def: UOValue{Kind: UOInteger}, low: 0, high: 100000000, ranged: true},
	{key: UOSettingPausedSessionTimeoutMinutes, def: UOValue{Kind: UOInteger}, low: 0, high: 1440, ranged: true},
	{key: UOSettingAdvertisedInterface, def: UOValue{Kind: UOText}},
	{key: UOSettingLanDiscoveryEnabled, def: UOValue{Kind: UOBool, Bool: true}},

	{key: UOSettingLogRetention, def: UOValue{Kind: UORetention, Retention: []UOLogRetention{}}},
	{key: UOSettingName, def: UOValue{Kind: UOText, Text: "Portico"}},
	{key: UOSettingDownloadMaxQuality, def: UOValue{Kind: UOText, Text: "original"}, options: uoDownloadQualities()},
	{key: UOSettingAnalysisStorageGiB, def: UOValue{Kind: UOInteger, Number: 32}, low: 1, high: 16384, ranged: true},
	{key: UOSettingAnalysisStageStorageMiB, def: UOValue{Kind: UOInteger, Number: 256}, low: 1, high: 4096, ranged: true},
	{key: UOSettingSubtitleTrackLimit, def: UOValue{Kind: UOInteger, Number: 128}, low: 32, high: 4096, ranged: true},
	{key: UOSettingSubtitleFileLimitMiB, def: UOValue{Kind: UOInteger, Number: 128}, low: 32, high: 512, ranged: true},
	{key: UOSettingThrottleBufferMs, def: UOValue{Kind: UOInteger, Number: 60000}, low: 10000, high: 600000, ranged: true},
	{key: UOSettingPlayedRetentionMs, def: UOValue{Kind: UOInteger, Number: 180000}, low: 0, high: 3600000, ranged: true},
	{key: UOSettingSessionStorageMiB, def: UOValue{Kind: UOInteger, Number: 4096}, low: 64, high: 1048576, ranged: true},
	{key: UOSettingMaxConcurrentSessions, def: UOValue{Kind: UOInteger, Number: 0}, low: 0, high: 1000, ranged: true},
	{key: UOSettingMaxHardwareSessions, def: UOValue{Kind: UOInteger, Number: 0}, low: 0, high: 1000, ranged: true},
	{key: UOSettingMaxSoftwareSessions, def: UOValue{Kind: UOInteger, Number: 0}, low: 0, high: 1000, ranged: true},
	{key: UOSettingMaxBackgroundSessions, def: UOValue{Kind: UOInteger, Number: 0}, low: 0, high: 1000, ranged: true},
	{key: UOSettingDiagnosticDays, def: UOValue{Kind: UOInteger, Number: 30}, low: 1, high: 30, ranged: true},
	{key: UOSettingNotificationDays, def: UOValue{Kind: UOInteger, Number: 180}, low: 1, high: 180, ranged: true},
	{key: UOSettingJobDays, def: UOValue{Kind: UOInteger, Number: 30}, low: 1, high: 30, ranged: true},
	{key: UOSettingLibraryMissingTitlesDays, def: UOValue{Kind: UOInteger, Number: 30}, low: 1, high: 3650, ranged: true},
	{key: UOSettingSegmentsShortestEpisode, def: UOValue{Kind: UOInteger, Number: 2}, low: 1, high: 30, ranged: true},
	{key: UOSettingLogDetailMinutes, def: UOValue{Kind: UOInteger, Number: 30}, low: 15, high: 240, ranged: true},
	{key: UOSettingWebhookTimeoutSeconds, def: UOValue{Kind: UOInteger, Number: 15}, low: 5, high: 60, ranged: true},
	{key: UOSettingWebhookQueueSize, def: UOValue{Kind: UOInteger, Number: 1000}, low: 100, high: 5000, ranged: true},
	{key: UOSettingWebhookLogSize, def: UOValue{Kind: UOInteger, Number: 200}, low: 50, high: 1000, ranged: true},
	{key: UOSettingHardwareDecode, def: UOValue{Kind: UOBool, Bool: true}},
	{key: UOSettingHdrToneMapping, def: UOValue{Kind: UOBool, Bool: true}},
	{key: UOSettingDirectStreamRemux, def: UOValue{Kind: UOBool, Bool: true}},
	{key: UOSettingRecommendationsTitleDataset, def: UOValue{Kind: UOBool, Bool: true}},
	{key: UOSettingHomeCommunityActivityEnabled, def: UOValue{Kind: UOBool, Bool: false}},
	{key: UOSettingSegmentsUseChapterNames, def: UOValue{Kind: UOBool, Bool: true}},
	{key: UOSettingHardwareBackend, def: UOValue{Kind: UOText, Text: "auto"}, options: []string{"auto", "vaapi", "qsv", "nvenc", "amf", "videotoolbox", "software"}},
	{key: UOSettingHdrToneMappingAlgorithm, def: UOValue{Kind: UOText, Text: "hable"}, options: []string{"clip", "linear", "gamma", "reinhard", "hable", "mobius"}},
	{key: UOSettingX264Preset, def: UOValue{Kind: UOText, Text: "veryfast"}, options: []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow"}},
	{key: UOSettingPlanningPolicy, def: UOValue{Kind: UOText, Text: "maximum_fidelity"}, options: []string{"maximum_fidelity", "maximum_compatibility", "minimize_server_work"}},
	{key: UOSettingTranscodeLocation, def: UOValue{Kind: UOText, Text: "memory"}, options: []string{"memory", "disk"}},
	{key: UOSettingMemoryCap, def: UOValue{Kind: UOText, Text: "quarter"}, options: []string{"quarter", "half", "2gb", "4gb", "8gb", "16gb"}},
	{key: UOSettingUpdatesReleaseChannel, def: UOValue{Kind: UOText, Text: "stable"}, options: []string{"stable", "beta"}},
	{key: UOSettingLibraryMissingTitles, def: UOValue{Kind: UOText, Text: "after_days"}, options: []string{"immediately", "after_days", "never"}},
	{key: UOSettingLibrarySegmentDetection, def: UOValue{Kind: UOText, Text: "standard"}, options: []string{"standard", "thorough", "off"}},
	{key: UOSettingSegmentsIntroWindow, def: UOValue{Kind: UOText, Text: "usual"}, options: []string{"usual", "long"}},
	{key: UOSettingSegmentsCreditsWindow, def: UOValue{Kind: UOText, Text: "usual"}, options: []string{"usual", "long"}},
	{key: UOSettingSegmentsFilmCreditsWindow, def: UOValue{Kind: UOText, Text: "usual"}, options: []string{"usual", "long"}},
	{key: UOSettingSegmentsTvMode, def: UOValue{Kind: UOText, Text: "server"}, options: []string{"server", "standard", "thorough", "off"}},
	{key: UOSettingSegmentsAnimeMode, def: UOValue{Kind: UOText, Text: "server"}, options: []string{"server", "standard", "thorough", "off"}},
	{key: UOSettingSegmentsMovieMode, def: UOValue{Kind: UOText, Text: "server"}, options: []string{"server", "standard", "thorough", "off"}},
	{key: UOSettingLogLevel, def: UOValue{Kind: UOText, Text: "info"}, options: []string{"error", "warn", "info", "debug"}},
	{key: UOSettingConversionMode, def: UOValue{Kind: UOText, Text: "all"}, options: []string{"all", "sound", "never"}},
	{key: UOSettingOutputFormat, def: UOValue{Kind: UOText, Text: "auto"}, options: []string{"auto", "h264"}},
	{key: UOSettingMusicAwayQuality, def: UOValue{Kind: UOText, Text: "original"}, options: []string{"original", "cd-quality", "320", "96"}},
	{key: UOSettingWebhookRetries, def: UOValue{Kind: UOText, Text: "long"}, options: []string{"long", "short", "none"}},
	{key: UOSettingHardwareDevice, def: UOValue{Kind: UOText, Text: ""}},
	{key: UOSettingHardwareSelection, def: UOValue{Kind: UOText, Text: "auto"}},
	{key: UOSettingTemporaryDirectory, def: UOValue{Kind: UOText, Text: ""}},
	{key: UOSettingFfmpegPath, def: UOValue{Kind: UOText, Text: ""}},
	{key: UOSettingUpdatesFeedUrl, def: UOValue{Kind: UOText, Text: "https://github.com/PorticoMediaServer/portico-server/releases/latest/download/portico-update-manifest.json"}},
	{key: UOSettingSegmentsShortestFilm, def: UOValue{Kind: UOInteger, Number: 10}, numbers: []float64{5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60}},
	{key: UOSettingSegmentsViewerConfidencePercent, def: UOValue{Kind: UOInteger, Number: 50}, numbers: []float64{30, 50, 70}},
	{key: UOSettingSegmentsAutomaticConfidencePercent, def: UOValue{Kind: UOInteger, Number: 85}, numbers: []float64{70, 85, 95}},
	{key: UOSettingAccessUrls, def: UOValue{Kind: UOList, List: []string{}}},
}

func uoSettingSpecFor(k UOSettingKey) (uoSettingSpec, bool) {
	for _, s := range uoSettingSpecs {
		if s.key == k {
			return s, true
		}
	}
	return uoSettingSpec{}, false
}
func UOSettingKeys() []UOSettingKey {
	out := make([]UOSettingKey, len(uoSettingSpecs))
	for i, s := range uoSettingSpecs {
		out[i] = s.key
	}
	return out
}
func UOSettingDefault(k UOSettingKey) (UOValue, bool) {
	s, ok := uoSettingSpecFor(k)
	v := s.def
	v.List = slices.Clone(v.List)
	v.Retention = slices.Clone(v.Retention)
	return v, ok
}
func UOValidateSetting(k UOSettingKey, v UOValue) (UOValue, error) {
	f, ok := uoSettingSpecFor(k)
	if !ok || v.Kind != f.def.Kind {
		return UOValue{}, ErrInput
	}
	if v.Kind != UORetention && v.Retention != nil || v.Kind != UOBool && v.Bool || v.Kind != UOInteger && v.Number != 0 || v.Kind != UOText && v.Text != "" || v.Kind != UOList && v.List != nil {
		return UOValue{}, ErrInput
	}
	switch v.Kind {
	case UORetention:
		if len(v.Retention) > 5 {
			return UOValue{}, ErrInput
		}
		v.Retention = slices.Clone(v.Retention)
		seen := map[UOLogCategory]bool{}
		for _, entry := range v.Retention {
			if !slices.Contains(uoLogCategories(), entry.Category) || seen[entry.Category] || entry.Days < 1 || entry.Days > 90 {
				return UOValue{}, ErrInput
			}
			seen[entry.Category] = true
		}
		slices.SortFunc(v.Retention, func(a, b UOLogRetention) int { return strings.Compare(string(a.Category), string(b.Category)) })
	case UOBool:
	case UOInteger:
		// Legacy remote bitrate input saturates at its endpoints; fractional and
		// non-finite inputs still fail the typed integer contract.
		if k == UOSettingRemoteBitrateLimitKbps && v.Number == float64(int64(v.Number)) {
			if v.Number < 0 {
				v.Number = 0
			}
			if v.Number > 200000 {
				v.Number = 200000
			}
		}
		if v.Number != float64(int64(v.Number)) || f.ranged && (v.Number < f.low || v.Number > f.high) || len(f.numbers) > 0 && !slices.Contains(f.numbers, v.Number) {
			return UOValue{}, ErrInput
		}
		if k == UOSettingLogDetailMinutes && !slices.Contains([]float64{15, 30, 60, 120, 240}, v.Number) {
			return UOValue{}, ErrInput
		}
	case UOText:
		if k == UOSettingAdvertiseUrl && v.Text != "" {
			normalized, ok := uoAdvertiseOrigin(v.Text)
			if !ok {
				return UOValue{}, ErrInput
			}
			v.Text = normalized
		}
		if k == UOSettingAdvertisedInterface {
			if len(v.Text) > 64 {
				return UOValue{}, ErrInput
			}
			for _, r := range v.Text {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-@ ", r)) {
					return UOValue{}, ErrInput
				}
			}
		}
		if k == UOSettingName && !uoText(v.Text, 100) {
			return UOValue{}, ErrInput
		}
		if len(f.options) > 0 && !slices.Contains(f.options, v.Text) {
			return UOValue{}, ErrInput
		}
		if !utf8.ValidString(v.Text) || len(v.Text) > 4096 || strings.ContainsAny(v.Text, "\x00\r\n\t") {
			return UOValue{}, ErrInput
		}
		if (k == UOSettingTemporaryDirectory || k == UOSettingFfmpegPath) && v.Text != "" && !filepath.IsAbs(v.Text) {
			return UOValue{}, ErrInput
		}
		if k == UOSettingUpdatesFeedUrl && v.Text != "" && !uoSettingURL(v.Text) {
			return UOValue{}, ErrInput
		}
	case UOList:
		if len(v.List) > 64 {
			return UOValue{}, ErrInput
		}
		v.List = slices.Clone(v.List)
		seen := map[string]bool{}
		for _, x := range v.List {
			if len(x) > 4096 || !uoSettingURL(x) || seen[x] {
				return UOValue{}, ErrInput
			}
			seen[x] = true
		}
	default:
		return UOValue{}, ErrInput
	}
	return v, nil
}
func uoSettingURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n\t") && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Fragment == ""
}

// Literal SQL selected by a finite compiled enum. No caller chooses a column.
func uoSettingSQL(k UOSettingKey) (point, octets, update string) {
	switch k {
	case UOSettingServerCap:
		return `SELECT coalesce(server_cap,0) FROM playback_owner_policy WHERE singleton=1`, `SELECT 0 FROM playback_owner_policy WHERE singleton=1`, `UPDATE playback_owner_policy SET server_cap=nullif(?,0) WHERE singleton=1 AND revision=?`
	case UOSettingPerAccountCap:
		return `SELECT coalesce(per_account_cap,0) FROM playback_owner_policy WHERE singleton=1`, `SELECT 0 FROM playback_owner_policy WHERE singleton=1`, `UPDATE playback_owner_policy SET per_account_cap=nullif(?,0) WHERE singleton=1 AND revision=?`
	case UOSettingRemoteBitrateLimitKbps:
		return `SELECT "remoteBitrateLimitKbps" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "remoteBitrateLimitKbps"=?,updated_ms=? WHERE singleton=1`
	case UOSettingConnectionLimit:
		return `SELECT "connectionLimit" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "connectionLimit"=?,updated_ms=? WHERE singleton=1`
	case UOSettingAdvertiseUrl:
		return `SELECT "advertiseUrl" FROM server_settings WHERE singleton=1`, `SELECT octet_length("advertiseUrl") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "advertiseUrl"=?,updated_ms=? WHERE singleton=1`
	case UOSettingUploadCapacityKbps:
		return `SELECT "uploadCapacityKbps" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "uploadCapacityKbps"=?,updated_ms=? WHERE singleton=1`
	case UOSettingPausedSessionTimeoutMinutes:
		return `SELECT "pausedSessionTimeoutMinutes" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "pausedSessionTimeoutMinutes"=?,updated_ms=? WHERE singleton=1`
	case UOSettingAdvertisedInterface:
		return `SELECT "advertisedInterface" FROM server_settings WHERE singleton=1`, `SELECT octet_length("advertisedInterface") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "advertisedInterface"=?,updated_ms=? WHERE singleton=1`
	case UOSettingLanDiscoveryEnabled:
		return `SELECT "lanDiscoveryEnabled" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "lanDiscoveryEnabled"=?,updated_ms=? WHERE singleton=1`
	case UOSettingName:
		return `SELECT value FROM configuration WHERE key='name'`, `SELECT octet_length(value) FROM configuration WHERE key='name'`, `INSERT INTO configuration(key,value) VALUES('name',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`
	case UOSettingDownloadMaxQuality:
		return `SELECT "downloadMaxQuality" FROM server_settings WHERE singleton=1`, `SELECT octet_length("downloadMaxQuality") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "downloadMaxQuality"=?,updated_ms=? WHERE singleton=1`
	case UOSettingAnalysisStorageGiB:
		return `SELECT "analysisStorageGiB" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "analysisStorageGiB"=?,updated_ms=? WHERE singleton=1`
	case UOSettingAnalysisStageStorageMiB:
		return `SELECT "analysisStageStorageMiB" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "analysisStageStorageMiB"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSubtitleTrackLimit:
		return `SELECT "subtitleTrackLimit" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "subtitleTrackLimit"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSubtitleFileLimitMiB:
		return `SELECT "subtitleFileLimitMiB" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "subtitleFileLimitMiB"=?,updated_ms=? WHERE singleton=1`
	case UOSettingThrottleBufferMs:
		return `SELECT "throttleBufferMs" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "throttleBufferMs"=?,updated_ms=? WHERE singleton=1`
	case UOSettingPlayedRetentionMs:
		return `SELECT "playedRetentionMs" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "playedRetentionMs"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSessionStorageMiB:
		return `SELECT "sessionStorageMiB" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "sessionStorageMiB"=?,updated_ms=? WHERE singleton=1`
	case UOSettingMaxConcurrentSessions:
		return `SELECT "maxConcurrentSessions" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "maxConcurrentSessions"=?,updated_ms=? WHERE singleton=1`
	case UOSettingMaxHardwareSessions:
		return `SELECT "maxHardwareSessions" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "maxHardwareSessions"=?,updated_ms=? WHERE singleton=1`
	case UOSettingMaxSoftwareSessions:
		return `SELECT "maxSoftwareSessions" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "maxSoftwareSessions"=?,updated_ms=? WHERE singleton=1`
	case UOSettingMaxBackgroundSessions:
		return `SELECT "maxBackgroundSessions" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "maxBackgroundSessions"=?,updated_ms=? WHERE singleton=1`
	case UOSettingDiagnosticDays:
		return `SELECT "diagnosticDays" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "diagnosticDays"=?,updated_ms=? WHERE singleton=1`
	case UOSettingNotificationDays:
		return `SELECT "notificationDays" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "notificationDays"=?,updated_ms=? WHERE singleton=1`
	case UOSettingJobDays:
		return `SELECT "jobDays" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "jobDays"=?,updated_ms=? WHERE singleton=1`
	case UOSettingLibraryMissingTitlesDays:
		return `SELECT "library.missingTitlesDays" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "library.missingTitlesDays"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsShortestEpisode:
		return `SELECT "segments.shortestEpisode" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.shortestEpisode"=?,updated_ms=? WHERE singleton=1`
	case UOSettingLogDetailMinutes:
		return `SELECT "logDetailMinutes" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "logDetailMinutes"=?,updated_ms=? WHERE singleton=1`
	case UOSettingWebhookTimeoutSeconds:
		return `SELECT "webhookTimeoutSeconds" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "webhookTimeoutSeconds"=?,updated_ms=? WHERE singleton=1`
	case UOSettingWebhookQueueSize:
		return `SELECT "webhookQueueSize" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "webhookQueueSize"=?,updated_ms=? WHERE singleton=1`
	case UOSettingWebhookLogSize:
		return `SELECT "webhookLogSize" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "webhookLogSize"=?,updated_ms=? WHERE singleton=1`
	case UOSettingHardwareDecode:
		return `SELECT "hardwareDecode" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "hardwareDecode"=?,updated_ms=? WHERE singleton=1`
	case UOSettingHdrToneMapping:
		return `SELECT "hdrToneMapping" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "hdrToneMapping"=?,updated_ms=? WHERE singleton=1`
	case UOSettingDirectStreamRemux:
		return `SELECT "directStreamRemux" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "directStreamRemux"=?,updated_ms=? WHERE singleton=1`
	case UOSettingRecommendationsTitleDataset:
		return `SELECT "recommendations.titleDataset" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "recommendations.titleDataset"=?,updated_ms=? WHERE singleton=1`
	case UOSettingHomeCommunityActivityEnabled:
		return `SELECT "home.communityActivityEnabled" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "home.communityActivityEnabled"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsUseChapterNames:
		return `SELECT "segments.useChapterNames" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.useChapterNames"=?,updated_ms=? WHERE singleton=1`
	case UOSettingHardwareBackend:
		return `SELECT "hardwareBackend" FROM server_settings WHERE singleton=1`, `SELECT octet_length("hardwareBackend") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "hardwareBackend"=?,updated_ms=? WHERE singleton=1`
	case UOSettingHdrToneMappingAlgorithm:
		return `SELECT "hdrToneMappingAlgorithm" FROM server_settings WHERE singleton=1`, `SELECT octet_length("hdrToneMappingAlgorithm") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "hdrToneMappingAlgorithm"=?,updated_ms=? WHERE singleton=1`
	case UOSettingX264Preset:
		return `SELECT "x264Preset" FROM server_settings WHERE singleton=1`, `SELECT octet_length("x264Preset") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "x264Preset"=?,updated_ms=? WHERE singleton=1`
	case UOSettingPlanningPolicy:
		return `SELECT "planningPolicy" FROM server_settings WHERE singleton=1`, `SELECT octet_length("planningPolicy") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "planningPolicy"=?,updated_ms=? WHERE singleton=1`
	case UOSettingTranscodeLocation:
		return `SELECT "transcodeLocation" FROM server_settings WHERE singleton=1`, `SELECT octet_length("transcodeLocation") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "transcodeLocation"=?,updated_ms=? WHERE singleton=1`
	case UOSettingMemoryCap:
		return `SELECT "memoryCap" FROM server_settings WHERE singleton=1`, `SELECT octet_length("memoryCap") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "memoryCap"=?,updated_ms=? WHERE singleton=1`
	case UOSettingUpdatesReleaseChannel:
		return `SELECT "updates.releaseChannel" FROM server_settings WHERE singleton=1`, `SELECT octet_length("updates.releaseChannel") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "updates.releaseChannel"=?,updated_ms=? WHERE singleton=1`
	case UOSettingLibraryMissingTitles:
		return `SELECT "library.missingTitles" FROM server_settings WHERE singleton=1`, `SELECT octet_length("library.missingTitles") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "library.missingTitles"=?,updated_ms=? WHERE singleton=1`
	case UOSettingLibrarySegmentDetection:
		return `SELECT "library.segmentDetection" FROM server_settings WHERE singleton=1`, `SELECT octet_length("library.segmentDetection") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "library.segmentDetection"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsIntroWindow:
		return `SELECT "segments.introWindow" FROM server_settings WHERE singleton=1`, `SELECT octet_length("segments.introWindow") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.introWindow"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsCreditsWindow:
		return `SELECT "segments.creditsWindow" FROM server_settings WHERE singleton=1`, `SELECT octet_length("segments.creditsWindow") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.creditsWindow"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsFilmCreditsWindow:
		return `SELECT "segments.filmCreditsWindow" FROM server_settings WHERE singleton=1`, `SELECT octet_length("segments.filmCreditsWindow") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.filmCreditsWindow"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsTvMode:
		return `SELECT "segments.tvMode" FROM server_settings WHERE singleton=1`, `SELECT octet_length("segments.tvMode") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.tvMode"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsAnimeMode:
		return `SELECT "segments.animeMode" FROM server_settings WHERE singleton=1`, `SELECT octet_length("segments.animeMode") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.animeMode"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsMovieMode:
		return `SELECT "segments.movieMode" FROM server_settings WHERE singleton=1`, `SELECT octet_length("segments.movieMode") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.movieMode"=?,updated_ms=? WHERE singleton=1`
	case UOSettingLogLevel:
		return `SELECT "logLevel" FROM server_settings WHERE singleton=1`, `SELECT octet_length("logLevel") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "logLevel"=?,updated_ms=? WHERE singleton=1`
	case UOSettingConversionMode:
		return `SELECT "conversionMode" FROM server_settings WHERE singleton=1`, `SELECT octet_length("conversionMode") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "conversionMode"=?,updated_ms=? WHERE singleton=1`
	case UOSettingOutputFormat:
		return `SELECT "outputFormat" FROM server_settings WHERE singleton=1`, `SELECT octet_length("outputFormat") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "outputFormat"=?,updated_ms=? WHERE singleton=1`
	case UOSettingMusicAwayQuality:
		return `SELECT "musicAwayQuality" FROM server_settings WHERE singleton=1`, `SELECT octet_length("musicAwayQuality") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "musicAwayQuality"=?,updated_ms=? WHERE singleton=1`
	case UOSettingWebhookRetries:
		return `SELECT "webhookRetries" FROM server_settings WHERE singleton=1`, `SELECT octet_length("webhookRetries") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "webhookRetries"=?,updated_ms=? WHERE singleton=1`
	case UOSettingHardwareDevice:
		return `SELECT "hardwareDevice" FROM server_settings WHERE singleton=1`, `SELECT octet_length("hardwareDevice") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "hardwareDevice"=?,updated_ms=? WHERE singleton=1`
	case UOSettingHardwareSelection:
		return `SELECT "hardwareSelection" FROM server_settings WHERE singleton=1`, `SELECT octet_length("hardwareSelection") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "hardwareSelection"=?,updated_ms=? WHERE singleton=1`
	case UOSettingTemporaryDirectory:
		return `SELECT "temporaryDirectory" FROM server_settings WHERE singleton=1`, `SELECT octet_length("temporaryDirectory") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "temporaryDirectory"=?,updated_ms=? WHERE singleton=1`
	case UOSettingFfmpegPath:
		return `SELECT "ffmpegPath" FROM server_settings WHERE singleton=1`, `SELECT octet_length("ffmpegPath") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "ffmpegPath"=?,updated_ms=? WHERE singleton=1`
	case UOSettingUpdatesFeedUrl:
		return `SELECT "updates.feedUrl" FROM server_settings WHERE singleton=1`, `SELECT octet_length("updates.feedUrl") FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "updates.feedUrl"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsShortestFilm:
		return `SELECT "segments.shortestFilm" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.shortestFilm"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsViewerConfidencePercent:
		return `SELECT "segments.viewerConfidencePercent" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.viewerConfidencePercent"=?,updated_ms=? WHERE singleton=1`
	case UOSettingSegmentsAutomaticConfidencePercent:
		return `SELECT "segments.automaticConfidencePercent" FROM server_settings WHERE singleton=1`, `SELECT 0 FROM server_settings WHERE singleton=1`, `UPDATE server_settings SET "segments.automaticConfidencePercent"=?,updated_ms=? WHERE singleton=1`
	}
	return "", "", ""
}

// Point definitions may also be composed into the reader's OWN registry.
// They remain inert until HN Capture admits their exact native byte shapes.
func UOSettingsPointDefinitions() []stmt.Definition {
	var out []stmt.Definition
	for _, f := range uoSettingSpecs {
		if f.def.Kind == UOList || f.def.Kind == UORetention {
			continue
		}
		point, octets, _ := uoSettingSQL(f.key)
		for _, part := range []struct{ suffix, sql string }{{"value", point}, {"octets", octets}} {
			out = append(out, stmt.Definition{ID: uoSettingID(f.key, part.suffix), Owner: "store UO ordinary settings draft", CallSites: []string{"store/writer/uo_settings.go", "store/read/uo_settings.go"}, Kind: stmt.Read, SQL: part.sql, Cohorts: []stmt.Cohort{{Name: "singleton", Bindings: []any{}}}, Plan: uoSettingValuePlan(f.key)})
		}
	}
	out = append(out, stmt.Definition{ID: "uo.settings.log_retention_value", Owner: "store UO ordinary settings draft", CallSites: []string{"store/writer/uo_settings.go", "store/read/uo_settings.go"}, Kind: stmt.Read, SQL: `SELECT days FROM server_log_retention WHERE category=?`, Params: []stmt.Param{{Name: "category", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "category", Bindings: []any{"server"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_server_log_retention_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "server_log_retention", Name: "sqlite_autoindex_server_log_retention_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "category", Collation: "BINARY"}}}}}})
	return out
}
func uoSettingID(k UOSettingKey, suffix string) stmt.ID {
	return stmt.ID("uo.settings." + string(k) + "." + suffix)
}

// Review/capture cohorts are native typed bindings, never an executable receipt.
func uoStatementBindings(params []stmt.Param) []any {
	args := make([]any, len(params))
	for i, p := range params {
		if p.Nullable {
			continue
		}
		switch p.Type {
		case stmt.Boolean:
			args[i] = true
		case stmt.Real:
			args[i] = float64(1)
		case stmt.Integer:
			args[i] = int64(1)
		case stmt.Text:
			args[i] = "unit"
			switch p.Name {
			case "authority":
				args[i] = "local"
			case "account":
				args[i] = "account"
			case "profile":
				args[i] = "profile"
			case "scope":
				args[i] = "profile-server"
			case "device":
				args[i] = ""
			case "key":
				args[i] = "home.rowOrder"
			case "field", "setting":
				args[i] = "hardwareDevice"
			case "kind":
				args[i] = "string-list"
			}
		}
	}
	return args
}
func uoSettingPointPlan(table string) stmt.Plan {
	p := stmt.Plan{Hot: true, PrimaryKeys: []string{table}}
	if table == "r" {
		p.PrimaryKeys = []string{"r", "s"}
	}
	if table == "server_setting_sources" || table == "server_configuration_pending" || table == "server_log_retention" {
		name := "sqlite_autoindex_" + table + "_1"
		p.PrimaryKeys = nil
		p.Indexes = []string{name}
		col := "field"
		if table == "server_log_retention" {
			col = "category"
		}
		p.AutoIndexes = []stmt.AutoIndexBinding{{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: col, Collation: "BINARY"}}}}
	}
	return p
}

func uoDownloadQualities() []string {
	var out []string
	for _, q := range qualityladder.IDs() {
		if q != "automatic" {
			out = append(out, q)
		}
	}
	return out
}
func uoSettingValuePlan(k UOSettingKey) stmt.Plan {
	if k == UOSettingPerAccountCap || k == UOSettingServerCap {
		return stmt.Plan{Hot: true, PrimaryKeys: []string{"playback_owner_policy"}}
	}
	if k == UOSettingName {
		return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_configuration_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "configuration", Name: "sqlite_autoindex_configuration_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "key", Collation: "BINARY"}}}}}
	}
	return stmt.Plan{Hot: true, PrimaryKeys: []string{"server_settings"}}
}

func uoLogCategories() []UOLogCategory {
	return []UOLogCategory{UOLogClient, UOLogNetwork, UOLogPlayback, UOLogScan, UOLogServer}
}

func uoAdvertiseOrigin(raw string) (string, bool) {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, e := url.Parse(s)
	if e != nil || !uoSettingURL(s) || u.Hostname() == "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Opaque != "" || len(s) > 300 || strings.ContainsAny(s, "\\\r\n\t ") {
		return "", false
	}
	if u.Port() != "" {
		n, e := strconv.Atoi(u.Port())
		if e != nil || n < 1 || n > 65535 {
			return "", false
		}
	}
	return strings.ToLower(s), true
}
