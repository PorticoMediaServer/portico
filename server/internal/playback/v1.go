package playback

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// V1Choice is what one presentation is planned from besides the file and the
// device (spec §5.1–§5.4): the version, the tracks, the quality's ceilings and
// the caller's reach. The quality is the viewer's (asked for in the request, or
// their saved default for where the request comes from); the owner's ceilings,
// the member's remote cap and the upload budget always narrow it.
type V1Choice struct {
	// AirPlay plans a separately owned native HLS rendition, never the page renderer.
	AirPlay bool
	// Process-private original POST borrow. Only Media's preparation factory
	// can supply it; it is neither a wire choice nor a recoverable row proof.
	discBorrow *discPlayBorrow
	DiscTitle  *catalog.DiscTitleRef
	// UseStoredResume is server-private intent, set only for an initial stored
	// resume. Replans, transfers and already chosen positions leave it false.
	UseStoredResume bool
	// PendingActivation hides a candidate and defers personal state until acceptance.
	PendingActivation bool
	SubtitleOff       bool // actual initial V1 request; never an authority flag
	// AssetID is the version to play; empty plays the item's first available one.
	AssetID string
	// AudioStream is the absolute source stream index the viewer chose.
	AudioStream *int
	// Limit is false for the original. The ceilings are the chosen quality's
	// (a fixed quality's own, or the client's custom limit); zero means none.
	Limit              bool
	MaxVideoBitrateBPS int
	MaxAudioBitrateBPS int
	MaxVideoHeight     int
	// QualityID names the quality the viewer is on, for the stored plan:
	// "original", a ladder id, "limit", or "automatic" with its current step.
	QualityID string
	// SubtitleKind is the kind of the subtitle the viewer chose (ass, pgs,
	// vobsub, dvb); empty for none and for text delivered beside the stream.
	SubtitleKind string
	// SubtitleBesideStream says that subtitle can be handed to the device as it
	// is beside a stream (see DeliveryInput.SubtitleBesideStream).
	SubtitleBesideStream bool
	// Engine pins the player engine (a re-plan of a playing presentation whose
	// engine must not change under the viewer); empty lets planning choose.
	Engine string
	// CarriedAudioLanguage is the audio language picked in the player earlier
	// in this playback; it is tried before the preferred languages.
	CarriedAudioLanguage string
	// AdminMaxVideoBitrateBPS is the member's remote bitrate cap from access
	// admission (spec §5.3); zero means none.
	AdminMaxVideoBitrateBPS int
	// Remote is true when the request came from outside the server's LAN.
	// Remote caps (the member's and the server-wide one) apply only then.
	Remote bool
	// PrivateFor makes this a private presentation (spec §18.2): the prepared
	// next item of the given (live) presentation, sharing its stream slot and
	// serving only its first audio-render window until released.
	PrivateFor     string
	PrivateUntilMs int64
	// AudioPlanV2 pins the version 2 audio plan (spec §18.1: the client decodes)
	// to an audio presentation, from the device's audioDecode.
	AudioServerRender bool
	AudioCDQuality    bool
	AudioPlanV2       bool
	AudioDecode       []AudioDecodeCap
}

func narrowCeiling(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0 || a < b:
		return a
	default:
		return b
	}
}

// target is the plan target for this choice: ceilings, never a forced rung, so
// a source within them is still played as original or copied.
func (c *V1Choice) target(policy ResolvedDeliveryPolicy, cfg DeliveryConfiguration) QualityTarget {
	out := QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: policy.AllowHDR}
	if c.Limit {
		out.RungID = "limit"
		out.MaxVideoBitrateBPS, out.MaxAudioBitrateBPS, out.MaxVideoHeight = c.MaxVideoBitrateBPS, c.MaxAudioBitrateBPS, c.MaxVideoHeight
	}
	if c.QualityID != "" {
		out.RungID = c.QualityID
	}
	if c.remote(policy) {
		// The server-wide limit is a remote-connection limit (delivery settings
		// adapter); a LAN viewer is never capped by it.
		out.MaxVideoBitrateBPS = narrowCeiling(narrowCeiling(out.MaxVideoBitrateBPS, c.AdminMaxVideoBitrateBPS), cfg.MaxVideoBitrateBPS)
	}
	out.MaxAudioBitrateBPS = narrowCeiling(out.MaxAudioBitrateBPS, cfg.MaxAudioBitrateBPS)
	out.MaxVideoHeight = narrowCeiling(out.MaxVideoHeight, cfg.MaxVideoHeight)
	if c.remote(policy) {
		remoteBits := narrowCeiling(c.AdminMaxVideoBitrateBPS, cfg.MaxVideoBitrateBPS)
		if step, ok := qualityladder.AtBitrate(remoteBits); ok {
			out.MaxVideoHeight = narrowCeiling(out.MaxVideoHeight, step.Height)
		}
	}
	return out
}

func (c *V1Choice) remote(policy ResolvedDeliveryPolicy) bool {
	return c.Remote || policy.NetworkClass == NetworkRemote
}

// AdminCapped reports whether the admin cap, not the client's own request, set
// the video ceiling (the decision then says admin_cap_remote_bitrate).
func (c V1Choice) AdminCapped() bool {
	if c.AdminMaxVideoBitrateBPS <= 0 || !c.Remote {
		return false
	}
	return !c.Limit || c.MaxVideoBitrateBPS <= 0 || c.AdminMaxVideoBitrateBPS < c.MaxVideoBitrateBPS
}

// selectAsset picks the version a session plays: the chosen one when it belongs
// to the item and is available, otherwise the item's best available version
// (compactcatalog.BestVersionOrder).
func (s *Service) selectAsset(ctx context.Context, item string, v1 *V1Choice) *sql.Row {
	if v1 != nil && v1.AssetID != "" {
		return dbwork.QueryRow(ctx, s.db, `SELECT a.token FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?) AND a.token=? AND a.available=1`, item, v1.AssetID)
	}
	return dbwork.QueryRow(ctx, s.db, `SELECT a.token FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?) AND a.available=1 ORDER BY `+compactcatalog.BestVersionOrder("l", "a")+` LIMIT 1`, item)
}

// SelectedAsset is the asset a v1 start of item plays (the chosen version, else
// the first available one), for work that must happen before the start (audio
// facts, spec §18.7).
func (s *Service) SelectedAsset(ctx context.Context, item string, v1 *V1Choice) (string, error) {
	var id string
	err := s.selectAsset(ctx, item, v1).Scan(&id)
	return id, err
}

// CreateV1 starts one presentation (a playback_sessions row with its own grant)
// for a v1 session. A replacement ends the previous presentation atomically and
// takes its slot, which is how a track or quality change mints a new generation.
func (s *Service) CreateV1(ctx context.Context, p identity.Principal, item, request string, choice V1Choice, replacement *SessionReplacement) (Session, error) {
	return s.create(ctx, p, item, request, replacement, &choice)
}

// PlanV1 is what CreateV1 would decide for this device, with no side effects
// (spec §4.1 `plan`). It returns the asset it planned for.
func (s *Service) PlanV1(ctx context.Context, p identity.Principal, item string, choice V1Choice) (DeliveryPlan, error) {
	var err error
	ctx, err = s.prepareDelivery(ctx)
	if err != nil {
		return DeliveryPlan{}, err
	}
	var aid string
	if e := s.selectAsset(ctx, item, &choice).Scan(&aid); e != nil {
		return DeliveryPlan{}, e
	}
	tx, done, e := dbwork.BeginRead(ctx, s.db)
	if e != nil {
		return DeliveryPlan{}, e
	}
	defer done()
	if s.AuthorityTx != nil {
		p, e = s.AuthorityTx(ctx, tx, p, item)
		if e != nil {
			return DeliveryPlan{}, e
		}
	}
	var container, video, audio string
	var duration float64
	if e = tx.QueryRow(`SELECT a.container,a.video_codec,a.audio_codec,a.duration FROM catalog_assets a WHERE a.token=?`, aid).Scan(&container, &video, &audio, &duration); e != nil {
		return DeliveryPlan{}, e
	}
	var remote *DeliverySource
	if container == "strm" {
		// A preview opens nothing: it plans from the media the server last
		// measured behind this STRM. A start measures it again.
		measured, ok, err := lastRemoteSourceTx(ctx, tx, aid)
		if err != nil {
			return DeliveryPlan{}, err
		}
		if !ok {
			// Never measured: the start decides, relayed as it is unless the
			// measured media needs a remux or a conversion.
			return DeliveryPlan{SourceID: aid, SourceContainer: "strm", Mode: "remote", Strategy: DeliveryOriginal, VideoAction: "copy", AudioAction: "copy", AudioStream: -1, Duration: duration}, nil
		}
		remote = &measured
	}
	inputs, e := s.planInputsTx(ctx, tx, p, item, aid, container, video, audio, duration, remote, true, choice.AudioStream, &choice)
	if e != nil {
		return DeliveryPlan{}, e
	}
	plan, e := planDelivery(inputs.input)
	if e != nil {
		return DeliveryPlan{}, e
	}
	if plan.Mode == "hls" && !inputs.input.Disc && s.hls != nil && s.hls.finite != nil && plan.ConversionTarget == nil {
		plan.Strategy, plan.VideoAction, plan.OutputVideoCodec = DeliveryVideoConversion, "convert", "h264"
		plan.ReasonCodes = append(plan.ReasonCodes, ReasonFiniteNormalization)
	}
	if !inputs.ownerTranscoding && (plan.AudioAction == "convert" || plan.VideoAction == "convert") {
		return plan, ErrTranscodingDisabled
	}
	return plan, nil
}

// SessionPlan is the immutable plan a presentation was created with.
func (s *Service) SessionPlan(ctx context.Context, session string) (*DeliveryPlan, error) {
	tx, done, e := dbwork.BeginRead(ctx, s.db)
	if e != nil {
		return nil, e
	}
	defer done()
	return loadDeliveryPlan(ctx, tx, session)
}

// ReadyWithin reports whether a presentation can be played now, waiting at most
// d. Production continues in the background either way (it runs on the
// service's context, not the request's), so false means "ask again".
func (s *Service) ReadyWithin(ctx context.Context, out Session, d time.Duration) (bool, error) {
	wait, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	err := s.Ready(wait, out)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return false, nil
	}
	return false, err
}

// StopV1 ends one presentation for the v1 layer, which has already decided who
// may stop it (the session's device, an admin, a lease expiry, a transfer
// commit). The grant is refused from the next media request on (invariant 4).
// StopFencedV1 releases production after its media row was already stopped in the
// caller's transaction. Commit callbacks must not acquire the writer gate again.
func (s *Service) StopFencedV1(id string) { s.stopHLS(id) }

func (s *Service) StopV1(ctx context.Context, id string) error {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	if err = terminateControlOccurrence(ctx, gated.Tx(), id, "stopped"); err != nil {
		return err
	}
	if err = gated.Commit(); err != nil {
		return err
	}
	s.stopHLS(id)
	return nil
}

// ReleasePrivateTx makes a private presentation (spec §18.3) an ordinary one
// at commit: its grant then serves the media and every render window, and it
// takes the stream slot its owner leaves. It fails when the preparation has
// expired, converting is no longer allowed, or the source changed.
func (s *Service) ReleasePrivateTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) error {
	var until int64
	var owner, item string
	if err := tx.QueryRowContext(ctx, `SELECT pp.expires_ms,pp.owner_session_id,pid(e.public_id) FROM playback_private_presentations pp JOIN playback_sessions ps ON ps.id=pp.session_id JOIN catalog_entities e ON e.id=ps.item_id WHERE pp.session_id=? AND ps.account_id=? AND ps.profile_id=?`, id, p.AccountID, p.ProfileID).Scan(&until, &owner, &item); err != nil {
		return identity.ErrUnauthorized
	}
	if until <= time.Now().UnixMilli() {
		return ErrPreparationExpired
	}
	// Converting must still be allowed only for a converted plan: a direct one
	// (the client decodes the original file, spec §18.1) converts nothing.
	converted := true
	var generation int
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM playback_sessions WHERE id=?`, id).Scan(&generation); err != nil {
		return err
	}
	if plan, err := readAudioPlan(tx, id, generation); err != nil {
		return err
	} else if plan != nil && plan.Mode == "direct" {
		converted = false
	}
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&allowed); err != nil {
		return err
	}
	if converted && !allowed {
		return ErrTranscodingDisabled
	}
	var changed int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_sessions ps JOIN playback_source_pins pin ON pin.session_id=ps.id JOIN catalog_assets a ON a.token=pin.asset_id WHERE ps.id=? AND (a.size<>pin.size OR a.modified_ns<>pin.modified_ns OR a.available=0)`, id).Scan(&changed); err != nil {
		return err
	}
	if changed != 0 {
		return ErrSourceChanged
	}
	ordinal, err := inheritedPersonalIntent(tx, p, owner)
	if err != nil {
		return err
	}
	if err = activatePersonalWriter(tx, p, item, id, 0, ordinal); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM playback_private_presentations WHERE session_id=?`, id)
	return err
}

var (
	// ErrPreparationExpired: a private presentation past its expiry.
	ErrPreparationExpired = errors.New("preparation expired")
	// ErrSourceChanged: the file behind a presentation changed since it was planned.
	ErrSourceChanged = errors.New("source changed")
)

// PublishPresentationTx exposes a validated candidate and activates its progress
// writer in the same transaction as the resource pointing to it. Replacements
// retire the old grant only here; a rejected start never takes progress authority.
func (s *Service) PublishPresentationTx(ctx context.Context, tx *sql.Tx, p identity.Principal, next string, positionMs int64, old *SessionReplacement, inherit bool) error {
	var item string
	query := `SELECT pid(e.public_id) FROM playback_private_presentations pp JOIN playback_sessions n ON n.id=pp.session_id JOIN catalog_entities e ON e.id=n.item_id`
	args := []any{next, p.AccountID, p.ProfileID, time.Now().UnixMilli()}
	if old != nil {
		query += ` JOIN playback_sessions o ON o.id=pp.owner_session_id`
	}
	query += ` WHERE n.id=? AND n.account_id=? AND n.profile_id=? AND n.state NOT IN('stopped','ended','failed') AND pp.expires_ms>?`
	if old == nil {
		query += ` AND pp.owner_session_id=''`
	} else {
		query += ` AND o.id=? AND o.generation=? AND o.account_id=? AND o.profile_id=? AND o.state NOT IN('stopped','ended','failed')`
		args = append(args, old.ID, old.Generation, p.AccountID, p.ProfileID)
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&item); err != nil {
		return err
	}
	if s.AuthorityTx != nil {
		if _, err := s.AuthorityTx(ctx, tx, p, item); err != nil {
			return err
		}
	}
	var ordinal int64
	var err error
	if inherit && old != nil {
		ordinal, err = inheritedPersonalIntent(tx, p, old.ID)
	} else {
		ordinal, err = reservePersonalIntent(tx, "playback:"+next)
	}
	if err != nil {
		return err
	}
	if err = activatePersonalWriter(tx, p, item, next, float64(positionMs)/1000, ordinal); err != nil {
		return err
	}
	if old != nil {
		if err = terminateControlOccurrence(ctx, tx, old.ID, "stopped"); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM playback_private_presentations WHERE session_id=?`, next)
	return err
}
