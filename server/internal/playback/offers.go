package playback

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"time"
)

type offerLoginKey struct{}

// WithOfferLogin names the login the request itself authenticated with, for a
// session whose ownership the caller has already decided another way (a v1
// presentation: its authority, account and profile). Offers then judge the
// session live by that login instead of the token binding the media session
// last recorded, so a menu opened right after a token renewal, before the next
// timeline report rebinds the session, still offers subtitles.
func WithOfferLogin(ctx context.Context, hash string) context.Context {
	return context.WithValue(ctx, offerLoginKey{}, hash)
}

var ErrStaleOffer = errors.New("Playback source facts changed. Refresh the available options.")

func (s *Service) Offers(ctx context.Context, p identity.Principal, scope OffersScope, session, expected string) (Offers, error) {
	out := Offers{Scope: scope, Sources: []SourceOffer{}, Controls: []OfferControl{{"source", false, "source_selection_unavailable"}, {"audio", false, "track_selection_unavailable"}, {"subtitles", false, "subtitle_delivery_unavailable"}, {"quality", false, "quality_selection_unavailable"}}}
	// Offers only read: one read snapshot, no write gate, so a scan or any
	// other write never delays the player's options (NEW-38).
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var kindNum int64
	if e = tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?)`, scope.ItemID).Scan(&kindNum); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	itemKind, _ := compactcatalog.Kind(kindNum).Name()
	// Facts are written synchronously now; the item's assets, links, stream
	// facts and boundaries need no readiness wait (the deleted
	// CheckItemReadiness only guarded the old projection).
	rows, e := tx.QueryContext(ctx, `SELECT a.token,a.path,a.available,a.container,a.video_codec,a.audio_codec,a.width,a.height,a.duration,COALESCE(f.revision,0),CASE WHEN f.asset_id IS NULL THEN 'unavailable' WHEN f.size!=a.size OR f.modified_ns!=a.modified_ns THEN 'stale' ELSE 'known' END,COALESCE(b.status,'whole_source'),a.size,a.modified_ns,l.part_index,l.start_seconds,COALESCE(l.end_seconds,-1)
 FROM catalog_entities item JOIN catalog_asset_links l ON l.entity_id=item.id JOIN catalog_assets a ON a.id=l.asset_id
 LEFT JOIN asset_stream_facts f ON f.asset_id=a.token LEFT JOIN episode_asset_boundaries b ON b.item_id=item.id AND b.asset_id=a.token
 WHERE item.public_id=pid_blob(?) ORDER BY l.part_index,a.token`, scope.ItemID)
	if e != nil {
		return out, e
	}
	identities := []any{}
	indexes := map[string]int{}
	for rows.Next() {
		var o SourceOffer
		var path string
		var size, modified int64
		var part int
		var start, end float64
		if e = rows.Scan(&o.ID, &path, &o.Available, &o.Container, &o.VideoCodec, &o.AudioCodec, &o.Width, &o.Height, &o.Duration, &o.FactsRevision, &o.FactsStatus, &o.Boundary, &size, &modified, &part, &start, &end); e != nil {
			rows.Close()
			return out, e
		}
		if o.Available && s.StorageGuard != nil && s.StorageGuard(path) != nil {
			o.Available = false
		}
		o.Relation = "unknown"
		o.Reason = "source_selection_unavailable"
		if !o.Available {
			o.Reason = "source_unavailable"
		}
		o.Streams = []OfferedStream{}
		o.Qualities = []QualityRung{}
		indexes[o.ID] = len(out.Sources)
		out.Sources = append(out.Sources, o)
		identities = append(identities, []any{o.ID, size, modified, part, start, end})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT f.asset_id,f.stream_index,f.type,f.codec,f.language,f.title,f.channels,f.channel_layout,f.is_default,f.is_forced FROM catalog_entities item JOIN catalog_asset_links l ON l.entity_id=item.id JOIN catalog_assets a ON a.id=l.asset_id JOIN asset_streams f ON f.asset_id=a.token WHERE item.public_id=pid_blob(?) ORDER BY f.asset_id,f.stream_index`, scope.ItemID)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var id string
		var st OfferedStream
		if e = rows.Scan(&id, &st.Index, &st.Type, &st.Codec, &st.Language, &st.Title, &st.Channels, &st.ChannelLayout, &st.Default, &st.Forced); e != nil {
			rows.Close()
			return out, e
		}
		index, ok := indexes[id]
		if !ok {
			rows.Close()
			return out, ErrStaleOffer
		}
		o := &out.Sources[index]
		if o.FactsStatus != "known" {
			continue
		}
		st.Reason = "track_selection_unavailable"
		switch st.Type {
		case "subtitle":
			st.Reason = "subtitle_delivery_unavailable"
		case "audio":
			// An audio track is chosen by asking for playback with its stream index
			// (`audioStream` on the play command, or an explicit audio track on an
			// intent). The server picks the delivery that reaches it.
			st.Enabled, st.Reason = true, ""
		}
		o.Streams = append(o.Streams, st)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	// One policy resolution serves the whole offer: every source is published
	// against the same network lane, so a client can compare rungs honestly.
	cfg := deliveryConfiguration(s.deliverySettings)
	edge := DeliveryContextFrom(ctx)
	policy := s.resolvePolicy(tx, p, edge, cfg)
	ceiling, err := memberAwayCeilingsTx(ctx, tx, p.AccountID, edge.NetworkClass == NetworkRemote)
	if err != nil {
		return out, err
	}
	policy.MaxVideoBitrateBPS = narrowCeiling(policy.MaxVideoBitrateBPS, ceiling.MaxVideoBitrateBPS)
	policy.MaxVideoHeight = narrowCeiling(policy.MaxVideoHeight, ceiling.MaxVideoHeight)
	out.DeliveryPolicy = &policy
	if e = tx.QueryRowContext(ctx, `SELECT transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&out.TranscodingOpen); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	legacyEnabled := out.TranscodingOpen
	out.TranscodingOpen = cfg.AudioConversionAllowed(legacyEnabled)
	for i := range out.Sources {
		source := &out.Sources[i]
		facts, _ := DeliverySourceTx(ctx, tx, source.ID)
		rate := facts.BitRate
		if facts.Video != nil && facts.Video.BitRate > 0 {
			rate = facts.Video.BitRate
		}
		if audioOnlyKind(itemKind) {
			source.Qualities = MusicQualityOffers(itemKind == "song" || itemKind == "track", source.Available && source.FactsStatus == "known", out.TranscodingOpen, rate)
			continue
		}
		source.Qualities = QualityOffers(source.Height, source.Available && source.FactsStatus == "known", cfg.VideoConversionAllowed(legacyEnabled), policy, rate)
	}

	out.PreparedVersions = []preparedmedia.Version{}
	if s.Prepared != nil {
		out.PreparedVersions, e = preparedmedia.VersionsTx(ctx, tx, scope.ItemID)
		if e != nil {
			return out, e
		}
	}
	out.PreparedOffersRevision = preparedmedia.OffersRevision(scope.ItemID, out.PreparedVersions)
	for _, v := range out.PreparedVersions {
		if v.Selectable {
			out.Controls[0] = OfferControl{ID: "source", Enabled: true, Reason: ""}
			break
		}
	}
	for _, source := range out.Sources {
		for _, rung := range source.Qualities {
			if rung.Enabled && rung.Kind != QualityAutomatic {
				out.Controls[3] = OfferControl{ID: "quality", Enabled: true, Reason: ""}
				break
			}
		}
	}
	if session != "" {
		c := &CurrentOffer{}
		var mode string
		e = tx.QueryRowContext(ctx, `SELECT id,generation,state,asset_id,mode FROM playback_sessions WHERE id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND session_hash=? AND account_id=? AND profile_id=?`, session, scope.ItemID, p.Hash, p.AccountID, p.ProfileID).Scan(&c.SessionID, &c.Generation, &c.State, &c.SourceID, &mode)
		if e != nil {
			return out, e
		}
		if _, ok := indexes[c.SourceID]; !ok {
			return out, ErrStaleOffer
		}
		c.Delivery = mode
		if mode == "remote" {
			c.Delivery = "direct"
		}
		c.AudioSelection = "platform_default"
		c.SubtitleSelection = "platform_default"
		if mode == "hls" {
			c.AudioSelection = "server_first_audio"
			c.SubtitleSelection = "disabled"
		}
		_ = tx.QueryRowContext(ctx, `SELECT version_id FROM prepared_media_session_pins WHERE session_id=?`, session).Scan(&c.PreparedVersionID)
		c.QualityID = "original"
		if plan, planErr := loadDeliveryPlan(ctx, tx, session); planErr == nil && plan != nil && plan.QualityID != "" {
			c.QualityID = plan.QualityID
		}
		c.QualityID = currentRungID(c.QualityID, out.Sources[indexes[c.SourceID]].Qualities)
		out.Current = c
		// A prepared derivative is not the original marker clock, for the same
		// reason it is not the original chapter clock. Publish no segments rather
		// than seek positions measured against different bytes.
		if s.MarkersTx != nil && c.PreparedVersionID == "" {
			markers, markerErr := s.MarkersTx(ctx, tx, p, scope, c.SourceID)
			if markerErr != nil {
				return out, markerErr
			}
			if markers.SourceID == c.SourceID {
				out.Markers = &markers
			}
		}
		out.DeliveryPlan, e = loadDeliveryPlan(ctx, tx, session)
		if e != nil {
			return out, e
		}
		var eligible bool
		login, _ := ctx.Value(offerLoginKey{}).(string)
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions s JOIN authorization_access login ON login.hash=COALESCE(NULLIF(?,''),s.session_hash) AND login.account_id=s.account_id AND login.profile_id=s.profile_id JOIN playback_source_pins pin ON pin.session_id=s.id JOIN catalog_assets a ON a.token=pin.asset_id WHERE s.id=? AND s.state NOT IN ('stopped','ended','failed') AND s.expires_at>? AND login.revoked=0 AND login.expires_at>strftime('%Y-%m-%dT%H:%M:%SZ','now') AND (login.authority!='local' OR EXISTS(SELECT 1 FROM accounts ac WHERE ac.id=login.account_id AND ac.epoch=login.epoch)) AND (EXISTS(SELECT 1 FROM prepared_media_session_pins pp WHERE pp.session_id=s.id) OR (a.available=1 AND a.size=pin.size AND a.modified_ns=pin.modified_ns)))`, login, session, time.Now().UTC().Format(time.RFC3339)).Scan(&eligible); err != nil {
			return out, err
		}
		subReason := "subtitle_delivery_unavailable"
		if c.PreparedVersionID != "" {
			subReason = "prepared_subtitle_mapping_unavailable"
		}
		if s.Subtitles != nil && eligible && c.PreparedVersionID == "" {
			sub, subErr := s.Subtitles.PlanTx(ctx, tx, p, scope.ItemID, session)
			if subErr != nil && !errors.Is(subErr, subtitles.ErrUnsupported) {
				return out, subErr
			}
			if subErr == nil {
				out.SubtitlePlan = &sub
				subReason = ""
				c.SubtitleSelection = sub.Mode
				out.Controls[2] = OfferControl{ID: "subtitles", Enabled: true, Reason: ""}
			}
		} else if !eligible {
			subReason = "session_inactive"
		}
		if subReason != "" {
			out.SubtitlePlanUnavailableReason = &subReason
		}

	}
	// The rung set has its own revision so a quality selection can be fenced
	// without refusing every other offer change.
	rungSource := ""
	var rungs []QualityRung
	if len(out.Sources) > 0 {
		rungSource, rungs = out.Sources[0].ID, out.Sources[0].Qualities
	}
	out.OffersRevision = OffersRevision(rungSource, rungs, policy)
	raw, _ := json.Marshal([]any{scope, out.Sources, identities, out.SubtitlePlan, out.PreparedVersions, out.PreparedOffersRevision, out.DeliveryPolicy, out.OffersRevision})
	digest := sha256.Sum256(raw)
	out.Revision = hex.EncodeToString(digest[:])
	if expected != "" && expected != out.Revision {
		return out, ErrStaleOffer
	}
	return out, gated.Commit()
}
