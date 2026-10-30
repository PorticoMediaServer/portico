package downloads

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
)

// SourceDescription is the untouched file behind an item: what a client gets if
// it downloads `original`.
type SourceDescription struct {
	Container       string  `json:"container"`
	VideoCodec      string  `json:"videoCodec,omitempty"`
	AudioCodec      string  `json:"audioCodec,omitempty"`
	Height          int     `json:"height,omitempty"`
	DurationSeconds float64 `json:"durationSeconds"`
	Bytes           int64   `json:"bytes"`
	Available       bool    `json:"available"`
}

// QualityOption is one thing a client may ask to download. estimatedBytes is
// exact for the original and for a rung that is already prepared; everywhere
// else it is bitrate times duration and `estimated` says so.
type QualityOption struct {
	Quality            string  `json:"quality"`
	Label              string  `json:"label"`
	Kind               string  `json:"kind"`
	TargetHeight       int     `json:"targetDisplayHeight,omitempty"`
	MaxVideoBitrateBPS int     `json:"maxVideoBitrateBps,omitempty"`
	MaxAudioBitrateBPS int     `json:"maxAudioBitrateBps,omitempty"`
	Bytes              int64   `json:"estimatedBytes"`
	Estimated          bool    `json:"estimated"`
	Available          bool    `json:"available"`
	Reason             string  `json:"reason,omitempty"`
	RequiresPrepare    bool    `json:"requiresPreparation"`
	PreparedProfileID  string  `json:"preparedProfileId,omitempty"`
	PreparedVersionID  string  `json:"preparedVersionId,omitempty"`
	ExistingID         string  `json:"preparationId,omitempty"`
	ExistingState      string  `json:"preparationState,omitempty"`
	SameAsOriginal     bool    `json:"sameAsOriginal"`
	DataGBPerHour      float64 `json:"dataGBPerHour,omitempty"`
	EffectiveQuality   string  `json:"effectiveQuality"`
}

// DownloadPolicy is what this profile is allowed to do, published next to the
// options so a client renders a disabled control with a reason rather than
// discovering the refusal on submit.
type DownloadPolicy struct {
	AllowDownloads bool   `json:"allowDownloads"`
	Reason         string `json:"reason,omitempty"`
}

// DownloadStorage is how much more the server is willing to prepare.
type DownloadStorage struct {
	MaxPreparedBytes int64  `json:"maxPreparedBytes"`
	CommittedBytes   int64  `json:"committedBytes"`
	RemainingBytes   *int64 `json:"remainingBytes"`
	RetentionDays    int    `json:"retentionDays"`
}

// DownloadOptionsView answers one item's download menu in a single read: the
// source, every rung with its estimate, the policy and the storage headroom. A
// client needs no second request to render the sheet.
type DownloadOptionsView struct {
	DefaultQuality string            `json:"defaultQuality"`
	ItemID         string            `json:"itemId"`
	Kind           string            `json:"kind"`
	Source         SourceDescription `json:"source"`
	Options        []QualityOption   `json:"options"`
	Policy         DownloadPolicy    `json:"policy"`
	Storage        DownloadStorage   `json:"storage"`
}

// Options publishes the complete canonical menu for one item. Source-equivalent
// steps retain the original; other steps need a playable retained or producible copy.
// owner says whether this viewer can order the conversion a rung needs; a
// viewer who cannot sees the rung as unavailable rather than being allowed to
// queue work that will never start.
func (s *Service) Options(ctx context.Context, p identity.Principal, item string, owner bool) (DownloadOptionsView, error) {
	return s.OptionsVersion(ctx, p, item, "", owner)
}
func (s *Service) OptionsVersion(ctx context.Context, p identity.Principal, item, version string, owner bool) (DownloadOptionsView, error) {
	out := DownloadOptionsView{ItemID: item, Options: []QualityOption{}}
	if !validID.MatchString(item) || version != "" && !validID.MatchString(version) {
		return out, ErrInput
	}
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer gated.Rollback()
	out, e = s.optionsVersionTx(ctx, gated.Tx(), p, item, version, owner)
	if e != nil {
		return out, e
	}
	return out, gated.Commit()
}

func (s *Service) optionsTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item string, owner bool) (DownloadOptionsView, error) {
	return s.optionsVersionTx(ctx, tx, p, item, "", owner)
}
func (s *Service) optionsVersionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, version string, owner bool) (DownloadOptionsView, error) {
	out := DownloadOptionsView{ItemID: item, Options: []QualityOption{}}
	var e error
	if e = s.visibleItem(ctx, tx, p, item); e != nil {
		if errors.Is(e, identity.ErrContentRestricted) {
			return out, ErrNotFound
		}
		return out, e
	}
	allowed, e := ProfileAllowsDownloads(tx, p.Viewer)
	if e != nil {
		return out, e
	}
	out.Policy = DownloadPolicy{AllowDownloads: allowed}
	if !allowed {
		out.Policy.Reason = ReasonNotAllowed
	}
	settings, e := readSettings(tx)
	if e != nil {
		return out, e
	}
	var committed sql.NullInt64
	if e = tx.QueryRowContext(ctx, `SELECT sum(bytes_total) FROM download_preparations WHERE state IN `+storedClaims).Scan(&committed); e != nil {
		return out, e
	}
	out.Storage = DownloadStorage{MaxPreparedBytes: settings.MaxPreparedBytes, CommittedBytes: committed.Int64, RetentionDays: settings.RetentionDays}
	if settings.MaxPreparedBytes > 0 {
		remaining := settings.MaxPreparedBytes - committed.Int64
		if remaining < 0 {
			remaining = 0
		}
		out.Storage.RemainingBytes = &remaining
	}
	found, err := resolveVersionSource(ctx, tx, item, version)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Kind = found.Kind
	if readingKind(found.Kind) {
		out.DefaultQuality = QualityOriginal
		reader := s.readingService()
		available := allowed && reader != nil
		if available {
			selected, selectionError := reader.SelectOfflineTx(ctx, tx, p, item, found.Asset)
			available = selectionError == nil
			if available {
				found.Container = selected.Format
			}
		}
		out.Source = SourceDescription{Container: found.Container, Bytes: found.Size, Available: available}
		option := QualityOption{Quality: QualityOriginal, Label: "Original", Kind: "source", Bytes: found.Size, Available: available, EffectiveQuality: QualityOriginal}
		if !available {
			option.Reason = ReasonSourceMissing
		}
		if !allowed {
			option.Reason = ReasonNotAllowed
		}
		out.Options = []QualityOption{option}
		return out, nil
	}
	out.Source = SourceDescription{Container: found.Container, VideoCodec: found.VideoCodec, AudioCodec: found.AudioCodec, Height: found.Height, DurationSeconds: found.Duration, Bytes: found.Size, Available: sourceReason(found, nil) == ""}
	transcoding := false
	if e = tx.QueryRowContext(ctx, `SELECT transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&transcoding); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if audioKind(found.Kind) {
		transcoding = s.deliveryConfig().AudioConversionAllowed(transcoding)
	} else {
		transcoding = s.deliveryConfig().VideoConversionAllowed(transcoding)
	}
	versions, e := preparedmedia.VersionsTx(ctx, tx, item)
	if e != nil {
		return out, e
	}
	live := map[string][2]string{}
	rows, e := tx.QueryContext(ctx, `SELECT quality,id,state FROM download_preparations WHERE profile_key=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND state IN `+committedStates, operations.ViewerKey(p), item)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var quality, id, state string
		if e = rows.Scan(&quality, &id, &state); e != nil {
			rows.Close()
			return out, e
		}
		live[quality] = [2]string{id, state}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}

	original := QualityOption{Quality: QualityOriginal, Label: "Original", Kind: "source", Bytes: found.Size, Available: out.Source.Available && allowed, TargetHeight: found.Height}
	if !out.Source.Available {
		original.Reason = sourceReason(found, nil)
		if original.Reason == "" {
			original.Reason = ReasonSourceMissing
		}
	} else if !allowed {
		original.Reason = ReasonNotAllowed
	}
	out.DefaultQuality, e = requestedDownloadQuality(ctx, tx, p, "", found)
	if e != nil {
		return out, e
	}
	if !s.withinCeiling(QualityOriginal, found) {
		original.Available, original.Reason = false, ReasonDownloadCeiling
	}
	original.EffectiveQuality = "original"
	if found.Kind == "song" {
		out.Options = s.musicOptions(ctx, tx, p, found, original, versions, live, owner, transcoding)
		return out, nil
	}
	out.Options = append(out.Options, decorate(original, live))
	// A recipe's audio path depends on this source's own tracks. Load them once
	// so the options list judges producibility exactly as admission does in
	// qualityReason: a 5.1 source under the rich recipe is written through AAC,
	// and only an unknown or wider source needs the FLAC carrier. A Dolby Vision
	// picture with no base layer (profile 5) has nothing a prepared copy could
	// show, so its optimized steps are not offered rather than offered and failed.
	sourceAudio, sourceAudioKnown := []int(nil), false
	noBaseLayer := false
	if !audioKind(found.Kind) && found.Asset != "" {
		if facts, err := playback.DeliverySourceTx(ctx, tx, found.Asset); err == nil {
			sourceAudio, sourceAudioKnown = sourceChannels(facts), true
			if facts.Video != nil && facts.Video.DynamicRange == assets.RangeDolbyVision {
				noBaseLayer = decoder.DolbyVisionBaseRange(facts.Video.DolbyVisionProfile, facts.Video.DolbyVisionCompatibility) == ""
			}
		}
	}
	producible := func(id string) bool {
		if id == "" {
			return false
		}
		if sourceAudioKnown {
			return preparedmedia.ProducibleForSource(id, sourceAudio)
		}
		return preparedmedia.Producible(id)
	}
	automatic := original
	automatic.Available = out.Source.Available && allowed
	if automatic.Available {
		automatic.Reason = ""
	}
	automatic.Quality = "automatic"
	automatic.Label = "Automatic"
	effective := s.resolveQuality(ctx, tx, playback.CapabilityDocumentTx(ctx, tx, p), "automatic", found)
	automatic.EffectiveQuality = effective
	if effective != "original" {
		automatic.Kind = "optimized"
		automatic.PreparedProfileID = chooseRecipe(ctx, tx, playback.CapabilityDocumentTx(ctx, tx, p), effective, found, s.deliveryConfig())
		automatic.Bytes, automatic.Estimated = estimateBytes(effective, automatic.PreparedProfileID, found)
		if recipe, err := preparedmedia.ProfileByID(automatic.PreparedProfileID); err == nil {
			automatic.MaxAudioBitrateBPS = recipe.AudioKbps * 1000
			automatic.TargetHeight = recipe.Height
			automatic.MaxVideoBitrateBPS = recipe.VideoKbps * 1000
		}
		if version := selectableVersionForSource(versions, automatic.PreparedProfileID, found.Asset); version != nil {
			automatic.PreparedVersionID = version.ID
			automatic.Bytes = version.Size
			automatic.Estimated = false
		} else if automatic.Available {
			switch {
			case !transcoding:
				automatic.Available = false
				automatic.Reason = playback.RungReasonTranscodeOff
			case owner && producible(automatic.PreparedProfileID):
				automatic.RequiresPrepare = true
			default:
				automatic.Available = false
				automatic.Reason = ReasonNotOptimized
			}
		}
	}

	out.Options = append(out.Options, decorate(automatic, live))

	// Audio uses Original or an actual Automatic audio recipe. The video steps
	// remain visible as inapplicable, without pretending to describe audio quality.
	device := playback.CapabilityDocumentTx(ctx, tx, p)
	presets := playback.QualityLadder
	for _, preset := range presets {
		if !audioKind(found.Kind) && !s.deliveryConfig().DownloadQualityAllowed(preset.ID) {
			out.Options = append(out.Options, QualityOption{Quality: preset.ID, Label: preset.Label, Kind: "optimized", TargetHeight: preset.Height, MaxVideoBitrateBPS: preset.VideoBitrateBPS, Reason: ReasonDownloadCeiling})
			continue
		}
		if audioKind(found.Kind) {
			out.Options = append(out.Options, QualityOption{Quality: preset.ID, Label: preset.Label, Kind: "optimized", TargetHeight: preset.Height, MaxVideoBitrateBPS: preset.VideoBitrateBPS, DataGBPerHour: preset.DataGBPerHour, Reason: "not_video"})
			continue
		}
		if sameAsSource(preset.ID, found) {
			option := original
			option.Quality = preset.ID
			option.Label = preset.Label
			option.SameAsOriginal = true
			option.DataGBPerHour = preset.DataGBPerHour
			option.TargetHeight = preset.Height
			option.MaxVideoBitrateBPS = preset.VideoBitrateBPS
			out.Options = append(out.Options, decorate(option, live))
			continue
		}
		option := QualityOption{
			Quality: preset.ID, Label: preset.Label, Kind: "optimized", EffectiveQuality: preset.ID, DataGBPerHour: preset.DataGBPerHour,
			TargetHeight: preset.Height, MaxVideoBitrateBPS: preset.VideoBitrateBPS, MaxAudioBitrateBPS: preset.MaxAudioBitrateBPS,
			PreparedProfileID: chooseRecipe(ctx, tx, device, preset.ID, found, s.deliveryConfig()),
		}
		option.Bytes, option.Estimated = estimateBytes(preset.ID, option.PreparedProfileID, found)
		version := selectableVersionForSource(versions, option.PreparedProfileID, found.Asset)
		recipe, recipeErr := preparedmedia.ProfileByID(option.PreparedProfileID)
		// Finished versions remain usable without an available encoder. New
		// work must pass the same recipe and inventory checks as the producer.
		if recipeErr != nil || version == nil && (noBaseLayer || !producible(option.PreparedProfileID)) {
			option.Reason = ReasonNotOptimized
			out.Options = append(out.Options, decorate(option, live))
			continue
		}
		// Rich recipes retain surround audio at their actual encoder budget.
		option.MaxAudioBitrateBPS = recipe.AudioKbps * 1000
		targetSource := playback.DeliverySource{Container: "mp4", Audio: []playback.SourceAudio{{Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{SampleRate: 48000}}}}
		if !audioKind(found.Kind) && preparedmedia.Rich(option.PreparedProfileID) {
			// The device's planner already took the HEVC recipe's result.
			targetSource = playback.DeliverySource{}
		} else if !audioKind(found.Kind) {
			targetSource.Video = &playback.SourceVideo{Codec: "h264", StreamDetail: assets.StreamDetail{Profile: "High", Level: decoder.H264OutputLevel(recipe.Width, recipe.Height, 60), Width: recipe.Width, Height: recipe.Height, FrameRate: 60, BitDepth: 8, DynamicRange: "sdr"}}
		}
		if !preparedmedia.Rich(option.PreparedProfileID) && !playback.DownloadPlayable(targetSource, device) {
			option.Reason = ReasonNotOptimized
			out.Options = append(out.Options, decorate(option, live))
			continue
		}
		switch {
		case !out.Source.Available:
			option.Reason = original.Reason
		case !allowed:
			option.Reason = ReasonNotAllowed
		case version != nil:
			option.Available, option.Estimated, option.Bytes = true, false, version.Size
			option.PreparedVersionID = version.ID
		case !transcoding:
			option.Reason = playback.RungReasonTranscodeOff
		case owner:
			option.Available, option.RequiresPrepare = true, true
		default:
			option.Reason = ReasonNotOptimized
		}
		out.Options = append(out.Options, decorate(option, live))
	}
	return out, nil
}

func decorate(option QualityOption, live map[string][2]string) QualityOption {
	if entry, ok := live[option.Quality]; ok {
		option.ExistingID, option.ExistingState = entry[0], entry[1]
	}
	return option
}

func selectableVersion(versions []preparedmedia.Version, profile string) *preparedmedia.Version {
	for i := range versions {
		v := &versions[i]
		if v.ProfileID == profile && v.Selectable && validDigest.MatchString(v.Digest) {
			return v
		}
	}
	return nil
}

// ladderRung resolves a published rung id.
func ladderRung(quality string) (playback.QualityPreset, bool) { return playback.LadderPreset(quality) }

func selectableVersionForSource(versions []preparedmedia.Version, profile, source string) *preparedmedia.Version {
	selected := []preparedmedia.Version{}
	for _, v := range versions {
		if v.SourceID == source {
			selected = append(selected, v)
		}
	}
	return selectableVersion(selected, profile)
}

// ContainerOptionsRequest reads exactly the members a container download would
// admit. It creates no preparation or durable request.
type ContainerOptionsRequest struct {
	Target   ContainerTarget `json:"target"`
	DeviceID string          `json:"deviceId"`
	Policy   EpisodePolicy   `json:"policy"`
}
type ContainerQualityOption struct {
	Quality            string  `json:"quality"`
	DataGBPerHour      float64 `json:"dataGBPerHour,omitempty"`
	MaxVideoBitrateBPS int     `json:"maxVideoBitrateBps,omitempty"`
	SameAsOriginal     bool    `json:"sameAsOriginal"`
	Label              string  `json:"label"`
	EstimatedBytes     int64   `json:"estimatedBytes"`
	Estimated          bool    `json:"estimated"`
	Available          bool    `json:"available"`
	UnavailableCount   int     `json:"unavailableCount"`
	UnknownSizeCount   int     `json:"unknownSizeCount"`
}
type ContainerOptionsView struct {
	Target  ContainerTarget          `json:"target"`
	Total   int                      `json:"total"`
	Options []ContainerQualityOption `json:"options"`
}

// ContainerOptions aggregates every selected member within one database
// snapshot. Keyset batches keep memory bounded independently of season size;
// each member uses the same policy, recipe and exact/prepared-size producer as
// the one-title sheet. An incomplete estimate explicitly names unknown sizes.
func (s *Service) ContainerOptions(ctx context.Context, p identity.Principal, in ContainerOptionsRequest, owner bool) (ContainerOptionsView, error) {
	out := ContainerOptionsView{Target: in.Target, Options: []ContainerQualityOption{}}
	normalized := ContainerRequest{OperationID: "options", Target: in.Target, DeviceID: in.DeviceID, Quality: QualityOriginal, Policy: in.Policy}
	if err := normalizeContainerRequest(&normalized); err != nil {
		return out, err
	}
	in.Policy = normalized.Policy
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer snapshot.Rollback()
	tx := snapshot.Tx()
	visible, args, err := s.requestAccess(ctx, tx, p, in.DeviceID)
	if err != nil {
		return out, err
	}
	if _, err = requestFence(ctx, tx, p, in.Target, in.Policy.Episodes, visible, args); err != nil {
		return out, err
	}
	query, bound := requestSelection(in.Target)
	query += ` AND (` + visible + `) AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements d WHERE d.item_id=i.id)`
	bound = append(bound, args...)
	if in.Policy.Episodes != "all" {
		query += ` AND NOT (` + personalstate.SQL("?", "i.id") + `)`
		bound = append(bound, identity.PersonalKey(p.Viewer))
	}
	byQuality := map[string]int{}
	coverage := map[string]int{}
	key, after := "", ""
	for {
		limit := requestBatchSize
		if in.Policy.Episodes == "next" {
			limit = min(limit, in.Policy.KeepNext-out.Total)
			if limit <= 0 {
				break
			}
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,position FROM (`+query+`) WHERE (position,id)>(?,?) ORDER BY position,id LIMIT ?`, append(append([]any{}, bound...), key, after, limit)...)
		if e != nil {
			return out, e
		}
		type member struct{ id, key string }
		batch := []member{}
		for rows.Next() {
			var m member
			if e = rows.Scan(&m.id, &m.key); e != nil {
				rows.Close()
				return out, e
			}
			batch = append(batch, m)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if len(batch) == 0 {
			break
		}
		observeRequestBatch(ctx, "options", len(batch))
		for _, m := range batch {
			options, e := s.optionsTx(ctx, tx, p, m.id, owner)
			if e != nil {
				return out, e
			}
			out.Total++
			key, after = m.key, m.id
			for _, option := range options.Options {
				index, exists := byQuality[option.Quality]
				if !exists {
					index = len(out.Options)
					byQuality[option.Quality] = index
					out.Options = append(out.Options, ContainerQualityOption{Quality: option.Quality, Label: option.Label, Available: true, DataGBPerHour: option.DataGBPerHour, MaxVideoBitrateBPS: option.MaxVideoBitrateBPS, SameAsOriginal: true})
				}
				sum := &out.Options[index]
				coverage[option.Quality]++
				sum.SameAsOriginal = sum.SameAsOriginal && option.SameAsOriginal
				sum.EstimatedBytes += option.Bytes
				sum.Estimated = sum.Estimated || option.Estimated
				if option.Bytes <= 0 {
					sum.UnknownSizeCount++
				}
				if !option.Available {
					sum.UnavailableCount++
				}
			}
		}
	}
	if out.Total == 0 {
		return out, ErrNotFound
	}
	for i := range out.Options {
		q := &out.Options[i]
		missing := out.Total - coverage[q.Quality]
		q.UnavailableCount += missing
		q.UnknownSizeCount += missing
		q.Available = q.UnavailableCount == 0
	}
	// Do not retain any per-title credential, path or preparation identity.
	return out, snapshot.Commit()
}
