package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"reflect"
	"strings"
	"time"
)

const RegistryRevision = "settings-r40.1"

type Field struct {
	ID          string   `json:"id"`
	Group       string   `json:"group"`
	Scope       string   `json:"scope"`
	Overrides   []string `json:"overrideScopes"`
	Type        string   `json:"type"`
	Default     any      `json:"default"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
	Options     []string `json:"options,omitempty"`
	Save        string   `json:"saveMode"`
	Apply       string   `json:"applicationMode"`
	Consumer    string   `json:"runtimeConsumer"`
	Description string   `json:"description"`
	Permission  string   `json:"permission"`
	Secret      bool     `json:"secret"`
	Retention   string   `json:"retentionClass"`
	Audit       string   `json:"auditClass"`
}

func bounded(v int) *int { return &v }

// Enumerated owner settings publish their accepted values so a client renders a
// real choice list instead of guessing at server policy.
var (
	HardwareBackends      = []string{"auto", "software", "videotoolbox", "vaapi", "qsv", "nvenc", "amf"}
	ToneMappingAlgorithms = []string{"clip", "linear", "gamma", "reinhard", "hable", "mobius"}
	X264Presets           = []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow"}
	PlanningPolicies      = []string{"maximum_fidelity", "maximum_compatibility", "minimize_server_work"}
	// MemoryCaps are Streaming › Memory limit's choices: a share of free
	// memory or a fixed size.
	MemoryCaps = []string{"quarter", "half", "2gb", "4gb", "8gb", "16gb"}
	// UpdateChannels are the release channels the updates page reports against.
	UpdateChannels = []string{"stable", "beta"}
	// MissingTitleRules are the owner's choices for when a title whose files
	// have gone from disk is removed.
	MissingTitleRules = []string{"immediately", "after_days", "never"}
	// SegmentDetectionModes are the server's intro and credits detection
	// choices: off; standard, which compares the sound of a show's episodes;
	// thorough, which also reads the picture near the end of every episode
	// and film. Libraries may follow this or choose for themselves.
	SegmentDetectionModes = []string{"off", "standard", "thorough"}
	// Administration (workstream G) publishes connectivity and diagnostics
	// policy through this same registry, so the console has one authority for
	// every server setting rather than a second document per feature area.
	RemoteSignInPolicies = []string{"allow", "owner-only", "off"}
	LogLevels            = []string{"error", "warn", "info", "debug"}
	LogCategories        = []string{"server", "playback", "scan", "network", "client"}
)

// DefaultAPIRequestsPerMinute is what one API key or personal access token may
// ask for each minute when the owner has not chosen a number.
const DefaultAPIRequestsPerMinute = 600

func oneOf(value string, allowed []string) bool {
	for _, v := range allowed {
		if v == value {
			return true
		}
	}
	return false
}
func Registry() []Field {
	field := func(id, group, kind string, def any, min, max *int, options []string, apply, consumer, description string) Field {
		return Field{ID: id, Group: group, Scope: "server", Overrides: []string{}, Type: kind, Default: def, Min: min, Max: max, Options: options, Save: "explicit-apply", Apply: apply, Consumer: consumer, Description: description, Permission: "owner", Secret: false, Retention: "configuration", Audit: "administrative"}
	}
	return []Field{
		field("analysisStorageGiB", "storage", "integer", 32, bounded(1), bounded(16384), nil, "next-operation", "media analysis artifact storage", "Maximum space for analysis artifacts in GiB. Existing artifacts are kept when the limit is lowered."),
		field("analysisStageStorageMiB", "storage", "integer", 256, bounded(1), bounded(4096), nil, "next-operation", "media analysis output", "Maximum output for one analysis stage in MiB."),
		field("subtitleTrackLimit", "storage", "integer", medialimits.DefaultSubtitleTracks, bounded(32), bounded(medialimits.MaxSubtitleTracks), nil, "next-operation", "subtitle inventory", "How many subtitle tracks one file may offer. Existing tracks remain available when the limit is lowered."),
		field("subtitleFileLimitMiB", "storage", "integer", medialimits.DefaultSubtitleFileMiB, bounded(32), bounded(medialimits.MaxSubtitleFileMiB), nil, "next-operation", "subtitle acquisition", "Largest image subtitle file Portico may read, in MiB."),
		field("name", "identity", "string", "Portico", nil, nil, nil, "hot", "identity.Service.Name", "Name shown when selecting this server."),
		field("conversionMode", "playback", "enumeration", "all", nil, nil, []string{"all", "sound", "never"}, "next-operation", "playback delivery planning", "Allows all conversions, audio-only conversions, or none."),
		field("outputFormat", "transcoding", "enumeration", "auto", nil, nil, []string{"auto", "h264"}, "next-operation", "playback delivery planning", "Output encoding policy."),
		field("musicAwayQuality", "playback", "enumeration", "original", nil, nil, qualityladder.MusicIDs(), "next-operation", "remote audio delivery planning", "The owner ceiling for remote music playback."),
		field("downloadMaxQuality", "playback", "enumeration", "original", nil, nil, ownerDownloadQualities(), "next-operation", "prepared media planning", "The owner ceiling for video downloads."),
		field("perAccountCap", "playback", "nullable-integer", nil, bounded(1), bounded(1000000), nil, "next-operation", "playback admission and expired-lease recovery", "Null means unlimited across an account's profiles and devices."),
		field("serverCap", "playback", "nullable-integer", nil, bounded(1), bounded(1000000), nil, "next-operation", "playback admission and expired-lease recovery", "Null means unlimited. Lowering a cap never evicts existing streams."),
		field("hardwareBackend", "transcoding", "enumeration", "auto", nil, nil, HardwareBackends, "next-operation", "delivery planning and conversion launch", "Encoder family used for conversions. Auto picks the first backend that probes successfully and falls back to software."),
		field("hardwareDevice", "transcoding", "string", "", nil, nil, nil, "next-operation", "delivery planning and conversion launch", "Optional device selector for the chosen backend, such as a render node path or adapter index. Empty lets the backend choose."),
		field("hardwareSelection", "transcoding", "string", "auto", nil, nil, nil, "next-operation", "delivery planning and conversion launch", "Which adapter converts: auto (every adapter found, best first), software (the processor only), or one adapter's opaque id from the hardware report. An id that is no longer present converts on the processor."),
		field("hardwareDecode", "transcoding", "boolean", true, nil, nil, nil, "next-operation", "conversion launch", "Decode on the conversion adapter where it has been proven to work. Off sends all decoding to the processor; encoding still uses the adapter."),
		field("hdrToneMapping", "transcoding", "boolean", true, nil, nil, nil, "next-operation", "delivery planning", "Converts HDR sources to SDR when a conversion is required. Costs additional processing per stream."),
		field("hdrToneMappingAlgorithm", "transcoding", "enumeration", "hable", nil, nil, ToneMappingAlgorithms, "next-operation", "delivery planning", "Tone curve used when HDR tone mapping runs."),
		field("x264Preset", "transcoding", "enumeration", "veryfast", nil, nil, X264Presets, "next-operation", "software conversion launch", "Software encoder speed against size. Slower presets need more CPU per stream."),
		field("directStreamRemux", "transcoding", "boolean", true, nil, nil, nil, "next-operation", "delivery planning", "Repackages compatible sources into a supported container without re-encoding video or audio."),
		field("planningPolicy", "transcoding", "enumeration", "maximum_fidelity", nil, nil, PlanningPolicies, "next-operation", "delivery planning", "Chooses how the planner trades picture fidelity, client compatibility and server work."),
		field("throttleBufferMs", "transcoding", "integer", 60000, bounded(10000), bounded(600000), nil, "next-operation", "conversion pacing", "How far ahead of the viewer a conversion may run before it pauses, in milliseconds."),
		field("playedRetentionMs", "transcoding", "integer", 180000, bounded(0), bounded(3600000), nil, "next-operation", "conversion segment retention", "How long already played segments stay on disk so a small rewind does not restart the conversion."),
		field("ffmpegPath", "transcoding", "path", "", nil, nil, nil, "restart", "media tool qualification and conversion launch", "Absolute path to another FFmpeg build. It is checked before use and runs with the same process restrictions."),
		field("sessionStorageMiB", "transcoding", "integer", 4096, bounded(64), bounded(1048576), nil, "next-operation", "conversion output retention", "Maximum working space for one playback session in MiB. Older played segments are removed first."),
		field("transcodeLocation", "transcoding", "enumeration", "memory", nil, nil, []string{"memory", "disk"}, "next-operation", "conversion placement", "Prefer bounded memory when available, or always use the disk conversion folder."),
		field("memoryCap", "transcoding", "enumeration", "quarter", nil, nil, MemoryCaps, "next-operation", "memory conversion placement", "How much memory conversions may use when the conversion location is memory: a quarter or half of free memory, or a fixed size. When it is full, conversions go to the separate folder."),
		field("temporaryDirectory", "transcoding", "path", "", nil, nil, nil, "restart", "conversion working directory", "Absolute path for conversion working files. Empty uses the server state directory. Readiness is reported by the transcode capacity report."),
		field("maxConcurrentSessions", "transcoding", "integer", 0, bounded(0), bounded(1000), nil, "next-operation", "conversion admission", "Total conversions allowed at once. 0 means unlimited."),
		field("maxHardwareSessions", "transcoding", "integer", 0, bounded(0), bounded(1000), nil, "next-operation", "conversion admission", "Hardware conversions allowed at once. 0 means unlimited."),
		field("maxSoftwareSessions", "transcoding", "integer", 0, bounded(0), bounded(1000), nil, "next-operation", "conversion admission", "Software conversions allowed at once. 0 means unlimited."),
		field("maxBackgroundSessions", "transcoding", "integer", 0, bounded(0), bounded(1000), nil, "next-operation", "background conversion admission", "Background conversions, such as download preparation, allowed at once. 0 means unlimited."),
		field("diagnosticDays", "retention", "integer", 30, bounded(1), bounded(30), nil, "hot", "operations.Prune", "Runtime and client records use separate quotas. Shortening retention prunes on the next maintenance tick."),
		field("notificationDays", "retention", "integer", 180, bounded(1), bounded(180), nil, "hot", "operations.Notify and Prune", "Local profile inbox retention. No Hosted media notifications."),
		field("jobDays", "retention", "integer", 30, bounded(1), bounded(30), nil, "hot", "operations.Prune", "Terminal orchestration history only; domain workers own their artifacts."),
		field("playHistoryDays", "retention", "integer", 0, bounded(0), bounded(36500), nil, "hot", "operations.Prune", "How long the server's play history is kept. 0 keeps every play since the server was set up. Play counts are kept either way."),
		field("recommendations.titleDataset", "home", "boolean", true, nil, nil, nil, "hot", "enrichment title dataset worker", "Download the Portico title dataset weekly for recommendations. Nothing about the library is sent. Off removes the downloaded data."),
		field("home.communityActivityEnabled", "home", "boolean", false, nil, nil, nil, "hot", "catalog home trending row", "Publish a server-wide popular-this-week row built from aggregated play counts across profiles. Off by default."),
		field("updates.releaseChannel", "updates", "enumeration", "stable", nil, nil, UpdateChannels, "hot", "administration updates report", "Which release channel the updates page reports against. Nothing is ever installed automatically."),
		field("updates.feedUrl", "updates", "string", DefaultUpdateFeedURL, nil, nil, nil, "hot", "administration updates report", "Release feed checked for newer versions. Clear the address to turn update checks off."),
		field("library.missingTitles", "library", "enumeration", "after_days", nil, nil, MissingTitleRules, "hot", "catalog missing titles", "When a title whose files are no longer on disk is removed from the library: immediately, after a number of days, or never. Until then it stays, marked missing, and a file that comes back restores it. Each profile's watch state is kept either way."),
		field("library.missingTitlesDays", "library", "integer", 30, bounded(1), bounded(3650), nil, "hot", "catalog missing titles", "How many days a missing title is kept when the rule is after a number of days."),
		field("library.segmentDetection", "library", "enumeration", "standard", nil, nil, SegmentDetectionModes, "hot", "mediaanalysis segment detection", "Finds intros and end credits so viewers can skip them. Standard compares the sound of a show's episodes and costs a few seconds of processing per episode. Thorough also reads the picture near the end of every episode and film to find credits and post-credits scenes; it decodes video and takes minutes per file. Each library can follow this or choose its own."),
		field("segments.shortestEpisode", "segments", "integer", 2, bounded(1), bounded(30), nil, "hot", "mediaanalysis segment planning", "Minimum file length in minutes for measured detection; owner decisions and stored evidence are retained."),
		field("segments.shortestFilm", "segments", "integer", 10, bounded(5), bounded(60), nil, "hot", "mediaanalysis segment planning", "Minimum file length in minutes for measured detection; owner decisions and stored evidence are retained."),
		field("segments.introWindow", "segments", "enumeration", "usual", nil, nil, []string{"usual", "long"}, "hot", "mediaanalysis segment planning", "Bound the region read by the existing segment detector."),
		field("segments.creditsWindow", "segments", "enumeration", "usual", nil, nil, []string{"usual", "long"}, "hot", "mediaanalysis segment planning", "Bound the region read by the existing segment detector."),
		field("segments.filmCreditsWindow", "segments", "enumeration", "usual", nil, nil, []string{"usual", "long"}, "hot", "mediaanalysis segment planning", "Bound the region read by the existing segment detector."),
		field("segments.tvMode", "segments", "enumeration", "server", nil, nil, []string{"server", "standard", "thorough", "off"}, "hot", "mediaanalysis library kind detection", "Default detection mode for this video library kind; an explicit per-library choice wins."),
		field("segments.animeMode", "segments", "enumeration", "server", nil, nil, []string{"server", "standard", "thorough", "off"}, "hot", "mediaanalysis library kind detection", "Default detection mode for this video library kind; an explicit per-library choice wins."),
		field("segments.movieMode", "segments", "enumeration", "server", nil, nil, []string{"server", "standard", "thorough", "off"}, "hot", "mediaanalysis library kind detection", "Default detection mode for this video library kind; an explicit per-library choice wins."),
		field("segments.useChapterNames", "segments", "boolean", true, nil, nil, nil, "hot", "segmentmarkers viewer projection", "Use embedded chapter labels for unapproved viewer skip suggestions. Owner decisions are retained."),
		field("segments.viewerConfidencePercent", "segments", "integer", 50, bounded(30), bounded(70), []string{"30", "50", "70"}, "hot", "segmentmarkers viewer projection", "Confidence floor for unapproved viewer skip buttons. Approval and ambiguity rules remain independent."),
		field("segments.automaticConfidencePercent", "segments", "integer", 85, bounded(70), bounded(95), []string{"70", "85", "95"}, "hot", "segmentmarkers measured automatic projection", "Confidence floor for automatic skips from cross-episode measurements. Single-file guesses never become automatic."),
		field("secureConnections", "connectivity", "enumeration", "preferred", nil, nil, []string{"preferred", "required"}, "hot", "HTTP transport admission", "Preferred allows HTTP; Required accepts only secure connections."),
		field("remoteSignInPolicy", "connectivity", "enumeration", "allow", nil, nil, RemoteSignInPolicies, "next-operation", "sign-in admission", "Who may sign in from outside this network. Owner-only still allows existing remote sessions to finish."),
		field("remoteBitrateLimitKbps", "connectivity", "integer", 0, bounded(0), bounded(200000), nil, "next-operation", "playback delivery planning", "Server-wide ceiling on video bitrate for remote playback, in kbps. 0 means no ceiling."),
		field("lanNetworks", "connectivity", "string-list", networkpolicy.DefaultHomeNetworks(), nil, nil, nil, "hot", "connectivity status and remote classification", "Home networks. Only these addresses count as local. An empty list makes every connection remote."),
		field("accessUrls", "connectivity", "string-list", []string{}, nil, nil, nil, "hot", "connectivity status", "HTTPS addresses members' apps may use to reach this server, such as a reverse proxy or VPN name. Published with the server's routes; each app tests them itself."),
		field("connectionLimit", "connectivity", "integer", 0, bounded(0), bounded(65536), nil, "restart", "listener connection admission", "Maximum ordinary connections. 0 uses a default based on available memory. The owner console has reserved connections."),
		field("advertiseUrl", "connectivity", "string", "", nil, nil, nil, "hot", "client connection candidates", "External address clients may use to reach this server. Use a hostname with no path. HTTPS addresses can also be listed by Hosted."),
		field("trustedProxies", "connectivity", "string-list", []string{}, nil, nil, nil, "hot", "request locality and forwarded client address", "Reverse proxies (addresses or CIDR networks) whose forwarded client address this server believes. Other peers are judged by their own address."),
		field("uploadCapacityKbps", "connectivity", "integer", 0, bounded(0), bounded(100000000), nil, "next-operation", "remote playback admission", "The home connection's upload speed in kbps. New remote streams share about 80% of it and get a lower quality when it is busy. 0 means unknown: no budget."),
		field("people.invitation_role", "people", "enumeration", "member", nil, nil, []string{"member", "administrator"}, "hot", "access invitation creation", "The role a new invitation starts with. The invitation dialog offers Member and Administrator and starts at this value; an invitation already started keeps the role it was opened with."),
		field("people.invitation_days", "people", "integer", 7, bounded(1), bounded(30), nil, "hot", "access invitation creation", "How long a new invitation stays usable, in days. The invitation dialog offers 1, 3, 7 and 30 and starts at this value; an invitation already started keeps the expiry it was opened with."),
		field("pausedSessionTimeoutMinutes", "connectivity", "integer", 0, bounded(0), bounded(1440), nil, "hot", "playback session sweeper", "End video sessions paused for longer than this many minutes, freeing their stream slot. Audio and Live are never ended. 0 means never."),
		field("advertisedInterface", "connectivity", "string", "", nil, nil, nil, "hot", "LAN route publication", "The network interface whose address LAN clients are given. Empty advertises all available interfaces."),
		field("ownAddressEnabled", "connectivity", "boolean", false, nil, nil, nil, "hot", "custom certificate TLS and route publication", "Serve the owner's own secure address independently of the Portico route."),
		field("customCertificatePath", "connectivity", "string", "", nil, nil, nil, "hot", "TLS listener", "Path to a PEM certificate chain for a custom domain. Reloaded when the file changes."),
		field("customCertificateKeyPath", "connectivity", "string", "", nil, nil, nil, "hot", "TLS listener", "Path to the PEM private key for the custom certificate."),
		field("customCertificateDomain", "connectivity", "string", "", nil, nil, nil, "hot", "TLS listener and route publication", "The domain the custom certificate serves. It is published to members as an access URL on the remote access port."),
		field("lanDiscoveryEnabled", "connectivity", "boolean", true, nil, nil, nil, "hot", "LAN discovery advertisement", "Advertise this server on the local network with mDNS/Bonjour. The connectivity status report says whether this host can."),
		field("dlnaEnabled", "connectivity", "boolean", false, nil, nil, nil, "hot", "DLNA server", "Let DLNA players on the home networks browse and play every library without signing in. Off by default; never offered outside the home networks."),
		field("integrations.compatibilityEnabled", "access", "boolean", false, nil, nil, nil, "hot", "integration protocol adapter", "Allow connected automation tools to use the integration compatibility endpoints. Off by default."),
		field("apiRequestsPerMinute", "access", "integer", 0, bounded(0), bounded(100000), nil, "hot", "API key and access token admission", "How many requests one API key or personal access token may make each minute. 0 uses the default of 600."),
		field("logLevel", "diagnostics", "enumeration", "info", nil, nil, LogLevels, "hot", "servicelog.Recorder", "How much detail the message log keeps. A debug window raises this temporarily without changing the saved value."),
		field("logRetention", "diagnostics", "log-retention-list", []LogRetention{}, nil, nil, LogCategories, "hot", "servicelog.Recorder and ClientLogStore prune", "Days of message log and client report history to keep per category. A category with no entry keeps its bounded history."),
	}
	// Viewer preferences are published by their own registry on GET /v1/preferences.
	// Listing them here too would give clients two disagreeing authorities.
}

const DefaultUpdateFeedURL = "https://github.com/PorticoMediaServer/portico-server/releases/latest/download/portico-update-manifest.json"

type Settings struct {
	AnalysisStorageGiB      int    `json:"analysisStorageGiB"`
	AnalysisStageStorageMiB int    `json:"analysisStageStorageMiB"`
	SubtitleTrackLimit      int    `json:"subtitleTrackLimit"`
	SubtitleFileLimitMiB    int    `json:"subtitleFileLimitMiB"`
	Name                    string `json:"name"`
	// Internal admission gate is derived from ConversionMode and is never an independent wire setting.
	TranscodingEnabled bool   `json:"-"`
	ConversionMode     string `json:"conversionMode"`
	OutputFormat       string `json:"outputFormat"`
	DownloadMaxQuality string `json:"downloadMaxQuality"`
	MusicAwayQuality   string `json:"musicAwayQuality"`
	PerAccountCap      *int   `json:"perAccountCap"`
	ServerCap          *int   `json:"serverCap"`
	// Transcoding administration. No domain table mirrors these, so the console
	// document is their single authority and the registry their only contract.
	HardwareBackend string `json:"hardwareBackend"`
	HardwareDevice  string `json:"hardwareDevice"`
	// HardwareSelection is Settings › Streaming › Device: "auto", "software" or
	// one adapter's opaque id (decoder.HardwareDevice.ID). HardwareDecode off
	// keeps decoding on the processor.
	HardwareSelection       string `json:"hardwareSelection"`
	HardwareDecode          bool   `json:"hardwareDecode"`
	HDRToneMapping          bool   `json:"hdrToneMapping"`
	HDRToneMappingAlgorithm string `json:"hdrToneMappingAlgorithm"`
	X264Preset              string `json:"x264Preset"`
	DirectStreamRemux       bool   `json:"directStreamRemux"`
	PlanningPolicy          string `json:"planningPolicy"`
	ThrottleBufferMs        int    `json:"throttleBufferMs"`
	PlayedRetentionMs       int    `json:"playedRetentionMs"`
	TranscodeLocation       string `json:"transcodeLocation"`
	TemporaryDirectory      string `json:"temporaryDirectory"`
	FFmpegPath              string `json:"ffmpegPath"`
	SessionStorageMiB       int    `json:"sessionStorageMiB"`
	// MemoryCap bounds conversion output placed in memory (Streaming ›
	// Memory limit); it only matters while the conversion location is a RAM
	// file system.
	MemoryCap             string `json:"memoryCap"`
	MaxConcurrentSessions int    `json:"maxConcurrentSessions"`
	MaxHardwareSessions   int    `json:"maxHardwareSessions"`
	MaxSoftwareSessions   int    `json:"maxSoftwareSessions"`
	MaxBackgroundSessions int    `json:"maxBackgroundSessions"`
	DiagnosticDays        int    `json:"diagnosticDays"`
	NotificationDays      int    `json:"notificationDays"`
	JobDays               int    `json:"jobDays"`
	// PlayHistoryDays is how long the server's play history is kept; 0, the
	// default, keeps all of it. A document saved before the field existed reads
	// as 0, which is the default.
	PlayHistoryDays int `json:"playHistoryDays"`
	// APIRequestsPerMinute is the rate one API key or personal access token is
	// held to; 0, the value a document saved before the field existed reads as,
	// is the default (DefaultAPIRequestsPerMinute).
	APIRequestsPerMinute     int  `json:"apiRequestsPerMinute"`
	IntegrationCompatibility bool `json:"integrations.compatibilityEnabled"`
	// TitleDatasetEnabled lets the server download the Portico title dataset
	// for recommendations (Settings › Metadata › Recommendations). On by
	// default; off makes no requests and removes what was downloaded.
	TitleDatasetEnabled bool `json:"recommendations.titleDataset"`
	// Integration limits restrict existing credentials and deliveries; they grant no authority.

	// Aggregated server-wide play counts stay off until an owner opts in.
	CommunityActivityEnabled bool `json:"home.communityActivityEnabled"`
	// Update reporting and the default trash retention are owner administration
	// values; the administration pages read them and never shadow them.
	UpdateChannel string `json:"updates.releaseChannel"`
	UpdateFeedURL string `json:"updates.feedUrl"`
	// Null means the shipped default, so a client that does not send this field
	// never silently switches the trash off.
	// MissingTitles and MissingTitlesDays are the owner's rule for titles whose
	// files are no longer on disk (catalog.MissingRule reads them).
	// PeopleInvitationDays is how long a new invitation stays usable, in days.
	// A document saved before the field existed reads as 0, which the accessor
	// resolves to the shipped default of 7 days.
	PeopleInvitationRole PeopleInviteRole     `json:"people.invitation_role,omitempty"`
	PeopleInvitationDays PeopleInvitationDays `json:"people.invitation_days,omitempty"`
	MissingTitles        string               `json:"library.missingTitles"`
	MissingTitlesDays    int                  `json:"library.missingTitlesDays"`
	// SegmentDetection is the server's intro and credits detection mode
	// (SegmentDetectionModes). A document saved before the field existed
	// reads as empty, which is the default, standard.
	SegmentDetection                  string `json:"library.segmentDetection"`
	SegmentShortestEpisode            int    `json:"segments.shortestEpisode,omitempty"`
	SegmentShortestFilm               int    `json:"segments.shortestFilm,omitempty"`
	SegmentIntroWindow                string `json:"segments.introWindow,omitempty"`
	SegmentCreditsWindow              string `json:"segments.creditsWindow,omitempty"`
	SegmentFilmCreditsWindow          string `json:"segments.filmCreditsWindow,omitempty"`
	SegmentTVMode                     string `json:"segments.tvMode,omitempty"`
	SegmentAnimeMode                  string `json:"segments.animeMode,omitempty"`
	SegmentMovieMode                  string `json:"segments.movieMode,omitempty"`
	SegmentUseChapterNames            *bool  `json:"segments.useChapterNames,omitempty"`
	SegmentViewerConfidencePercent    int    `json:"segments.viewerConfidencePercent,omitempty"`
	SegmentAutomaticConfidencePercent int    `json:"segments.automaticConfidencePercent,omitempty"`
	// Connectivity, access and diagnostics policy (workstream G). The console
	// document is their single authority; the typed /v1/admin/connectivity and
	// /v1/admin/logs endpoints project these same fields.
	SecureConnections      string   `json:"secureConnections"`
	RemoteSignInPolicy     string   `json:"remoteSignInPolicy"`
	RemoteBitrateLimitKbps int      `json:"remoteBitrateLimitKbps"`
	ConnectionLimit        int      `json:"connectionLimit"`
	AdvertiseURL           string   `json:"advertiseUrl"`
	LANNetworks            []string `json:"lanNetworks"`
	AccessURLs             []string `json:"accessUrls"`
	LANDiscoveryEnabled    bool     `json:"lanDiscoveryEnabled"`
	// DLNAEnabled is the owner's one DLNA switch: anonymous browsing and
	// playback of everything, for players on the home networks only. Off is
	// the zero value, so a document saved before the field existed reads off.
	DLNAEnabled bool `json:"dlnaEnabled"`
	// Network parity with the usual self-hosted servers (25 Sep): proxies that
	// may speak for the client address, the server's own public address counted
	// as the LAN, the owner's upload capacity (the remote stream budget), the
	// paused-session limit, the interface advertised to LAN clients, and an
	// owner-supplied certificate for a custom domain.
	TrustedProxies              []string         `json:"trustedProxies"`
	UploadCapacityKbps          int              `json:"uploadCapacityKbps"`
	PausedSessionTimeoutMinutes int              `json:"pausedSessionTimeoutMinutes"`
	AdvertisedInterface         string           `json:"advertisedInterface"`
	OwnAddressEnabled           bool             `json:"ownAddressEnabled"`
	CustomCertificatePath       string           `json:"customCertificatePath"`
	CustomCertificateKeyPath    string           `json:"customCertificateKeyPath"`
	CustomCertificateDomain     string           `json:"customCertificateDomain"`
	LogLevel                    string           `json:"logLevel"`
	LogDetailMinutes            LogDetailMinutes `json:"logDetailMinutes,omitempty"`
	LogRetention                []LogRetention   `json:"logRetention"`
}

// LogRetention is one message log category's history budget in days.
type LogRetention struct {
	Category string `json:"category"`
	Days     int    `json:"days"`
}

// DefaultSettings is the registry's own answer before any owner write, so a
// fresh database and a document saved before these fields existed agree.
func DefaultSettings() Settings {
	chapterNames := true
	return Settings{AnalysisStorageGiB: 32, AnalysisStageStorageMiB: 256, SubtitleTrackLimit: medialimits.DefaultSubtitleTracks, SubtitleFileLimitMiB: medialimits.DefaultSubtitleFileMiB, Name: "Portico", TitleDatasetEnabled: true, TranscodingEnabled: true, ConversionMode: "all", OutputFormat: "auto", DownloadMaxQuality: "original", MusicAwayQuality: "original", HDRToneMapping: true, HardwareBackend: "auto", HardwareSelection: "auto", HardwareDecode: true, HDRToneMappingAlgorithm: "hable", X264Preset: "veryfast", DirectStreamRemux: true, PlanningPolicy: "maximum_fidelity", ThrottleBufferMs: 60000, PlayedRetentionMs: 180000, SessionStorageMiB: 4096, MemoryCap: "quarter", TranscodeLocation: "memory", DiagnosticDays: 30, NotificationDays: 180, JobDays: 30, UpdateChannel: "stable", UpdateFeedURL: DefaultUpdateFeedURL, MissingTitles: "after_days", MissingTitlesDays: 30, PeopleInvitationRole: "member", PeopleInvitationDays: 7, SegmentDetection: "standard", SegmentUseChapterNames: &chapterNames, SegmentShortestEpisode: 2, SegmentShortestFilm: 10, SegmentIntroWindow: "usual", SegmentCreditsWindow: "usual", SegmentFilmCreditsWindow: "usual", SegmentTVMode: "server", SegmentAnimeMode: "server", SegmentMovieMode: "server", SegmentViewerConfidencePercent: 50, SegmentAutomaticConfidencePercent: 85,
		SecureConnections: "preferred", RemoteSignInPolicy: "allow", LANNetworks: networkpolicy.DefaultHomeNetworks(), AccessURLs: []string{}, LANDiscoveryEnabled: true, TrustedProxies: []string{}, LogLevel: "info", LogDetailMinutes: 30, LogRetention: []LogRetention{}}
}

// PeopleInviteRole is the role a new invitation starts with, in the owner
// screen's own words: member or administrator. Its empty value represents an
// omitted field in an older request only; the shipped default is member.
type PeopleInviteRole string

// InviteRoles is the one ladder the owner is offered.
var InviteRoles = []PeopleInviteRole{"member", "administrator"}

func (PeopleInviteRole) JSONSchema() map[string]any {
	offered := make([]string, 0, len(InviteRoles))
	for _, role := range InviteRoles {
		offered = append(offered, string(role))
	}
	return map[string]any{"type": "string", "enum": offered}
}

func (m *PeopleInviteRole) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if !validPeopleInviteRole(PeopleInviteRole(value)) {
		return errors.New("invalid people.invitation_role")
	}
	*m = PeopleInviteRole(value)
	return nil
}

func validPeopleInviteRole(value PeopleInviteRole) bool {
	for _, offered := range InviteRoles {
		if value == offered {
			return true
		}
	}
	return false
}

// PeopleInvitationDays is the saved default expiry for a new invitation, in
// whole days. Its zero value represents an omitted field in an older request
// only; the registry, the column default and the accessor all agree on 7.
type PeopleInvitationDays int

// InvitationExpiryDays is the one ladder the owner is offered, in days.
var InvitationExpiryDays = []PeopleInvitationDays{1, 3, 7, 30}

func (PeopleInvitationDays) JSONSchema() map[string]any {
	offered := make([]int, 0, len(InvitationExpiryDays))
	for _, days := range InvitationExpiryDays {
		offered = append(offered, int(days))
	}
	return map[string]any{"type": "integer", "minimum": 1, "maximum": 30, "enum": offered}
}

func (m *PeopleInvitationDays) UnmarshalJSON(raw []byte) error {
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if !validPeopleInvitationDays(PeopleInvitationDays(value)) {
		return errors.New("invalid people.invitation_days")
	}
	*m = PeopleInvitationDays(value)
	return nil
}

func validPeopleInvitationDays(value PeopleInvitationDays) bool {
	for _, offered := range InvitationExpiryDays {
		if value == offered {
			return true
		}
	}
	return false
}

// LogDetailMinutes is a saved default, distinct from an active debug window.
// Its zero value represents an omitted field in older settings requests only.
type LogDetailMinutes int

func (LogDetailMinutes) JSONSchema() map[string]any {
	return map[string]any{"type": "integer", "enum": []int{15, 30, 60, 120, 240}}
}

func (m *LogDetailMinutes) UnmarshalJSON(raw []byte) error {
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if !validLogDetailMinutes(LogDetailMinutes(value)) {
		return errors.New("invalid logDetailMinutes")
	}
	*m = LogDetailMinutes(value)
	return nil
}
func validLogDetailMinutes(value LogDetailMinutes) bool {
	return value == 15 || value == 30 || value == 60 || value == 120 || value == 240
}

type SettingsDocument struct {
	FFmpegValidation *FFmpegStatus `json:"ffmpegValidation,omitempty"`
	Revision         int64         `json:"revision"`
	Digest           string        `json:"digest"`
	Requested        Settings      `json:"requested"`
	Effective        Settings      `json:"effective"`
	ActiveRevision   int64         `json:"activeRevision"`
	RestartFields    []string      `json:"restartFields"`
	RegistryRevision string        `json:"registryRevision"`
}
type SettingsChange struct {
	ExpectedRevision int64    `json:"expectedRevision"`
	IdempotencyKey   string   `json:"idempotencyKey"`
	Values           Settings `json:"values"`
}

func readSettings(tx *sql.Tx) (SettingsDocument, error) {
	v := DefaultSettings()
	// Until a canonical mode is stored, preserve the existing owner gate.
	v.ConversionMode = ""
	var revision int64 = 1
	e := readStoredSettings(tx, &v)
	if e != nil {
		return SettingsDocument{}, e
	}
	if e = tx.QueryRow(`SELECT revision FROM console_settings_revision WHERE singleton=1`).Scan(&revision); e != nil {
		return SettingsDocument{}, e
	}
	// Existing product settings stay single-authority, rather than mirrored values.
	var name string
	e = tx.QueryRow(`SELECT value FROM configuration WHERE key='name'`).Scan(&name)
	if e == nil {
		v.Name = name
	} else if !errors.Is(e, sql.ErrNoRows) {
		return SettingsDocument{}, e
	}
	if e = tx.QueryRow(`SELECT transcoding_enabled,per_account_cap,server_cap FROM playback_owner_policy WHERE singleton=1`).Scan(&v.TranscodingEnabled, &v.PerAccountCap, &v.ServerCap); e != nil {
		return SettingsDocument{}, e
	}
	if v.ConversionMode == "" {
		v.ConversionMode = "never"
		if v.TranscodingEnabled {
			v.ConversionMode = "all"
		}
	}
	v.TranscodingEnabled = v.ConversionMode != "never"
	b, _ := json.Marshal(v)
	effective, pending := effectiveMediaSettings(v)
	return SettingsDocument{Revision: revision, Digest: Hash(string(b)), Requested: v, Effective: effective, ActiveRevision: revision, RestartFields: pending, RegistryRevision: RegistryRevision}, nil
}

// SettingsTx reads the canonical stored settings in the exact caller-owned
// transaction. It performs no tool validation, applied-runtime callbacks,
// transaction ownership or publication. Admission uses its stored policy rows;
// the ordinary Settings projection retains its runtime/status decoration.
func (s *Store) SettingsTx(ctx context.Context, tx *sql.Tx, auth Authorize) (SettingsDocument, error) {
	if s == nil || tx == nil || ctx == nil {
		return SettingsDocument{}, ErrInvalid
	}
	if auth == nil {
		return SettingsDocument{}, identity.ErrUnauthorized
	}
	bound, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return SettingsDocument{}, err
	}
	if err = auth(bound, tx, ""); err != nil {
		return SettingsDocument{}, err
	}
	document, err := readSettings(tx)
	if err != nil {
		return SettingsDocument{}, err
	}
	return document, ctx.Err()
}

// RuntimeSettings reads the current stored/runtime policy without console-only
// tool qualification. TLS handshakes and route observation must never launch
// FFmpeg validation merely to obtain an interface or a certificate path.
func (s *Store) RuntimeSettings(ctx context.Context, auth Authorize) (out SettingsDocument, e error) {
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error { var err error; out, err = readSettings(tx); return err })
	if e == nil {
		s.projectAppliedSettings(&out)
	}
	return
}

func (s *Store) Settings(ctx context.Context, auth Authorize) (out SettingsDocument, e error) {
	out, e = s.RuntimeSettings(ctx, auth)
	if e == nil {
		if s.FFmpegValidation != nil {
			v := s.FFmpegValidation(ctx, out.Requested.FFmpegPath)
			out.FFmpegValidation = &v
		}
	}
	return
}

// projectAppliedSettings uses the startup value held by the running consumer.
// The stored requested document remains the single settings authority.
func (s *Store) projectAppliedSettings(document *SettingsDocument) {
	fields := document.RestartFields[:0]
	for _, field := range document.RestartFields {
		if field == "temporaryDirectory" && s.AppliedTemporaryDirectory != nil || field == "connectionLimit" && s.AppliedConnectionLimit != nil || field == "ffmpegPath" && s.AppliedFFmpegPath != nil {
			continue
		}
		fields = append(fields, field)
	}
	document.RestartFields = fields
	if s.AppliedTemporaryDirectory != nil {
		applied := *s.AppliedTemporaryDirectory
		document.Effective.TemporaryDirectory = applied
		if document.Requested.TemporaryDirectory != applied {
			document.RestartFields = append(document.RestartFields, "temporaryDirectory")
		}
	}
	if s.AppliedConnectionLimit != nil {
		applied := *s.AppliedConnectionLimit
		document.Effective.ConnectionLimit = applied
		if document.Requested.ConnectionLimit != applied {
			document.RestartFields = append(document.RestartFields, "connectionLimit")
		}
	}
	if s.AppliedFFmpegPath != nil {
		applied := *s.AppliedFFmpegPath
		document.Effective.FFmpegPath = applied
		if document.Requested.FFmpegPath != applied {
			document.RestartFields = append(document.RestartFields, "ffmpegPath")
		}
	}
}

func validCap(n *int) bool { return n == nil || *n >= 1 && *n <= 1000000 }

// clamp keeps a numeric transcoding control inside its published bounds rather
// than failing an owner's save: a control that overshoots its range is a client
// rounding problem. Enumerations and paths still reject, because silently
// substituting a backend or a directory would apply a policy nobody chose.
func clamp(v *int, low, high int) {
	if *v < low {
		*v = low
	}
	if *v > high {
		*v = high
	}
}

// validHardwareDeviceID accepts the shape of an adapter id the hardware report
// publishes: a short lowercase token. Whether that adapter is present is a
// runtime fact; an absent one converts on the processor.
func validHardwareDeviceID(v string) bool {
	if v == "" || len(v) > 80 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// validTemporaryDirectory accepts only an absolute, already-clean path without
// control characters. Empty means "use the server state directory".
func validTemporaryDirectory(v string) bool {
	if v == "" {
		return true
	}
	if len(v) > 1024 || !SafeText(v, 1024) || strings.ContainsAny(v, "\n\t") {
		return false
	}
	return filepath.IsAbs(v) && filepath.Clean(v) == v
}
func validateSettings(v *Settings) error {
	if !oneOf(v.ConversionMode, []string{"all", "sound", "never"}) {
		return invalidFields("values.conversionMode")
	}
	if !oneOf(v.OutputFormat, []string{"auto", "h264"}) {
		return invalidFields("values.outputFormat")
	}
	if !oneOf(v.DownloadMaxQuality, ownerDownloadQualities()) {
		return invalidFields("values.downloadMaxQuality")
	}
	if !oneOf(v.MusicAwayQuality, qualityladder.MusicIDs()) {
		return invalidFields("values.musicAwayQuality")
	}
	v.TranscodingEnabled = v.ConversionMode != "never"

	fields := []string{}
	if v.AnalysisStorageGiB < 1 || v.AnalysisStorageGiB > 16384 {
		fields = append(fields, "values.analysisStorageGiB")
	}
	if v.AnalysisStageStorageMiB < 1 || v.AnalysisStageStorageMiB > 4096 || int64(v.AnalysisStageStorageMiB) > int64(v.AnalysisStorageGiB)*1024 {
		fields = append(fields, "values.analysisStageStorageMiB")
	}
	if v.SubtitleTrackLimit < 32 || v.SubtitleTrackLimit > medialimits.MaxSubtitleTracks {
		fields = append(fields, "values.subtitleTrackLimit")
	}
	if v.SubtitleFileLimitMiB < 32 || v.SubtitleFileLimitMiB > medialimits.MaxSubtitleFileMiB {
		fields = append(fields, "values.subtitleFileLimitMiB")
	}
	if !SafeText(v.Name, 100) || strings.ContainsAny(v.Name, "\n\t") {
		fields = append(fields, "values.name")
	}
	if !validCap(v.PerAccountCap) {
		fields = append(fields, "values.perAccountCap")
	}
	if !validCap(v.ServerCap) {
		fields = append(fields, "values.serverCap")
	}
	if v.DiagnosticDays < 1 || v.DiagnosticDays > 30 {
		fields = append(fields, "values.diagnosticDays")
	}
	if v.NotificationDays < 1 || v.NotificationDays > 180 {
		fields = append(fields, "values.notificationDays")
	}
	if v.JobDays < 1 || v.JobDays > 30 {
		fields = append(fields, "values.jobDays")
	}
	if v.APIRequestsPerMinute < 0 || v.APIRequestsPerMinute > 100000 {
		fields = append(fields, "values.apiRequestsPerMinute")
	}
	if v.PlayHistoryDays < 0 || v.PlayHistoryDays > 36500 {
		fields = append(fields, "values.playHistoryDays")
	}
	if !oneOf(v.HardwareBackend, HardwareBackends) {
		fields = append(fields, "values.hardwareBackend")
	}
	if v.HardwareDevice != "" && (len(v.HardwareDevice) > 200 || !SafeText(v.HardwareDevice, 200) || strings.ContainsAny(v.HardwareDevice, "\n\t")) {
		fields = append(fields, "values.hardwareDevice")
	}
	if v.HardwareSelection != "auto" && v.HardwareSelection != "software" && !validHardwareDeviceID(v.HardwareSelection) {
		fields = append(fields, "values.hardwareSelection")
	}
	if !oneOf(v.HDRToneMappingAlgorithm, ToneMappingAlgorithms) {
		fields = append(fields, "values.hdrToneMappingAlgorithm")
	}
	if !oneOf(v.X264Preset, X264Presets) {
		fields = append(fields, "values.x264Preset")
	}
	if !oneOf(v.PlanningPolicy, PlanningPolicies) {
		fields = append(fields, "values.planningPolicy")
	}
	if !validTemporaryDirectory(v.TemporaryDirectory) {
		fields = append(fields, "values.temporaryDirectory")
	}
	if !validTemporaryDirectory(v.FFmpegPath) {
		fields = append(fields, "values.ffmpegPath")
	}
	if v.SessionStorageMiB < 64 || v.SessionStorageMiB > 1048576 {
		fields = append(fields, "values.sessionStorageMiB")
	}
	// An omitted limit is the default, so an older client still saves.
	if v.TranscodeLocation == "" {
		v.TranscodeLocation = "memory"
	}
	if !oneOf(v.TranscodeLocation, []string{"memory", "disk"}) {
		fields = append(fields, "values.transcodeLocation")
	}
	if v.MemoryCap == "" {
		v.MemoryCap = "quarter"
	}
	if !oneOf(v.MemoryCap, MemoryCaps) {
		fields = append(fields, "values.memoryCap")
	}
	// An omitted channel is the default channel rather than a refusal: a client
	// that does not know this field yet still saves the rest of the document.
	if v.UpdateChannel == "" {
		v.UpdateChannel = "stable"
	}
	if !oneOf(v.UpdateChannel, UpdateChannels) {
		fields = append(fields, "values.updates.releaseChannel")
	}
	// A feed is read by this server, so only an absolute http(s) URL is accepted
	// and nothing else is dereferenced.
	if v.UpdateFeedURL != "" && (len(v.UpdateFeedURL) > 2048 || !SafeText(v.UpdateFeedURL, 2048) || strings.ContainsAny(v.UpdateFeedURL, " \n\t") || !strings.HasPrefix(v.UpdateFeedURL, "https://") && !strings.HasPrefix(v.UpdateFeedURL, "http://")) {
		fields = append(fields, "values.updates.feedUrl")
	}
	// Empty means the shipped rule: kept for thirty days.
	if v.MissingTitles == "" {
		v.MissingTitles = "after_days"
	}
	if v.MissingTitlesDays == 0 {
		v.MissingTitlesDays = 30
	}
	if !oneOf(v.MissingTitles, MissingTitleRules) {
		fields = append(fields, "values.library.missingTitles")
	}
	if v.MissingTitlesDays < 1 || v.MissingTitlesDays > 3650 {
		fields = append(fields, "values.library.missingTitlesDays")
	}
	if v.SegmentShortestEpisode == 0 {
		v.SegmentShortestEpisode = 2
	}
	if v.SegmentShortestEpisode < 1 || v.SegmentShortestEpisode > 30 || v.SegmentShortestEpisode%1 != 0 {
		fields = append(fields, "values.segments.shortestEpisode")
	}
	if v.SegmentShortestFilm == 0 {
		v.SegmentShortestFilm = 10
	}
	if v.SegmentShortestFilm < 5 || v.SegmentShortestFilm > 60 || v.SegmentShortestFilm%5 != 0 {
		fields = append(fields, "values.segments.shortestFilm")
	}
	if v.SegmentIntroWindow == "" {
		v.SegmentIntroWindow = "usual"
	}
	if !oneOf(v.SegmentIntroWindow, []string{"usual", "long"}) {
		fields = append(fields, "values.segments.introWindow")
	}
	if v.SegmentCreditsWindow == "" {
		v.SegmentCreditsWindow = "usual"
	}
	if !oneOf(v.SegmentCreditsWindow, []string{"usual", "long"}) {
		fields = append(fields, "values.segments.creditsWindow")
	}
	if v.SegmentFilmCreditsWindow == "" {
		v.SegmentFilmCreditsWindow = "usual"
	}
	if !oneOf(v.SegmentFilmCreditsWindow, []string{"usual", "long"}) {
		fields = append(fields, "values.segments.filmCreditsWindow")
	}
	if v.SegmentTVMode == "" {
		v.SegmentTVMode = "server"
	}
	if !oneOf(v.SegmentTVMode, []string{"server", "standard", "thorough", "off"}) {
		fields = append(fields, "values.segments.tvMode")
	}
	if v.SegmentAnimeMode == "" {
		v.SegmentAnimeMode = "server"
	}
	if !oneOf(v.SegmentAnimeMode, []string{"server", "standard", "thorough", "off"}) {
		fields = append(fields, "values.segments.animeMode")
	}
	if v.SegmentMovieMode == "" {
		v.SegmentMovieMode = "server"
	}
	if !oneOf(v.SegmentMovieMode, []string{"server", "standard", "thorough", "off"}) {
		fields = append(fields, "values.segments.movieMode")
	}
	if v.SegmentUseChapterNames == nil {
		chapterNames := true
		v.SegmentUseChapterNames = &chapterNames
	}
	if v.SegmentViewerConfidencePercent == 0 {
		v.SegmentViewerConfidencePercent = 50
	}
	if v.SegmentAutomaticConfidencePercent == 0 {
		v.SegmentAutomaticConfidencePercent = 85
	}
	if v.SegmentViewerConfidencePercent != 30 && v.SegmentViewerConfidencePercent != 50 && v.SegmentViewerConfidencePercent != 70 {
		fields = append(fields, "values.segments.viewerConfidencePercent")
	}
	if v.SegmentAutomaticConfidencePercent != 70 && v.SegmentAutomaticConfidencePercent != 85 && v.SegmentAutomaticConfidencePercent != 95 {
		fields = append(fields, "values.segments.automaticConfidencePercent")
	}
	if v.SegmentDetection == "" {
		v.SegmentDetection = "standard"
	}
	if !oneOf(v.SegmentDetection, SegmentDetectionModes) {
		fields = append(fields, "values.library.segmentDetection")
	}
	fields = append(fields, validateAdministration(v)...)
	if len(fields) > 0 {
		return &ValidationError{fields}
	}
	clamp(&v.ThrottleBufferMs, 10000, 600000)
	clamp(&v.RemoteBitrateLimitKbps, 0, 200000)
	clamp(&v.PlayedRetentionMs, 0, 3600000)
	for _, target := range []*int{&v.MaxConcurrentSessions, &v.MaxHardwareSessions, &v.MaxSoftwareSessions, &v.MaxBackgroundSessions} {
		clamp(target, 0, 1000)
	}
	return nil
}
func (s *Store) ApplySettings(ctx context.Context, p identity.Principal, auth Authorize, c SettingsChange) (SettingsDocument, error) {
	return s.applySettings(ctx, p, auth, c, nil)
}

// ApplyLogSettingsInput keeps the captured typed logs request as receipt identity.
// Authorization and receipt lookup share the write transaction; a receipt can
// replay before the current revision fence, but a new stale write still fails.
func (s *Store) ApplyLogSettingsInput(ctx context.Context, p identity.Principal, auth Authorize, c SettingsChange, input json.RawMessage) (SettingsDocument, error) {
	return s.applySettings(ctx, p, auth, c, append(json.RawMessage(nil), input...))
}
func (s *Store) applySettings(ctx context.Context, p identity.Principal, auth Authorize, c SettingsChange, input json.RawMessage) (out SettingsDocument, e error) {
	omittedPeopleInvitationRole := c.Values.PeopleInvitationRole == ""
	omittedPeopleInvitationDays := c.Values.PeopleInvitationDays == 0
	omittedLogDetailMinutes := c.Values.LogDetailMinutes == 0
	omittedSegmentShortestEpisode := c.Values.SegmentShortestEpisode == 0
	omittedSegmentShortestFilm := c.Values.SegmentShortestFilm == 0
	omittedSegmentIntroWindow := c.Values.SegmentIntroWindow == ""
	omittedSegmentCreditsWindow := c.Values.SegmentCreditsWindow == ""
	omittedSegmentFilmCreditsWindow := c.Values.SegmentFilmCreditsWindow == ""
	omittedSegmentTVMode := c.Values.SegmentTVMode == ""
	omittedSegmentAnimeMode := c.Values.SegmentAnimeMode == ""
	omittedSegmentMovieMode := c.Values.SegmentMovieMode == ""
	omittedChapters := c.Values.SegmentUseChapterNames == nil
	omittedViewer := c.Values.SegmentViewerConfidencePercent == 0
	omittedAutomatic := c.Values.SegmentAutomaticConfidencePercent == 0
	normalized := c.Values
	if len(input) == 0 {
		if e = validateSettings(&normalized); e != nil {
			return out, e
		}
	}
	e = s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		receipt, err := usermutation.CheckContext(ctx, tx, s.now())
		if err != nil {
			return err
		}
		if receipt != nil {
			out, err = readSettings(tx)
			return err
		}

		scope := "settings:" + AccountKey(p)
		var receiptInput any = c
		if len(input) != 0 {
			receiptInput = struct {
				Domain string
				Input  json.RawMessage
			}{"logs-settings-v1", input}
		}
		raw, digest, err := settingsLegacyReceipt(ctx, tx, scope, c.IdempotencyKey, receiptInput, s.now())
		if err != nil {
			return err
		}
		if raw != "" {
			return decodeDocument(raw, &out)
		}
		if len(input) != 0 {
			if err = validateSettings(&normalized); err != nil {
				return err
			}
		}
		current, err := readSettings(tx)
		if err != nil {
			return err
		}
		if current.Revision != c.ExpectedRevision {
			return &ConflictError{current.Revision}
		}
		if normalized.TemporaryDirectory != current.Requested.TemporaryDirectory && s.TranscodeDirectoryAllowed != nil && !s.TranscodeDirectoryAllowed(normalized.TemporaryDirectory) {
			return invalidFields("values.temporaryDirectory")
		}
		values := normalized
		if omittedPeopleInvitationRole {
			values.PeopleInvitationRole = current.Requested.PeopleInvitationRole
		}
		if omittedPeopleInvitationDays {
			values.PeopleInvitationDays = current.Requested.PeopleInvitationDays
		}
		if omittedLogDetailMinutes {
			values.LogDetailMinutes = current.Requested.LogDetailMinutes
		}
		if omittedSegmentShortestEpisode {
			values.SegmentShortestEpisode = current.Requested.SegmentShortestEpisode
		}
		if omittedSegmentShortestFilm {
			values.SegmentShortestFilm = current.Requested.SegmentShortestFilm
		}
		if omittedSegmentIntroWindow {
			values.SegmentIntroWindow = current.Requested.SegmentIntroWindow
		}
		if omittedSegmentCreditsWindow {
			values.SegmentCreditsWindow = current.Requested.SegmentCreditsWindow
		}
		if omittedSegmentFilmCreditsWindow {
			values.SegmentFilmCreditsWindow = current.Requested.SegmentFilmCreditsWindow
		}
		if omittedSegmentTVMode {
			values.SegmentTVMode = current.Requested.SegmentTVMode
		}
		if omittedSegmentAnimeMode {
			values.SegmentAnimeMode = current.Requested.SegmentAnimeMode
		}
		if omittedSegmentMovieMode {
			values.SegmentMovieMode = current.Requested.SegmentMovieMode
		}
		if omittedChapters {
			values.SegmentUseChapterNames = current.Requested.SegmentUseChapterNames
		}
		if omittedViewer {
			values.SegmentViewerConfidencePercent = current.Requested.SegmentViewerConfidencePercent
		}
		if omittedAutomatic {
			values.SegmentAutomaticConfidencePercent = current.Requested.SegmentAutomaticConfidencePercent
		}
		revision := current.Revision + 1
		if err = writeStoredSettings(tx, values, s.now()); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO configuration VALUES('name',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, values.Name); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE playback_owner_policy SET revision=revision+1,transcoding_enabled=?,per_account_cap=?,server_cap=? WHERE singleton=1`, values.TranscodingEnabled, values.PerAccountCap, values.ServerCap); err != nil {
			return err
		}
		// Domain triggers fence legacy writes. This entire authorized transaction is
		// one externally visible console revision, regardless of account-cap count.
		if _, err = tx.Exec(`UPDATE console_settings_revision SET revision=? WHERE singleton=1`, revision); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE server_configuration_state SET pending_revision=? WHERE singleton=1`, revision); err != nil {
			return err
		}
		beforeValues := SettingsValues(current.Requested)
		for key, value := range SettingsValues(values) {
			if reflect.DeepEqual(value, beforeValues[key]) {
				continue
			}
			if _, err = tx.Exec(`INSERT INTO server_setting_sources VALUES(?,'server') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, key); err != nil {
				return err
			}
			if err = QueueConfigurationProjectionTx(ctx, tx, key, revision); err != nil {
				return err
			}
		}
		if err = Audit(tx, s.now(), AccountKey(p), "settings.apply", "server", revision); err != nil {
			return err
		}
		out, err = readSettings(tx)
		if err != nil {
			return err
		}
		return saveSettingsReceipt(ctx, tx, scope, c.IdempotencyKey, digest, out, "server", out.Revision, s.now())
	})
	if e == nil {
		e = s.FlushConfiguration(context.WithoutCancel(ctx))
		if s.ConfigChanged != nil {
			s.ConfigChanged(context.WithoutCancel(ctx))
		}
	}
	if e == nil {
		s.projectAppliedSettings(&out)
		if s.FFmpegValidation != nil {
			v := s.FFmpegValidation(ctx, out.Requested.FFmpegPath)
			out.FFmpegValidation = &v
		}
	}
	return
}

func days(n int) int64 { return int64(time.Duration(n) * 24 * time.Hour / time.Millisecond) }

func ownerDownloadQualities() []string {
	out := []string{}
	for _, id := range qualityladder.IDs() {
		if id != "automatic" {
			out = append(out, id)
		}
	}
	return out
}
