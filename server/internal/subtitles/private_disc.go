package subtitles

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
)

// PrivateDiscPresentation is a copied fact set, never a reader or a grant.
// The resolver must repeat the original immutable snapshot/receipt and ordinary
// active presentation authority in this same transaction on EVERY call.
type PrivateDiscPresentation struct {
	SnapshotID, SessionID, ItemID, AssetID, ObjectID, TitleKey, FactsRevision, Owner string
	Container                                                                        string
	Generation                                                                       int
	Size, ModifiedNS, DurationMS, OriginUS                                           int64
	TimingKnown                                                                      bool
}

type DiscPresentationResolver interface {
	ResolveDiscSubtitlePresentationTx(context.Context, *sql.Tx, string) (PrivateDiscPresentation, bool, error)
}

func (v PrivateDiscPresentation) source() Source {
	return Source{ID: v.AssetID, Available: true, TimingKnown: v.TimingKnown, container: v.Container,
		DurationUS: strconv.FormatInt(v.DurationMS*1000, 10), size: v.Size,
		modified: v.ModifiedNS, originUS: v.OriginUS, duration: float64(v.DurationMS) / 1000}
}

func (s *Service) privateDiscPresentationTx(ctx context.Context, tx *sql.Tx, session string) (PrivateDiscPresentation, bool, error) {
	var known bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_snapshots WHERE session_id=?)`, session).Scan(&known); err != nil {
		return PrivateDiscPresentation{}, true, err
	}
	if !known {
		return PrivateDiscPresentation{}, false, nil
	}
	if s.DiscPresentations == nil {
		return PrivateDiscPresentation{}, true, ErrUnavailable
	}
	v, private, err := s.DiscPresentations.ResolveDiscSubtitlePresentationTx(ctx, tx, session)
	if err != nil {
		return PrivateDiscPresentation{}, true, err
	}
	if !private || v.SessionID != session || v.Generation < 1 || v.Owner == "" || !validID(v.SnapshotID) || !validID(v.AssetID) || v.DurationMS <= 0 {
		return PrivateDiscPresentation{}, true, ErrConflict
	}
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_snapshots d
 JOIN playback_disc_lifecycle l ON l.snapshot_id=d.id
 JOIN playback_disc_snapshot_source source ON source.snapshot_id=d.id
 JOIN playback_disc_snapshot_disc disc ON disc.snapshot_id=d.id
 JOIN playback_disc_snapshot_media media ON media.snapshot_id=d.id
 JOIN playback_sessions session ON session.id=d.session_id AND session.generation=d.generation
 JOIN catalog_entities item ON item.id=session.item_id
 WHERE d.id=? AND d.session_id=? AND d.generation=? AND l.state='active' AND l.sealed=1
 AND pid(item.public_id)=? AND source.asset_id=? AND source.inventory_object_id=?
 AND disc.selected_title_key=? AND disc.facts_revision=? AND source.size=? AND source.modified_ns=?
 AND media.duration_ms=? AND media.origin_us=? AND media.timing_known=? AND media.container=?)`,
		v.SnapshotID, session, v.Generation, v.ItemID, v.AssetID, v.ObjectID, v.TitleKey, v.FactsRevision,
		v.Size, v.ModifiedNS, v.DurationMS, v.OriginUS, v.TimingKnown, v.Container).Scan(&current)
	if err != nil {
		return PrivateDiscPresentation{}, true, err
	}
	if !current {
		return PrivateDiscPresentation{}, true, ErrConflict
	}
	return v, true, nil
}

func (s *Service) sourceQuery(ctx context.Context, tx *sql.Tx, item, asset string) (Source, error) {
	if invocation, ok := PlaybackSourceInvocationFromContext(ctx); ok {
		facts := invocation.Facts()
		if facts.ItemID != item || facts.AssetID != asset {
			return Source{}, ErrConflict
		}
		v, private, err := s.privateDiscPresentationTx(ctx, tx, facts.SessionID)
		if err != nil {
			return Source{}, err
		}
		if private {
			if err = invocation.CheckTx(ctx, tx); err != nil {
				return Source{}, err
			}
			if v.ItemID != item || v.AssetID != asset || v.Generation != facts.SessionGeneration || v.Size != facts.SourceSize || v.ModifiedNS != facts.SourceModifiedNS {
				return Source{}, ErrConflict
			}
			return v.source(), nil
		}
	}
	return sourceQuery(ctx, tx, item, asset)
}

func (s *Service) sessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, session string) (sessionScope, error) {
	v, err := sessionTx(ctx, tx, p, item, session)
	if err != nil {
		return v, err
	}
	private, known, err := s.privateDiscPresentationTx(ctx, tx, session)
	if err != nil {
		return v, err
	}
	if known && (private.Owner != actor(p) || private.ItemID != item || private.AssetID != v.source || private.Generation != v.generation || private.Size != v.size || private.ModifiedNS != v.modified) {
		return v, ErrConflict
	}
	return v, nil
}

func (s *Service) checkDiscResourceSessionTx(ctx context.Context, tx *sql.Tx, session, resource string) error {
	v, private, err := s.privateDiscPresentationTx(ctx, tx, session)
	if err != nil {
		return err
	}
	if !private {
		return checkDiscResourceSessionTx(ctx, tx, session, resource)
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_subtitle_resources map
 JOIN subtitle_resources r ON r.id=map.resource_id
 JOIN playback_disc_snapshot_streams stream ON stream.snapshot_id=map.snapshot_id AND stream.stream_index=map.stream_index
 JOIN catalog_entities item ON item.id=r.item_id
 WHERE map.snapshot_id=? AND r.id=? AND r.source_id=? AND r.scope='personal' AND r.owner=? AND r.deleted=0
 AND pid(item.public_id)=? AND stream.type='subtitle')`, v.SnapshotID, resource, v.AssetID, v.Owner, v.ItemID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	return nil
}

func (s *Service) CheckDiscResourceSessionTx(ctx context.Context, tx *sql.Tx, session, resource string) error {
	return s.checkDiscResourceSessionTx(ctx, tx, session, resource)
}

func (s *Service) listPrivateDiscTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item string, v PrivateDiscPresentation) (Catalog, error) {
	out := Catalog{Version: 1, ItemID: item, Revision: 1, CanShare: false, Sources: []Source{}, Resources: []Resource{}, Discovered: []Discovery{}, Provider: s.ProviderStatus()}
	if v.Owner != actor(p) || v.ItemID != item {
		return out, ErrConflict
	}
	// This immutable per-snapshot collection is independent of another
	// viewer's captures or ordinary shared subtitle collection revisions.
	out.Sources = append(out.Sources, v.source())
	rows, err := tx.QueryContext(ctx, `SELECT `+resourceColumns+` FROM playback_disc_subtitle_resources map
 JOIN subtitle_resources r ON r.id=map.resource_id JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision
 WHERE map.snapshot_id=? AND r.source_id=? AND r.owner=? AND r.scope='personal' AND r.deleted=0 ORDER BY map.stream_index LIMIT 129`, v.SnapshotID, v.AssetID, v.Owner)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(out.Resources) >= MaxTracks {
			return out, ErrCapacity
		}
		r, err := scanResource(rows)
		if err != nil {
			return out, err
		}
		r.CanManage = false // Immutable measured tracks are not generic personal uploads.
		r.Enabled = r.sourceSize == v.Size && r.sourceModified == v.ModifiedNS && v.TimingKnown
		if !r.Enabled {
			r.Reason = "source_changed"
		}
		extractor, ok := s.Extractor.(DiscSubtitleExtractor)
		if !ok || !extractor.DiscSubtitleAvailable() {
			r.Enabled = false
			r.Reason = "renderer_unavailable"
		}
		out.Resources = append(out.Resources, r)
	}
	return out, rows.Err()
}

// PersistPrivateDiscResourcesTx runs AFTER PublishPendingTx sealed the graph,
// in the SAME original legacy writer. The mandatory check belongs to its
// current POST borrow, not to a worker or a row-reconstructed measurement.
func PersistPrivateDiscResourcesTx(ctx context.Context, tx *sql.Tx, p identity.Principal, v PrivateDiscPresentation, choice AutoChoice, check func(context.Context, *sql.Tx) error) error {
	if ctx == nil || tx == nil || check == nil || v.Owner != actor(p) || !validID(v.SnapshotID) {
		return ErrInput
	}
	if err := dbwork.RequireGatedWriteTx(tx); err != nil {
		return err
	}
	if err := check(ctx, tx); err != nil {
		return err
	}
	var item int64
	err := tx.QueryRowContext(ctx, `SELECT session.item_id FROM playback_disc_snapshots d
 JOIN playback_disc_lifecycle life ON life.snapshot_id=d.id
 JOIN playback_disc_snapshot_actor actor ON actor.snapshot_id=d.id
 JOIN playback_disc_snapshot_source source ON source.snapshot_id=d.id
 JOIN playback_disc_snapshot_disc disc ON disc.snapshot_id=d.id
 JOIN playback_disc_snapshot_media media ON media.snapshot_id=d.id
 JOIN playback_sessions session ON session.id=d.session_id AND session.generation=d.generation
 JOIN catalog_entities item ON item.id=session.item_id
 WHERE d.id=? AND d.session_id=? AND d.generation=? AND life.state='pending' AND life.sealed=1
 AND actor.server_id=? AND actor.authority=? AND actor.account_id=? AND actor.profile_id=? AND actor.session_hash=? AND actor.device_id=? AND actor.epoch=?
 AND pid(item.public_id)=? AND source.asset_id=? AND source.inventory_object_id=? AND source.size=? AND source.modified_ns=?
 AND disc.selected_title_key=? AND disc.facts_revision=? AND media.duration_ms=? AND media.origin_us=? AND media.timing_known=? AND media.container=?`,
		v.SnapshotID, v.SessionID, v.Generation, p.ServerID, p.Authority, p.AccountID, p.ProfileID, p.Hash, p.DeviceID, p.Epoch,
		v.ItemID, v.AssetID, v.ObjectID, v.Size, v.ModifiedNS, v.TitleKey, v.FactsRevision, v.DurationMS, v.OriginUS, v.TimingKnown, v.Container).Scan(&item)
	if err != nil {
		return err
	}
	// Derive every resource scalar from the exact sealed stream graph. The
	// caller supplies no replacement codec, language, disposition or title.
	rows, err := tx.QueryContext(ctx, `SELECT stream_index,codec,language,title,is_default,is_forced,hearing_impaired FROM playback_disc_snapshot_streams WHERE snapshot_id=? AND type='subtitle' ORDER BY stream_order LIMIT 129`, v.SnapshotID)
	if err != nil {
		return err
	}
	tracks := []Track{}
	for rows.Next() {
		var track Track
		var codec string
		if err = rows.Scan(&track.StreamIndex, &codec, &track.Language, &track.Title, &track.Default, &track.Forced, &track.HearingImpaired); err != nil {
			rows.Close()
			return err
		}
		track.Format, track.Origin = CodecFormat(codec), "embedded"
		if track.StreamIndex < 0 || !validFormat(track.Format) {
			rows.Close()
			return ErrUnsupported
		}
		tracks = append(tracks, track)
		if len(tracks) > MaxTracks || len(tracks) > medialimits.SubtitleTracks() {
			rows.Close()
			return ErrCapacity
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(tracks) > 0 && !v.TimingKnown {
		return ErrUnsupported
	}
	if err = PinSessionTx(ctx, tx, v.SessionID, v.Generation); err != nil {
		return err
	}
	candidates := []AutoCandidate{}
	for _, track := range tracks {
		if track.StreamIndex < 0 || track.Reason != "" || !validFormat(track.Format) {
			return ErrUnsupported
		}
		id := stable("private-disc-subtitle-v1:" + v.SnapshotID + ":" + strconv.Itoa(track.StreamIndex))
		language, err := Language(track.Language)
		if err != nil {
			language = "und"
		}
		title := safeTitle(track.Title)
		renderer := "external_text"
		if !textFormat(track.Format) || track.Format == "ass" || track.Format == "ssa" {
			renderer = "burn_in"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,discovery_id,discovery_revision,discovered) VALUES(?,?,?,'personal',?,1,'',1,0)`, id, item, v.AssetID, v.Owner); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) VALUES(?,1,'',0,?,?,?,'embedded','','',?,?,0,strftime('%Y-%m-%dT%H:%M:%SZ','now'))`, id, track.Format, language, title, v.Size, v.ModifiedNS); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_render_revisions(resource_id,revision,renderer,is_default,is_forced,source_evidence,hearing_impaired) VALUES(?,1,?,?,?,'',?)`, id, renderer, track.Default, track.Forced, track.HearingImpaired); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO playback_disc_subtitle_resources(snapshot_id,resource_id,stream_index) VALUES(?,?,?)`, v.SnapshotID, id, track.StreamIndex); err != nil {
			return err
		}
		if renderer == "external_text" {
			candidates = append(candidates, AutoCandidate{ID: id, Revision: 1, Language: language, Title: title, Default: track.Default, Forced: track.Forced, HearingImpaired: track.HearingImpaired})
			if _, err = tx.ExecContext(ctx, `INSERT INTO playback_manifest_subtitles(session_id,resource_id,resource_revision) VALUES(?,?,1)`, v.SessionID, id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO playback_subtitle_pins(session_id,resource_id,resource_revision) VALUES(?,?,1)`, v.SessionID, id); err != nil {
				return err
			}
		}
	}
	if selected := ChooseAutomatic(choice, candidates); selected != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE playback_subtitle_state SET resource_id=?,resource_revision=1,offset_us=0,renderer='external_text' WHERE session_id=? AND generation=?`, selected.ID, v.SessionID, v.Generation); err != nil {
			return err
		}
	}
	return check(ctx, tx)
}
