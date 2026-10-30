package operations

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
)

// Typed nullable columns preserve registry defaults until an owner stores them.
// All required names are qualified: damaged schema must never become a value.
func readStoredSettings(tx *sql.Tx, v *Settings) error {
	var stored struct {
		SegmentShortestEpisode   sql.NullInt64
		SegmentShortestFilm      sql.NullInt64
		SegmentIntroWindow       sql.NullString
		SegmentCreditsWindow     sql.NullString
		SegmentFilmCreditsWindow sql.NullString
		SegmentTVMode            sql.NullString
		SegmentAnimeMode         sql.NullString
		SegmentMovieMode         sql.NullString

		SegmentUseChapterNames            sql.NullBool
		SegmentViewerConfidencePercent    sql.NullInt64
		SegmentAutomaticConfidencePercent sql.NullInt64
		AnalysisStorageGiB                sql.NullInt64
		AnalysisStageStorageMiB           sql.NullInt64
		SubtitleTrackLimit                sql.NullInt64
		SubtitleFileLimitMiB              sql.NullInt64
		HardwareBackend                   sql.NullString
		HardwareDevice                    sql.NullString
		HardwareSelection                 sql.NullString
		HardwareDecode                    sql.NullBool
		MemoryCap                         sql.NullString
		TranscodeLocation                 sql.NullString
		HDRToneMapping                    sql.NullBool
		HDRToneMappingAlgorithm           sql.NullString
		X264Preset                        sql.NullString
		DirectStreamRemux                 sql.NullBool
		PlanningPolicy                    sql.NullString
		ThrottleBufferMs                  sql.NullInt64
		PlayedRetentionMs                 sql.NullInt64
		TemporaryDirectory                sql.NullString
		FFmpegPath                        sql.NullString
		SessionStorageMiB                 sql.NullInt64
		MaxConcurrentSessions             sql.NullInt64
		MaxHardwareSessions               sql.NullInt64
		MaxSoftwareSessions               sql.NullInt64
		MaxBackgroundSessions             sql.NullInt64
		DiagnosticDays                    sql.NullInt64
		NotificationDays                  sql.NullInt64
		JobDays                           sql.NullInt64
		PlayHistoryDays                   sql.NullInt64
		APIRequestsPerMinute              sql.NullInt64
		CommunityActivityEnabled          sql.NullBool
		UpdateChannel                     sql.NullString
		UpdateFeedURL                     sql.NullString
		MissingTitles                     sql.NullString
		MissingTitlesDays                 sql.NullInt64
		SegmentDetection                  sql.NullString
		SecureConnections                 sql.NullString
		RemoteSignInPolicy                sql.NullString
		RemoteBitrateLimitKbps            sql.NullInt64
		ConnectionLimit                   sql.NullInt64
		AdvertiseURL                      sql.NullString
		LANDiscoveryEnabled               sql.NullBool
		DLNAEnabled                       sql.NullBool
		UploadCapacityKbps                sql.NullInt64
		PausedSessionTimeoutMinutes       sql.NullInt64
		AdvertisedInterface               sql.NullString
		OwnAddressEnabled                 sql.NullBool
		CustomCertificatePath             sql.NullString
		CustomCertificateKeyPath          sql.NullString
		CustomCertificateDomain           sql.NullString
		LogLevel                          sql.NullString
		LogDetailMinutes                  sql.NullInt64
		PeopleInvitationRole              sql.NullString
		PeopleInvitationDays              sql.NullInt64
		ConversionMode                    sql.NullString
		OutputFormat                      sql.NullString
		DownloadMaxQuality                sql.NullString
		MusicAwayQuality                  sql.NullString
		IntegrationCompatibility          sql.NullBool
	}
	err := tx.QueryRow(`SELECT s."segments.shortestFilm",s."segments.shortestEpisode",s."segments.introWindow",s."segments.creditsWindow",s."segments.filmCreditsWindow",s."segments.tvMode",s."segments.animeMode",s."segments.movieMode",s."segments.useChapterNames", s."segments.viewerConfidencePercent", s."segments.automaticConfidencePercent", s."analysisStorageGiB", s."analysisStageStorageMiB", s."subtitleTrackLimit", s."subtitleFileLimitMiB", s."hardwareBackend", s."hardwareDevice", s."hardwareSelection", s."hardwareDecode", s."hdrToneMapping", s."hdrToneMappingAlgorithm", s."x264Preset", s."directStreamRemux", s."planningPolicy", s."throttleBufferMs", s."playedRetentionMs", s."temporaryDirectory", s."ffmpegPath", s."sessionStorageMiB", s."memoryCap", s."transcodeLocation", s."maxConcurrentSessions", s."maxHardwareSessions", s."maxSoftwareSessions", s."maxBackgroundSessions", s."diagnosticDays", s."notificationDays", s."jobDays", s."playHistoryDays", s."apiRequestsPerMinute", s."home.communityActivityEnabled", s."updates.releaseChannel", s."updates.feedUrl", s."library.missingTitles", s."library.missingTitlesDays", s."library.segmentDetection", s."secureConnections",s."remoteSignInPolicy", s."remoteBitrateLimitKbps", s."connectionLimit", s."advertiseUrl", s."lanDiscoveryEnabled", s."dlnaEnabled", s."uploadCapacityKbps", s."pausedSessionTimeoutMinutes", s."advertisedInterface", s."ownAddressEnabled", s."customCertificatePath", s."customCertificateKeyPath", s."customCertificateDomain", s."logLevel",s."logDetailMinutes", s."people.invitation_role", s."people.invitation_days", s."conversionMode", s."outputFormat", s."downloadMaxQuality", s."musicAwayQuality",s."integrations.compatibilityEnabled" FROM server_settings AS s WHERE s.singleton=1`).Scan(&stored.SegmentShortestFilm, &stored.SegmentShortestEpisode, &stored.SegmentIntroWindow, &stored.SegmentCreditsWindow, &stored.SegmentFilmCreditsWindow, &stored.SegmentTVMode, &stored.SegmentAnimeMode, &stored.SegmentMovieMode, &stored.SegmentUseChapterNames, &stored.SegmentViewerConfidencePercent, &stored.SegmentAutomaticConfidencePercent, &stored.AnalysisStorageGiB, &stored.AnalysisStageStorageMiB, &stored.SubtitleTrackLimit, &stored.SubtitleFileLimitMiB, &stored.HardwareBackend, &stored.HardwareDevice, &stored.HardwareSelection, &stored.HardwareDecode, &stored.HDRToneMapping, &stored.HDRToneMappingAlgorithm, &stored.X264Preset, &stored.DirectStreamRemux, &stored.PlanningPolicy, &stored.ThrottleBufferMs, &stored.PlayedRetentionMs, &stored.TemporaryDirectory, &stored.FFmpegPath, &stored.SessionStorageMiB, &stored.MemoryCap, &stored.TranscodeLocation, &stored.MaxConcurrentSessions, &stored.MaxHardwareSessions, &stored.MaxSoftwareSessions, &stored.MaxBackgroundSessions, &stored.DiagnosticDays, &stored.NotificationDays, &stored.JobDays, &stored.PlayHistoryDays, &stored.APIRequestsPerMinute, &stored.CommunityActivityEnabled, &stored.UpdateChannel, &stored.UpdateFeedURL, &stored.MissingTitles, &stored.MissingTitlesDays, &stored.SegmentDetection, &stored.SecureConnections, &stored.RemoteSignInPolicy, &stored.RemoteBitrateLimitKbps, &stored.ConnectionLimit, &stored.AdvertiseURL, &stored.LANDiscoveryEnabled, &stored.DLNAEnabled, &stored.UploadCapacityKbps, &stored.PausedSessionTimeoutMinutes, &stored.AdvertisedInterface, &stored.OwnAddressEnabled, &stored.CustomCertificatePath, &stored.CustomCertificateKeyPath, &stored.CustomCertificateDomain, &stored.LogLevel, &stored.LogDetailMinutes, &stored.PeopleInvitationRole, &stored.PeopleInvitationDays, &stored.ConversionMode, &stored.OutputFormat, &stored.DownloadMaxQuality, &stored.MusicAwayQuality, &stored.IntegrationCompatibility)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	next := *v
	if stored.SegmentFilmCreditsWindow.Valid {
		next.SegmentFilmCreditsWindow = stored.SegmentFilmCreditsWindow.String
	}
	if stored.SegmentCreditsWindow.Valid {
		next.SegmentCreditsWindow = stored.SegmentCreditsWindow.String
	}
	if stored.SegmentIntroWindow.Valid {
		next.SegmentIntroWindow = stored.SegmentIntroWindow.String
	}
	if stored.SegmentShortestFilm.Valid {
		next.SegmentShortestFilm = int(stored.SegmentShortestFilm.Int64)
	}
	if stored.SegmentShortestEpisode.Valid {
		next.SegmentShortestEpisode = int(stored.SegmentShortestEpisode.Int64)
	}
	if stored.SegmentTVMode.Valid {
		next.SegmentTVMode = stored.SegmentTVMode.String
	}
	if stored.SegmentAnimeMode.Valid {
		next.SegmentAnimeMode = stored.SegmentAnimeMode.String
	}
	if stored.SegmentMovieMode.Valid {
		next.SegmentMovieMode = stored.SegmentMovieMode.String
	}
	if stored.SegmentUseChapterNames.Valid {
		chapterNames := stored.SegmentUseChapterNames.Bool
		next.SegmentUseChapterNames = &chapterNames
	}
	if stored.SegmentViewerConfidencePercent.Valid {
		next.SegmentViewerConfidencePercent = int(stored.SegmentViewerConfidencePercent.Int64)
	}
	if stored.SegmentAutomaticConfidencePercent.Valid {
		next.SegmentAutomaticConfidencePercent = int(stored.SegmentAutomaticConfidencePercent.Int64)
	}
	if stored.AnalysisStorageGiB.Valid {
		next.AnalysisStorageGiB = int(stored.AnalysisStorageGiB.Int64)
	}
	if stored.AnalysisStageStorageMiB.Valid {
		next.AnalysisStageStorageMiB = int(stored.AnalysisStageStorageMiB.Int64)
	}
	if stored.SubtitleTrackLimit.Valid {
		next.SubtitleTrackLimit = int(stored.SubtitleTrackLimit.Int64)
	}
	if stored.SubtitleFileLimitMiB.Valid {
		next.SubtitleFileLimitMiB = int(stored.SubtitleFileLimitMiB.Int64)
	}
	if stored.HardwareBackend.Valid {
		next.HardwareBackend = stored.HardwareBackend.String
	}
	if stored.HardwareDevice.Valid {
		next.HardwareDevice = stored.HardwareDevice.String
	}
	if stored.HardwareSelection.Valid {
		next.HardwareSelection = stored.HardwareSelection.String
	}
	if stored.HardwareDecode.Valid {
		next.HardwareDecode = stored.HardwareDecode.Bool
	}
	if stored.TranscodeLocation.Valid {
		next.TranscodeLocation = stored.TranscodeLocation.String
	}
	if stored.MemoryCap.Valid {
		next.MemoryCap = stored.MemoryCap.String
	}
	if stored.HDRToneMapping.Valid {
		next.HDRToneMapping = stored.HDRToneMapping.Bool
	}
	if stored.HDRToneMappingAlgorithm.Valid {
		next.HDRToneMappingAlgorithm = stored.HDRToneMappingAlgorithm.String
	}
	if stored.X264Preset.Valid {
		next.X264Preset = stored.X264Preset.String
	}
	if stored.DirectStreamRemux.Valid {
		next.DirectStreamRemux = stored.DirectStreamRemux.Bool
	}
	if stored.PlanningPolicy.Valid {
		next.PlanningPolicy = stored.PlanningPolicy.String
	}
	if stored.ThrottleBufferMs.Valid {
		next.ThrottleBufferMs = int(stored.ThrottleBufferMs.Int64)
	}
	if stored.PlayedRetentionMs.Valid {
		next.PlayedRetentionMs = int(stored.PlayedRetentionMs.Int64)
	}
	if stored.TemporaryDirectory.Valid {
		next.TemporaryDirectory = stored.TemporaryDirectory.String
	}
	if stored.FFmpegPath.Valid {
		next.FFmpegPath = stored.FFmpegPath.String
	}
	if stored.SessionStorageMiB.Valid {
		next.SessionStorageMiB = int(stored.SessionStorageMiB.Int64)
	}
	if stored.MaxConcurrentSessions.Valid {
		next.MaxConcurrentSessions = int(stored.MaxConcurrentSessions.Int64)
	}
	if stored.MaxHardwareSessions.Valid {
		next.MaxHardwareSessions = int(stored.MaxHardwareSessions.Int64)
	}
	if stored.MaxSoftwareSessions.Valid {
		next.MaxSoftwareSessions = int(stored.MaxSoftwareSessions.Int64)
	}
	if stored.MaxBackgroundSessions.Valid {
		next.MaxBackgroundSessions = int(stored.MaxBackgroundSessions.Int64)
	}
	if stored.DiagnosticDays.Valid {
		next.DiagnosticDays = int(stored.DiagnosticDays.Int64)
	}
	if stored.NotificationDays.Valid {
		next.NotificationDays = int(stored.NotificationDays.Int64)
	}
	if stored.JobDays.Valid {
		next.JobDays = int(stored.JobDays.Int64)
	}
	if stored.PlayHistoryDays.Valid {
		next.PlayHistoryDays = int(stored.PlayHistoryDays.Int64)
	}
	if stored.APIRequestsPerMinute.Valid {
		next.APIRequestsPerMinute = int(stored.APIRequestsPerMinute.Int64)
	}
	if stored.CommunityActivityEnabled.Valid {
		next.CommunityActivityEnabled = stored.CommunityActivityEnabled.Bool
	}
	if stored.UpdateChannel.Valid {
		next.UpdateChannel = stored.UpdateChannel.String
	}
	if stored.UpdateFeedURL.Valid {
		next.UpdateFeedURL = stored.UpdateFeedURL.String
	}
	if stored.MissingTitles.Valid {
		next.MissingTitles = stored.MissingTitles.String
	}
	if stored.MissingTitlesDays.Valid {
		next.MissingTitlesDays = int(stored.MissingTitlesDays.Int64)
	}
	if stored.SegmentDetection.Valid {
		next.SegmentDetection = stored.SegmentDetection.String
	}
	if stored.SecureConnections.Valid {
		next.SecureConnections = stored.SecureConnections.String
	}
	if stored.RemoteSignInPolicy.Valid {
		next.RemoteSignInPolicy = stored.RemoteSignInPolicy.String
	}
	if stored.RemoteBitrateLimitKbps.Valid {
		next.RemoteBitrateLimitKbps = int(stored.RemoteBitrateLimitKbps.Int64)
	}
	if stored.ConnectionLimit.Valid {
		next.ConnectionLimit = int(stored.ConnectionLimit.Int64)
	}
	if stored.AdvertiseURL.Valid {
		next.AdvertiseURL = stored.AdvertiseURL.String
	}
	if stored.LANDiscoveryEnabled.Valid {
		next.LANDiscoveryEnabled = stored.LANDiscoveryEnabled.Bool
	}
	if stored.DLNAEnabled.Valid {
		next.DLNAEnabled = stored.DLNAEnabled.Bool
	}
	if stored.UploadCapacityKbps.Valid {
		next.UploadCapacityKbps = int(stored.UploadCapacityKbps.Int64)
	}
	if stored.PausedSessionTimeoutMinutes.Valid {
		next.PausedSessionTimeoutMinutes = int(stored.PausedSessionTimeoutMinutes.Int64)
	}
	if stored.AdvertisedInterface.Valid {
		next.AdvertisedInterface = stored.AdvertisedInterface.String
	}
	if stored.OwnAddressEnabled.Valid {
		next.OwnAddressEnabled = stored.OwnAddressEnabled.Bool
	}
	if stored.CustomCertificatePath.Valid {
		next.CustomCertificatePath = stored.CustomCertificatePath.String
	}
	if stored.CustomCertificateKeyPath.Valid {
		next.CustomCertificateKeyPath = stored.CustomCertificateKeyPath.String
	}
	if stored.CustomCertificateDomain.Valid {
		next.CustomCertificateDomain = stored.CustomCertificateDomain.String
	}
	if stored.PeopleInvitationRole.Valid {
		next.PeopleInvitationRole = PeopleInviteRole(stored.PeopleInvitationRole.String)
	}
	if stored.PeopleInvitationDays.Valid {
		next.PeopleInvitationDays = PeopleInvitationDays(stored.PeopleInvitationDays.Int64)
	}
	if stored.LogDetailMinutes.Valid {
		next.LogDetailMinutes = LogDetailMinutes(stored.LogDetailMinutes.Int64)
	}
	if stored.LogLevel.Valid {
		next.LogLevel = stored.LogLevel.String
	}
	if stored.ConversionMode.Valid {
		next.ConversionMode = stored.ConversionMode.String
	}
	if stored.OutputFormat.Valid {
		next.OutputFormat = stored.OutputFormat.String
	}
	if stored.DownloadMaxQuality.Valid {
		next.DownloadMaxQuality = stored.DownloadMaxQuality.String
	}
	if stored.MusicAwayQuality.Valid {
		next.MusicAwayQuality = stored.MusicAwayQuality.String
	}
	next.LANNetworks = []string{}
	next.AccessURLs = []string{}
	next.TrustedProxies = []string{}
	rows, err := tx.Query(`SELECT setting,value FROM server_setting_lists ORDER BY setting,position`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		switch key {
		case "lanNetworks":
			next.LANNetworks = append(next.LANNetworks, value)
		case "accessUrls":
			next.AccessURLs = append(next.AccessURLs, value)
		case "trustedProxies":
			next.TrustedProxies = append(next.TrustedProxies, value)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.Query(`SELECT category,days FROM server_log_retention ORDER BY category`)
	if err != nil {
		return err
	}
	defer rows.Close()
	next.LogRetention = []LogRetention{}
	for rows.Next() {
		var entry LogRetention
		if err = rows.Scan(&entry.Category, &entry.Days); err != nil {
			return err
		}
		next.LogRetention = append(next.LogRetention, entry)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	*v = next
	if stored.IntegrationCompatibility.Valid {
		v.IntegrationCompatibility = stored.IntegrationCompatibility.Bool
	}

	return nil
}

func writeStoredSettings(tx *sql.Tx, v Settings, now int64) error {
	_, err := tx.Exec(`INSERT INTO server_settings(singleton,updated_ms,"segments.shortestFilm","segments.shortestEpisode","segments.introWindow","segments.creditsWindow","segments.filmCreditsWindow","segments.tvMode","segments.animeMode","segments.movieMode","segments.useChapterNames","segments.viewerConfidencePercent","segments.automaticConfidencePercent","analysisStorageGiB","analysisStageStorageMiB","subtitleTrackLimit","subtitleFileLimitMiB","hardwareBackend","hardwareDevice","hardwareSelection","hardwareDecode","hdrToneMapping","hdrToneMappingAlgorithm","x264Preset","directStreamRemux","planningPolicy","throttleBufferMs","playedRetentionMs","temporaryDirectory","ffmpegPath","sessionStorageMiB","memoryCap","transcodeLocation","maxConcurrentSessions","maxHardwareSessions","maxSoftwareSessions","maxBackgroundSessions","diagnosticDays","notificationDays","jobDays","playHistoryDays","apiRequestsPerMinute","home.communityActivityEnabled","updates.releaseChannel","updates.feedUrl","library.missingTitles","library.missingTitlesDays","library.segmentDetection","secureConnections","remoteSignInPolicy","remoteBitrateLimitKbps","connectionLimit","advertiseUrl","lanDiscoveryEnabled","dlnaEnabled","uploadCapacityKbps","pausedSessionTimeoutMinutes","advertisedInterface","ownAddressEnabled","customCertificatePath","customCertificateKeyPath","customCertificateDomain","logLevel","logDetailMinutes","people.invitation_role","people.invitation_days","conversionMode","outputFormat","downloadMaxQuality","musicAwayQuality","integrations.compatibilityEnabled") VALUES(1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET updated_ms=excluded.updated_ms,"secureConnections"=excluded."secureConnections","segments.filmCreditsWindow"=excluded."segments.filmCreditsWindow","segments.creditsWindow"=excluded."segments.creditsWindow","segments.introWindow"=excluded."segments.introWindow","segments.shortestFilm"=excluded."segments.shortestFilm","segments.shortestEpisode"=excluded."segments.shortestEpisode","segments.tvMode"=excluded."segments.tvMode","segments.animeMode"=excluded."segments.animeMode","segments.movieMode"=excluded."segments.movieMode","segments.useChapterNames"=excluded."segments.useChapterNames","segments.viewerConfidencePercent"=excluded."segments.viewerConfidencePercent","segments.automaticConfidencePercent"=excluded."segments.automaticConfidencePercent","analysisStorageGiB"=excluded."analysisStorageGiB","analysisStageStorageMiB"=excluded."analysisStageStorageMiB","subtitleTrackLimit"=excluded."subtitleTrackLimit","subtitleFileLimitMiB"=excluded."subtitleFileLimitMiB","hardwareBackend"=excluded."hardwareBackend","hardwareDevice"=excluded."hardwareDevice","hardwareSelection"=excluded."hardwareSelection","hardwareDecode"=excluded."hardwareDecode","hdrToneMapping"=excluded."hdrToneMapping","hdrToneMappingAlgorithm"=excluded."hdrToneMappingAlgorithm","x264Preset"=excluded."x264Preset","directStreamRemux"=excluded."directStreamRemux","planningPolicy"=excluded."planningPolicy","throttleBufferMs"=excluded."throttleBufferMs","playedRetentionMs"=excluded."playedRetentionMs","temporaryDirectory"=excluded."temporaryDirectory","ffmpegPath"=excluded."ffmpegPath","sessionStorageMiB"=excluded."sessionStorageMiB","memoryCap"=excluded."memoryCap","transcodeLocation"=excluded."transcodeLocation","maxConcurrentSessions"=excluded."maxConcurrentSessions","maxHardwareSessions"=excluded."maxHardwareSessions","maxSoftwareSessions"=excluded."maxSoftwareSessions","maxBackgroundSessions"=excluded."maxBackgroundSessions","diagnosticDays"=excluded."diagnosticDays","notificationDays"=excluded."notificationDays","jobDays"=excluded."jobDays","playHistoryDays"=excluded."playHistoryDays","apiRequestsPerMinute"=excluded."apiRequestsPerMinute","home.communityActivityEnabled"=excluded."home.communityActivityEnabled","updates.releaseChannel"=excluded."updates.releaseChannel","updates.feedUrl"=excluded."updates.feedUrl","library.missingTitles"=excluded."library.missingTitles","library.missingTitlesDays"=excluded."library.missingTitlesDays","library.segmentDetection"=excluded."library.segmentDetection","remoteSignInPolicy"=excluded."remoteSignInPolicy","remoteBitrateLimitKbps"=excluded."remoteBitrateLimitKbps","connectionLimit"=excluded."connectionLimit","advertiseUrl"=excluded."advertiseUrl","lanDiscoveryEnabled"=excluded."lanDiscoveryEnabled","dlnaEnabled"=excluded."dlnaEnabled","uploadCapacityKbps"=excluded."uploadCapacityKbps","pausedSessionTimeoutMinutes"=excluded."pausedSessionTimeoutMinutes","advertisedInterface"=excluded."advertisedInterface","ownAddressEnabled"=excluded."ownAddressEnabled","customCertificatePath"=excluded."customCertificatePath","customCertificateKeyPath"=excluded."customCertificateKeyPath","customCertificateDomain"=excluded."customCertificateDomain","logLevel"=excluded."logLevel","logDetailMinutes"=excluded."logDetailMinutes","people.invitation_role"=excluded."people.invitation_role","people.invitation_days"=excluded."people.invitation_days","conversionMode"=excluded."conversionMode","outputFormat"=excluded."outputFormat","downloadMaxQuality"=excluded."downloadMaxQuality","musicAwayQuality"=excluded."musicAwayQuality","integrations.compatibilityEnabled"=excluded."integrations.compatibilityEnabled"`, now, v.SegmentShortestFilm, v.SegmentShortestEpisode, v.SegmentIntroWindow, v.SegmentCreditsWindow, v.SegmentFilmCreditsWindow, v.SegmentTVMode, v.SegmentAnimeMode, v.SegmentMovieMode, v.SegmentUseChapterNames, v.SegmentViewerConfidencePercent, v.SegmentAutomaticConfidencePercent, v.AnalysisStorageGiB, v.AnalysisStageStorageMiB, v.SubtitleTrackLimit, v.SubtitleFileLimitMiB, v.HardwareBackend, v.HardwareDevice, v.HardwareSelection, v.HardwareDecode, v.HDRToneMapping, v.HDRToneMappingAlgorithm, v.X264Preset, v.DirectStreamRemux, v.PlanningPolicy, v.ThrottleBufferMs, v.PlayedRetentionMs, v.TemporaryDirectory, v.FFmpegPath, v.SessionStorageMiB, v.MemoryCap, v.TranscodeLocation, v.MaxConcurrentSessions, v.MaxHardwareSessions, v.MaxSoftwareSessions, v.MaxBackgroundSessions, v.DiagnosticDays, v.NotificationDays, v.JobDays, v.PlayHistoryDays, v.APIRequestsPerMinute, v.CommunityActivityEnabled, v.UpdateChannel, v.UpdateFeedURL, v.MissingTitles, v.MissingTitlesDays, v.SegmentDetection, v.SecureConnections, v.RemoteSignInPolicy, v.RemoteBitrateLimitKbps, v.ConnectionLimit, v.AdvertiseURL, v.LANDiscoveryEnabled, v.DLNAEnabled, v.UploadCapacityKbps, v.PausedSessionTimeoutMinutes, v.AdvertisedInterface, v.OwnAddressEnabled, v.CustomCertificatePath, v.CustomCertificateKeyPath, v.CustomCertificateDomain, v.LogLevel, v.LogDetailMinutes, v.PeopleInvitationRole, v.PeopleInvitationDays, v.ConversionMode, v.OutputFormat, v.DownloadMaxQuality, v.MusicAwayQuality, v.IntegrationCompatibility)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM server_setting_lists`); err != nil {
		return err
	}
	for key, values := range map[string][]string{"lanNetworks": v.LANNetworks, "accessUrls": v.AccessURLs, "trustedProxies": v.TrustedProxies} {
		for position, value := range values {
			if _, err = tx.Exec(`INSERT INTO server_setting_lists VALUES(?,?,?)`, key, position, value); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`DELETE FROM server_log_retention`); err != nil {
		return err
	}
	for _, entry := range v.LogRetention {
		if _, err = tx.Exec(`INSERT INTO server_log_retention VALUES(?,?)`, entry.Category, entry.Days); err != nil {
			return err
		}
	}
	return nil
}

// SettingsValues projects the typed wire field names without serializing a
// second persistence document or silently coercing values.
func SettingsValues(v Settings) map[string]any {
	out := map[string]any{}
	value := reflect.ValueOf(v)
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		key := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if key != "" && key != "-" {
			out[key] = value.Field(i).Interface()
		}
	}
	return out
}
