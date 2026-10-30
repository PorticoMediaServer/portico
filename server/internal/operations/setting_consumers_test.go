package operations_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type settingConsumer struct{ file, witness, effect string }

// This executable trigger, rather than settings readback, consumes the region
// default for new screen libraries. Keep its admission and complete INSERT in
// the witness so a column declaration or unrelated SQL cannot satisfy it.
const newLibraryScreenPolicy = `CREATE TRIGGER screen_policy_create AFTER INSERT ON libraries WHEN NEW.kind IN('movie','tv','anime') BEGIN
 INSERT INTO screen_metadata_policies(library_id,providers,language,region) SELECT NEW.id,'["tmdb"]',default_language,default_region FROM metadata_runtime_settings WHERE singleton=1;
END;`

// Every entry names a reviewed runtime boundary, not a settings form or registry.
// Semantic tests beside those consumers verify behavior; this guard catches new
// unowned fields and consumer removal. It is not a substitute for runtime tests.
var settingConsumers = map[string]settingConsumer{
	// integrationEnabled reads the persisted switch on each compatibility admission.
	// TestCompatibilityDisabledPublicAndDeclaredAliasBoundary exercises off/on/off
	// for both public discovery and an authenticated catalogue route.
	"operations.Settings.integrations.compatibilityEnabled":                  {"internal/httpapi/integration_credentials_routes.go", "literal:integrations.compatibilityEnabled", "Refuse compatibility routes while disabled and admit them through native authority when enabled"},
	"livechannels.LiveSourceSettings.enabled":                                {"internal/livechannels/persisted_configuration.go", "Enabled", "Pause or activate the source and fence its publishers"},
	"livechannels.LiveSourceSettings.viewerAccess":                           {"internal/livechannels/persisted_configuration.go", "ViewerAccess", "Persist and enforce owner-only or server-member access"},
	"livechannels.LiveSourceSettings.remote.kind":                            {"internal/livechannels/persisted_configuration.go", "Kind", "Keep the saved remote provider grammar immutable"},
	"livechannels.LiveSourceSettings.remote.guideUrl":                        {"internal/livechannels/persisted_configuration.go", "GuideURL", "Admit and persist the replacement guide address"},
	"livechannels.LiveSourceSettings.remote.guideUrlRedacted":                {"internal/livechannels/persisted_configuration.go", "GuideURLRedacted", "Preserve the private guide address when its redacted readback is saved"},
	"livechannels.LiveSourceSettings.remote.refreshMs":                       {"internal/livechannels/persisted_configuration.go", "RefreshMs", "Schedule the next remote guide refresh"},
	"livechannels.LiveSourceSettings.remote.ownerLimit":                      {"internal/livechannels/persisted_configuration.go", "OwnerLimit", "Limit the effective tuner capacity"},
	"livechannels.LiveSourceSettings.remote.useDiscoveredCapacity":           {"internal/livechannels/persisted_configuration.go", "UseDiscoveredCapacity", "Choose discovered versus owner-provided tuner capacity"},
	"livechannels.LiveSourceSettings.remote.username":                        {"internal/livechannels/persisted_configuration.go", "Username", "Replace or preserve the write-only provider username"},
	"livechannels.LiveSourceSettings.remote.password":                        {"internal/livechannels/persisted_configuration.go", "Password", "Replace or preserve the write-only provider password"},
	"livechannels.LiveSourceSettings.remote.confirmedLanRoots":               {"internal/livechannels/persisted_configuration.go", "ConfirmedLANRoots", "Approve exact local-network endpoints before changing source configuration"},
	"metadata.MetadataSettingsUpdate.libraries.lyricsSource":                 {"internal/lyrics/bulk.go", "literal:lyrics_source", "Fence lyric acquisition with the selected library source"},
	"metadata.MetadataSettingsUpdate.libraries.episodeThumbnailMode":         {"internal/metadata/runtime_libraries.go", "ApplyEpisodeThumbnailModeTx", "Apply the library's canonical episode-thumbnail publication policy"},
	"metadata.MetadataSettingsUpdate.libraries.scrubPreviews":                {"internal/metadata/runtime_libraries.go", "ApplyScanPolicyTx", "Add or remove trickplay from the library's admitted scan operations"},
	"metadata.MetadataSettingsUpdate.artworkLanguage":                        {"internal/metadata/artwork_preference.go", "literal:artwork_language", "Rank automatic artwork by the saved language without replacing owner choices"},
	"metadata.MetadataSettingsUpdate.artworkPreference":                      {"internal/metadata/artwork_preference.go", "literal:artwork_preference", "Choose online versus folder automatic artwork while preserving owner selections"},
	"metadata.MetadataSettingsUpdate.onlineLookups":                          {"internal/localonly/localonly.go", "literal:online_lookups", "Admit online acquisition and fence publication with the current policy epoch"},
	"metadata.MetadataSettingsUpdate.defaultLanguage":                        {"internal/metadata/library_agent.go", "literal:default_language", "Seed the supported language choice when creating a new library"},
	"metadata.MetadataSettingsUpdate.defaultRegion":                          {"internal/persistence/migrations/0001_baseline.sql", "sql:" + newLibraryScreenPolicy, "Seed a new screen library's regional metadata policy without changing existing libraries"},
	"metadata.MetadataSettingsUpdate.libraries.id":                           {"internal/metadata/runtime_libraries.go", "function:readMetadataLibrary", "Bind each metadata update to the exact existing library and its revision"},
	"metadata.MetadataSettingsUpdate.libraries.agent":                        {"internal/localonly/localonly.go", "literal:a.agent='local'", "Enforce each library's local-only acquisition policy"},
	"operations.Settings.segments.filmCreditsWindow":                         {"internal/mediaanalysis/segment_planning.go", "literal:segments.filmCreditsWindow", "Bound the region read by the existing detector"},
	"operations.Settings.segments.creditsWindow":                             {"internal/mediaanalysis/segment_planning.go", "literal:segments.creditsWindow", "Bound the region read by the existing detector"},
	"operations.Settings.segments.introWindow":                               {"internal/mediaanalysis/segment_planning.go", "literal:segments.introWindow", "Bound the region read by the existing detector"},
	"operations.Settings.segments.shortestFilm":                              {"internal/mediaanalysis/segment_planning.go", "literal:segments.shortestFilm", "Bound measured detection to eligible file lengths"},
	"operations.Settings.segments.shortestEpisode":                           {"internal/mediaanalysis/segment_planning.go", "literal:segments.shortestEpisode", "Bound measured detection to eligible file lengths"},
	"operations.Settings.segments.tvMode":                                    {"internal/mediaanalysis/segment_modes.go", "literal:segments.tvMode", "Resolve the effective library kind detection mode"},
	"operations.Settings.segments.animeMode":                                 {"internal/mediaanalysis/segment_modes.go", "literal:segments.animeMode", "Resolve the effective library kind detection mode"},
	"operations.Settings.segments.movieMode":                                 {"internal/mediaanalysis/segment_modes.go", "literal:segments.movieMode", "Resolve the effective library kind detection mode"},
	"operations.Settings.segments.useChapterNames":                           {"internal/segmentmarkers/policy.go", "literal:segments.useChapterNames", "Choose whether unapproved embedded chapter labels reach viewers"},
	"operations.Settings.segments.viewerConfidencePercent":                   {"internal/segmentmarkers/policy.go", "literal:segments.viewerConfidencePercent", "Choose the confidence floor of unapproved viewer markers"},
	"operations.Settings.segments.automaticConfidencePercent":                {"internal/segmentmarkers/policy.go", "literal:segments.automaticConfidencePercent", "Choose the confidence floor of measured automatic markers"},
	"administration.DVRDefaults.keepPolicy.keepCount":                        {"internal/administration/dvr_runtime.go", "KeepCount", "Set padding or retained recording count/days"},
	"administration.DVRDefaults.keepPolicy.keepDays":                         {"internal/administration/dvr_runtime.go", "KeepDays", "Set padding or retained recording count/days"},
	"administration.DVRDefaults.keepPolicy.mode":                             {"internal/administration/dvr_runtime.go", "Mode", "Set padding or retained recording count/days"},
	"administration.DVRDefaults.liveTunerConflict":                           {"internal/livechannels/allocator.go", "literal:live_tuner_conflict", "Keep or stop a live viewer whose tuner a recording needs"},
	"administration.DVRDefaults.postPaddingMs":                               {"internal/administration/dvr_runtime.go", "PostPaddingMs", "Set padding or retained recording count/days"},
	"administration.DVRDefaults.prePaddingMs":                                {"internal/administration/dvr_runtime.go", "PrePaddingMs", "Set padding or retained recording count/days"},
	"administration.LibrarySettings.allowMediaDeletion":                      {"internal/administration/deletion.go", "AllowMediaDeletion", "Authorize physical media deletion"},
	"administration.LibrarySettings.analysis":                                {"internal/administration/library_runtime.go", "Analysis", "Select analysis recipes and trickplay geometry"},
	"administration.LibrarySettings.scanDepth":                               {"internal/administration/library_runtime.go", "ScanDepth", "Apply Basic, Complete, Custom or file-list scan policy"},
	"administration.LibrarySettings.groupCollections":                        {"internal/catalog/collections.go", "literal:group_collections", "Gate automatic library collection grouping"},
	"administration.LibrarySettings.localTags":                               {"internal/metadata/music_inputs.go", "LocalMode", "Include or suppress local audio tag evidence"},
	"administration.LibrarySettings.lyricsSource":                            {"internal/lyrics/bulk.go", "literal:lyrics_source", "Fence online lyric acquisition with the current library policy"},
	"administration.LibrarySettings.titleSource":                             {"internal/catalog/inventory.go", "TitleSource", "Choose admitted tags or source names for ingested titles"},
	"administration.LibrarySettings.episodeNumbering":                        {"internal/catalog/show_settings.go", "literal:episode_numbering", "Choose season or absolute episode numbering"},
	"administration.LibrarySettings.findExtras":                              {"internal/catalog/extras.go", "FindExtras", "Gate discovery of related extra media"},
	"administration.LibrarySettings.sortIgnoreArticles":                      {"internal/compactcatalog/write.go", "literal:sort_ignore_articles", "Build title sort keys using actual library article policy"},
	"administration.LibrarySettings.refreshMode":                             {"internal/metadata/screen_base.go", "RefreshMode", "Capture replace-unlocked versus fill-missing publication policy"},
	"administration.LibrarySettings.acoustidEnabled":                         {"internal/metadata/music_inputs.go", "AcoustIDEnabled", "Gate the audio fingerprint provider request"},
	"administration.LibrarySettings.segmentDetection":                        {"internal/mediaanalysis/segment_modes.go", "literal:$.segmentDetection", "Choose a library's intro and credits detection mode"},
	"administration.LibrarySettings.monitorChanges":                          {"internal/ingestion/monitor.go", "literal:$.monitorChanges", "Watch the library's folders and scan what changes"},
	"administration.LibrarySettings.continueWatching.includeSeasonPremieres": {"internal/catalog/continue_policy.go", "IncludeSeasonPremieres", "Select continue-watching rows and completion rules"},
	"administration.LibrarySettings.continueWatching.maximumItems":           {"internal/catalog/continue_policy.go", "MaximumItems", "Select continue-watching rows and completion rules"},
	"administration.LibrarySettings.continueWatching.videoCompletion":        {"internal/catalog/continue_policy.go", "VideoCompletion", "Select continue-watching rows and completion rules"},
	"administration.LibrarySettings.continueWatching.videoPlayedThreshold":   {"internal/catalog/continue_policy.go", "VideoPlayedThreshold", "Select continue-watching rows and completion rules"},
	"administration.LibrarySettings.continueWatching.weeks":                  {"internal/catalog/continue_policy.go", "Weeks", "Select continue-watching rows and completion rules"},
	"administration.LibrarySettings.navigation.chapterThumbnailMode":         {"internal/administration/library_runtime.go", "ChapterThumbnailMode", "Select analysis recipes and trickplay geometry"},
	"administration.LibrarySettings.navigation.trickplayIntervalMs":          {"internal/administration/library_runtime.go", "TrickplayIntervalMs", "Select analysis recipes and trickplay geometry"},
	"administration.LibrarySettings.navigation.trickplayMaxTiles":            {"internal/administration/library_runtime.go", "TrickplayMaxTiles", "Select analysis recipes and trickplay geometry"},
	"administration.LibrarySettings.navigation.trickplayTileWidth":           {"internal/administration/library_runtime.go", "TrickplayTileWidth", "Select analysis recipes and trickplay geometry"},
	"administration.LibrarySettings.providers.language":                      {"internal/administration/library_runtime.go", "Language", "Apply provider and locale policy to domain tables"},
	"administration.LibrarySettings.providers.mediaKind":                     {"internal/administration/library_runtime.go", "MediaKind", "Apply provider and locale policy to domain tables"},
	"administration.LibrarySettings.providers.provider":                      {"internal/administration/library_runtime.go", "Provider", "Apply provider and locale policy to domain tables"},
	"administration.LibrarySettings.providers.region":                        {"internal/administration/library_runtime.go", "Region", "Apply provider and locale policy to domain tables"},
	"administration.MaintenanceSettingsDocument.backgroundSpeed":             {"internal/administration/background_policy.go", "BackgroundSpeed", "Set shared batch pacing and derived process priority"},
	"administration.MaintenanceSettingsDocument.backupKeepCount":             {"internal/administration/maintenance_runtime.go", "BackupKeepCount", "Keep the owner number of scheduled backups"},
	"administration.MaintenanceSettingsDocument.retention":                   {"internal/administration/maintenance.go", "Retention", "Select the age for an owner cleanup action"},
	"administration.MaintenanceSettingsDocument.windows.days":                {"internal/workpolicy/policy.go", "Days", "Select scheduled backup occurrences"},
	"administration.MaintenanceSettingsDocument.windows.durationMinutes":     {"internal/workpolicy/policy.go", "DurationMinutes", "Select scheduled backup occurrences"},
	"administration.MaintenanceSettingsDocument.windows.enabled":             {"internal/workpolicy/policy.go", "Enabled", "Select scheduled backup occurrences"},
	"administration.MaintenanceSettingsDocument.windows.id":                  {"internal/workpolicy/policy.go", "ID", "Select scheduled backup occurrences"},
	"administration.MaintenanceSettingsDocument.windows.startMinute":         {"internal/workpolicy/policy.go", "StartMinute", "Select scheduled backup occurrences"},
	"administration.MaintenanceSettingsDocument.windows.tasks":               {"internal/workpolicy/policy.go", "Tasks", "Select scheduled backup occurrences"},
	"administration.MaintenanceSettingsDocument.windows.timezone":            {"internal/workpolicy/policy.go", "Timezone", "Select scheduled backup occurrences"},
	"downloads.Settings.maxPreparedBytes":                                    {"internal/downloads/service.go", "MaxPreparedBytes", "Admit prepared bytes or expire download claims"},
	"downloads.Settings.retentionDays":                                       {"internal/downloads/service.go", "RetentionDays", "Admit prepared bytes or expire download claims"},
	"livechannels.LiveDefaults.discoveryEnabled":                             {"internal/administration/live_sources.go", "DiscoveryEnabled", "Apply shared guide and live-source defaults"},
	"livechannels.LiveDefaults.guideDays":                                    {"internal/livechannels/directory.go", "GuideDays", "Apply shared guide and live-source defaults"},
	"livechannels.LiveDefaults.importLimits.bytes":                           {"internal/livechannels/model.go", "Bytes", "Bound guide or playlist bytes and row counts"},
	"livechannels.LiveDefaults.importLimits.channels":                        {"internal/livechannels/model.go", "Channels", "Bound guide or playlist bytes and row counts"},
	"livechannels.LiveDefaults.importLimits.programmes":                      {"internal/livechannels/model.go", "Programmes", "Bound guide or playlist bytes and row counts"},
	"livechannels.LiveDefaults.logoImport":                                   {"internal/administration/guide_image_worker.go", "LogoImport", "Apply shared guide and live-source defaults"},
	"livechannels.LiveDefaults.retryWindowMs":                                {"internal/livechannels/configuration.go", "RetryWindowMs", "Apply shared guide and live-source defaults"},
	"livechannels.LiveDefaults.streamBufferMs":                               {"internal/livechannels/configuration.go", "StreamBufferMs", "Apply shared guide and live-source defaults"},
	"livechannels.LiveDefaults.userAgent":                                    {"internal/livechannels/configuration.go", "UserAgent", "Apply shared guide and live-source defaults"},
	"livechannels.LiveDefaults.timeshiftMinutes":                             {"cmd/server/main.go", "TimeshiftLimits", "Size the live rewind window and buffer budget from the saved server default"},
	"livechannels.LiveSourceSettings.channelNumbering.mode":                  {"internal/administration/live_sources.go", "Mode", "Set source kind, guide mapping and channel numbers"},
	"livechannels.LiveSourceSettings.channelNumbering.startAt":               {"internal/administration/live_sources.go", "StartAt", "Set source kind, guide mapping and channel numbers"},
	"livechannels.LiveSourceSettings.channelNumbering.step":                  {"internal/administration/live_sources.go", "Step", "Set source kind, guide mapping and channel numbers"},
	"livechannels.LiveSourceSettings.enabledChannels":                        {"internal/livechannels/remote.go", "EnabledChannels", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.enabledGroups":                          {"internal/livechannels/remote.go", "EnabledGroups", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.filters.categories.mode":                {"internal/livechannels/configuration.go", "Mode", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.filters.categories.values":              {"internal/livechannels/configuration.go", "Values", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.filters.countries.mode":                 {"internal/livechannels/configuration.go", "Mode", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.filters.countries.values":               {"internal/livechannels/configuration.go", "Values", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.filters.keywords.mode":                  {"internal/livechannels/configuration.go", "Mode", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.filters.keywords.values":                {"internal/livechannels/configuration.go", "Values", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.guideSourceId":                          {"internal/administration/live_sources.go", "GuideSourceID", "Set source kind, guide mapping and channel numbers"},
	"livechannels.LiveSourceSettings.importLimits.bytes":                     {"internal/livechannels/configuration.go", "function:SourceImportLimits", "Lower one source's guide or playlist bytes and row counts"},
	"livechannels.LiveSourceSettings.importLimits.channels":                  {"internal/livechannels/configuration.go", "function:SourceImportLimits", "Lower one source's guide or playlist bytes and row counts"},
	"livechannels.LiveSourceSettings.importLimits.programmes":                {"internal/livechannels/configuration.go", "function:SourceImportLimits", "Lower one source's guide or playlist bytes and row counts"},
	"livechannels.LiveSourceSettings.kind":                                   {"internal/administration/live_sources.go", "Kind", "Set source kind, guide mapping and channel numbers"},
	"livechannels.LiveSourceSettings.logoImport.enabled":                     {"internal/livechannels/configuration.go", "Enabled", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.logoImport.followServer":                {"internal/livechannels/configuration.go", "function:EffectiveLogoImport", "Follow the server-wide logo import choice instead of this source's own values"},
	"livechannels.LiveSourceSettings.logoImport.overwriteExisting":           {"internal/livechannels/store.go", "OverwriteExisting", "Preserve or replace imported logos"},
	"livechannels.LiveSourceSettings.retryWindowMs":                          {"internal/livechannels/configuration.go", "RetryWindowMs", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.streamBufferMs":                         {"internal/livechannels/configuration.go", "StreamBufferMs", "Filter or override the saved live source"},
	"livechannels.LiveSourceSettings.userAgent":                              {"internal/livechannels/configuration.go", "UserAgent", "Filter or override the saved live source"},
	"metadata.MetadataSettingsUpdate.includeAdult":                           {"internal/metadata/runtime_settings.go", "IncludeAdult", "Apply artwork storage, adult matching or provider credentials"},
	"metadata.MetadataSettingsUpdate.artworkMaxEdge":                         {"internal/metadata/artwork_size_sweep.go", "function:readArtworkEncodingPolicyTx", "Bound new pictures and durably reconcile retained artwork"},
	"metadata.MetadataSettingsUpdate.localArtworkLimitBytes":                 {"cmd/server/main.go", "LocalArtworkLimit", "Apply the owner storage bound to local artwork ingestion"},
	"metadata.MetadataSettingsUpdate.openSubtitlesApiKey":                    {"cmd/server/main.go", "SetCredentials", "Hand the saved OpenSubtitles key and account to the subtitle provider"},
	"metadata.MetadataSettingsUpdate.openSubtitlesPassword":                  {"cmd/server/main.go", "SetCredentials", "Hand the saved OpenSubtitles key and account to the subtitle provider"},
	"metadata.MetadataSettingsUpdate.openSubtitlesUsername":                  {"cmd/server/main.go", "SetCredentials", "Hand the saved OpenSubtitles key and account to the subtitle provider"},
	"metadata.MetadataSettingsUpdate.tmdbReadToken":                          {"internal/metadata/runtime_settings.go", "TMDBReadToken", "Apply artwork storage, adult matching or provider credentials"},
	"metadata.MetadataSettingsUpdate.refreshPolicy":                          {"internal/metadata/freshness.go", "refreshPolicy", "Decide which held titles a refresh may ask the provider for again"},
	"metadata.MetadataSettingsUpdate.fallbackLanguage":                       {"internal/metadataprovider/tmdb.go", "function:fillFallback", "Fill what the metadata language lacks from English or the original language"},
	"metadata.MetadataSettingsUpdate.animeTitles":                            {"internal/metadataprovider/anilist.go", "titlePreference", "Name AniList titles in the owner's chosen form"},
	"metadata.MetadataSettingsUpdate.castPhotos":                             {"internal/metadata/artwork_discovery.go", "withoutCastPhotos", "Skip cast and crew portraits"},
	"metadata.MetadataSettingsUpdate.acoustidKey":                            {"internal/metadata/runtime_settings.go", "useAcoustIDKey", "Install the owner's AcoustID key over the server's"},
	"metadata.MetadataSettingsUpdate.tvdbProjectKey":                         {"internal/metadata/runtime_settings.go", "TVDBProjectKey", "Apply artwork storage, adult matching or provider credentials"},
	"operations.NotificationSettings.certificateWarningDays":                 {"internal/operations/notifications.go", "CertificateWarningDays", "Prune inbox history or emit owner storage/certificate notices"},
	"operations.NotificationSettings.noticeAudience":                         {"internal/notify/notify.go", "function:NoticeAudience", "Route each server notice to the owner and administrators, the owner, or no one"},
	"operations.NotificationSettings.retentionDays":                          {"internal/operations/notifications.go", "RetentionDays", "Prune inbox history or emit owner storage/certificate notices"},
	"operations.NotificationSettings.storageCriticalPercent":                 {"internal/operations/notifications.go", "StorageCriticalPercent", "Prune inbox history or emit owner storage/certificate notices"},
	"operations.NotificationSettings.storageWarningPercent":                  {"internal/operations/notifications.go", "StorageWarningPercent", "Prune inbox history or emit owner storage/certificate notices"},
	"operations.Settings.accessUrls":                                         {"cmd/server/networking_initializer.go", "AccessURLs", "Publish owner connection candidates"},
	"operations.Settings.advertiseUrl":                                       {"cmd/server/networking_initializer.go", "AdvertiseURL", "Publish owner connection candidates"},
	"operations.Settings.advertisedInterface":                                {"cmd/server/main.go", "AdvertisedInterface", "Configure the running listener or media process"},
	"operations.Settings.analysisStageStorageMiB":                            {"internal/mediaanalysis/storage_limits.go", "literal:analysisStageStorageMiB", "Bound analysis artifacts or one stage output"},
	"operations.Settings.analysisStorageGiB":                                 {"internal/mediaanalysis/storage_limits.go", "literal:analysisStorageGiB", "Bound analysis artifacts or one stage output"},
	"operations.Settings.apiRequestsPerMinute":                               {"internal/httpapi/connection_policy.go", "APIRequestsPerMinute", "Rate limit a key or token"},
	"operations.Settings.connectionLimit":                                    {"cmd/server/main.go", "ConnectionLimit", "Configure the running listener or media process"},
	"operations.Settings.customCertificateDomain":                            {"cmd/server/main.go", "CustomCertificateDomain", "Configure the running listener or media process"},
	"operations.Settings.customCertificateKeyPath":                           {"cmd/server/main.go", "CustomCertificateKeyPath", "Configure the running listener or media process"},
	"operations.Settings.customCertificatePath":                              {"cmd/server/main.go", "CustomCertificatePath", "Configure the running listener or media process"},
	"operations.Settings.diagnosticDays":                                     {"internal/operations/evidence.go", "DiagnosticDays", "Prune the corresponding history by its retention rule"},
	"operations.Settings.directStreamRemux":                                  {"internal/httpapi/delivery_settings_adapter.go", "DirectStreamRemux", "Project conversion policy to runtime delivery settings"},
	"operations.Settings.ffmpegPath":                                         {"cmd/server/main.go", "FFmpegPath", "Configure the running listener or media process"},
	"operations.Settings.transcodeLocation":                                  {"internal/playback/hls_memory.go", "TranscodeLocation", "Choose bounded RAM preference or disk conversion output"},
	"operations.Settings.memoryCap":                                          {"internal/playback/hls_memory.go", "function:memoryBudget", "Bound RAM conversion output before placement and spill"},
	"operations.Settings.hardwareBackend":                                    {"internal/playback/delivery_plan.go", "HardwareBackend", "Plan media delivery using owner policy"},
	"operations.Settings.hardwareSelection":                                  {"internal/playback/delivery_context.go", "hardwareConfiguration", "Convert on the owner's chosen adapter or the processor"},
	"operations.Settings.hardwareDecode":                                     {"internal/playback/delivery_context.go", "function:withoutHardwareDecode", "Keep decoding on the processor when hardware decoding is off"},
	"operations.Settings.hardwareDevice":                                     {"internal/playback/delivery_plan.go", "HardwareDevice", "Plan media delivery using owner policy"},
	"operations.Settings.hdrToneMapping":                                     {"internal/httpapi/delivery_settings_adapter.go", "HDRToneMapping", "Project conversion policy to runtime delivery settings"},
	"operations.Settings.hdrToneMappingAlgorithm":                            {"internal/httpapi/delivery_settings_adapter.go", "HDRToneMappingAlgorithm", "Project conversion policy to runtime delivery settings"},
	"operations.Settings.home.communityActivityEnabled":                      {"internal/httpapi/home_routes.go", "CommunityActivityEnabled", "Include community activity in home content"},
	"operations.Settings.jobDays":                                            {"internal/operations/evidence.go", "JobDays", "Prune the corresponding history by its retention rule"},
	"operations.Settings.lanDiscoveryEnabled":                                {"cmd/server/administration.go", "LANDiscoveryEnabled", "Start or stop LAN advertisement"},
	"operations.Settings.dlnaEnabled":                                        {"internal/httpapi/connection_settings.go", "DLNAEnabled", "Start or stop the DLNA server and its SSDP advertisement"},
	"operations.Settings.secureConnections":                                  {"internal/httpapi/connection_policy.go", "SecureConnections", "Require secure HTTP transport by owner policy"},
	"operations.Settings.lanNetworks":                                        {"internal/httpapi/request_locality.go", "LANNetworks", "Classify home connections and forwarded client addresses"},
	"operations.Settings.library.missingTitles":                              {"internal/catalog/missing.go", "literal:library.missingTitles", "Keep or remove missing titles by the owner rule"},
	"operations.Settings.library.segmentDetection":                           {"internal/mediaanalysis/segment_modes.go", "literal:library.segmentDetection", "Choose the server's intro and credits detection mode"},
	"operations.Settings.library.missingTitlesDays":                          {"internal/catalog/missing.go", "literal:library.missingTitlesDays", "Keep or remove missing titles by the owner rule"},
	"operations.Settings.logDetailMinutes":                                   {"internal/httpapi/admin_diagnostics.go", "LogDetailMinutes", "Publish the saved duration used by the explicit detailed-log window command"},
	// The two invitation defaults are read on the accepting owner write, which
	// refuses anything outside the drawn ladder, and then published in the
	// settings document the invitation dialog starts from (settings-bindings rows
	// people.inviteRole and people.inviteExpiry, frozen per opened invitation).
	"operations.Settings.people.invitation_role":      {"internal/operations/settings_administration.go", "function:validPeopleInviteRole", "Domain check on the accepted owner write; the invitation dialog reads the saved default"},
	"operations.Settings.people.invitation_days":      {"internal/operations/settings_administration.go", "function:validPeopleInvitationDays", "Domain check on the accepted owner write; the invitation dialog reads the saved default"},
	"operations.Settings.logLevel":                    {"cmd/server/administration.go", "LogLevel", "Choose recorded log detail"},
	"operations.Settings.logRetention.category":       {"internal/httpapi/admin_diagnostics.go", "LogRetentionDays", "Prune the message log by category"},
	"operations.Settings.logRetention.days":           {"internal/httpapi/admin_diagnostics.go", "LogRetentionDays", "Prune the message log by category"},
	"operations.Settings.maxBackgroundSessions":       {"internal/httpapi/delivery_settings_adapter.go", "MaxBackgroundSessions", "Project conversion policy to runtime delivery settings"},
	"operations.Settings.maxConcurrentSessions":       {"internal/httpapi/delivery_settings_adapter.go", "MaxConcurrentSessions", "Project conversion policy to runtime delivery settings"},
	"operations.Settings.maxHardwareSessions":         {"internal/httpapi/delivery_settings_adapter.go", "MaxHardwareSessions", "Project conversion policy to runtime delivery settings"},
	"operations.Settings.maxSoftwareSessions":         {"internal/httpapi/delivery_settings_adapter.go", "MaxSoftwareSessions", "Project conversion policy to runtime delivery settings"},
	"operations.Settings.name":                        {"internal/identity/service.go", "literal:name", "Name the server in identity documents"},
	"operations.Settings.notificationDays":            {"internal/operations/evidence.go", "NotificationDays", "Prune the corresponding history by its retention rule"},
	"operations.Settings.pausedSessionTimeoutMinutes": {"internal/httpapi/playback_v1_routes.go", "PausedSessionTimeoutMinutes", "Expire paused playback sessions"},
	"operations.Settings.perAccountCap":               {"internal/streamlimits/limits.go", "literal:per_account_cap", "Resolve the account limit against the owner hard ceiling for playback admission"},
	"operations.Settings.planningPolicy":              {"internal/playback/delivery_plan.go", "PlanningPolicy", "Plan media delivery using owner policy"},
	"operations.Settings.playHistoryDays":             {"internal/operations/evidence.go", "PlayHistoryDays", "Prune the corresponding history by its retention rule"},
	"operations.Settings.playedRetentionMs":           {"internal/httpapi/delivery_settings_adapter.go", "PlayedRetentionMs", "Pace conversion and retain played segments"},
	"operations.Settings.remoteBitrateLimitKbps":      {"internal/httpapi/delivery_settings_adapter.go", "RemoteBitrateLimitKbps", "Project conversion policy to runtime delivery settings"},
	"operations.Settings.remoteSignInPolicy":          {"internal/httpapi/connection_policy.go", "RemoteSignInPolicy", "Admit remote sign-ins under owner policy"},
	"operations.Settings.serverCap":                   {"internal/playback/v1_session_helpers.go", "literal:server_cap", "Admit streams against the owner cap"},
	"operations.Settings.sessionStorageMiB":           {"internal/httpapi/delivery_settings_adapter.go", "SessionStorageMiB", "Project conversion policy to runtime delivery settings"},
	"operations.Settings.subtitleFileLimitMiB":        {"internal/httpapi/connection_policy.go", "SubtitleFileLimitMiB", "Apply subtitle discovery and acquisition bounds"},
	"operations.Settings.subtitleTrackLimit":          {"internal/httpapi/connection_policy.go", "SubtitleTrackLimit", "Apply subtitle discovery and acquisition bounds"},
	"operations.Settings.temporaryDirectory":          {"cmd/server/main.go", "TemporaryDirectory", "Configure the running listener or media process"},
	"operations.Settings.throttleBufferMs":            {"internal/httpapi/delivery_settings_adapter.go", "ThrottleBufferMs", "Pace conversion and retain played segments"},
	"operations.Settings.conversionMode":              {"internal/playback/delivery_settings.go", "ConversionMode", "Resolve actual owner audio/video conversion policy"},
	"operations.Settings.outputFormat":                {"internal/playback/delivery_plan.go", "OutputFormat", "Restrict actual conversion outputs"},
	"operations.Settings.downloadMaxQuality":          {"internal/downloads/delivery_policy.go", "DownloadMaxQuality", "Clamp actual download admission and preparation"},
	"operations.Settings.musicAwayQuality":            {"internal/httpapi/telemetry_routes.go", "MusicAwayQuality", "Supply actual remote music owner ceiling"},
	"operations.Settings.ownAddressEnabled":           {"cmd/server/main.go", "OwnAddressEnabled", "Gate the owner's independent certificate source"},
	"operations.Settings.trustedProxies":              {"internal/httpapi/request_locality.go", "TrustedProxies", "Classify home connections and forwarded client addresses"},
	"operations.Settings.updates.feedUrl":             {"cmd/server/main.go", "UpdateFeedURL", "Check the selected release feed and channel"},
	"operations.Settings.updates.releaseChannel":      {"cmd/server/main.go", "UpdateChannel", "Check the selected release feed and channel"},
	"operations.Settings.uploadCapacityKbps":          {"internal/httpapi/playback_v1_routes.go", "UploadCapacityKbps", "Divide the remote upload ceiling among streams"},
	"operations.Settings.x264Preset":                  {"internal/httpapi/delivery_settings_adapter.go", "X264Preset", "Project conversion policy to runtime delivery settings"},
}

// These fields are readback identity/vocabulary, never editable stored controls.
// Unsupported alternatives are rejected by the domain's save validation.
var informationalSettings = map[string]string{
	"downloads.Settings.qualities.*": "Computed producible download menu, never a stored mutation field",
	"*.revision":                     "Readback revision fence",
	"*.expectedRevision":             "Mutation revision fence",
	"administration.MaintenanceSettingsDocument.windows.name":    "Owner-authored label for the schedule editor",
	"administration.MaintenanceSettingsDocument.windows.cadence": "Schedule editor preset; expanded days are runtime authority",
	"administration.LibrarySettings.providers.apiKey*":           "Keys are held by metadata runtime; library key writes are rejected",
	"administration.LibrarySettings.navigation.videoPreview*":    "Fixed unsupported preview status; changing either value is rejected",
	"administration.DVRDefaults.conversion.*":                    "Only original recording is supported; conversion alternatives are rejected",
	"administration.DVRDefaults.sidecars.*":                      "Unsupported sidecar alternatives are rejected",
	"administration.DVRDefaults.keepPolicy.keepUntilWatched":     "Unsupported keep-until-watched alternative is rejected",
	"administration.DVRDefaults.*Template":                       "Unconfigured folder templates; nonempty writes are rejected",
	"administration.DVRDefaults.recordingProfile":                "Only original recording is supported",
	"administration.DVRDefaults.tunerPreference":                 "Only first-available allocation is supported",
	"operations.NotificationSettings.retentionSettingsField":     "Alias for the single notificationDays authority",
	"operations.NotificationSettings.maintenanceIntervalMinutes": "Readback cadence description, not an accepted mutation field",
}

func inventoryFields(typ reflect.Type, prefix string, visit func(string)) {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		visit(prefix)
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		inventoryFields(field.Type, prefix+"."+tag, visit)
	}
}
func TestStoredSettingsHaveRuntimeConsumers(t *testing.T) {
	roots := map[string]reflect.Type{
		"operations.Settings":                        reflect.TypeFor[operations.Settings](),
		"administration.MaintenanceSettingsDocument": reflect.TypeFor[administration.MaintenanceSettingsDocument](),
		"administration.LibrarySettings":             reflect.TypeFor[administration.LibrarySettings](),
		"administration.DVRDefaults":                 reflect.TypeFor[administration.DVRDefaults](),
		"livechannels.LiveDefaults":                  reflect.TypeFor[livechannels.LiveDefaults](),
		"livechannels.LiveSourceSettings":            reflect.TypeFor[livechannels.LiveSourceSettings](),
		"metadata.MetadataSettingsUpdate":            reflect.TypeFor[metadata.MetadataSettingsUpdate](),
		"downloads.Settings":                         reflect.TypeFor[downloads.Settings](),
		"operations.NotificationSettings":            reflect.TypeFor[operations.NotificationSettings](),
	}
	seen := map[string]bool{}
	for name, typ := range roots {
		inventoryFields(typ, name, func(key string) {
			seen[key] = true
			consumer, ok := settingConsumers[key]
			if !ok {
				for pattern, reason := range informationalSettings {
					if matched, _ := filepath.Match(pattern, key); matched && reason != "" {
						return
					}
				}
				t.Errorf("stored setting %s has no reviewed runtime consumer", key)
				return
			}
			if consumer.effect == "" {
				t.Errorf("%s has no stated effect", key)
			}
			path := filepath.Join("../..", consumer.file)
			if strings.HasPrefix(consumer.witness, "sql:") {
				body, err := os.ReadFile(path)
				statement := strings.TrimPrefix(consumer.witness, "sql:")
				if err != nil || !strings.HasSuffix(path, ".sql") || statement == "" || !strings.Contains(string(body), statement) {
					t.Errorf("%s lost its executable SQL consumer in %s: %v", key, consumer.file, err)
				}
				return
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Error(key, err)
				return
			}
			found := false
			ast.Inspect(file, func(node ast.Node) bool {
				if strings.HasPrefix(consumer.witness, "literal:") {
					if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
						value, _ := strconv.Unquote(literal.Value)
						found = found || strings.Contains(value, strings.TrimPrefix(consumer.witness, "literal:"))
					}
				}
				if call, ok := node.(*ast.CallExpr); ok && strings.HasPrefix(consumer.witness, "function:") {
					if name, ok := call.Fun.(*ast.Ident); ok {
						found = found || name.Name == strings.TrimPrefix(consumer.witness, "function:")
					}
				}
				if selector, ok := node.(*ast.SelectorExpr); ok {
					found = found || selector.Sel.Name == consumer.witness
				}
				return true
			})
			if !found {
				t.Errorf("%s lost its runtime witness %s in %s", key, consumer.witness, consumer.file)
			}
		})
	}
	for key := range settingConsumers {
		if !seen[key] {
			t.Errorf("stale consumer entry %s", key)
		}
	}
}

var preferenceRuntimeSources = map[string][2]string{
	"music.qualityHome":       {"server/internal/httpapi/playback_v1_routes.go", "function:SavedMusicQualityPreference"},
	"music.qualityAway":       {"server/internal/httpapi/playback_v1_routes.go", "function:SavedMusicQualityPreference"},
	"music.qualityMobile":     {"server/internal/httpapi/playback_v1_routes.go", "function:SavedMusicQualityPreference"},
	"music.qualityDownloads":  {"server/internal/downloads/music.go", "music.qualityDownloads"},
	"music.neverBoost":        {"packages/client-core/src/music-preferences.ts", "music.neverBoost"},
	"music.smoothTransitions": {"packages/client-core/src/music-preferences.ts", "music.smoothTransitions"},
	"music.dsdGainDb":         {"server/internal/playback/audio_plan_v2.go", "music.dsdGainDb"},
	"quality.downloads":       {"server/internal/downloads/music.go", "quality.downloads"},
	// The network lane builds the key dynamically. Witness the actual runtime
	// invocation that feeds QualityMode, not the key prefix in a local assignment.
	"quality.home":                        {"server/internal/playback/delivery_policy.go", "function:SavedQualityPreference"},
	"quality.remote":                      {"server/internal/httpapi/playback_v1_routes.go", "function:SavedQualityPreference"},
	"quality.mobile":                      {"server/internal/httpapi/playback_v1_routes.go", "function:SavedQualityPreference"},
	"appearance.cardSizePercent":          {"packages/client-core/src/preference-effects.ts", "appearance.cardSizePercent"},
	"appearance.showBackdrops":            {"packages/client-core/src/preference-effects.ts", "appearance.showBackdrops"},
	"audiobooks.skipBackMs":               {"packages/client-core/src/audiobook-preferences.ts", "audiobooks.skipBackMs"},
	"audiobooks.skipForwardMs":            {"packages/client-core/src/audiobook-preferences.ts", "audiobooks.skipForwardMs"},
	"audiobooks.resumeRewindMs":           {"server/internal/playback/service.go", "audiobooks.resumeRewindMs"},
	"audiobooks.defaultSpeed":             {"server/internal/catalog/listening_preferences.go", "audiobooks.defaultSpeed"},
	"delivery.directPlay":                 {"server/internal/playback/delivery_policy.go", "delivery.directPlay"},
	"delivery.directStream":               {"server/internal/playback/delivery_policy.go", "delivery.directStream"},
	"delivery.transcode":                  {"server/internal/playback/delivery_policy.go", "delivery.transcode"},
	"home.hiddenRowIds":                   {"server/internal/operations/home_layout.go", "home.hiddenRowIds"},
	"home.rowOrder":                       {"server/internal/operations/home_layout.go", "home.rowOrder"},
	"music.audioNormalization":            {"packages/client-core/src/music-preferences.ts", "music.audioNormalization"},
	"music.crossfadeMs":                   {"packages/client-core/src/music-preferences.ts", "music.crossfadeMs"},
	"music.gapless":                       {"packages/client-core/src/music-preferences.ts", "music.gapless"},
	"notifications.badges":                {"web/src/shell/bootstrap.ts", "notifications.badges"},
	"playback.autoplayNext":               {"server/internal/httpapi/playback_v1_routes.go", "playback.autoplayNext"},
	"playback.creditsSkip":                {"web/src/shell/settings.ts", "playback.creditsSkip"},
	"playback.defaultSpeed":               {"web/src/shell/settings.ts", "playback.defaultSpeed"},
	"playback.introSkip":                  {"web/src/shell/settings.ts", "playback.introSkip"},
	"playback.passoutAfterEpisodes":       {"server/internal/httpapi/playback_v1_routes.go", "playback.passoutAfterEpisodes"},
	"playback.passoutProtection":          {"server/internal/httpapi/playback_v1_routes.go", "playback.passoutProtection"},
	"playback.playedThresholdPercent":     {"server/internal/catalog/personal_activity.go", "playback.playedThresholdPercent"},
	"playback.preferredAudioLanguages":    {"server/internal/playback/delivery_context.go", "playback.preferredAudioLanguages"},
	"playback.preferredSubtitleLanguages": {"server/internal/subtitles/provider.go", "playback.preferredSubtitleLanguages"},
	"playback.recapSkip":                  {"web/src/shell/settings.ts", "playback.recapSkip"},
	"playback.showSyncedLyrics":           {"web/src/shell/settings.ts", "playback.showSyncedLyrics"},
	"playback.skipBackMs":                 {"web/src/shell/settings.ts", "playback.skipBackMs"},
	"playback.skipForwardMs":              {"web/src/shell/settings.ts", "playback.skipForwardMs"},
	"playback.sleepTimerMinutes":          {"server/internal/catalog/listening_preferences.go", "playback.sleepTimerMinutes"},
	"playback.startNextDuringCredits":     {"server/internal/httpapi/playback_v1_routes.go", "playback.startNextDuringCredits"},
	"playback.startedThresholdPercent":    {"server/internal/catalog/personal_activity.go", "playback.startedThresholdPercent"},
	"playback.subtitleBackground":         {"packages/client-core/src/preference-effects.ts", "playback.subtitleBackground"},
	"playback.subtitleMode":               {"server/internal/playback/delivery_context.go", "playback.subtitleMode"},
	"playback.subtitleSize":               {"packages/client-core/src/preference-effects.ts", "playback.subtitleSize"},
	"playback.upNextCountdownMs":          {"server/internal/httpapi/playback_v1_routes.go", "playback.upNextCountdownMs"},
	"privacy.includeInWatchTogether":      {"server/internal/social/privacy.go", "function:watchTogetherVisible"},
	"privacy.pauseWatchHistory":           {"server/internal/downloads/progress.go", "privacy.pauseWatchHistory"},
	"privacy.showActivityToMembers":       {"server/internal/catalog/recommendation_engine.go", "privacy.showActivityToMembers"},
	"region.hourCycle":                    {"packages/client-core/src/preference-effects.ts", "region.hourCycle"},
	"region.locale":                       {"packages/client-core/src/preference-effects.ts", "region.locale"},
	"search.rememberHistory":              {"server/internal/catalog/search_history.go", "search.rememberHistory"},
	"playback.subtitleColor":              {"packages/client-core/src/preference-effects.ts", "playback.subtitleColor"},
	"playback.subtitlePosition":           {"packages/client-core/src/preference-effects.ts", "playback.subtitlePosition"},
	"playback.subtitleFont":               {"packages/client-core/src/preference-effects.ts", "playback.subtitleFont"},
	"playback.keepSubtitleStyling":        {"packages/client-core/src/preference-effects.ts", "playback.keepSubtitleStyling"},
	"playback.useSystemCaptionStyle":      {"packages/client-core/src/preference-effects.ts", "playback.useSystemCaptionStyle"},
	"appearance.motion":                   {"packages/client-core/src/preference-effects.ts", "appearance.motion"},
	"appearance.textSize":                 {"packages/client-core/src/preference-effects.ts", "appearance.textSize"},
	"appearance.layout":                   {"packages/client-core/src/preference-effects.ts", "appearance.layout"},
	"appearance.libraryView":              {"packages/client-core/src/preference-effects.ts", "appearance.libraryView"},
	"appearance.episodesLayout":           {"packages/client-core/src/preference-effects.ts", "appearance.episodesLayout"},
	"reading.theme":                       {"packages/client-core/src/preference-effects.ts", "reading.theme"},
	"reading.font":                        {"packages/client-core/src/preference-effects.ts", "reading.font"},
	"reading.textSize":                    {"packages/client-core/src/preference-effects.ts", "reading.textSize"},
	"reading.lineSpacing":                 {"packages/client-core/src/preference-effects.ts", "reading.lineSpacing"},
	"reading.margins":                     {"packages/client-core/src/preference-effects.ts", "reading.margins"},
	"reading.alignment":                   {"packages/client-core/src/preference-effects.ts", "reading.alignment"},
	"reading.layout":                      {"packages/client-core/src/preference-effects.ts", "reading.layout"},
	"reading.twoPages":                    {"packages/client-core/src/preference-effects.ts", "reading.twoPages"},
	"reading.tapToTurn":                   {"packages/client-core/src/preference-effects.ts", "reading.tapToTurn"},
	"reading.hyphenation":                 {"packages/client-core/src/preference-effects.ts", "reading.hyphenation"},
	"reading.highlightColour":             {"packages/client-core/src/preference-effects.ts", "reading.highlightColour"},
	"reading.pdfView":                     {"packages/client-core/src/preference-effects.ts", "reading.pdfView"},
	"comics.fit":                          {"packages/client-core/src/preference-effects.ts", "comics.fit"},
	"comics.direction":                    {"packages/client-core/src/preference-effects.ts", "comics.direction"},
	"comics.firstPageAlone":               {"packages/client-core/src/preference-effects.ts", "comics.firstPageAlone"},
	"region.formats":                      {"packages/client-core/src/preference-effects.ts", "region.formats"},
	"region.dateOrder":                    {"packages/client-core/src/preference-effects.ts", "region.dateOrder"},
	"region.firstDay":                     {"packages/client-core/src/preference-effects.ts", "region.firstDay"},
	"region.sizeUnits":                    {"packages/client-core/src/preference-effects.ts", "region.sizeUnits"},
	"playback.preferSdhSubtitles":         {"server/internal/playback/delivery_context.go", "playback.preferSdhSubtitles"},
	"playback.forcedSubtitleFallback":     {"server/internal/playback/delivery_context.go", "playback.forcedSubtitleFallback"},
	"playback.useUntaggedForced":          {"server/internal/playback/delivery_context.go", "playback.useUntaggedForced"},
	"playback.audioDescription":           {"server/internal/playback/service.go", "playback.audioDescription"},
	"playback.audioTrackChoice":           {"server/internal/playback/service.go", "playback.audioTrackChoice"},
}

func TestStoredPreferencesHaveRuntimeConsumers(t *testing.T) {
	seen := map[string]bool{}
	for _, field := range operations.PreferenceRegistry() {
		seen[field.Key] = true
		witness, ok := preferenceRuntimeSources[field.Key]
		if !ok {
			t.Errorf("stored preference %s has no runtime consumer", field.Key)
			continue
		}
		path := filepath.Join("../../..", witness[0])
		found := false
		if strings.HasSuffix(path, ".go") {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Error(err)
				continue
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if strings.HasPrefix(witness[1], "function:") {
					want := strings.TrimPrefix(witness[1], "function:")
					switch name := call.Fun.(type) {
					case *ast.Ident:
						found = found || name.Name == want
					case *ast.SelectorExpr:
						found = found || name.Sel.Name == want
					}
				}
				for _, arg := range call.Args {
					ast.Inspect(arg, func(node ast.Node) bool {
						if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
							value, _ := strconv.Unquote(literal.Value)
							found = found || value == witness[1]
						}
						return true
					})
				}
				return true
			})
		} else {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Error(err)
				continue
			}
			// Local readers take the key first; the canonical projection takes
			// its parsed snapshot (a name or an accessor call) first and the
			// literal key second; a resolved document is indexed by the key.
			pattern := `(?:\b(?:value|read)(?:<[^;\n]*?>)?\(\s*|\bpreferenceValue(?:<[^;\n]*?>)?\(\s*[A-Za-z_$][\w$]*(?:\(\))?\s*,\s*|\.effective\[\s*)['"]` + regexp.QuoteMeta(witness[1]) + `['"]`
			found = regexp.MustCompile(pattern).Match(source)
		}
		if !found {
			t.Errorf("%s lost its runtime reader in %s", field.Key, witness[0])
		}
	}
	for key := range preferenceRuntimeSources {
		if !seen[key] {
			t.Errorf("stale preference consumer %s", key)
		}
	}
}
