package playback

// Channels on v1 sessions (spec §18.6, option B; Plan — Client Playback
// Migration §10, B3). ChannelSessions is the linear runtime's authority for a
// channel that plays as an ordinary v1 session: the same work, buffer,
// producer, media and status contract the /v2 occurrences give the runtime
// (control_linear_work.go), over playback_channel_sessions. The v1 session's
// lease (renewed by the timeline) is the channel's lease; the viewer's
// authorization family fences it (a sign-out or revocation ends the media); the
// producers, the timeshift buffer and the tuner locks are the runtime's, as
// before. No occurrence, controller, lane or receipt exists for these.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type ChannelSessions struct {
	db       *sql.DB
	families ControlFamilyAuthority
	now      func() time.Time
	linear   LinearResolver
	delivery LinearDelivery
}

func NewChannelSessions(db *sql.DB, families ControlFamilyAuthority) (*ChannelSessions, error) {
	if db == nil || families == nil {
		return nil, errors.New("channel sessions require a database and family authority")
	}
	return &ChannelSessions{db: db, families: families, now: time.Now}, nil
}

// ConfigureLinear is the composition root's wiring (the runtime's resolver and
// its delivery), before Start.
func (c *ChannelSessions) ConfigureLinear(resolver LinearResolver, delivery LinearDelivery) error {
	if resolver == nil || delivery == nil || c.linear != nil {
		return errors.New("invalid channel session configuration")
	}
	c.linear, c.delivery = resolver, delivery
	return nil
}

// Configured reports whether channels can play here at all.
func (c *ChannelSessions) Configured() bool { return c != nil && c.linear != nil && c.delivery != nil }

// ChannelStart is what StartTx records: the v1 session it belongs to and what the
// viewer asked for.
type ChannelStart struct {
	SessionID string
	Channel   LinearReference
	State     string
	Profile   ClientProfile
	// MPVProfile and EngineSetting are the device's second engine, when it
	// plays channels, and the viewer's player setting (LinearEngines).
	MPVProfile    *ClientProfile
	EngineSetting string
	// LeaseUntil is the v1 session's lease end (the tuner binding follows it).
	LeaseUntil time.Time
}

// StartTx selects the channel's current source for the viewer and records the
// channel state of a new v1 session, in the caller's transaction (the v1 row
// exists already). Refusals are the resolver's (not_found for a channel the
// viewer can't see, SEC-02; the runtime's availability).
func (c *ChannelSessions) StartTx(ctx context.Context, tx *sql.Tx, p identity.Principal, in ChannelStart) (LinearSelection, error) {
	if !c.Configured() {
		return LinearSelection{}, controlFault("channel_runtime_unavailable", 503)
	}
	if !validLinearReference(in.Channel) || !controlOneOf(in.State, "playing", "paused") {
		return LinearSelection{}, errControlJSON
	}
	family, err := c.families.SessionFamilyTx(ctx, tx, p)
	if err != nil {
		return LinearSelection{}, err
	}
	now := c.now()
	selected, err := c.linear.ResolveTx(ctx, tx, p, in.Channel, "", now)
	if err != nil {
		return LinearSelection{}, err
	}
	selection, _ := json.Marshal(selected)
	transport, _ := json.Marshal(LinearDesired{State: in.State})
	profile, _ := json.Marshal(in.Profile)
	mpv := []byte{}
	if in.MPVProfile != nil {
		mpv, _ = json.Marshal(in.MPVProfile)
	}
	setting := in.EngineSetting
	if !controlOneOf(setting, EngineSettingAutomatic, EngineSettingNative, EngineSettingMPV) {
		setting = EngineSettingAutomatic
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO playback_channel_sessions(session_id,authority,account_id,profile_id,server_id,role,account_epoch,family_id,kind,source_id,channel_id,selection_json,transport_json,client_profile_json,mpv_profile_json,engine_setting,created_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.SessionID, p.Authority, p.AccountID, p.ProfileID, p.ServerID, p.Role, p.Epoch, family.ID, selected.Reference.Kind, selected.Reference.SourceID, selected.Reference.ChannelID, string(selection), string(transport), string(profile), string(mpv), setting, now.UnixMilli()); err != nil {
		return LinearSelection{}, err
	}
	if err = c.linear.BindTx(ctx, tx, in.SessionID, selected, in.LeaseUntil); err != nil {
		return LinearSelection{}, err
	}
	return selected, nil
}

// EndTx ends a channel session's linear work (the v1 session ended: stopped,
// replaced, terminated, its lease lapsed). The runtime retires its producer on
// its next look, and media stops at once (the work is no longer valid).
func (c *ChannelSessions) EndTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	r, err := tx.ExecContext(ctx, `UPDATE playback_channel_sessions SET ended_ms=? WHERE session_id=? AND ended_ms=0`, c.now().UnixMilli(), id)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// IsChannelSession reports whether id is a v1 channel session's (for the
// runtime's dispatch while /v2 occurrences still play too).
func (c *ChannelSessions) IsChannelSession(ctx context.Context, id string) bool {
	var one int
	return c != nil && c.db.QueryRowContext(ctx, `SELECT 1 FROM playback_channel_sessions WHERE session_id=?`, id).Scan(&one) == nil
}

func (c *ChannelSessions) linearWorkTx(ctx context.Context, tx *sql.Tx, id string, checkCurrentSelection bool) (LinearWork, error) {
	w := LinearWork{PlaybackID: id}
	var selection, transport, profile string
	var ended, v1Ended, lease int64
	var mpvProfile string
	err := tx.QueryRowContext(ctx, `SELECT c.authority,c.account_id,c.profile_id,c.server_id,c.role,c.account_epoch,c.family_id,c.selection_json,c.transport_json,c.client_profile_json,c.mpv_profile_json,c.engine_setting,
 c.source_revision,c.retry_revision,c.buffer_ordinal,c.producer_ordinal,c.ended_ms,v.ended_ms,v.lease_expires_ms,p.revision,p.transcoding_enabled
 FROM playback_channel_sessions c JOIN playback_v1_sessions v ON v.id=c.session_id JOIN playback_owner_policy p ON p.singleton=1 WHERE c.session_id=?`, id).Scan(
		&w.Principal.Authority, &w.Principal.AccountID, &w.Principal.ProfileID, &w.Principal.ServerID, &w.Principal.Role, &w.Principal.Epoch, &w.FamilyID, &selection, &transport, &profile, &mpvProfile, &w.EngineSetting,
		&w.SourceRevision, &w.RetryRevision, &w.BufferOrdinal, &w.ProducerOrdinal, &ended, &v1Ended, &lease, &w.PolicyRevision, &w.TranscodingEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return w, controlFault("not_found", 404)
	}
	if err != nil {
		return w, err
	}
	now := c.now().UnixMilli()
	if ended != 0 || v1Ended != 0 || lease <= now {
		return w, controlFault("lease_expired", 410)
	}
	f, err := c.families.FamilyAuthorityTx(ctx, tx, w.FamilyID, w.Principal)
	if err != nil {
		return w, err
	}
	if f.ID != w.FamilyID || !f.AuthorizationHorizon.After(c.now()) {
		return w, identity.ErrUnauthorized
	}
	// One lease and one authority for the session's life: the runtime restarts
	// work only when the owner's policy changes, as with an occurrence.
	w.LeaseGeneration, w.LeaseAuthorityRevision = 1, 1
	w.DirectInput = true
	w.LeaseUntil = time.UnixMilli(min(lease, f.AuthorizationHorizon.UnixMilli()))
	w.ClientProfile = BaselineClientProfile()
	if profile != "" {
		if parsed, err := ParseClientProfile([]byte(profile)); err == nil {
			w.ClientProfile = parsed
		}
	}
	if mpvProfile != "" {
		if parsed, err := ParseClientProfile([]byte(mpvProfile)); err == nil && parsed.Engine == EngineMPV && parsed.LiveChannels {
			w.MPVProfile = &parsed
		}
	}
	if err = json.Unmarshal([]byte(selection), &w.Selection); err != nil {
		return w, err
	}
	if err = json.Unmarshal([]byte(transport), &w.Desired); err != nil {
		return w, err
	}
	w.MemberCeilings, err = memberChannelCeilingsTx(ctx, tx, w.Principal.AccountID, id)
	if err != nil {
		return w, err
	}
	w.ClientProfile = memberChannelProfile(w.ClientProfile, w.MemberCeilings)
	if w.MPVProfile != nil {
		narrowed := memberChannelProfile(*w.MPVProfile, w.MemberCeilings)
		w.MPVProfile = &narrowed
	}
	if w.MemberCeilings.MaxVideoHeight > 0 {
		height := w.MemberCeilings.MaxVideoHeight
		if w.Selection.Quality.MaxHeight != nil {
			height = narrowCeiling(height, *w.Selection.Quality.MaxHeight)
		}
		w.Selection.Quality.MaxHeight = &height
	}
	if c.linear == nil {
		return w, controlFault("channel_runtime_unavailable", 503)
	}
	if checkCurrentSelection {
		if err = c.linear.CheckTx(ctx, tx, w.Principal, w.Selection); err != nil {
			return w, err
		}
	}
	return w, nil
}

func (c *ChannelSessions) withLinearWork(ctx context.Context, id string, write bool, use func(*sql.Tx, LinearWork) error) error {
	var gated *dbwork.Write
	var err error
	if write {
		gated, err = dbwork.Begin(ctx, c.db, dbwork.ClassEstablishedPlayback)
	} else {
		gated, err = dbwork.BeginSnapshot(ctx, c.db)
	}
	if err != nil {
		return err
	}
	defer gated.Rollback()
	w, err := c.linearWorkTx(ctx, gated.Tx(), id, true)
	if err != nil {
		return err
	}
	if err = use(gated.Tx(), w); err != nil {
		return err
	}
	return gated.Commit()
}

// The runtime seam (LinearAuthority), as control_linear_work.go gives it for occurrences.

func (c *ChannelSessions) WithLinearWorkTx(ctx context.Context, id string, use func(*sql.Tx, LinearWork) error) error {
	return c.withLinearWork(ctx, id, false, use)
}
func (c *ChannelSessions) WithLinearWriteTx(ctx context.Context, id string, use func(*sql.Tx, LinearWork) error) error {
	return c.withLinearWork(ctx, id, true, use)
}
func (c *ChannelSessions) LinearWork(ctx context.Context, id string) (LinearWork, error) {
	var out LinearWork
	err := c.WithLinearWorkTx(ctx, id, func(_ *sql.Tx, w LinearWork) error { out = w; return nil })
	return out, err
}
func (c *ChannelSessions) LinearWorkIDs(ctx context.Context, after string, limit int) ([]string, error) {
	if limit < 1 || limit > 128 {
		return nil, errControlJSON
	}
	rows, err := c.db.QueryContext(ctx, `SELECT session_id FROM playback_channel_sessions INDEXED BY playback_channel_sessions_live WHERE ended_ms=0 AND session_id>? ORDER BY session_id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (c *ChannelSessions) BeginLinearBuffer(ctx context.Context, id string) (LinearWork, error) {
	var out LinearWork
	err := c.WithLinearWriteTx(ctx, id, func(tx *sql.Tx, w LinearWork) error {
		if w.BufferOrdinal == 9223372036854775807 {
			return controlFault("generation_exhausted", 409)
		}
		_, err := tx.ExecContext(ctx, `UPDATE playback_channel_sessions SET buffer_ordinal=buffer_ordinal+1,observation_sequence=0,observation_generation='',confirmed_position_us=NULL,acknowledged_seek_id=NULL,seek_error=CASE WHEN json_extract(transport_json,'$.seek') IS NULL THEN '' ELSE 'window_expired' END WHERE session_id=?`, id)
		out = w
		out.BufferOrdinal++
		return err
	})
	return out, err
}
func (c *ChannelSessions) BeginLinearProducer(ctx context.Context, id string, sourceRevision, bufferOrdinal int64) (LinearWork, error) {
	var out LinearWork
	err := c.WithLinearWriteTx(ctx, id, func(tx *sql.Tx, w LinearWork) error {
		if w.SourceRevision != sourceRevision || w.BufferOrdinal != bufferOrdinal {
			return controlFault("source_changed", 409)
		}
		if w.ProducerOrdinal == 9223372036854775807 {
			return controlFault("generation_exhausted", 409)
		}
		_, err := tx.ExecContext(ctx, `UPDATE playback_channel_sessions SET producer_ordinal=producer_ordinal+1,error_code='' WHERE session_id=?`, id)
		out = w
		out.ProducerOrdinal++
		return err
	})
	return out, err
}

// AuthorizeLinearMedia validates the retained segment's own selection (a revoked
// library can't leak through an older timeshift segment).
func (c *ChannelSessions) AuthorizeLinearMedia(ctx context.Context, id string, bufferOrdinal int64, selection *LinearSelection) (identity.Principal, error) {
	gated, err := dbwork.BeginSnapshot(ctx, c.db)
	if err != nil {
		return identity.Principal{}, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	w, err := c.linearWorkTx(ctx, tx, id, false)
	if err != nil {
		return w.Principal, err
	}
	if w.BufferOrdinal != bufferOrdinal {
		return w.Principal, controlFault("media_generation_expired", 410)
	}
	selected := w.Selection
	if selection != nil {
		selected = *selection
	}
	if selected.Reference.Kind != w.Selection.Reference.Kind || selected.Reference.ChannelID != w.Selection.Reference.ChannelID || selected.Reference.SourceID != w.Selection.Reference.SourceID {
		return w.Principal, identity.ErrUnauthorized
	}
	return w.Principal, c.linear.CheckTx(ctx, tx, w.Principal, selected)
}
func (c *ChannelSessions) CheckLinearProducer(ctx context.Context, w LinearWork) error {
	return c.WithLinearWorkTx(ctx, w.PlaybackID, func(tx *sql.Tx, current LinearWork) error {
		if current.SourceRevision != w.SourceRevision || current.BufferOrdinal != w.BufferOrdinal || current.ProducerOrdinal != w.ProducerOrdinal || current.PolicyRevision != w.PolicyRevision || current.MemberCeilings != w.MemberCeilings {
			return controlFault("source_changed", 409)
		}
		selected, err := c.linear.ResolveTx(ctx, tx, current.Principal, current.Selection.Reference, current.PlaybackID, c.now())
		if err != nil {
			return err
		}
		if linearProducerOutdated(w.Selection, selected) {
			return controlFault("source_changed", 409)
		}
		return nil
	})
}

// linearProducerOutdated reports whether a running producer no longer matches
// what its session now resolves to. The source fence carries what the
// producer's input depends on. A live channel's guide generation is not part
// of that: a refresh that leaves the stream alone publishes a new generation
// and must not stop anyone's producer.
func linearProducerOutdated(producing, resolved LinearSelection) bool {
	return resolved.SourceFence != producing.SourceFence || resolved.Reference.Kind == "library-channel" && resolved.EntryID != producing.EntryID
}

// linearSelectionRetunes reports whether a session's newly resolved selection
// needs a new producer, on the same terms.
func linearSelectionRetunes(held, resolved LinearSelection) bool {
	return resolved.SourceFence != held.SourceFence || resolved.Reference.Kind == "library-channel" && (resolved.EntryID != held.EntryID || resolved.AssetID != held.AssetID)
}
func (c *ChannelSessions) RefreshLinearSelection(ctx context.Context, id string) (bool, error) {
	return c.refreshLinearSelection(ctx, id, false)
}
func (c *ChannelSessions) RestartLinearSource(ctx context.Context, id string) error {
	_, err := c.refreshLinearSelection(ctx, id, true)
	return err
}

// refreshLinearSelection follows the schedule and the source: a new programme
// on a library channel, or a changed source fence, is a new source revision
// within the same session (a retune, never a new playback). Anything else,
// including a live source's new guide generation, only updates what the
// session shows.
func (c *ChannelSessions) refreshLinearSelection(ctx context.Context, id string, force bool) (bool, error) {
	gated, err := dbwork.Begin(ctx, c.db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		return false, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	w, err := c.linearWorkTx(ctx, tx, id, false)
	if err != nil {
		return false, err
	}
	selected, err := c.linear.ResolveTx(ctx, tx, w.Principal, w.Selection.Reference, id, c.now())
	if err != nil {
		return false, err
	}
	different := force || linearSelectionRetunes(w.Selection, selected)
	source, err := json.Marshal(selected)
	if err != nil {
		return false, err
	}
	if !different {
		if _, err = tx.ExecContext(ctx, `UPDATE playback_channel_sessions SET selection_json=? WHERE session_id=?`, string(source), id); err != nil {
			return false, err
		}
	} else {
		if err = c.linear.CheckTx(ctx, tx, w.Principal, selected); err != nil {
			return false, err
		}
		if w.SourceRevision == 9223372036854775807 {
			return false, controlFault("revision_exhausted", 409)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE playback_channel_sessions SET selection_json=?,source_revision=source_revision+1,error_code='',status='preparing',stage='source' WHERE session_id=?`, string(source), id); err != nil {
			return false, err
		}
	}
	if err = c.linear.BindTx(ctx, tx, id, selected, w.LeaseUntil); err != nil {
		return false, err
	}
	return different, gated.Commit()
}
func (c *ChannelSessions) LinearStatus(ctx context.Context, w LinearWork, status, stage, code string) error {
	if !controlOneOf(status, "preparing", "attachable", "active", "recoverable") || !controlOneOf(stage, "source", "production", "delivery", "engine", "recovery") {
		return errControlJSON
	}
	_, err := dbwork.ExecWrite(ctx, c.db, dbwork.ClassEstablishedPlayback, `UPDATE playback_channel_sessions SET error_code=?,status=?,stage=? WHERE session_id=? AND source_revision=? AND buffer_ordinal=? AND ended_ms=0`, code, status, stage, w.PlaybackID, w.SourceRevision, w.BufferOrdinal)
	return err
}

// ChannelState is what the v1 layer presents (spec §11/§18.6): the channel,
// its programmes, the linear clock and the stream, the desired transport and
// the viewer's acknowledged seek.
type ChannelState struct {
	Channel             LinearReference
	Name                string
	Programme, Next     *LinearProgramme
	Desired             LinearDesired
	Media               LinearMedia
	Status, Stage       string
	ErrorCode           string
	ConfirmedPositionUS *int64
	AcknowledgedSeekID  string
	SeekError           string
	LogoItemID          string
}

// ReadTx reads a channel session's state, rechecking the viewer may still see
// its source.
func (c *ChannelSessions) ReadTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (ChannelState, error) {
	var out ChannelState
	var position sql.NullInt64
	var ack sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT status,stage,error_code,confirmed_position_us,acknowledged_seek_id,seek_error FROM playback_channel_sessions WHERE session_id=? AND authority=? AND account_id=? AND profile_id=?`, id, p.Authority, p.AccountID, p.ProfileID).Scan(&out.Status, &out.Stage, &out.ErrorCode, &position, &ack, &out.SeekError)
	if errors.Is(err, sql.ErrNoRows) {
		return out, controlFault("not_found", 404)
	}
	if err != nil {
		return out, err
	}
	if !c.Configured() {
		return out, controlFault("channel_runtime_unavailable", 503)
	}
	// v1 supplies the session's viewer key, not a complete principal. Reuse
	// the retained authority path used by transport controls and producers:
	// it rechecks the bound family, current lease and selected source before
	// exposing delivery state. Never infer an owner role from the viewer key.
	w, err := c.linearWorkTx(ctx, tx, id, true)
	if err != nil {
		return out, err
	}
	selected := w.Selection
	out.Desired = w.Desired
	out.Channel, out.Name, out.Next, out.LogoItemID = selected.Reference, selected.Name, selected.Next, selected.LogoItemID
	if selected.EntryID != "" {
		out.Programme = &LinearProgramme{ID: selected.EntryID, ItemID: selected.ItemID, Title: selected.Title, StartMS: selected.EntryStartMS, EndMS: selected.EntryEndMS}
	}
	out.Media = c.delivery.Snapshot(id)
	var deadline int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(min(NULLIF(preempt_at_ms,0)),0) FROM live_allocations WHERE resource_id=? AND kind='live' AND state='active'`, id).Scan(&deadline); err != nil {
		return out, err
	}
	if deadline > 0 {
		out.Media.ReservationRequiredAtMS = &deadline
	}
	if out.ErrorCode != "" {
		out.Media.ErrorCode, out.Media.State = out.ErrorCode, "recoverable"
	}
	if position.Valid {
		v := position.Int64
		out.ConfirmedPositionUS = &v
	}
	out.AcknowledgedSeekID = ack.String
	return out, nil
}

// SetIntentTx sets the desired transport (play or pause, a seek in the retained
// window, go live, a retry), resolving a seek against the buffer it names and
// the viewer's access to what's retained there.
func (c *ChannelSessions) SetIntentTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string, desired LinearDesired) (LinearDesired, error) {
	if !validLinearDesired(desired) {
		return desired, errControlJSON
	}
	w, err := c.linearWorkTx(ctx, tx, id, true)
	if err != nil {
		return desired, err
	}
	if w.Principal.Authority != p.Authority || w.Principal.AccountID != p.AccountID || w.Principal.ProfileID != p.ProfileID {
		return desired, controlFault("not_found", 404)
	}
	previous := w.Desired
	accepted := desired
	if accepted.Seek != nil {
		if previous.Seek != nil && previous.Seek.ID == accepted.Seek.ID {
			if previous.Seek.Generation != accepted.Seek.Generation || accepted.Seek.Kind != "live" && (previous.Seek.Kind != accepted.Seek.Kind || optionalLinearPosition(previous.Seek.PositionUS) != optionalLinearPosition(accepted.Seek.PositionUS)) {
				return desired, controlFault("seek_identity_conflict", 409)
			}
			accepted.Seek = previous.Seek
		} else {
			resolved, err := c.delivery.ResolveSeek(id, accepted.Seek.Generation, accepted.Seek.Kind, accepted.Seek.PositionUS)
			if err != nil {
				return desired, err
			}
			retained, err := c.delivery.SelectionAt(id, accepted.Seek.Generation, resolved)
			if err != nil {
				return desired, err
			}
			if err = c.linear.CheckTx(ctx, tx, w.Principal, *retained); err != nil {
				return desired, err
			}
			accepted.Seek = &LinearSeek{ID: accepted.Seek.ID, Generation: accepted.Seek.Generation, Kind: "position", PositionUS: &resolved}
		}
	}
	transport, err := json.Marshal(accepted)
	if err != nil {
		return desired, err
	}
	retry := 0
	if accepted.RetryID != nil && (previous.RetryID == nil || *previous.RetryID != *accepted.RetryID) {
		retry = 1
	}
	clearSeek := previous.Seek == nil && accepted.Seek != nil || previous.Seek != nil && (accepted.Seek == nil || previous.Seek.ID != accepted.Seek.ID)
	_, err = tx.ExecContext(ctx, `UPDATE playback_channel_sessions SET transport_json=?,retry_revision=retry_revision+?,acknowledged_seek_id=CASE WHEN ? THEN NULL ELSE acknowledged_seek_id END,seek_error=CASE WHEN ? THEN '' ELSE seek_error END,error_code=CASE WHEN ? THEN '' ELSE error_code END WHERE session_id=?`, string(transport), retry, clearSeek, clearSeek, retry, id)
	return accepted, err
}

// ObserveTx records where the viewer is on the channel timeline (the timeline
// report, spec §6), acknowledging a seek it reached. It never changes intent.
func (c *ChannelSessions) ObserveTx(ctx context.Context, tx *sql.Tx, id string, generation string, sequence, positionUS int64, acknowledgedSeek string) error {
	w, err := c.linearWorkTx(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if generation != strconv.FormatInt(w.BufferOrdinal, 10) {
		return controlFault("media_generation_expired", 409)
	}
	var previous int64
	if err = tx.QueryRowContext(ctx, `SELECT observation_sequence FROM playback_channel_sessions WHERE session_id=?`, id).Scan(&previous); err != nil {
		return err
	}
	if sequence <= previous {
		return nil
	}
	seekError := ""
	var ack any
	if acknowledgedSeek != "" {
		q := w.Desired.Seek
		if q == nil || q.ID != acknowledgedSeek || q.Generation != generation || q.PositionUS == nil {
			return controlFault("seek_identity_conflict", 409)
		}
		target, err := parseControlDecimal(*q.PositionUS, true, false)
		if err != nil {
			return err
		}
		resolved, err := c.delivery.ResolveSeek(id, q.Generation, "position", q.PositionUS)
		switch {
		case err != nil || resolved != *q.PositionUS:
			seekError = "window_expired"
		case positionUS < target-1_000_000 || positionUS > target+1_000_000:
			seekError = "engine_seek_failed"
		default:
			ack = q.ID
		}
	}
	if seekError == "" {
		text := strconv.FormatInt(positionUS, 10)
		if _, err = c.delivery.ResolveSeek(id, generation, "position", &text); err != nil {
			seekError = "window_expired"
		} else {
			selected, err := c.delivery.SelectionAt(id, generation, text)
			if err != nil {
				return err
			}
			if err = c.linear.CheckTx(ctx, tx, w.Principal, *selected); err != nil {
				return err
			}
		}
	}
	var value any
	if seekError == "" {
		value = positionUS
	} else {
		ack = nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE playback_channel_sessions SET observation_sequence=?,observation_generation=?,confirmed_position_us=COALESCE(?,confirmed_position_us),acknowledged_seek_id=COALESCE(?,acknowledged_seek_id),seek_error=? WHERE session_id=?`, sequence, generation, value, ack, seekError, id)
	return err
}
