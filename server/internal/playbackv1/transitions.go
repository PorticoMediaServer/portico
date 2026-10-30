package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// Queue transitions for rendered audio (spec §18; Phase 5). The contract is
// fixed here so clients and the server lane build against the same shapes;
// until the server implements them the routes answer 501 not_implemented and
// /v1/capabilities does not advertise features.queueTransitions.

// PrepareNextRequest is POST /v1/queues/{id}:prepare-next (If-Match and
// Idempotency-Key required).
type PrepareNextRequest struct {
	SessionID         string `json:"sessionId"`
	SessionGeneration int    `json:"sessionGeneration"`
}

// PreparedNext is a private preparation of the next entry's audio: its
// presentation serves the render window at frame 0 only until commit.
type PreparedNext struct {
	Token        string       `json:"token"`
	ExpiresAt    string       `json:"expiresAt"`
	EntryID      string       `json:"entryId"`
	ItemID       string       `json:"itemId"`
	Presentation Presentation `json:"presentation"`
}

// CommitNextRequest is POST /v1/queues/{id}:commit-next.
type CommitNextRequest struct {
	Token string `json:"token"`
}

// QueueNext is what the queue plays after the current entry (spec §18.5).
type QueueNext struct {
	EntryID   *string `json:"entryId,omitempty"`
	Available bool    `json:"available"`
	Reason    string  `json:"reason"` // ready | end | unavailable
}

// QueuePostPlay is the viewer's post-play policy for this queue (spec §18.5).
type QueuePostPlay struct {
	Autoplay          bool `json:"autoplay"`
	CountdownMs       int  `json:"countdownMs"`
	PassoutCheckDue   bool `json:"passoutCheckDue"`
	AutomaticAdvances int  `json:"automaticAdvances"`
	// StartDuringCredits: the viewer lets the next title start during the
	// credits (with autoplay, on credits the server is sure of). Off by default:
	// the next title starts when this one ends.
	StartDuringCredits bool `json:"startDuringCredits"`
}

var (
	// ErrNotImplemented: a contracted route the server doesn't serve yet.
	ErrNotImplemented = errors.New("not implemented")
	// ErrPrepareNotAllowed: the queue isn't at a completion edge this caller can prepare.
	ErrPrepareNotAllowed = errors.New("prepare not allowed")
	// ErrPreparedExpired and ErrPreparedCanceled: a commit whose preparation is gone.
	ErrPreparedExpired  = errors.New("prepared expired")
	ErrPreparedCanceled = errors.New("prepared canceled")
)

// TransitionError refines a transition error with the reason clients act on
// (spec §18.2/18.3: prepare_not_allowed, prepared_canceled).
type TransitionError struct {
	Err    error
	Reason string
}

func (e *TransitionError) Error() string { return e.Err.Error() + ": " + e.Reason }
func (e *TransitionError) Unwrap() error { return e.Err }

func notAllowed(reason string) error {
	return &TransitionError{Err: ErrPrepareNotAllowed, Reason: reason}
}
func canceled(reason string) error { return &TransitionError{Err: ErrPreparedCanceled, Reason: reason} }

// preparationLifetime is a preparation's hard expiry (spec §18.2).
const preparationLifetime = 60 * time.Second

// completionNext is the position :advance {reason: completion} would play
// (repeat and shuffle laps included), and whether that wraps the queue.
func completionNext(l *layout) (pos int64, wraps bool, ok bool) {
	total := l.total()
	switch {
	case l.q.repeat == "one" && total > 0:
		return l.q.current, false, true
	case l.q.current+1 < total:
		return l.q.current + 1, false, true
	case l.q.repeat == "all" && total > 0:
		return 0, true, true
	}
	return 0, false, false
}

// preparedRow is one playback_v1_prepared row.
type preparedRow struct {
	token, queue, device, key, digest, session, sessionState, entry, item, media, request, presentation, state, cancelReason, response string
	sessionGeneration                                                                                                                  int
	queueRevision, nextPosition, expires, created                                                                                      int64
}

const preparedColumns = `token,queue_id,device_id,create_key,digest,session_id,session_state,entry_id,item_id,media_session_id,request,presentation,state,cancel_reason,response,session_generation,queue_revision,next_position,expires_ms,created_ms`

// As for sessions (rowSelect, rowValues): item_id is the INTEGER entity id.
const preparedSelect = `token,queue_id,device_id,create_key,digest,session_id,session_state,entry_id,COALESCE((SELECT pid(item.public_id) FROM catalog_entities item WHERE item.id=playback_v1_prepared.item_id),''),media_session_id,request,presentation,state,cancel_reason,response,session_generation,queue_revision,next_position,expires_ms,created_ms`

const preparedValues = `VALUES(?,?,?,?,?,?,?,?,COALESCE((SELECT id FROM catalog_entities WHERE public_id=pid_blob(NULLIF(?,''))),0),?,?,?,?,?,?,?,?,?,?,?)`

func scanPrepared(r scanner) (preparedRow, error) {
	var p preparedRow
	err := r.Scan(&p.token, &p.queue, &p.device, &p.key, &p.digest, &p.session, &p.sessionState, &p.entry, &p.item, &p.media, &p.request, &p.presentation, &p.state, &p.cancelReason, &p.response, &p.sessionGeneration, &p.queueRevision, &p.nextPosition, &p.expires, &p.created)
	return p, err
}

// preparedAnswer is what :prepare-next answers: the presentation without its
// media URL or sidecars, which it has none of until committed.
func (p preparedRow) answer() (PreparedNext, error) {
	var view Presentation
	if err := json.Unmarshal([]byte(p.presentation), &view); err != nil {
		return PreparedNext{}, err
	}
	if view.AudioRender.playable() && view.URL != view.AudioRender.URL {
		view.URL = ""
	}
	view.Subtitles = []SubtitleFile{}
	return PreparedNext{Token: p.token, ExpiresAt: fmtMs(time.UnixMilli(p.expires)), EntryID: p.entry, ItemID: p.item, Presentation: view}, nil
}

// cancelPreparedTx cancels a queue's (or a session's) outstanding preparations
// and returns their private presentations, for the caller to stop after its
// write commits.
func cancelPreparedTx(ctx context.Context, tx *sql.Tx, column, value, reason string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT media_session_id FROM playback_v1_prepared WHERE `+column+`=? AND state='prepared'`, value)
	if err != nil {
		return nil, err
	}
	var media []string
	for rows.Next() {
		var m string
		if err = rows.Scan(&m); err != nil {
			rows.Close()
			return nil, err
		}
		media = append(media, m)
	}
	rows.Close()
	if len(media) == 0 {
		return nil, rows.Err()
	}
	_, err = tx.ExecContext(ctx, `UPDATE playback_v1_prepared SET state='canceled',cancel_reason=? WHERE `+column+`=? AND state='prepared'`, reason, value)
	return media, err
}

// stopMedia ends private presentations a cancellation left behind.
func (s *Service) stopMedia(ctx context.Context, media []string) {
	if s.Playback == nil {
		return
	}
	for _, m := range media {
		_ = s.Playback.StopV1(context.WithoutCancel(ctx), m)
	}
}

// PrepareNext prepares the next entry's audio (spec §18.2).
func (s *Service) PrepareNext(ctx context.Context, c Caller, id, ifMatch, key string, req PrepareNextRequest) (PreparedNext, error) {
	if key == "" {
		return PreparedNext{}, ErrKeyRequired
	}
	if !keyPattern.MatchString(key) {
		return PreparedNext{}, &FieldError{Path: "Idempotency-Key"}
	}
	if req.SessionID == "" || req.SessionGeneration < 1 {
		return PreparedNext{}, &FieldError{Path: "sessionId"}
	}
	want := digest(struct {
		Queue   string
		Request PrepareNextRequest
	}{id, req})
	if prior, err := scanPrepared(s.DB.QueryRowContext(ctx, `SELECT `+preparedSelect+` FROM playback_v1_prepared WHERE device_id=? AND create_key=?`, c.DeviceID, key)); err == nil {
		switch {
		case prior.digest != want:
			return PreparedNext{}, ErrIdempotencyMismatch
		case prior.state == "prepared" && prior.expires > s.now().UnixMilli():
			return prior.answer()
		case prior.state == "canceled":
			return PreparedNext{}, canceled(prior.cancelReason)
		}
		return PreparedNext{}, ErrPreparedExpired
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PreparedNext{}, err
	}
	q, err := s.queueFor(ctx, c, id, ifMatch, true)
	if err != nil {
		return PreparedNext{}, err
	}
	now := s.now()
	r, err := loadRow(ctx, s.DB, req.SessionID)
	if err != nil || r.device != c.DeviceID || q.session != r.id || r.ended != 0 || r.lease <= now.UnixMilli() {
		return PreparedNext{}, notAllowed("session_not_current")
	}
	if r.generation != req.SessionGeneration || r.state != "playing" && r.state != "paused" && r.state != "preparing" {
		return PreparedNext{}, notAllowed("session_changed")
	}
	if err = c.authorizeSession(ctx, r); err != nil {
		return PreparedNext{}, err
	}
	preparing, err := s.Playback.RetainPreparation(ctx, r.media, r.mediaGeneration)
	if err != nil {
		return PreparedNext{}, err
	}
	defer preparing.Close()
	ctx = preparing.Context()
	var current Presentation
	if json.Unmarshal([]byte(r.presentation), &current) != nil || !current.AudioRender.playable() {
		return PreparedNext{}, notAllowed("not_rendered")
	}
	var pos int64
	var entry, item string
	if err = s.withMusicLayout(ctx, c, q.id, func(l *layout) ([]int64, error) {
		if l.q.revision != q.revision {
			return nil, ErrRevision
		}
		position, _, ok := completionNext(l)
		if !ok {
			return nil, ErrQueueEnded
		}
		return l.sonicWindowsForRange(position, 1)
	}, func(l *layout) error {
		if l.waiting() {
			return ErrQueueBuilding
		}
		p, _, ok := completionNext(l)
		if !ok {
			return ErrQueueEnded
		}
		e, k, err := l.entryAtErr(p)
		if err != nil {
			return err
		}
		pos, entry, item = p, e, k
		return nil
	}); err != nil {
		return PreparedNext{}, err
	}
	if err = c.admit(WithReplan(ctx, r.id, ""), item); err != nil {
		return PreparedNext{}, ErrNotFound // a withheld next entry is absent (SEC-02)
	}
	// The next session inherits the quality request (spec §18.3); tracks are per item.
	var prior storedRequest
	_ = json.Unmarshal([]byte(r.request), &prior)
	stored := storedRequest{Quality: prior.Quality, Network: prior.Network}.normalized()
	token := newID("pn_")
	expires := now.Add(preparationLifetime)
	media, view, err := s.present(ctx, c, presentationTarget{item: item, request: stored, requestID: "v1." + token, subtitleOp: "v1-" + token, privateFor: r.media, privateUntilMs: expires.UnixMilli()})
	if err != nil {
		return PreparedNext{}, err
	}
	if !view.AudioRender.playable() {
		s.stopMedia(ctx, []string{media.ID})
		return PreparedNext{}, notAllowed("not_rendered")
	}
	// The prepared view records the exact private presentation generation it
	// was planned for. Commit keeps this captured generation instead of reading
	// a newer plan, so a replanned row is refused rather than borrowed.
	view.Generation = media.Generation
	requestJSON, _ := json.Marshal(stored)
	presentationJSON, _ := json.Marshal(view)
	p := preparedRow{token: token, queue: q.id, device: c.DeviceID, key: key, digest: want, session: r.id, sessionState: r.state, entry: entry, item: item, media: media.ID,
		request: string(requestJSON), presentation: string(presentationJSON), state: "prepared", sessionGeneration: r.generation, queueRevision: q.revision, nextPosition: pos, expires: expires.UnixMilli(), created: now.UnixMilli()}
	var replaced []string
	err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		// Presentation creation ran outside the writer. An end/replan must not
		// leave a newly inserted child behind an accepted generation stop.
		parent, err := loadRow(ctx, tx, r.id)
		if err != nil {
			return err
		}
		if !sameStopBinding(r, parent) || parent.ended != 0 || parent.state != r.state {
			return ErrRevision
		}
		queue, err := loadQueue(ctx, tx, q.id)
		if err != nil {
			return err
		}
		if queue.revision != q.revision || queue.session != r.id {
			return ErrRevision
		}
		// A new preparation replaces the queue's earlier one (spec §18.2).
		if replaced, err = cancelPreparedTx(ctx, tx, "queue_id", q.id, "replaced"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO playback_v1_prepared(`+preparedColumns+`) `+preparedValues,
			p.token, p.queue, p.device, p.key, p.digest, p.session, p.sessionState, p.entry, p.item, p.media, p.request, p.presentation, p.state, p.cancelReason, p.response, p.sessionGeneration, p.queueRevision, p.nextPosition, p.expires, p.created)
		return err
	})
	s.stopMedia(ctx, replaced)
	if err != nil {
		s.stopMedia(ctx, []string{media.ID})
		// A concurrent request with this key won: answer as its replay.
		if prior, e := scanPrepared(s.DB.QueryRowContext(ctx, `SELECT `+preparedSelect+` FROM playback_v1_prepared WHERE device_id=? AND create_key=?`, c.DeviceID, key)); e == nil && prior.digest == want {
			return prior.answer()
		}
		return PreparedNext{}, err
	}
	return p.answer()
}

// CommitNext commits a prepared next entry at the audio boundary (spec §18.3).
func (s *Service) CommitNext(ctx context.Context, c Caller, id string, req CommitNextRequest) (QueueReply, error) {
	if err := s.requireObservationProtocol(c); err != nil {
		return QueueReply{}, err
	}
	if req.Token == "" {
		return QueueReply{}, &FieldError{Path: "token"}
	}
	p, err := scanPrepared(s.DB.QueryRowContext(ctx, `SELECT `+preparedSelect+` FROM playback_v1_prepared WHERE token=? AND queue_id=?`, req.Token, id))
	if err != nil || p.device != c.DeviceID {
		return QueueReply{}, ErrNotFound
	}
	switch p.state {
	case "committed":
		var reply QueueReply
		if json.Unmarshal([]byte(p.response), &reply) == nil {
			return reply, nil
		}
		return QueueReply{}, ErrNotFound
	case "canceled":
		return QueueReply{}, canceled(p.cancelReason)
	}
	now := s.now()
	if p.expires <= now.UnixMilli() {
		s.cancelPrepared(ctx, "token", p.token, "expired")
		return QueueReply{}, ErrPreparedExpired
	}
	// Access is checked before the write; the next entry must still be the caller's.
	if err = c.admit(WithReplan(ctx, p.session, p.media), p.item); err != nil {
		s.cancelPrepared(ctx, "token", p.token, "access_changed")
		return QueueReply{}, canceled("access_changed")
	}
	qBefore, err := loadQueue(ctx, s.DB, id)
	if err != nil || !ownsQueue(c, qBefore) {
		return QueueReply{}, ErrNotFound
	}
	if qBefore.revision != p.queueRevision {
		return QueueReply{}, canceled("queue_changed")
	}
	refill, err := s.prepareRadioRefill(ctx, c, qBefore)
	if err != nil {
		return QueueReply{}, err
	}
	var reply QueueReply
	var old row
	var fence error
	err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		fail := func(reason string) error {
			fence = canceled(reason)
			_, err := tx.ExecContext(ctx, `UPDATE playback_v1_prepared SET state='canceled',cancel_reason=? WHERE token=? AND state='prepared'`, reason, p.token)
			return err
		}
		q, err := loadQueue(ctx, tx, id)
		if err != nil {
			return err
		}
		if q.revision != p.queueRevision {
			return fail("queue_changed")
		}
		if old, err = scanRow(tx.QueryRowContext(ctx, `SELECT `+rowSelect+` FROM playback_v1_sessions WHERE id=?`, p.session)); err != nil {
			return err
		}
		if old.ended != 0 || old.generation != p.sessionGeneration || old.state != p.sessionState || old.lease <= now.UnixMilli() || q.session != old.id {
			return fail("session_changed")
		}
		l, err := s.readMusicWriterLayout(ctx, tx, q, c)
		if err != nil {
			return err
		}
		defer func() {
			if l != nil {
				l.sonicPrepare = nil
			}
		}()
		pos, wraps, ok := completionNext(l)
		if !ok || pos != p.nextPosition {
			return fail("queue_changed")
		}
		if entry, _, found := l.entryAt(pos); !found || entry != p.entry {
			return fail("queue_changed")
		}
		// The preparation recorded the exact private presentation generation it
		// was planned for. Commit keeps that exact generation and refuses a row
		// replanned after the preparation: the stored view no longer describes
		// the newer plan, and borrowing it would commit bytes never prepared.
		var view Presentation
		if err = json.Unmarshal([]byte(p.presentation), &view); err != nil {
			return err
		}
		var mediaGeneration int
		if err = tx.QueryRowContext(ctx, `SELECT generation FROM playback_sessions WHERE id=? AND account_id=? AND profile_id=? AND state NOT IN('stopped','ended','failed')`, p.media, c.Principal.AccountID, c.Principal.ProfileID).Scan(&mediaGeneration); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fail("source_changed")
			}
			return err
		}
		if view.Generation < 1 || mediaGeneration != view.Generation {
			return fail("source_changed")
		}
		// Refill only after validating the exact held next occurrence. The
		// existing commit transaction transfers its single authority and also
		// persists the following bounded radio keys under that same revision.
		if err := s.appendRadioTx(ctx, tx, l, c.Principal, refill); err != nil {
			return err
		}
		switch err := s.Playback.ReleasePrivateTx(ctx, tx, c.Principal, p.media); {
		case errors.Is(err, playback.ErrPreparationExpired):
			fence = ErrPreparedExpired
			_, err = tx.ExecContext(ctx, `UPDATE playback_v1_prepared SET state='canceled',cancel_reason='expired' WHERE token=?`, p.token)
			return err
		case errors.Is(err, playback.ErrTranscodingDisabled):
			return fail("policy_changed")
		case errors.Is(err, playback.ErrSourceChanged):
			return fail("source_changed")
		case err != nil:
			return err
		}
		// The queue moves and the next session starts, inheriting state (spec §18.3).
		l.q.current = pos
		if wraps && l.q.shuffle {
			l.q.lap++
		}
		l.q.revision++
		l.q.updated = now.UnixMilli()
		state := "playing"
		if old.state == "paused" {
			state = "paused"
		}
		requestJSON, _ := json.Marshal(struct {
			storedRequest
			State string `json:"state,omitempty"`
		}{func() storedRequest { var sr storedRequest; _ = json.Unmarshal([]byte(p.request), &sr); return sr }(), state})
		next := row{id: newID("ps_"), device: c.DeviceID, account: c.Principal.AccountID, profile: c.Principal.ProfileID, authority: c.Principal.Authority,
			startKey: "commit." + p.token, startDigest: p.digest, kind: "audio", role: "local", state: state, item: p.item, queueID: q.id, entryID: p.entry,
			request: string(requestJSON), media: p.media, presentation: p.presentation, location: old.location,
			revision: 1, generation: 1, mediaGeneration: mediaGeneration, lease: now.Add(s.lease()).UnixMilli(), created: now.UnixMilli(), updated: now.UnixMilli()}
		if state == "paused" {
			// The successor inherits the pause: staying paused keeps the old
			// stamp (or stamps now for a pause that predates the column).
			next.pausedSince = old.pausedSince
			if next.pausedSince <= 0 {
				next.pausedSince = now.UnixMilli()
			}
		}
		if plan, _ := s.Playback.SessionPlan(ctx, p.media); plan != nil {
			next.version = plan.SourceID
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO playback_v1_sessions(`+rowColumns+`) `+rowValues,
			next.id, next.device, next.account, next.profile, next.authority, next.startKey, next.startDigest, next.kind, next.role, next.state, next.item, next.version, next.channel, next.queueID, next.entryID, next.transfer, next.group, next.request, next.media, next.presentation, next.audioTrack, next.subtitleTrack, next.location, next.endReason, next.message, next.partIndex, next.revision, next.generation, next.mediaGeneration, next.startPosition, next.position, next.lastSeq, next.bandwidth, next.lease, next.created, next.updated, next.ended, next.pausedSince); err != nil {
			return err
		}
		l.q.session = next.id
		if _, err = tx.ExecContext(ctx, `UPDATE queues_v1 SET revision=?,current_position=?,shuffle_lap=?,session_id=?,updated_ms=? WHERE id=?`, l.q.revision, l.q.current, l.q.lap, l.q.session, l.q.updated, l.q.id); err != nil {
			return err
		}
		if err = s.endTx(ctx, tx, old, now, "completed", ""); err != nil {
			return err
		}
		if err = s.sessionEventsTx(ctx, tx, next, now, true); err != nil {
			return err
		}
		if l, err = s.readMusicWriterLayout(ctx, tx, l.q, c); err != nil {
			return err
		}
		if err = s.queueEventsTx(ctx, tx, l, now); err != nil {
			return err
		}
		v := s.view(next)
		reply = QueueReply{Queue: s.header(l), Session: &v}
		raw, _ := json.Marshal(reply)
		_, err = tx.ExecContext(ctx, `UPDATE playback_v1_prepared SET state='committed',response=? WHERE token=?`, string(raw), p.token)
		return err
	})
	if err != nil {
		return QueueReply{}, err
	}
	if fence != nil {
		s.stopMedia(ctx, []string{p.media})
		return QueueReply{}, fence
	}
	// The previous presentation refuses new requests from here; decoded audio drains.
	if s.Playback != nil {
		s.Playback.ForgetPrefetch(p.media)
	}
	s.stopMedia(ctx, []string{old.media})
	s.hub.wake()
	s.sweep.kick(s)
	if q, err := loadQueue(ctx, s.DB, id); err == nil {
		s.withNext(ctx, c, &reply.Queue, q)
	}
	return reply, nil
}

// withNext fills what plays after the current entry, through the caller's
// fence, and the caller's post-play policy (spec §18.5), in one read.
func (s *Service) withNext(ctx context.Context, c Caller, v *QueueView, q queueRow) {
	_ = s.withMusicLayout(ctx, c, q.id, func(l *layout) ([]int64, error) {
		if l.q.revision != q.revision {
			return nil, ErrRevision
		}
		windows, err := sonicCurrentWindow(l)
		if err != nil {
			return nil, err
		}
		if position, _, ok := completionNext(l); ok {
			next, err := l.sonicWindowsForRange(position, 1)
			if err != nil {
				return nil, err
			}
			windows = append(windows, next...)
		}
		return windows, nil
	}, func(l *layout) error {
		tx := l.tx
		next := QueueNext{Reason: "end"}
		if pos, _, ok := completionNext(l); ok {
			entry, key, err := l.entryAtErr(pos)
			switch {
			case err == nil:
				next.EntryID = &entry
				next.Reason = "unavailable"
				if visible, err := s.visibleFilter(ctx, tx, c.Principal); err == nil {
					if allowed, err := visible([]string{key}); err == nil && len(allowed) == 1 {
						next.Available, next.Reason = true, "ready"
					}
				}
			case errors.Is(err, ErrQueueBuilding):
				next.Reason = "unavailable"
			}
		}
		v.Next = &next

		// A growing capture or endless radio has no final duration total.
		if len(l.building) == 0 && radioSegment(l) < 0 {
			if summary, err := s.queueDuration(ctx, l, c.Principal); err == nil {
				v.TotalDurationMs = &summary.ms
				v.DurationKnown = summary.known
			}
		}

		if policy, err := s.postPlayPolicy(ctx, tx, c, l, next.Available); err == nil {
			v.PostPlay = &policy
		}
		return nil
	})
}

// cancelPreparedFor cancels a session's prepared next, if it has one (one
// indexed read otherwise).
func (s *Service) cancelPreparedFor(ctx context.Context, session string) {
	var pending bool
	if s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_v1_prepared WHERE session_id=? AND state='prepared')`, session).Scan(&pending) == nil && pending {
		s.cancelPrepared(ctx, "session_id", session, "session_changed")
	}
}

// cancelPrepared cancels outstanding preparations by a column and stops their
// private presentations.
func (s *Service) cancelPrepared(ctx context.Context, column, value, reason string) {
	var media []string
	_ = dbwork.WithWriteTxContext(context.WithoutCancel(ctx), s.DB, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		media, err = cancelPreparedTx(ctx, tx, column, value, reason)
		return err
	})
	s.stopMedia(ctx, media)
}

// AudioRender is an audio presentation's plan on the v1 wire (spec §18.1,
// version 2): the client decodes the original file (direct) or a FLAC/Opus
// conversion (converted) with the server's exact trim and gains; unavailable
// says why neither is possible. No nulls: unknown values are absent.
type AudioRender struct {
	SourceBitrateBPS  int64                           `json:"sourceBitrateBps,omitempty"`
	ConversionGainDB  float64                         `json:"conversionGainDb,omitempty"`
	LoudnessAppliedDB *float64                        `json:"loudnessAppliedDb,omitempty"`
	LoudnessEnvelope  *playback.AudioLoudnessEnvelope `json:"loudnessEnvelope,omitempty"`
	Version           int                             `json:"version"`
	Mode              string                          `json:"mode"`
	ID                string                          `json:"id"`
	Reason            string                          `json:"reason,omitempty"`
	URL               string                          `json:"url,omitempty"`
	Container         string                          `json:"container,omitempty"`
	Codec             string                          `json:"codec,omitempty"`
	DecoderConfig     string                          `json:"decoderConfig,omitempty"`
	SampleRate        int                             `json:"sampleRate,omitempty"`
	Channels          int                             `json:"channels,omitempty"`
	BitDepth          int                             `json:"bitDepth,omitempty"`
	Bytes             int64                           `json:"bytes,omitempty"`
	PrefetchBytes     int64                           `json:"prefetchBytes,omitempty"`
	DurationFrames    int64                           `json:"durationFrames,omitempty"`
	Downmixed         bool                            `json:"downmixed,omitempty"`
	Trim              *AudioRenderTrim                `json:"trim,omitempty"`
	Gain              *AudioRenderGain                `json:"gain,omitempty"`
	// SeekIndex: a direct plan in a container with no seek index of its own
	// (MP3, ADTS, Ogg) has the server's at this route: byte offsets at packet
	// or page boundaries and the raw frame each starts at, so the client reads
	// from the nearest one with a Range request and decodes to the exact frame.
	SeekIndex *AudioRenderSeekIndex `json:"seekIndex,omitempty"`
}
type AudioRenderSeekIndex struct {
	URL string `json:"url"`
}
type AudioRenderTrim struct {
	StartFrames int64  `json:"startFrames"`
	EndFrames   int64  `json:"endFrames"`
	Source      string `json:"source"`
}
type AudioRenderGain struct {
	TrackDB   *float64 `json:"trackDb,omitempty"`
	AlbumDB   *float64 `json:"albumDb,omitempty"`
	TrackPeak *float64 `json:"trackPeak,omitempty"`
	AlbumPeak *float64 `json:"albumPeak,omitempty"`
	Source    string   `json:"source"`
}

// playable reports a plan an engine can render (the §18 edges need one).
func (a *AudioRender) playable() bool {
	return a != nil && (a.Mode == "direct" || a.Mode == "converted")
}

func audioRenderOf(p *playback.AudioPlan) *AudioRender {
	if p == nil {
		return nil
	}
	out := &AudioRender{SourceBitrateBPS: p.SourceBitrateBPS, ConversionGainDB: p.ConversionGainDB, LoudnessAppliedDB: p.LoudnessAppliedDB, LoudnessEnvelope: p.LoudnessEnvelope, Version: p.Version, Mode: p.Mode, ID: p.ID, Reason: p.Reason, URL: p.URL, Container: p.Container, Codec: p.Codec, DecoderConfig: p.DecoderConfig,
		SampleRate: p.SampleRate, Channels: p.Channels, BitDepth: p.BitDepth, Bytes: p.Bytes, PrefetchBytes: p.PrefetchBytes, DurationFrames: p.DurationFrames, Downmixed: p.Downmixed}
	if p.Trim != nil {
		out.Trim = &AudioRenderTrim{StartFrames: p.Trim.StartFrames, EndFrames: p.Trim.EndFrames, Source: p.Trim.Source}
	}
	if p.Gain != nil {
		out.Gain = &AudioRenderGain{TrackDB: p.Gain.TrackDB, AlbumDB: p.Gain.AlbumDB, TrackPeak: p.Gain.TrackPeak, AlbumPeak: p.Gain.AlbumPeak, Source: p.Gain.Source}
	}
	if x := p.SeekIndex; x != nil && p.Mode == "direct" {
		out.SeekIndex = &AudioRenderSeekIndex{URL: x.URL}
	}
	return out
}

// PostPlayPolicy is the effective viewer choice, read for each completion edge.
type PostPlayPolicy struct {
	Autoplay           bool
	CountdownMs        int
	StartDuringCredits bool
	Protection         bool
	Episodes           int
}

func (s *Service) postPlayPolicy(ctx context.Context, tx *sql.Tx, c Caller, l *layout, nextAvailable bool) (QueuePostPlay, error) {
	out := QueuePostPlay{Autoplay: true, CountdownMs: 10000, AutomaticAdvances: l.q.automatic}
	if currentListeningItem(ctx, tx, l) {
		out.CountdownMs = 0
		return out, nil
	}
	if s.PostPlay == nil {
		return out, nil
	}
	policy, err := s.PostPlay(ctx, tx, c.Principal)
	if err != nil {
		return out, err
	}
	out.Autoplay, out.CountdownMs, out.StartDuringCredits = policy.Autoplay, policy.CountdownMs, policy.StartDuringCredits
	// Attention protects an actual unattended edge. An unavailable/end-of-queue
	// completion has no next offer or confirmation action and stays ordinarily ended.
	out.PassoutCheckDue = nextAvailable && policy.Autoplay && currentEpisode(ctx, tx, l) && policy.Protection && policy.Episodes > 0 && l.q.automatic+1 >= policy.Episodes
	return out, nil
}

func currentEpisode(ctx context.Context, tx *sql.Tx, l *layout) bool {
	_, key, err := l.entryAtErr(l.q.current)
	if err != nil {
		return false
	}
	var episode bool
	if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE kind=? AND public_id=pid_blob(?))`, compactcatalog.Episode, key).Scan(&episode) != nil {
		return false
	}
	return episode
}

// CollectionSuccessor is the genuine next available film in a collection's
// saved order. A missing successor is absent, never inferred from a year.
type CollectionSuccessor struct {
	ID    string              `json:"id"`
	Title string              `json:"title"`
	Next  CollectionNextTitle `json:"next"`
}
type CollectionNextTitle struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Year        int      `json:"year,omitempty"`
	Genres      []string `json:"genres"`
	BackdropURL string   `json:"backdropUrl,omitempty"`
}

func (s *Service) collectionSuccessor(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (*CollectionSuccessor, error) {
	visible, args := "1=1", []any{}
	if s.Visibility != nil {
		var err error
		visible, args, err = s.Visibility(ctx, tx, p, "i.id")
		if err != nil {
			return nil, err
		}
	}
	out := &CollectionSuccessor{Next: CollectionNextTitle{Genres: []string{}}}
	var entity int64
	var backdrop bool
	var library string
	err := tx.QueryRowContext(ctx, `SELECT pid(c.public_id),c.title,library.library_id,pid(i.public_id),i.id,i.title,i.year,
 EXISTS(SELECT 1 FROM artwork_selections art JOIN artwork_sources obj ON obj.digest=art.digest WHERE art.kind='item' AND art.entity_id=i.id AND art.role='backdrop' AND art.subject='')
 FROM catalog_entities current JOIN catalog_collection_members anchor ON anchor.item_id=current.id
 JOIN catalog_entities c ON c.id=anchor.collection_id AND c.retired=0
 JOIN catalog_libraries library ON library.id=c.library_id AND library.retired=0
 JOIN catalog_collection_members member ON member.collection_id=anchor.collection_id AND (member.order_key,member.item_id)>(anchor.order_key,anchor.item_id)
 JOIN catalog_entities i ON i.id=member.item_id AND i.kind=1 AND i.retired=0
 WHERE current.public_id=pid_blob(?) AND current.kind=1 AND (`+visible+`) AND EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=i.id AND a.available=1)
 ORDER BY c.id,member.order_key,member.item_id LIMIT 1`, append([]any{item}, args...)...).Scan(&out.ID, &out.Title, &library, &out.Next.ID, &entity, &out.Next.Title, &out.Next.Year, &backdrop)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.LibraryCheck != nil {
		if err := s.LibraryCheck(ctx, tx, p, library); err != nil {
			return nil, nil
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT COALESCE(src.label_override,term.label) FROM catalog_term_sources src JOIN catalog_terms term ON term.id=src.term_id WHERE src.entity_id=? AND term.vocab=? ORDER BY 1`, entity, compactcatalog.VocabGenre)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		out.Next.Genres = append(out.Next.Genres, name)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if backdrop {
		out.Next.BackdropURL = "/v1/items/" + url.PathEscape(out.Next.ID) + "/art/backdrop"
	}
	return out, artworkref.DescribeItemURLs(ctx, tx, &out.Next.BackdropURL)
}
