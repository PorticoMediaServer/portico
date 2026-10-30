package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

func (r row) stored() storedRequest {
	var out storedRequest
	_ = json.Unmarshal([]byte(r.request), &out)
	return out.normalized()
}

func (r row) presentationView() Presentation {
	var p Presentation
	_ = json.Unmarshal([]byte(r.presentation), &p)
	if p.Subtitles == nil {
		p.Subtitles = []SubtitleFile{}
	}
	return p
}

// converting reports whether a presentation re-produces bytes (so a seek must
// restart production on the server, spec §17.4).
func (p Presentation) converting() bool {
	for _, d := range []*Decision{p.Decision.Video, p.Decision.Audio} {
		if d != nil && (d.Action == "transcode" || d.Action == "burn") {
			return true
		}
	}
	return false
}

// Patch is PATCH /v1/playback/sessions/{id} with If-Match (spec §5.7): the one
// operation that changes a session while it plays. A change of version, audio
// track, quality or part, a subtitle track that changes what the picture or
// the engine has to be, and a reported player failure each re-plan the same
// session: a new presentation (a new generation, a new URL, possibly another
// engine) at the current position. The session keeps its id, its queue and
// its place in it. Other changes keep the generation.
func (s *Service) Patch(ctx context.Context, c Caller, id, ifMatch string, change Change) (SessionView, error) {
	release := s.admission.hold(c.Principal.AccountID)
	defer release()
	if err := validateTracks(change.Audio, change.Subtitles); err != nil {
		return SessionView{}, err
	}
	switch change.State {
	case "", "playing", "paused":
	default:
		return SessionView{}, &FieldError{Path: "state"}
	}
	if change.Quality != nil {
		if err := change.Quality.Validate("quality"); err != nil {
			return SessionView{}, err
		}
	}
	if change.Seek != nil && (change.Seek.PositionMs < 0 || change.Seek.PositionMs > maxStartPositionMs) {
		return SessionView{}, &FieldError{Path: "seek.positionMs"}
	}
	if change.PositionMs != nil && (*change.PositionMs < 0 || *change.PositionMs > maxStartPositionMs) {
		return SessionView{}, &FieldError{Path: "positionMs"}
	}
	if change.SubtitleOffsetUS != nil {
		if change.SubtitleOffsetMs != nil {
			return SessionView{}, &FieldError{Path: "subtitleOffsetUs"}
		}
		if _, err := subtitles.Offset(*change.SubtitleOffsetUS); err != nil {
			return SessionView{}, &FieldError{Path: "subtitleOffsetUs"}
		}
	}
	if change.ThroughputKbps != nil && (*change.ThroughputKbps < 0 || *change.ThroughputKbps > 10_000_000) {
		return SessionView{}, &FieldError{Path: "throughputKbps"}
	}
	if f := change.Failure; f != nil && (!failureCodePattern.MatchString(f.Code) || len(f.Detail) > 512) {
		return SessionView{}, &FieldError{Path: "failure"}
	}
	r, err := s.session(ctx, c, id)
	if err != nil {
		return SessionView{}, err
	}
	if r.ended > 0 {
		return SessionView{}, ErrEnded
	}
	if ifMatch == "" {
		return SessionView{}, ErrRevisionRequired
	}
	if want, ok := ParseRevision(ifMatch); !ok || want != int64(r.revision) {
		return SessionView{}, &RevisionError{Current: s.view(r), Revision: int64(r.revision)}
	}
	if isChannel(r) {
		if change.Replan {
			return SessionView{}, &FieldError{Path: "replan"}
		}
		return s.patchChannel(ctx, c, r, change)
	}
	if c.DeviceID != r.device {
		// A controller acts for the profile, but the engine and media connection
		// still belong to the device playing this occurrence. Use its current
		// token family binding even if its bearer rotated since playback began.
		var hash string
		err := dbwork.QueryRow(ctx, s.DB, `SELECT COALESCE((SELECT t.token_hash FROM authorization_family_tokens origin JOIN authorization_family_tokens t ON t.family_id=origin.family_id JOIN authorization_access a ON a.hash=t.token_hash WHERE origin.token_hash=ps.session_hash AND a.revoked=0 AND a.expires_at>? ORDER BY a.expires_at DESC LIMIT 1),ps.session_hash) FROM playback_sessions ps WHERE ps.id=? AND ps.account_id=? AND ps.profile_id=?`, s.now().UTC().Format(time.RFC3339), r.media, r.account, r.profile).Scan(&hash)
		if err != nil {
			return SessionView{}, err
		}
		c.Principal.Hash, c.DeviceID = hash, r.device
		c.Remote = r.location == "remote" || r.location == "cellular"
	}
	if change.Subtitles != nil || change.SubtitleOffsetMs != nil || change.SubtitleOffsetUS != nil {
		if view, private, err := s.privateSubtitlePatch(ctx, c, r, change); private || err != nil {
			return view, discStartError(err)
		}
	}
	stored := r.stored()
	next := stored
	if change.State != "" {
		next.State = change.State
	}
	if change.State != "" || change.Seek != nil {
		next.ControlRevision++
	}
	if change.Seek != nil {
		// The seeker's own id when it gives one (a resend keeps it), else ours.
		next.SeekID = newID("sk_")
		if seekIDPattern.MatchString(change.Seek.ID) {
			next.SeekID = change.Seek.ID
		}
	}
	presentation := r.presentationView()
	bytes := change.Replan
	if change.VersionID != "" && change.VersionID != r.version {
		next.VersionID, bytes = change.VersionID, true
		next.DiscTitle = nil
		next.SelectedDiscTitle = nil
	}
	if change.Audio != nil && (stored.Audio == nil || !reflect.DeepEqual(*change.Audio, *stored.Audio)) {
		next.Audio, bytes = change.Audio, true
	}
	if change.Quality != nil {
		wanted := *change.Quality
		if wanted.Mode == QualityAutomatic {
			// Automatic is a place on the ladder the session moves along. A step
			// moves from where it is; naming a step jumps there ("original"
			// is back on no step); naming neither keeps the place an
			// automatic session already has.
			here := Quality{Mode: QualityAutomatic}
			if stored.Quality.Mode == QualityAutomatic {
				here.ID = stored.Quality.ID
			}
			switch {
			case wanted.Step != "":
				plan, _ := s.Playback.SessionPlan(ctx, r.media)
				wanted = stepAutomatic(here, wanted.Step, sourceFactsOf(plan))
			case wanted.ID == QualityOriginal:
				wanted.ID = ""
			case wanted.ID == "":
				wanted = here
			}
		}
		wanted.Step = ""
		if wanted != stored.Quality {
			next.Quality, bytes = wanted, true
		}
	}
	if change.PartIndex != nil && *change.PartIndex != r.partIndex {
		bytes = true
	}
	if change.Subtitles != nil {
		next.Subtitles = change.Subtitles
		// A text track drawn beside the stream changes no bytes. A styled or
		// image track can: it may need the picture converted, or another
		// engine that draws it; and leaving one can undo either. Those are
		// planned again from the start, as one presentation.
		if s.subtitleReplans(ctx, c, r, presentation, change.Subtitles) {
			bytes = true
		}
	}
	if change.Subtitles != nil || change.SubtitleOffsetMs != nil {
		next.SubtitleOffsetUS = ""
	}
	if change.SubtitleOffsetMs != nil {
		next.SubtitleOffsetMs = *change.SubtitleOffsetMs
	}
	if change.SubtitleOffsetUS != nil {
		next.SubtitleOffsetUS = *change.SubtitleOffsetUS
	}
	oldMediaGeneration := r.mediaGeneration
	rMediaBefore := r.media
	committed := false
	restartConversion := false
	generation, media, mediaGeneration := r.generation, r.media, r.mediaGeneration
	position := r.position
	if latest, ok := s.latestObservedPosition(r.id, r.generation); ok {
		position = latest
	}
	if change.PositionMs != nil {
		position = *change.PositionMs
	}
	if change.Seek != nil {
		position = change.Seek.PositionMs
	}
	if f := change.Failure; f != nil {
		// The player could not play what it was given. Remember that for this
		// file on this device, and plan the next way up in the same session.
		result, err := s.Playback.RecordRouteFailure(ctx, c.Principal, r.media, f.Code, f.Detail)
		if err != nil {
			return SessionView{}, &FieldError{Path: "failure"}
		}
		if !result.Escalates {
			return SessionView{}, playback.ErrIncompatible
		}
		bytes = true
	}
	// A re-plan re-admits like a start, so the remote bitrate cap and the
	// schedule and item policy apply mid-session. It already holds its stream,
	// so its own slot is not counted again: a member at their stream limit can
	// still change quality.
	if bytes {
		admitCtx := WithReplan(ctx, id, "")
		replan := ReplanOf(admitCtx)
		replan.Remote = c.Remote
		admitCtx = context.WithValue(admitCtx, replanKey{}, replan)
		if err := c.admit(admitCtx, r.item); err != nil {
			return SessionView{}, err
		}
	}
	throughput := 0
	if change.ThroughputKbps != nil {
		throughput = *change.ThroughputKbps
	}
	switch {
	case bytes:
		// Subtitle delivery can renew a media grant independently of the v1
		// row. Replace the media generation that is actually live, and guard
		// that same generation again when publishing the replacement.
		if err := dbwork.QueryRow(ctx, s.DB, `SELECT generation FROM playback_sessions WHERE id=? AND account_id=? AND profile_id=? AND state NOT IN('stopped','ended','failed')`, r.media, r.account, r.profile).Scan(&oldMediaGeneration); err != nil {
			return SessionView{}, err
		}
		version := next.VersionID
		if version == "" {
			version = r.version
		}
		attempt := newID("change_")
		op := "v1-" + r.id + "-" + attempt
		part := changedPart(r, change, version)
		target := presentationTarget{item: r.item, version: version, part: part, request: next, startMs: position,
			replacing: &playback.SessionReplacement{ID: r.media, Generation: oldMediaGeneration, Deferred: true}, requestID: "v1." + r.id + "." + attempt, subtitleOp: op}
		if change.Quality != nil {
			// Only a quality asked for now is checked against the connection.
			target.throughputKbps = throughput
		}
		newMedia, view, err := s.present(ctx, c, target)

		if err != nil {
			if newMedia.ID != "" {
				_ = s.Playback.StopV1(context.WithoutCancel(ctx), newMedia.ID)
			}
			return SessionView{}, err
		}
		defer func() {
			if !committed {
				_ = s.Playback.StopV1(context.WithoutCancel(ctx), newMedia.ID)
			}
		}()
		presentation, media, mediaGeneration, generation = view, newMedia.ID, newMedia.Generation, r.generation+1
		if view.Quality != nil {
			next.Quality = *view.Quality
		}
		if plan, _ := s.Playback.SessionPlan(ctx, newMedia.ID); plan != nil {
			next.VersionID = plan.SourceID
		}
	case change.Subtitles != nil || change.SubtitleOffsetMs != nil || change.SubtitleOffsetUS != nil:
		// A sidecar switch keeps the bytes and the generation.
		current, err := c.replanContext(ctx, r.id)
		if err != nil {
			return SessionView{}, err
		}
		result, err := s.applySubtitles(current, c.Principal, r.item, r.media, next.Subtitles, next.SubtitleOffsetMs, position, "v1-"+r.id+"-r"+strconv.Itoa(r.revision+1), false, next.SubtitleOffsetUS)
		if err != nil {
			return SessionView{}, err
		}
		presentation.Subtitles, presentation.Subtitle = result.files, result.shown

		if result.replaced != nil {
			// The stream changed after all (an offset moved a converted
			// stream's subtitles): new bytes from the position.
			s.adoptReplaced(ctx, &presentation, r.media, result.replaced, playback.V1Choice{AdminMaxVideoBitrateBPS: c.AdminMaxVideoBitrateBPS, Remote: c.Remote})
			presentation.StartPositionMs = position
			mediaGeneration, generation = result.replaced.Generation, r.generation+1
		} else if change.Seek != nil && presentation.converting() {
			restartConversion = true
			presentation.StartPositionMs = position
			generation = r.generation + 1
		}
	case change.Seek != nil && presentation.converting():
		// Production restarts at the position; the client reloads the same URL
		// there because the generation moved.
		restartConversion = true
		presentation.StartPositionMs = position
		generation = r.generation + 1
	}
	if bytes || change.Subtitles != nil || change.SubtitleOffsetMs != nil || change.SubtitleOffsetUS != nil {
		presentation.ChoiceRevision = r.choiceRevision() + 1
	}
	state := r.state
	if change.State != "" && state != "preparing" {
		state = change.State
	}
	bandwidth := r.bandwidth
	if change.ThroughputKbps != nil {
		bandwidth = int64(throughput)
	}
	location := r.location
	if bytes {
		location = "local"
		if c.Remote {
			location = "remote"
		}
	}
	if r.kind == "vod" {
		// A track picked in the player is for this playback: it carries to the
		// queue's next entry and is not remembered anywhere else.
		next.Carry = s.carryChoice(ctx, c.Principal, r.item, media, presentation, change.Audio != nil, change.Subtitles != nil, next.Carry)
	}
	requestJSON, _ := json.Marshal(next)
	presentationJSON, _ := json.Marshal(presentation)
	now := s.now()
	version := next.VersionID
	if version == "" {
		version = r.version
	}
	part := changedPart(r, change, version)
	err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassEstablishedPlayback, func(ctx context.Context, tx *sql.Tx) error {
		// The renderer's unwritten position and watched time land before the
		// change, so a pause, seek or re-plan never loses them.
		if err := s.drainObservationTx(ctx, tx, r.id); err != nil {
			return err
		}
		if bytes {
			selected, err := s.Playback.PresentationDiscTitleTx(ctx, tx, media, mediaGeneration)
			if err != nil {
				return err
			}
			if asked := next.selectedDiscTitle(); asked != nil && (selected == nil || *asked != *selected) {
				return playback.ErrStaleOffer
			}
			next.SelectedDiscTitle = selected
			requestJSON, err = json.Marshal(next)
			if err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET state=?,location=?,version_id=?,part_index=?,request=?,media_session_id=?,media_generation=?,presentation=?,generation=?,position_ms=?,start_position_ms=?,bandwidth_kbps=?,revision=revision+1,updated_ms=?,`+pausedSinceUpdate+` WHERE id=? AND revision=? AND ended_ms=0`,
			state, location, version, part, string(requestJSON), media, mediaGeneration, string(presentationJSON), generation, position, presentation.StartPositionMs, bandwidth, now.UnixMilli(), state, now.UnixMilli(), r.id, r.revision)
		if err != nil {
			return err
		}

		if bytes && c.UploadLimited != nil {
			if _, e := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET upload_limited=? WHERE id=?`, *c.UploadLimited, r.id); e != nil {
				return e
			}
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrRevision
		}
		if bytes {
			if err := s.Playback.PublishPresentationTx(ctx, tx, c.Principal, media, position, &playback.SessionReplacement{ID: r.media, Generation: oldMediaGeneration}, true); err != nil {
				return err
			}
		}
		r.location = location
		r.state, r.version, r.partIndex, r.request, r.media, r.mediaGeneration = state, version, part, string(requestJSON), media, mediaGeneration
		r.presentation, r.generation, r.position, r.startPosition, r.revision, r.updated, r.bandwidth = string(presentationJSON), generation, position, presentation.StartPositionMs, r.revision+1, now.UnixMilli(), bandwidth
		return s.sessionEventsTx(ctx, tx, r, now, false)
	})
	if errors.Is(err, ErrRevision) {
		current, _ := loadRow(ctx, s.DB, id)
		return SessionView{}, &RevisionError{Current: s.view(current), Revision: int64(current.revision)}
	}
	if err != nil {
		return SessionView{}, err
	}
	committed = true
	if restartConversion {
		// A losing revision must leave the current producer and its output
		// intact. The account admission gate still orders this restart before
		// another command can replace this presentation.
		s.Playback.RestartConversion(ctx, r.media, float64(position)/1000)
	}
	if bytes {
		_ = s.Playback.StopV1(context.WithoutCancel(ctx), rMediaBefore)
	}
	s.cancelPreparedFor(ctx, id)
	s.hub.wake()
	return s.view(r), nil
}

var failureCodePattern = regexp.MustCompile(`^[a-z0-9_]{1,48}$`)

// subtitleReplans reports whether a subtitle change has to plan the
// presentation again: the track asked for is a styled or image one (it weighs
// in the engine choice and may need the picture converted), or the track now
// in force is drawn by the engine or burned in (leaving it frees both).
func (s *Service) subtitleReplans(ctx context.Context, c Caller, r row, current Presentation, req *SubtitleRequest) bool {
	if shown := current.Subtitle; shown != nil && shown.Delivery != "sidecar" {
		// A track the client asked to draw itself, from the original handed
		// over beside the stream, never weighed in the plan (subtitleWeight):
		// the picture and the engine were chosen without it, so leaving it
		// changes neither. Anything else burned in or drawn by the engine does.
		asked := r.stored().Subtitles
		if !(shown.Delivery == "device" && asked != nil && asked.Delivery == "embeddedClient" && len(current.Subtitles) > 0) {
			return true
		}
	}
	if d := current.Decision.Video; d != nil && d.Action == "burn" {
		return true
	}
	if req == nil || req.TrackID == TrackOff || req.TrackID == "" || r.version == "" {
		return false
	}
	asset, err := s.partAsset(ctx, r.item, r.version, r.partIndex)
	if err != nil {
		return false
	}
	chosen := resourceByID(s.subtitleResources(ctx, c.Principal, r.item, asset, r.stored().selectedDiscTitle()), string(req.TrackID))
	return subtitleWeight(chosen, req, s.keepsStyling(ctx, c.Principal, chosen)) != ""
}

var seekIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Stop is DELETE /v1/playback/sessions/{id} (spec §5.8): idempotent. One
// writer transaction writes the renderer's unwritten latest sample, then the
// final position, then ends the session, so none of them can be lost between
// the others. A final position counts only from the playing device itself: a
// remote controller cannot say where the renderer was, and the renderer's own
// latest sample stands. An optional generation captures the renderer occurrence;
// expectedGeneration's
// conditional cleanup contract remains a separate StopGeneration operation.
func (s *Service) Stop(ctx context.Context, c Caller, id string, positionMs *int64, generation ...*int) error {
	var captured *int
	if len(generation) > 0 {
		captured = generation[0]
	}
	return s.stopWithReceipt(ctx, c, id, positionMs, captured)
}

// SkipReport is a marker the viewer skipped (spec §4.2): the segment marker's
// id, whether the skip was automatic (the viewer's preference) or manual, and
// the position the skip left from. It is evidence, never transport: the client
// has already seeked.
type SkipReport struct {
	MarkerID   string `json:"markerId"`
	Mode       string `json:"mode"`
	PositionMs int64  `json:"positionMs"`
}
type ReportedError struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// engineFailureCodes are the observation error codes that mean the player itself
// could not play what it was given (as opposed to the network failing).
var engineFailureCodes = map[string]bool{"decode_error": true, "source_not_supported": true, "audio_decode_error": true, "manifest_incompatible": true, "engine_error": true}

// endedNearEndIn reports whether a player that says it reached the end did so
// within the last fifth of the title. A title whose length is unknown is
// taken at its word.
func (s *Service) endedNearEndIn(ctx context.Context, q querier, media string, positionMs int64) bool {
	var duration float64
	if media == "" || q.QueryRowContext(ctx, `SELECT duration FROM playback_sessions WHERE id=?`, media).Scan(&duration) != nil || duration <= 0 {
		return true
	}
	return float64(positionMs) >= duration*1000*0.8
}

// EndReasonPausedTimeout is why a video session paused longer than the
// paused-session limit ended; PausedTimeoutMessage is what its viewer is told.
const (
	EndReasonPausedTimeout = "paused_timeout"
	PausedTimeoutMessage   = "Playback stopped after a long pause."
)

// EndReasonOutsideHours is why a session ended when its account's allowed
// hours closed while it played; OutsideHoursMessage is what its viewer is told.
const (
	EndReasonOutsideHours = "outside_allowed_hours"
	OutsideHoursMessage   = "This account cannot be used at this time of day."
)

// sweeper ends sessions whose lease lapsed (spec §5.8, invariant 4). It runs
// only while sessions exist: it sleeps until the earliest lease, is kicked
// when a session starts, and exits when none remain. Nothing polls idly.
type sweeper struct {
	mu      sync.Mutex
	running bool
	kickCh  chan struct{}
	// pausedAt is when the paused-too-long sweep last ran (at most once a
	// minute, so the common case costs one indexed read per minute).
	pausedAt time.Time
	// hoursAt is when the allowed-hours sweep last ran (also once a minute).
	hoursAt time.Time
}

func (w *sweeper) kick(s *Service) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.kickCh == nil {
		w.kickCh = make(chan struct{}, 1)
	}
	if w.running {
		select {
		case w.kickCh <- struct{}{}:
		default:
		}
		return
	}
	w.running = true
	supervise.Supervise(context.Background(), "playbackv1.lease-sweeper", func(context.Context) { w.run(s) })
}

func (w *sweeper) run(s *Service) {
	ctx := context.Background()
	for {
		var next sql.NullInt64
		if err := s.DB.QueryRowContext(ctx, `SELECT MIN(lease_expires_ms) FROM playback_v1_sessions WHERE ended_ms=0`).Scan(&next); err != nil || !next.Valid {
			w.mu.Lock()
			// Re-check under the lock so a start that raced the exit is swept.
			var again sql.NullInt64
			if err == nil {
				_ = s.DB.QueryRowContext(ctx, `SELECT MIN(lease_expires_ms) FROM playback_v1_sessions WHERE ended_ms=0`).Scan(&again)
			}
			if err != nil || !again.Valid {
				w.running = false
				w.mu.Unlock()
				return
			}
			w.mu.Unlock()
			continue
		}
		wait := time.Until(time.UnixMilli(next.Int64))
		if s.Now != nil {
			wait = time.UnixMilli(next.Int64).Sub(s.now())
		}
		if wait > 0 {
			timer := time.NewTimer(min(wait, time.Hour))
			select {
			case <-timer.C:
			case <-w.kickCh:
				timer.Stop()
				continue
			}
		}
		s.ExpireLapsed(ctx)
		s.sweepPausedIfDue(ctx)
		s.sweepHoursIfDue(ctx)
	}
}

// sweepHoursIfDue ends the sessions of accounts whose allowed hours have
// closed, at most once a minute.
func (s *Service) sweepHoursIfDue(ctx context.Context) {
	s.sweep.mu.Lock()
	if s.now().Sub(s.sweep.hoursAt) < time.Minute {
		s.sweep.mu.Unlock()
		return
	}
	s.sweep.hoursAt = s.now()
	s.sweep.mu.Unlock()
	s.EndOutsideHours(ctx)
}

// EndOutsideHours ends every running session of an account that is now
// outside its allowed hours (a member's access schedule). Admission refuses a
// new playback then; this tells a playback already running, through the
// session's end and its session.updated event, instead of letting it fail on
// its next request. The cost is one read of the accounts with running
// sessions (at most 256) and one schedule check per account, once a minute.
func (s *Service) EndOutsideHours(ctx context.Context) int {
	if s.OutsideHours == nil {
		return 0
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT account_id FROM playback_v1_sessions WHERE ended_ms=0 LIMIT 256`)
	if err != nil {
		return 0
	}
	var accounts []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && id != "" {
			accounts = append(accounts, id)
		}
	}
	rows.Close()
	ended := 0
	for _, account := range accounts {
		if !s.OutsideHours(ctx, account) {
			continue
		}
		ids, err := s.runningSessionsOf(ctx, account)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if s.End(ctx, id, EndReasonOutsideHours, OutsideHoursMessage) == nil {
				ended++
			}
		}
	}
	return ended
}

func (s *Service) runningSessionsOf(ctx context.Context, account string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM playback_v1_sessions WHERE ended_ms=0 AND account_id=? LIMIT 64`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// sweepPausedIfDue ends sessions paused past the limit, at most once a minute.
func (s *Service) sweepPausedIfDue(ctx context.Context) {
	s.sweep.mu.Lock()
	if s.now().Sub(s.sweep.pausedAt) < time.Minute {
		s.sweep.mu.Unlock()
		return
	}
	s.sweep.pausedAt = s.now()
	s.sweep.mu.Unlock()
	s.EndPausedTooLong(ctx)
}

// ExpireLapsed ends every session whose lease has lapsed.
func (s *Service) ExpireLapsed(ctx context.Context) int {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM playback_v1_sessions WHERE ended_ms=0 AND lease_expires_ms<=? ORDER BY lease_expires_ms LIMIT 256`, s.now().UnixMilli())
	if err != nil {
		return 0
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_ = s.endWhen(ctx, id, "lease_expired", "", func(_ context.Context, _ *sql.Tx, current row) (bool, error) {
			// The lease is re-read after the renderer's latest sample was
			// written: a session heard from since it was chosen keeps playing.
			return current.lease <= s.now().UnixMilli(), nil
		})
	}
	return len(ids)
}

// EndPausedTooLong ends video sessions paused longer than PausedLimit (Plex
// "Terminate Sessions Paused for Longer Than"). Listening sessions (kind
// "audio": songs, tracks, audiobooks) and Live/Library channels (kinds "live"
// and "channel", see isChannel) are never ended. Each ends through End like an
// administrator terminate, so its slot and transcode free through the normal
// end path. At most 64 sessions per call; the sweep loop calls it at most once
// a minute, so the idle cost is one indexed read per minute.
func (s *Service) EndPausedTooLong(ctx context.Context) int {
	var limit time.Duration
	if s.PausedLimit != nil {
		limit = s.PausedLimit(ctx)
	}
	if limit <= 0 {
		return 0
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM playback_v1_sessions WHERE ended_ms=0 AND state='paused' AND paused_since_ms>0 AND paused_since_ms<=? AND kind NOT IN ('audio','live','channel') ORDER BY paused_since_ms LIMIT 64`, s.now().Add(-limit).UnixMilli())
	if err != nil {
		return 0
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	cutoff := s.now().Add(-limit).UnixMilli()
	for _, id := range ids {
		_ = s.endWhen(ctx, id, EndReasonPausedTimeout, PausedTimeoutMessage, func(_ context.Context, _ *sql.Tx, current row) (bool, error) {
			// Still paused since before the cutoff, and the renderer has not
			// since said it is playing again (a resume not yet written).
			if state, _, ok := s.latestObservedState(id); ok && state == "playing" {
				return false, nil
			}
			return current.state == "paused" && current.pausedSince > 0 && current.pausedSince <= cutoff, nil
		})
	}
	return len(ids)
}

// changedPart is the part a change plays: the one it names, else the current
// one, except that another version starts at its first part.
func changedPart(r row, change Change, version string) int {
	switch {
	case change.PartIndex != nil:
		return *change.PartIndex
	case version != r.version:
		return 0
	}
	return r.partIndex
}
