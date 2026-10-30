package administration

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"sort"
)

var analysisRuntime = map[string]string{"probe": "probe", "local_metadata": "local_metadata", "subtitle_discovery": "subtitles", "loudness": "loudness", "audio_fingerprint": "fingerprint", "trickplay": "trickplay", "chapter_images": "chapter_images", "checksum": "checksum", "waveform": "waveform"}

func libraryMediaKind(kind string) string {
	switch kind {
	case "tv", "anime":
		return "show"
	case "audiobook":
		return "book"
	}
	return kind
}
func effectiveLibraryPolicy(ctx context.Context, tx *sql.Tx, id, kind string, v *LibrarySettings, revision int64) (int64, error) {
	prefs, err := catalog.LibraryPreferencesTx(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	v.GroupCollections = prefs.GroupCollections
	v.LocalTags = prefs.LocalTags
	v.LyricsSource = prefs.LyricsSource
	v.TitleSource = prefs.TitleSource
	v.EpisodeNumbering = prefs.EpisodeNumbering
	v.FindExtras = prefs.FindExtras
	v.SortIgnoreArticles = prefs.SortIgnoreArticles
	v.RefreshMode = "replace_unlocked"
	v.AcoustIDEnabled = false
	if kind == "music" || kind == "audiobook" {
		if err = tx.QueryRowContext(ctx, `SELECT local_mode,acoustid_enabled FROM audio_metadata_policies WHERE library_id=?`, id).Scan(&v.LocalTags, &v.AcoustIDEnabled); err != nil {
			return 0, err
		}
	}
	defaultsRevision := int64(0)
	if kind == "movie" || kind == "tv" || kind == "anime" {
		if err = tx.QueryRowContext(ctx, `SELECT refresh_mode FROM screen_metadata_policies WHERE library_id=?`, id).Scan(&v.RefreshMode); err != nil {
			return 0, err
		}
		// The defaults this page offers for Reset are the server's metadata
		// language and region, so their own revision joins the fence: a change to
		// either must stale a page that would otherwise reset to the old pair.
		var localeRevision int64
		err = tx.QueryRowContext(ctx, `SELECT locale_revision FROM metadata_runtime_settings WHERE singleton=1`).Scan(&localeRevision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if localeRevision > 1 {
			defaultsRevision = localeRevision - 1
		}
	}
	p, err := catalog.ScanPolicyTx(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	provider, err := metadata.AdministrationPolicyTx(ctx, tx, id, kind)
	if err != nil {
		return 0, err
	}
	v.Analysis = analysisOperations(p)
	v.ScanDepth = scanDepthFor(p)
	geometry := p.Trickplay.Normalized()
	v.Navigation = GeneratedNavigation{TrickplayIntervalMs: geometry.IntervalSeconds * 1000, TrickplayTileWidth: geometry.TileWidth, TrickplayMaxTiles: geometry.MaxTiles, ChapterThumbnailMode: "embedded", VideoPreviewMs: 30000}
	if containsOperation(p.Operations, "chapter_images") {
		v.Navigation.ChapterThumbnailMode = "generated"
	}
	v.Providers = []ProviderSelection{}
	for _, providerID := range provider.Providers {
		v.Providers = append(v.Providers, ProviderSelection{MediaKind: libraryMediaKind(kind), Provider: providerID, Language: provider.Language, Region: provider.Region})
	}
	if len(v.Providers) == 0 {
		// Nothing online is selected, but the language and region are still the
		// library's own stored choices. Publishing them keeps the page showing what
		// the owner picked, and stops a later return to an online provider from
		// sending blanks that would clear them.
		v.Providers = append(v.Providers, ProviderSelection{MediaKind: libraryMediaKind(kind), Provider: "local", Language: provider.Language, Region: provider.Region})
	}
	// Every contributing revision only increases, so all administration and
	// domain writers invalidate this single optimistic revision without hashes.
	return libraryRevision(revision, p.Revision, provider.Revision, prefs.Revision, defaultsRevision), nil
}

// libraryRevision is the one composite fence a library's page publishes and its
// next write must present: the stored document, its scan policy, its provider
// policy, its preferences and, where the page offers them, the metadata defaults
// its Reset uses, each contribution counted once. A listing that publishes this
// value can be saved from exactly as the page itself can.
func libraryRevision(document, scan, provider, preferences, defaults int64) int64 {
	return document + scan - 1 + provider + preferences - 1 + defaults
}

// analysisOperations lists the drawn analysis steps a stored scan policy
// enables, in the matrix the page renders. A count of them is the number of
// steps the library will actually run.
func analysisOperations(p catalog.ScanPolicy) []string {
	out := []string{}
	for ui, op := range analysisRuntime {
		if containsOperation(p.Operations, op) {
			out = append(out, ui)
		}
	}
	sort.Strings(out)
	return out
}
func applyLibraryPolicy(ctx context.Context, tx *sql.Tx, id, kind string, v LibrarySettings) error {
	if kind != "movie" && (!v.GroupCollections) {
		return invalid("settings.groupCollections")
	}
	if kind != "movie" && kind != "tv" && kind != "anime" {
		if v.TitleSource != "tags" || !v.FindExtras || v.RefreshMode != "replace_unlocked" {
			return invalid("settings.titleSource", "settings.findExtras", "settings.refreshMode")
		}
	}
	if (kind == "book" || kind == "comic") && v.LocalTags != "prefer" {
		return invalid("settings.localTags")
	}
	if kind != "music" && kind != "audiobook" && v.LocalTags == "supplement" {
		return invalid("settings.localTags")
	}
	if kind != "music" && (v.LyricsSource != "files" || v.AcoustIDEnabled) {
		return invalid("settings.lyricsSource", "settings.acoustidEnabled")
	}
	if kind != "tv" && kind != "anime" && v.EpisodeNumbering != "season" {
		return invalid("settings.episodeNumbering")
	}
	prefs := catalog.LibraryPreferences{GroupCollections: v.GroupCollections, LocalTags: v.LocalTags, LyricsSource: v.LyricsSource, TitleSource: v.TitleSource, EpisodeNumbering: v.EpisodeNumbering, FindExtras: v.FindExtras, SortIgnoreArticles: v.SortIgnoreArticles}
	if kind == "music" || kind == "audiobook" {
		prefs.LocalTags = "prefer"
	}
	if err := catalog.ApplyLibraryPreferencesTx(ctx, tx, id, prefs); err != nil {
		return err
	}
	if kind == "music" || kind == "audiobook" {
		if _, err := tx.ExecContext(ctx, `UPDATE audio_metadata_policies SET local_mode=?,acoustid_enabled=?,revision=revision+1 WHERE library_id=? AND (local_mode<>? OR acoustid_enabled<>?)`, v.LocalTags, v.AcoustIDEnabled, id, v.LocalTags, v.AcoustIDEnabled); err != nil {
			return err
		}
	}
	ops := []string{}
	seen := map[string]bool{}
	for _, ui := range v.Analysis {
		op, ok := analysisRuntime[ui]
		if !ok {
			return invalid("settings.analysis")
		}
		if !seen[op] {
			ops = append(ops, op)
			seen[op] = true
		}
	}
	// This form owns only its drawn analysis operations. Registered internal
	// stages must survive an unrelated save without becoming new UI values.
	// Explicit Complete gets the domain's Complete set; explicit lower depths
	// withdraw deep admission without changing independent stored mode choices.
	current, err := catalog.ScanPolicyTx(ctx, tx, id)
	if err != nil {
		return err
	}
	managed := map[string]bool{}
	for _, op := range analysisRuntime {
		managed[op] = true
	}
	internal := []string{}
	if v.ScanDepth != "file_list_only" && (v.ScanDepth == "custom" || v.ScanDepth == scanDepthFor(current)) {
		internal = append(internal, current.Operations...)
	}
	if v.ScanDepth == "complete" {
		internal = append(internal, catalog.CompleteScanOperations...)
	}
	for _, op := range internal {
		if !managed[op] && !seen[op] {
			ops = append(ops, op)
			seen[op] = true
		}
	}
	if v.Navigation.VideoPreviewEnabled || v.Navigation.VideoPreviewMs != 30000 {
		return invalid("settings.navigation.videoPreviewEnabled")
	}
	if (v.Navigation.ChapterThumbnailMode == "generated") != seen["chapter_images"] || v.Navigation.ChapterThumbnailMode == "none" {
		return invalid("settings.navigation.chapterThumbnailMode")
	}
	p := catalog.ScanPolicy{Tier: "custom", Operations: ops, Trickplay: catalog.TrickplaySettings{IntervalSeconds: v.Navigation.TrickplayIntervalMs / 1000, TileWidth: v.Navigation.TrickplayTileWidth, MaxTiles: v.Navigation.TrickplayMaxTiles}}
	if v.ScanDepth == "file_list_only" {
		p.Tier = "file_list_only"
	}
	if err := catalog.ApplyScanPolicyTx(ctx, tx, id, p); err != nil {
		return invalid("settings.analysis", "settings.navigation")
	}
	provider := metadata.LibraryProviderPolicy{Providers: []string{}}
	selected := map[string]bool{}
	for i, sel := range v.Providers {
		if sel.APIKey != "" {
			return invalid("settings.providers.apiKey")
		}
		if sel.MediaKind != libraryMediaKind(kind) {
			return invalid("settings.providers.mediaKind")
		}
		if i == 0 {
			provider.Language, provider.Region = sel.Language, sel.Region
		} else if provider.Language != sel.Language || provider.Region != sel.Region {
			return invalid("settings.providers")
		}
		if sel.Provider == "local" {
			if len(v.Providers) > 1 {
				return invalid("settings.providers")
			}
			continue
		}
		if !selected[sel.Provider] {
			provider.Providers = append(provider.Providers, sel.Provider)
			selected[sel.Provider] = true
		}
	}
	provider.RefreshMode = v.RefreshMode
	if err := metadata.ApplyAdministrationPolicyTx(ctx, tx, id, kind, provider); err != nil {
		return invalid("settings.providers")
	}
	return nil
}

func containsOperation(ops []string, op string) bool {
	for _, v := range ops {
		if v == op {
			return true
		}
	}
	return false
}
func scanDepthFor(p catalog.ScanPolicy) string {
	if p.Tier == "file_list_only" {
		return p.Tier
	}
	if !containsOperation(p.Operations, "probe") {
		return "custom"
	}
	basic := containsOperation(p.Operations, "local_metadata") && containsOperation(p.Operations, "subtitles") && !containsOperation(p.Operations, "checksum") && !containsOperation(p.Operations, "fingerprint")
	if basic && !containsOperation(p.Operations, "waveform") && !containsOperation(p.Operations, "loudness") {
		return "basic"
	}
	if basic && containsOperation(p.Operations, "waveform") && containsOperation(p.Operations, "loudness") {
		return "complete"
	}
	return "custom"
}
