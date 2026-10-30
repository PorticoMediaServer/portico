package downloads

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// source is everything downloads needs to know about the one file behind an
// item: where it is, how big it is, what it contains and whether the library
// can currently see it.
type source struct {
	AudioLossless                                         *bool
	Item, Library, Kind                                   string
	Asset, Path                                           string
	Size, ModifiedNS                                      int64
	Container                                             string
	VideoCodec                                            string
	AudioCodec                                            string
	Height                                                int
	VideoBitrateBPS                                       int64
	Duration                                              float64
	Available                                             bool
	Parts                                                 int
	AudioRate, AudioDepth, AudioChannels, AudioBitrateBPS int
	Cue                                                   bool
}

func audioKind(kind string) bool { return kind == "song" || kind == "audiobook_file" }

// resolveSource reads the file a download of this item is a download of.
//
// A title may have several files in two different ways. Several files at the
// same place in the title are versions of it (a 4K copy and a 1080p copy):
// one of them is the download, the best version that is there, by the same
// rule playback uses when no version is named. Several files at different
// places are parts of one title (a film in two files): there is no single
// file to hand over, so the title is refused with a reason that says so. This
// is the one place that choice is made.
func resolveSource(ctx context.Context, tx *sql.Tx, item string) (source, error) {
	return resolveVersionSource(ctx, tx, item, "")
}
func resolveVersionSource(ctx context.Context, tx *sql.Tx, item, version string) (source, error) {
	var s source
	var available int
	var kindInt int
	var start float64
	var end *float64
	e := tx.QueryRowContext(ctx, `SELECT cl.library_id,e.kind,a.token,a.path,a.size,a.modified_ns,a.container,a.video_codec,a.audio_codec,a.height,a.duration,link.available,link.start_seconds,link.end_seconds,
 (SELECT count(DISTINCT p.part_index) FROM catalog_asset_links p WHERE p.entity_id=e.id)
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_asset_links link ON link.entity_id=e.id JOIN catalog_assets a ON a.id=link.asset_id
 WHERE e.public_id=pid_blob(?) AND (?='' OR a.token=?) ORDER BY `+compactcatalog.BestVersionOrder("link", "a")+` LIMIT 1`, item, version, version).Scan(&s.Library, &kindInt, &s.Asset, &s.Path, &s.Size, &s.ModifiedNS, &s.Container, &s.VideoCodec, &s.AudioCodec, &s.Height, &s.Duration, &available, &start, &end, &s.Parts)
	if e != nil {
		return s, e
	}
	s.Kind, _ = compactcatalog.Kind(kindInt).Name()
	s.Item, s.Available = item, available != 0
	if s.Kind == "song" {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM music_virtual_tracks v JOIN catalog_entities e ON e.id=v.entity_id WHERE v.asset_id=? AND e.public_id=pid_blob(?) AND v.start_seconds=? AND v.end_seconds=?)`, s.Asset, item, start, end).Scan(&s.Cue); err != nil {
			return s, err
		}
		if s.Cue && end != nil {
			s.Duration = *end - start
		}
	}
	if s.Duration > 0 {
		s.VideoBitrateBPS = int64(float64(s.Size) * 8 / s.Duration)
	}
	if facts, err := playback.DeliverySourceTx(ctx, tx, s.Asset); err == nil {
		if len(facts.Audio) > 0 {
			a := facts.Audio[0]
			s.AudioRate = a.SampleRate
			s.AudioDepth = a.BitDepth
			s.AudioChannels = a.Channels
			s.AudioCodec = a.Codec
			s.AudioBitrateBPS = int(a.BitRate)
		}
		if facts.Video != nil {
			if facts.Video.Height > 0 {
				s.Height = facts.Video.Height
			}
			if facts.Video.BitRate > 0 {
				s.VideoBitrateBPS = facts.Video.BitRate
			}
		}
	}
	if s.Kind == "song" {
		s.AudioLossless = assets.KnownLosslessAudio(s.AudioCodec)
		var known *bool
		err := tx.QueryRowContext(ctx, `SELECT lossless FROM music_technical_facts WHERE asset_id=? AND size=? AND modified_ns=?`, s.Asset, s.Size, s.ModifiedNS).Scan(&known)
		if err == nil {
			s.AudioLossless = known
		} else if !errors.Is(err, sql.ErrNoRows) {
			return s, err
		}
		if f, ok, err := audiofacts.Read(ctx, tx, s.Asset); err != nil {
			return s, err
		} else if ok {
			s.AudioRate, s.AudioDepth, s.AudioChannels = f.SampleRate, f.BitDepth, f.Channels
			if f.BitRate > 0 {
				s.AudioBitrateBPS = int(f.BitRate)
			}
			if s.AudioBitrateBPS == 0 && f.DurationFrames > 0 {
				s.AudioBitrateBPS = int(float64(s.Size) * 8 * float64(f.SampleRate) / float64(f.DurationFrames))
			}
		}
	}
	return s, nil
}

// sourceReason turns a resolution outcome into a published reason code.
func sourceReason(s source, e error) string {
	switch {
	case errors.Is(e, sql.ErrNoRows):
		return ReasonItemDeleted
	case e != nil:
		return ReasonFailed
	case s.Parts != 1:
		return ReasonSeveralFiles
	case !s.Available || s.Size <= 0:
		return ReasonSourceMissing
	}
	return ""
}

// qualityReason says why a quality cannot be had for this file, or "" when it
// can. A converted quality is real when a finished copy of it already exists,
// or when the conversion code reports it can make one; a rung that is only a
// name in the ladder is refused before a claim that could only fail is made.
func qualityReason(ctx context.Context, tx *sql.Tx, quality, recipe string, found source) string {
	if readingKind(found.Kind) && quality != QualityOriginal {
		return ReasonNotOptimized
	}
	if quality == QualityOriginal && !found.Cue || sameAsSource(quality, found) {
		return ""
	}
	// A recipe's audio path depends on this source's own tracks, so the
	// quality is judged with the same channels the encoder will read.
	channels, haveChannels := []int(nil), false
	if facts, err := playback.DeliverySourceTx(ctx, tx, found.Asset); err == nil {
		channels, haveChannels = sourceChannels(facts), true
	}
	producible := func(id string) bool {
		if id == "" {
			return false
		}
		if haveChannels {
			return preparedmedia.ProducibleForSource(id, channels)
		}
		return preparedmedia.Producible(id)
	}
	if found.Kind == "song" && canonicalMusicQuality(quality) {
		if producible(recipe) {
			return ""
		}
		if versions, e := selectionDownloadVersions(ctx, tx, found, recipe); e == nil && selectableVersionForSource(versions, recipe, found.Asset) != nil {
			return ""
		}
		return ReasonNotOptimized
	}
	if quality == "automatic" && recipe != "" {
		if producible(recipe) {
			return ""
		}
		if versions, e := selectionDownloadVersions(ctx, tx, found, recipe); e == nil && selectableVersionForSource(versions, recipe, found.Asset) != nil {
			return ""
		}
		return ReasonNotOptimized
	}
	_, ok := ladderRung(quality)
	if !ok {
		return ReasonNotOptimized
	}
	if audioKind(found.Kind) {
		return ReasonNotOptimized
	}
	if recipe == "" {
		return ReasonNotOptimized
	}
	if producible(recipe) {
		return ""
	}
	versions, e := selectionDownloadVersions(ctx, tx, found, recipe)
	if e == nil && selectableVersionForSource(versions, recipe, found.Asset) != nil {
		return ""
	}
	return ReasonNotOptimized
}

// preparedProfile maps a published quality ladder rung onto the immutable
// prepared-media recipe that can satisfy it. The ladder is a delivery vocabulary
// and the recipes are an encoder vocabulary; downloads is the only place the two
// have to meet, so the mapping lives here and is published in the API document.
//
//	audio item                → audio-aac-v3
//	video rung               → portable-{height}-v3
func preparedProfile(kind string, rung playback.QualityPreset) string {
	if audioKind(kind) {
		return "audio-aac-v3"
	}
	return "portable-" + rung.ID + "-v3"
}

// estimateBytes is bitrate times duration, and the caller publishes it as an
// estimate. Only the original's size is exact before the artifact exists.
func estimateBytes(quality, id string, s source) (int64, bool) {
	if quality == QualityOriginal && !s.Cue || sameAsSource(quality, s) {
		return s.Size, false
	}
	rung, ok := playback.LadderPreset(quality)
	if (!ok && id == "") || s.Duration <= 0 || math.IsNaN(s.Duration) || math.IsInf(s.Duration, 0) {
		return 0, true
	}
	if id == "" {
		id = preparedProfile(s.Kind, rung)
	}
	recipe, err := preparedmedia.ProfileByID(id)
	if err != nil {
		return 0, true
	}
	bits := float64((recipe.VideoKbps + recipe.AudioKbps) * 1000)
	if _, c, ok := preparedmedia.MusicProfile(id); ok && c == "flac" {
		bits = float64(s.AudioRate*s.AudioDepth*s.AudioChannels) * 0.7
	}
	// Container overhead is real and small; 3% keeps the estimate honest about
	// erring high rather than promising a client less than it will receive.
	estimate := int64(bits / 8 * s.Duration * 1.03)
	if estimate < 1 {
		estimate = 1
	}
	return estimate, true
}

// artifactDelivery names the bytes for the client's filesystem. The extension
// follows the container, and the file name carries the preparation id so two
// downloads of one item at different qualities never collide on disk.
func artifactDelivery(container, preparation string) (string, string) {
	kind := "application/octet-stream"
	extension := container
	switch container {
	case "mp4", "m4v":
		kind, extension = "video/mp4", "mp4"
	case "m4a", "m4b":
		kind, extension = "audio/mp4", container
	case "mkv":
		kind, extension = "video/x-matroska", "mkv"
	case "webm":
		kind, extension = "video/webm", "webm"
	case "epub":
		kind, extension = "application/epub+zip", "epub"
	case "pdf":
		kind, extension = "application/pdf", "pdf"
	case "cbz":
		kind, extension = "application/vnd.comicbook+zip", "cbz"
	case "cb7":
		kind, extension = "application/x-7z-compressed", "cb7"
	case "cbr":
		kind, extension = "application/vnd.comicbook-rar", "cbr"
	case "mp3":
		kind, extension = "audio/mpeg", "mp3"
	case "flac":
		kind, extension = "audio/flac", "flac"
	case "aac":
		kind, extension = "audio/aac", "aac"
	case "ogg", "opus":
		kind, extension = "audio/ogg", container
	case "wav":
		kind, extension = "audio/wav", "wav"
	case "":
		extension = "bin"
	}
	if extension == "" {
		extension = "bin"
	}
	// path.Base, never filepath.Base: this is a download file name in a URL and
	// a header, not a path on this server's filesystem.
	return kind, path.Base(preparation) + "." + extension
}

// expandTargets resolves one request into the ordered item list it names.
// Exactly one target form is accepted; naming two is a client bug, not a merge.
// A container (show, season, album, book, playlist) is not a target here: it is
// a durable request (POST /v1/downloads/requests) resolved in keyset pages with
// no size limit (PERF-S23), so this synchronous batch never truncates one.
func expandTargets(ctx context.Context, tx *sql.Tx, r Request) ([]string, string, error) {
	forms := 0
	origin := ""
	if r.MediaID != "" {
		forms, origin = forms+1, OriginItem
	}
	if len(r.MediaIDs) > 0 {
		forms, origin = forms+1, OriginItems
	}
	if r.NextAfterMediaID != "" {
		forms, origin = forms+1, OriginNext
	}
	if forms != 1 {
		return nil, "", ErrInput
	}
	switch origin {
	case OriginItem:
		if !validID.MatchString(r.MediaID) {
			return nil, "", ErrInput
		}
		return []string{r.MediaID}, origin, nil
	case OriginItems:
		if len(r.MediaIDs) > MaxBatchTargets {
			return nil, "", ErrInput
		}
		seen := map[string]bool{}
		out := []string{}
		for _, id := range r.MediaIDs {
			if !validID.MatchString(id) {
				return nil, "", ErrInput
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
		if len(out) == 0 {
			return nil, "", ErrInput
		}
		return out, origin, nil
	default:
		if !validID.MatchString(r.NextAfterMediaID) {
			return nil, "", ErrInput
		}
		out, e := nextEpisode(ctx, tx, r.NextAfterMediaID)
		return out, origin, e
	}
}

// nextEpisode is the one episode that follows the named one: the next number in
// the same season, else the first episode of the next season of the same show.
// Absolute numbering is followed on its own axis; dated episodes follow air
// date and entity id within their show. It answers one item, never a tail, so
// an auto-next download stays one episode ahead rather than silently pulling
// a season.
func nextEpisode(ctx context.Context, tx *sql.Tx, after string) ([]string, error) {
	var show, numbering string
	var season sql.NullString
	var number int
	var airDate sql.NullInt64
	var entity int64
	e := tx.QueryRowContext(ctx, `SELECT pid(show.public_id),e.numbering,pid(season.public_id),e.number,e.air_date,e.entity_id FROM catalog_entities item JOIN catalog_episodes e ON e.entity_id=item.id JOIN catalog_entities show ON show.id=e.show_id LEFT JOIN catalog_entities season ON season.id=e.season_id WHERE item.public_id=pid_blob(?)`, after).Scan(&show, &numbering, &season, &number, &airDate, &entity)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	var next string
	if numbering == "absolute" {
		e = tx.QueryRowContext(ctx, `SELECT pid(item.public_id) FROM catalog_entities show JOIN catalog_episodes e ON e.show_id=show.id JOIN catalog_entities item ON item.id=e.entity_id WHERE show.public_id=pid_blob(?) AND e.numbering='absolute' AND e.number>? ORDER BY e.number LIMIT 1`, show, number).Scan(&next)
	} else if numbering == "date" {
		if !airDate.Valid {
			return nil, ErrNotFound
		}
		e = tx.QueryRowContext(ctx, `SELECT pid(item.public_id) FROM catalog_entities show JOIN catalog_episodes e ON e.show_id=show.id JOIN catalog_entities item ON item.id=e.entity_id WHERE show.public_id=pid_blob(?) AND e.numbering='date' AND (e.air_date>? OR (e.air_date=? AND e.entity_id>?)) ORDER BY e.air_date,e.entity_id LIMIT 1`, show, airDate.Int64, airDate.Int64, entity).Scan(&next)
	} else {
		e = tx.QueryRowContext(ctx, `SELECT pid(item.public_id) FROM catalog_entities season CROSS JOIN catalog_episodes e INDEXED BY catalog_episodes_season ON e.season_id=season.id CROSS JOIN catalog_entities item ON item.id=e.entity_id WHERE season.public_id=pid_blob(?) AND e.numbering='seasonal' AND e.number>? ORDER BY e.number LIMIT 1`, season.String, number).Scan(&next)
		if errors.Is(e, sql.ErrNoRows) {
			// Resolve the unique Show before its indexed children, independent of
			// which additional episode indexes a fresh catalog installs.
			e = tx.QueryRowContext(ctx, `SELECT pid(item.public_id) FROM catalog_entities show CROSS JOIN catalog_episodes e INDEXED BY catalog_episodes_show ON e.show_id=show.id CROSS JOIN catalog_seasons s ON s.entity_id=e.season_id CROSS JOIN catalog_entities item ON item.id=e.entity_id
 WHERE show.public_id=pid_blob(?) AND e.numbering='seasonal' AND s.number>(SELECT next.number FROM catalog_seasons next JOIN catalog_entities current ON current.id=next.entity_id WHERE current.public_id=pid_blob(?))
 ORDER BY s.number,e.number LIMIT 1`, show, season.String).Scan(&next)
		}
	}
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	return []string{next}, nil
}

// libraryOf is used by the routes layer's per-item authorization check.
func libraryOf(ctx context.Context, tx *sql.Tx, item string) (string, error) {
	var library string
	e := tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&library)
	return library, e
}

// qualityLabel renders a rung for a human-facing list without a client having to
// keep its own table of ladder names.
func qualityLabel(quality string) string {
	if quality == QualityOriginal {
		return "Original"
	}
	if quality == "cd-quality" {
		return "CD quality"
	}
	if quality == "320" || quality == "96" {
		return quality + " kbps"
	}
	if quality == "automatic" {
		return "Automatic"
	}
	if rung, ok := playback.LadderPreset(quality); ok {
		return rung.Label
	}
	return strings.ToUpper(quality)
}

// chooseRecipe is the prepared recipe a rung resolves to for this device and
// this source. The HEVC recipe is chosen when it keeps something the H.264 one
// would give up (HDR, ten bits, surround sound, or an HEVC or AV1 original)
// and the device plays its result; otherwise the H.264 recipe, which is quicker
// to make and plays everywhere. "" for the original.
func chooseRecipe(ctx context.Context, tx *sql.Tx, device playback.CapabilityDocument, quality string, found source, configuration ...playback.DeliveryConfiguration) string {
	if found.Kind == "song" && canonicalMusicQuality(quality) {
		return musicRecipeFor(device, quality, found)
	}
	if quality == "automatic" {
		if audioKind(found.Kind) {
			if recipePlayable(ctx, tx, device, "audio-aac-v3", found) {
				return "audio-aac-v3"
			}
			return ""
		}
		for _, p := range playback.QualityLadder {
			if found.Height > 0 && p.Height <= found.Height {
				return chooseRecipe(ctx, tx, device, p.ID, found, configuration...)
			}
		}
		return ""
	}
	rung, ok := ladderRung(quality)
	if quality == QualityOriginal || sameAsSource(quality, found) || !ok {
		return ""
	}
	plain := preparedProfile(found.Kind, rung)
	if !recipePlayable(ctx, tx, device, plain, found) {
		plain = ""
	}
	rich := preparedmedia.RichProfileFor(rung.ID)
	if audioKind(found.Kind) || rich == "" || found.Asset == "" || len(configuration) > 0 && configuration[0].OutputFormat == "h264" {
		return plain
	}
	facts, err := playback.DeliverySourceTx(ctx, tx, found.Asset)
	if err != nil || facts.Video == nil || !richGains(facts) {
		return plain
	}
	if !preparedmedia.ProducibleForSource(rich, sourceChannels(facts)) {
		versions, e := selectionDownloadVersions(ctx, tx, found, rich)
		if e != nil || selectableVersionForSource(versions, rich, found.Asset) == nil {
			return plain
		}
	}
	recipe, err := preparedmedia.ProfileByID(rich)
	if err != nil || !playback.DownloadPlayable(richTarget(facts, recipe), device) {
		return plain
	}
	return rich
}

// richGains reports whether the HEVC recipe keeps something of this source the
// H.264 recipe would lose.
func richGains(f playback.DeliverySource) bool {
	v := f.Video
	if v.Codec == "hevc" || v.Codec == "av1" || v.BitDepth >= 10 || richRange(v) != "sdr" {
		return true
	}
	for _, a := range f.Audio {
		if a.Channels > 2 {
			return true
		}
	}
	return false
}

// richRange is the range an HEVC recipe writes for the source: HDR10 or HLG
// kept (a Dolby Vision picture through its base layer), SDR otherwise.
func richRange(v *playback.SourceVideo) string {
	r := v.DynamicRange
	if r == assets.RangeDolbyVision {
		r = decoder.DolbyVisionBaseRange(v.DolbyVisionProfile, v.DolbyVisionCompatibility)
	}
	if r == assets.RangeHDR10 || r == assets.RangeHLG {
		return r
	}
	return assets.RangeSDR
}

// richTarget describes the file an HEVC recipe makes from this source, for the
// device's own planner to judge.
func richTarget(f playback.DeliverySource, recipe preparedmedia.Profile) playback.DeliverySource {
	v := f.Video
	r := richRange(v)
	depth := 8
	if r != assets.RangeSDR || v.BitDepth >= 10 {
		depth = 10
	}
	width, height := v.Width, v.Height
	if width <= 0 || height <= 0 {
		width, height = recipe.Width, recipe.Height
	}
	if scale := math.Min(1, math.Min(float64(recipe.Width)/float64(width), float64(recipe.Height)/float64(height))); scale < 1 {
		width, height = int(float64(width)*scale)/2*2, int(float64(height)*scale)/2*2
	}
	rate := v.FrameRate
	if rate <= 0 || rate > 60 {
		rate = 60
	}
	profile := "Main"
	if depth == 10 {
		profile = "Main 10"
	}
	out := playback.DeliverySource{Container: "mp4", Video: &playback.SourceVideo{Codec: "hevc", StreamDetail: assets.StreamDetail{Profile: profile, Level: decoder.HEVCOutputLevel(width, height, rate), BitDepth: depth, Width: width, Height: height, FrameRate: rate, DynamicRange: r, CodecTag: "hvc1"}}}
	channels := make([]int, 0, len(f.Audio))
	for _, a := range f.Audio {
		channels = append(channels, a.Channels)
	}
	codec := decoder.PreparedAudioCodec(true, channels)
	for i, a := range f.Audio {
		out.Audio = append(out.Audio, playback.SourceAudio{Index: i + 1, Ordinal: i, Codec: codec, Channels: decoder.PreparedChannelsRich(true, a.Channels), Default: a.Default, StreamDetail: assets.StreamDetail{SampleRate: 48000}})
	}
	return out
}

// sourceChannels is every audio track's channel count in stream order.
func sourceChannels(f playback.DeliverySource) []int {
	out := make([]int, 0, len(f.Audio))
	for _, a := range f.Audio {
		out = append(out, a.Channels)
	}
	return out
}

// sameAsSource is an evidenced partial ordering, never a resolution guess.
func sameAsSource(quality string, s source) bool {
	if s.Kind == "song" {
		return musicSourceEquivalent(quality, s)
	}
	r, ok := playback.LadderPreset(quality)
	return ok && !audioKind(s.Kind) && r.SameAsSource(s.Height, s.VideoBitrateBPS)
}

// resolvedQuality is frozen before admission. A source-equivalent step stores
// an original-file claim, so neither the worker nor a retry can create work.
func resolvedQuality(ctx context.Context, tx *sql.Tx, device playback.CapabilityDocument, quality string, s source) string {
	if readingKind(s.Kind) {
		return QualityOriginal
	}
	if sameAsSource(quality, s) {
		return QualityOriginal
	}
	if quality != "automatic" {
		return quality
	}
	facts, err := playback.DeliverySourceTx(ctx, tx, s.Asset)
	if err != nil || playback.DownloadPlayable(facts, device) {
		return QualityOriginal
	}
	if audioKind(s.Kind) {
		return "automatic"
	}
	for _, r := range playback.QualityLadder {
		if s.Height > 0 && r.Height <= s.Height && !sameAsSource(r.ID, s) {
			recipe := chooseRecipe(ctx, tx, device, r.ID, s)
			if recipe != "" && recipePlayable(ctx, tx, device, recipe, s) {
				return r.ID
			}
		}
	}

	// Unknown dimensions cannot justify inventing a conversion size.
	return QualityOriginal
}

// recipePlayable shares the exact prepared-output description between the
// option projection and admission. Device codec limits cannot be bypassed by
// posting a row that was disabled in the menu.
func recipePlayable(ctx context.Context, tx *sql.Tx, device playback.CapabilityDocument, id string, s source) bool {
	if _, _, ok := preparedmedia.MusicProfile(id); ok {
		target, valid := musicTarget(id, s)
		return valid && playback.DownloadPlayable(target, device)
	}
	r, err := preparedmedia.ProfileByID(id)
	if err != nil {
		return false
	}
	target := playback.DeliverySource{Container: "mp4", Audio: []playback.SourceAudio{{Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{SampleRate: 48000}}}}
	if audioKind(s.Kind) {
		return playback.DownloadPlayable(target, device)
	}
	facts, err := playback.DeliverySourceTx(ctx, tx, s.Asset)
	if err == nil && facts.Video != nil && facts.Video.DynamicRange == assets.RangeDolbyVision && decoder.DolbyVisionBaseRange(facts.Video.DolbyVisionProfile, facts.Video.DolbyVisionCompatibility) == "" {
		return false
	}
	if preparedmedia.Rich(id) {
		return err == nil && facts.Video != nil && playback.DownloadPlayable(richTarget(facts, r), device)
	}
	target.Video = &playback.SourceVideo{Codec: "h264", StreamDetail: assets.StreamDetail{Profile: "High", Level: decoder.H264OutputLevel(r.Width, r.Height, 60), Width: r.Width, Height: r.Height, FrameRate: 60, BitDepth: 8, DynamicRange: "sdr"}}
	return playback.DownloadPlayable(target, device)
}
