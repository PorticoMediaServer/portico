package metadata

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const DefaultLocalArtworkLimitBytes int64 = 2 << 30

// MetadataSettings exposes credential presence only. Submitted keys never return
// in JSON or provider failures; blank keys restore Portico's application key.
type MetadataSettings struct {
	ArtworkLanguage          string                    `json:"artworkLanguage,omitempty"`
	ArtworkPreference        string                    `json:"artworkPreference,omitempty"`
	OnlineLookups            *bool                     `json:"onlineLookups,omitempty"`
	DefaultLanguage          string                    `json:"defaultLanguage,omitempty"`
	DefaultRegion            string                    `json:"defaultRegion,omitempty"`
	LibrariesComplete        *bool                     `json:"librariesComplete,omitempty"`
	Libraries                []MetadataLibrarySettings `json:"libraries,omitempty"`
	LocalArtworkUsedBytes    int64                     `json:"localArtworkUsedBytes"`
	Revision                 int64                     `json:"revision"`
	LocalArtworkLimitBytes   int64                     `json:"localArtworkLimitBytes"`
	IncludeAdult             bool                      `json:"includeAdult"`
	ArtworkMaxEdge           int                       `json:"artworkMaxEdge"`
	TMDBCredentialConfigured bool                      `json:"tmdbCredentialConfigured"`
	TVDBCredentialConfigured bool                      `json:"tvdbCredentialConfigured"`
	// OpenSubtitles, for subtitles found online: whether the owner saved their
	// own API key and password (neither is ever sent back), and the account name.
	OpenSubtitlesKeyConfigured      bool   `json:"openSubtitlesKeyConfigured"`
	OpenSubtitlesUsername           string `json:"openSubtitlesUsername"`
	OpenSubtitlesPasswordConfigured bool   `json:"openSubtitlesPasswordConfigured"`
	// Where the key and account in force come from and how OpenSubtitles last answered.
	OpenSubtitles OpenSubtitlesStatus `json:"openSubtitles"`
	// Advanced: which held titles are refreshed on their own ("regular",
	// "recent" or "never"), what fills a field the metadata language lacks
	// ("en", "original" or "blank"), AniList's title form ("language",
	// "english", "romaji" or "native"), and whether cast and crew photos are
	// fetched.
	RefreshPolicy    string `json:"refreshPolicy"`
	FallbackLanguage string `json:"fallbackLanguage"`
	AnimeTitles      string `json:"animeTitles"`
	CastPhotos       bool   `json:"castPhotos"`
	// The owner's AcoustID application key is never sent back. AcoustIDKeySource
	// says which key is in force: "owner", "environment" or "none".
	AcoustIDKeyConfigured bool   `json:"acoustidKeyConfigured"`
	AcoustIDKeySource     string `json:"acoustidKeySource"`
	// RefreshAll reports an owner's Refresh all: when it was asked for and
	// whether its sweep is still waking titles.
	RefreshAll RefreshAllStatus `json:"refreshAll"`
}

// RefreshAllStatus is the state of Server › Metadata › Refresh all.
type RefreshAllStatus struct {
	Running     bool   `json:"running"`
	RequestedAt string `json:"requestedAt,omitempty"`
}

var (
	MetadataRefreshPolicies   = []string{"regular", "recent", "never"}
	MetadataFallbackLanguages = []string{"en", "original", "blank"}
	MetadataAnimeTitleChoices = []string{"language", "english", "romaji", "native"}
)

// OpenSubtitlesStatus mirrors subtitles.OwnerStatus field for field (the
// server converts one to the other); it never carries a secret.
type OpenSubtitlesStatus struct {
	// "owner", "environment" or "none".
	KeySource     string `json:"keySource"`
	AccountSource string `json:"accountSource"`
	// "none", "incomplete", "not_checked", "signed_in" or "rejected".
	Account string `json:"account"`
	// The last refusal (provider_key_rejected, provider_sign_in_failed,
	// provider_sign_in_required, provider_quota_exhausted, provider_rate_limited,
	// provider_unreachable); empty when the last exchange worked.
	Problem   string `json:"problem,omitempty"`
	CheckedAt string `json:"checkedAt,omitempty"`
	RetryAt   string `json:"retryAt,omitempty"`
	// As OpenSubtitles last reported them; -1 when unknown.
	DownloadsRemaining int    `json:"downloadsRemaining"`
	DownloadsAllowed   int    `json:"downloadsAllowed"`
	DownloadsResetAt   string `json:"downloadsResetAt,omitempty"`
}

var noSubtitleProvider = OpenSubtitlesStatus{KeySource: "none", AccountSource: "none", Account: "none", DownloadsRemaining: -1, DownloadsAllowed: -1}

func (s *Service) subtitleStatus() OpenSubtitlesStatus {
	if s.SubtitleStatus == nil {
		return noSubtitleProvider
	}
	return s.SubtitleStatus()
}

// CheckOpenSubtitles asks OpenSubtitles whether the credentials in force work
// now (Server › Metadata › Test connection).
func (s *Service) CheckOpenSubtitles(ctx context.Context) (OpenSubtitlesStatus, error) {
	if e := s.loadProviderSettings(ctx); e != nil {
		return noSubtitleProvider, e
	}
	if s.SubtitleCheck == nil {
		return noSubtitleProvider, nil
	}
	return s.SubtitleCheck(ctx), nil
}

type MetadataSettingsUpdate struct {
	ArtworkLanguage        *string                 `json:"artworkLanguage,omitempty"`
	ArtworkPreference      *string                 `json:"artworkPreference,omitempty"`
	OnlineLookups          *bool                   `json:"onlineLookups,omitempty"`
	DefaultLanguage        *string                 `json:"defaultLanguage,omitempty"`
	DefaultRegion          *string                 `json:"defaultRegion,omitempty"`
	Libraries              []MetadataLibraryChange `json:"libraries,omitempty"`
	ExpectedRevision       int64                   `json:"expectedRevision"`
	LocalArtworkLimitBytes int64                   `json:"localArtworkLimitBytes"`
	IncludeAdult           bool                    `json:"includeAdult"`
	ArtworkMaxEdge         *int                    `json:"artworkMaxEdge,omitempty"`
	TMDBReadToken          *string                 `json:"tmdbReadToken,omitempty"`
	TVDBProjectKey         *string                 `json:"tvdbProjectKey,omitempty"`
	// Absent: unchanged. Empty: back to the server's own (the environment's).
	OpenSubtitlesAPIKey   *string `json:"openSubtitlesApiKey,omitempty"`
	OpenSubtitlesUsername *string `json:"openSubtitlesUsername,omitempty"`
	OpenSubtitlesPassword *string `json:"openSubtitlesPassword,omitempty"`
	// Absent: unchanged.
	RefreshPolicy    *string `json:"refreshPolicy,omitempty"`
	FallbackLanguage *string `json:"fallbackLanguage,omitempty"`
	AnimeTitles      *string `json:"animeTitles,omitempty"`
	CastPhotos       *bool   `json:"castPhotos,omitempty"`
	// Absent: unchanged. Empty: back to the server's own (the environment's).
	AcoustIDKey *string `json:"acoustidKey,omitempty"`
}

// subtitleCredentials are the owner's saved OpenSubtitles key and account,
// read together with the other provider secrets the settings hold.
type subtitleCredentials struct{ key, username, password, acoustid string }

func readMetadataSettings(ctx context.Context, tx *sql.Tx) (MetadataSettings, string, string, error) {
	out, tmdb, tvdb, _, err := readMetadataSettingsWithSubtitles(ctx, tx)
	if err == nil {
		var complete bool
		out.Libraries, complete, err = readMetadataLibraries(ctx, tx)
		out.LibrariesComplete = &complete
	}
	return out, tmdb, tvdb, err
}
func readMetadataSettingsWithSubtitles(ctx context.Context, tx *sql.Tx) (MetadataSettings, string, string, subtitleCredentials, error) {
	var out MetadataSettings
	var online bool
	var tmdb, tvdb string
	var subs subtitleCredentials
	var refreshEpoch int64
	err := tx.QueryRowContext(ctx, `SELECT revision,local_artwork_limit_bytes,include_adult,artwork_max_edge,tmdb_read_token,tvdb_project_key,opensubtitles_api_key,opensubtitles_username,opensubtitles_password,default_language,default_region,online_lookups,artwork_preference,artwork_language,refresh_policy,fallback_language,anime_titles,cast_photos,acoustid_key,refresh_all_epoch,refresh_all_running FROM metadata_runtime_settings WHERE singleton=1`).Scan(&out.Revision, &out.LocalArtworkLimitBytes, &out.IncludeAdult, &out.ArtworkMaxEdge, &tmdb, &tvdb, &subs.key, &subs.username, &subs.password, &out.DefaultLanguage, &out.DefaultRegion, &online, &out.ArtworkPreference, &out.ArtworkLanguage, &out.RefreshPolicy, &out.FallbackLanguage, &out.AnimeTitles, &out.CastPhotos, &subs.acoustid, &refreshEpoch, &out.RefreshAll.Running)
	out.OnlineLookups = &online
	out.TMDBCredentialConfigured = tmdb != ""
	out.TVDBCredentialConfigured = tvdb != ""
	out.OpenSubtitlesKeyConfigured = subs.key != ""
	out.OpenSubtitlesUsername = subs.username
	out.OpenSubtitlesPasswordConfigured = subs.password != ""
	out.AcoustIDKeyConfigured = subs.acoustid != ""
	if refreshEpoch > 0 {
		out.RefreshAll.RequestedAt = time.Unix(refreshEpoch, 0).UTC().Format(time.RFC3339)
	}
	return out, tmdb, tvdb, subs, err
}

// validSubtitleAccount bounds an account name or password: printable, one line.
func validSubtitleAccount(v *string) bool {
	if v == nil {
		return true
	}
	if len(*v) > 512 {
		return false
	}
	for _, r := range *v {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func (s *Service) MetadataSettings(ctx context.Context) (MetadataSettings, error) {
	read, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return MetadataSettings{}, e
	}
	defer read.Rollback()
	out, _, _, e := readMetadataSettings(ctx, read.Tx())
	read.Rollback()
	if e == nil && s.LocalArtworkUsage != nil {
		out.LocalArtworkUsedBytes, e = s.LocalArtworkUsage(ctx)
	}
	out.OpenSubtitles = s.subtitleStatus()
	out.AcoustIDKeySource = s.acoustidKeySource(out.AcoustIDKeyConfigured)
	return out, e
}
func LocalArtworkLimit(ctx context.Context, db *sql.DB) int64 {
	var limit int64
	if dbwork.QueryRow(ctx, db, `SELECT local_artwork_limit_bytes FROM metadata_runtime_settings WHERE singleton=1`).Scan(&limit) != nil || limit < 1 {
		return DefaultLocalArtworkLimitBytes
	}
	return limit
}
func validProviderCredential(v *string) bool {
	if v == nil {
		return true
	}
	if len(*v) > 8192 {
		return false
	}
	for _, r := range *v {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func (s *Service) UpdateMetadataSettings(ctx context.Context, in MetadataSettingsUpdate, authorize func(*sql.Tx) error) (MetadataSettings, error) {
	if !validMetadataSettingsUpdate(in) {
		return MetadataSettings{}, ErrRepairInput
	}
	if _, _, protocol := usermutation.RequestFrom(ctx); protocol && authorize == nil {
		return MetadataSettings{}, ErrRepairInput
	}
	write, e := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if e != nil {
		return MetadataSettings{}, e
	}
	defer write.Rollback()
	tx := write.Tx()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return MetadataSettings{}, e
		}
	}
	r, e := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if e != nil {
		return MetadataSettings{}, e
	}
	if r != nil {
		write.Rollback()
		return s.MetadataSettings(ctx)
	}
	before, e := ReadMetadataSettingsTx(ctx, tx)
	if e != nil {
		return before, e
	}
	out, e := ApplyMetadataSettingsTx(ctx, tx, in, s.publicationTime())
	if e != nil {
		return out, e
	}
	if e = (Configuration{}).queueChange(ctx, tx, before, out); e != nil {
		return out, e
	}
	if e = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: "metadata", Revision: out.Revision}, time.Now().UnixMilli()); e != nil {
		return MetadataSettings{}, e
	}
	if e = write.Commit(); e != nil {
		return out, e
	}
	if s.ConfigurationCommitted != nil {
		if e = s.ConfigurationCommitted(context.WithoutCancel(ctx)); e != nil {
			return out, e
		}
	}
	if e = s.loadProviderSettings(ctx); e != nil {
		return out, e
	}
	if s.LocalArtworkUsage != nil {
		out.LocalArtworkUsedBytes, e = s.LocalArtworkUsage(ctx)
	}
	out.OpenSubtitles = s.subtitleStatus()
	out.AcoustIDKeySource = s.acoustidKeySource(out.AcoustIDKeyConfigured)
	return out, e
}
func (s *Service) loadProviderSettings(ctx context.Context) error {
	// Serialize key installation so an older settings read cannot undo a newer save.
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	read, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return e
	}
	out, tmdb, tvdb, subs, e := readMetadataSettingsWithSubtitles(ctx, read.Tx())
	read.Rollback()
	if e != nil {
		return e
	}
	if s.settingsRevision == out.Revision {
		return nil
	}
	if s.SubtitleCredentials != nil {
		s.SubtitleCredentials(subs.key, subs.username, subs.password)
	}
	if tmdb == "" {
		tmdb = s.token
	}
	if tvdb == "" {
		tvdb = TVDBProjectKey
	}
	if s.tmdbPaced != nil {
		s.tmdbPaced.SetCredential(tmdb)
		s.tmdbPaced.SetIncludeAdult(out.IncludeAdult)
		s.tmdbPaced.SetFallbackLanguage(out.FallbackLanguage)
	}
	if s.anilistPaced != nil {
		s.anilistPaced.SetTitlePreference(out.AnimeTitles)
	}
	s.refreshPolicy.Store(out.RefreshPolicy)
	s.castPhotos.Store(out.CastPhotos)
	if e = s.useAcoustIDKey(subs.acoustid); e != nil {
		return e
	}
	if p, ok := s.tvdb.(interface {
		SetCredential(context.Context, string) error
	}); ok {
		if e = p.SetCredential(ctx, tvdb); e != nil {
			return e
		}
	}
	s.settingsRevision = out.Revision
	return nil
}

// ReadMetadataSettingsTx returns only sanitized stored settings in the caller's snapshot.
func ReadMetadataSettingsTx(ctx context.Context, tx *sql.Tx) (MetadataSettings, error) {
	if tx == nil {
		return MetadataSettings{}, ErrRepairInput
	}
	out, _, _, err := readMetadataSettings(ctx, tx)
	out.OpenSubtitles = noSubtitleProvider
	return out, err
}

func validMetadataSettingsUpdate(in MetadataSettingsUpdate) bool {
	oneOf := func(v *string, set []string) bool { return v == nil || slices.Contains(set, *v) }
	if !oneOf(in.RefreshPolicy, MetadataRefreshPolicies) || !oneOf(in.FallbackLanguage, MetadataFallbackLanguages) || !oneOf(in.AnimeTitles, MetadataAnimeTitleChoices) || !validProviderCredential(in.AcoustIDKey) || in.AcoustIDKey != nil && len(*in.AcoustIDKey) > 256 {
		return false
	}
	return !(in.ExpectedRevision < 1 || in.LocalArtworkLimitBytes < 1 || in.LocalArtworkLimitBytes > 1<<50 || !validProviderCredential(in.TMDBReadToken) || !validProviderCredential(in.TVDBProjectKey) || !validProviderCredential(in.OpenSubtitlesAPIKey) || !validSubtitleAccount(in.OpenSubtitlesUsername) || !validSubtitleAccount(in.OpenSubtitlesPassword) || in.ArtworkMaxEdge != nil && !validArtworkEdge(*in.ArtworkMaxEdge))
}

// ApplyMetadataSettingsTx is the single validated CAS writer. The caller authorizes
// and commits; provider installation and file projection happen only after commit.
func ApplyMetadataSettingsTx(ctx context.Context, tx *sql.Tx, in MetadataSettingsUpdate, _ time.Time) (MetadataSettings, error) {
	if tx == nil {
		return MetadataSettings{}, ErrRepairInput
	}
	if !validMetadataSettingsUpdate(in) {
		return MetadataSettings{}, ErrRepairInput
	}
	current, tmdb, tvdb, subs, e := readMetadataSettingsWithSubtitles(ctx, tx)
	if e != nil {
		return current, e
	}
	previousTMDB, previousTVDB := tmdb, tvdb
	if current.Revision != in.ExpectedRevision {
		return current, ErrScreenConflict
	}
	if in.DefaultLanguage != nil && !slices.Contains(ScreenMetadataLanguages, *in.DefaultLanguage) {
		return current, ErrRepairInput
	}
	if in.DefaultRegion != nil && (len(*in.DefaultRegion) != 2 || (*in.DefaultRegion)[0] < 'A' || (*in.DefaultRegion)[0] > 'Z' || (*in.DefaultRegion)[1] < 'A' || (*in.DefaultRegion)[1] > 'Z') {
		return current, ErrRepairInput
	}
	artworkLanguage := current.ArtworkLanguage
	if in.ArtworkLanguage != nil {
		if *in.ArtworkLanguage != "same" && *in.ArtworkLanguage != "en" && *in.ArtworkLanguage != "textless" {
			return current, ErrRepairInput
		}
		artworkLanguage = *in.ArtworkLanguage
	}
	artworkPreference := current.ArtworkPreference
	if in.ArtworkPreference != nil {
		if *in.ArtworkPreference != "folders" && *in.ArtworkPreference != "online" {
			return current, ErrRepairInput
		}
		artworkPreference = *in.ArtworkPreference
	}
	online := current.OnlineLookups != nil && *current.OnlineLookups
	if in.OnlineLookups != nil {
		online = *in.OnlineLookups
	}
	lookupChanged := current.OnlineLookups != nil && online != *current.OnlineLookups
	defaultLanguage, defaultRegion := current.DefaultLanguage, current.DefaultRegion
	if in.DefaultLanguage != nil {
		defaultLanguage = *in.DefaultLanguage
	}
	if in.DefaultRegion != nil {
		defaultRegion = *in.DefaultRegion
	}

	if e = applyMetadataLibraries(ctx, tx, in.Libraries); e != nil {
		return current, e
	}
	if in.TMDBReadToken != nil {
		tmdb = strings.TrimSpace(*in.TMDBReadToken)
	}
	if in.TVDBProjectKey != nil {
		tvdb = strings.TrimSpace(*in.TVDBProjectKey)
	}
	if in.OpenSubtitlesAPIKey != nil {
		subs.key = strings.TrimSpace(*in.OpenSubtitlesAPIKey)
	}
	if in.OpenSubtitlesUsername != nil {
		username := strings.TrimSpace(*in.OpenSubtitlesUsername)
		// A different account (or none) never keeps the previous account's password.
		if username != subs.username {
			subs.password = ""
		}
		subs.username = username
	}
	if in.OpenSubtitlesPassword != nil {
		subs.password = *in.OpenSubtitlesPassword
	}
	edge := current.ArtworkMaxEdge
	if in.ArtworkMaxEdge != nil {
		edge = *in.ArtworkMaxEdge
	}
	advanced := current
	for _, v := range []struct {
		in  *string
		out *string
	}{{in.RefreshPolicy, &advanced.RefreshPolicy}, {in.FallbackLanguage, &advanced.FallbackLanguage}, {in.AnimeTitles, &advanced.AnimeTitles}} {
		if v.in != nil {
			*v.out = *v.in
		}
	}
	if in.CastPhotos != nil {
		advanced.CastPhotos = *in.CastPhotos
	}
	if in.AcoustIDKey != nil {
		subs.acoustid = strings.TrimSpace(*in.AcoustIDKey)
	}
	_, e = tx.ExecContext(ctx, `UPDATE metadata_runtime_settings SET revision=revision+1,local_artwork_limit_bytes=?,include_adult=?,artwork_max_edge=?,tmdb_read_token=?,tvdb_project_key=?,opensubtitles_api_key=?,opensubtitles_username=?,opensubtitles_password=?,default_language=?,default_region=?,lookup_epoch=lookup_epoch+CASE WHEN online_lookups<>? THEN 1 ELSE 0 END,online_lookups=?,artwork_policy_revision=artwork_policy_revision+CASE WHEN artwork_preference<>? OR artwork_language<>? THEN 1 ELSE 0 END,artwork_preference=?,artwork_language=?,refresh_policy=?,fallback_language=?,anime_titles=?,cast_photos=?,acoustid_key=? WHERE singleton=1`, in.LocalArtworkLimitBytes, in.IncludeAdult, edge, tmdb, tvdb, subs.key, subs.username, subs.password, defaultLanguage, defaultRegion, online, online, artworkPreference, artworkLanguage, artworkPreference, artworkLanguage, advanced.RefreshPolicy, advanced.FallbackLanguage, advanced.AnimeTitles, advanced.CastPhotos, subs.acoustid)
	if e == nil && lookupChanged {
		e = invalidateLookupWorkTx(ctx, tx)
	}
	if e == nil && !lookupChanged && (artworkPreference != current.ArtworkPreference || artworkLanguage != current.ArtworkLanguage) {
		e = requestLookupFanoutTx(ctx, tx, "artwork")
	}
	if e == nil && edge != current.ArtworkMaxEdge {
		e = scheduleArtworkSizeTx(ctx, tx, current.ArtworkMaxEdge, edge)
	}
	if e != nil {
		return current, e
	}
	// Previously final authentication failures can recover after a key correction.
	if tmdb != previousTMDB || tvdb != previousTVDB {
		if e = requestLookupFanoutTx(ctx, tx, "screen_retry"); e != nil {
			return current, e
		}
	}
	out, _, _, e := readMetadataSettings(ctx, tx)
	if e != nil {
		return out, e
	}

	out.OpenSubtitles = noSubtitleProvider
	return out, nil
}
