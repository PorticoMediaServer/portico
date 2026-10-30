package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// The existing V1 command owns the accepting writer. The subtitle service may
// prepare/cache outside SQL, but its generation and V1 revision are not
// committed separately. This original command is never recovered from rows.
type privateSubtitleAcceptance struct {
	service         *Service
	ctx             context.Context
	caller          Caller
	original        row
	change          Change
	next            storedRequest
	transition      *playback.DiscSubtitleTransition
	mu              sync.Mutex
	tx              *sql.Tx
	published       bool
	request         string
	result          row
	restart         bool
	restartPosition int64
}

func (a *privateSubtitleAcceptance) current(ctx context.Context, tx *sql.Tx, generation int) error {
	bound, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return err
	}
	ctx = bound
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if a.caller.Admit == nil || a.caller.AuthorizeItem == nil {
		return ErrNotFound
	}
	r, err := loadRow(ctx, tx, a.original.id)
	if err != nil {
		return err
	}
	a.mu.Lock()
	published := a.tx == tx && a.published
	request := a.request
	a.mu.Unlock()
	expectedRequest := a.original.request
	if published {
		expectedRequest = request
	}
	expectedRevision := a.original.revision
	if published {
		expectedRevision++
	}
	expectedGeneration := a.original.generation
	if generation != a.original.mediaGeneration || published && a.change.Seek != nil && a.original.presentationView().converting() {
		expectedGeneration++
	}
	if r.revision != expectedRevision || r.generation != expectedGeneration || r.media != a.original.media || r.mediaGeneration != generation || r.request != expectedRequest || r.item != a.original.item || r.device != a.original.device || r.startKey != a.original.startKey || r.startDigest != a.original.startDigest || r.queueID != a.original.queueID || r.entryID != a.original.entryID || r.ended != 0 {
		return ErrRevision
	}
	if err = a.caller.authorizeSession(ctx, r); err != nil {
		return err
	}
	return a.caller.admit(WithReplan(ctx, r.id, r.media), r.item)
}

func (a *privateSubtitleAcceptance) CheckTx(ctx context.Context, tx *sql.Tx) error {
	return a.transition.CheckTx(ctx, tx)
}

func (a *privateSubtitleAcceptance) BeforeGenerationTx(ctx context.Context, tx *sql.Tx, session string, generation int) error {
	a.mu.Lock()
	if a.tx != nil && a.tx != tx {
		a.mu.Unlock()
		return ErrRevision
	}
	a.tx = tx
	a.mu.Unlock()
	// The renderer's unwritten evidence lands under the generation it was
	// observed in, before that generation is replaced.
	if err := a.service.drainObservationTx(ctx, tx, a.original.id); err != nil {
		return err
	}
	return a.transition.BeforeGenerationTx(ctx, tx, session, generation)
}

func (a *privateSubtitleAcceptance) AfterGenerationTx(ctx context.Context, tx *sql.Tx, session string, generation int, resource string) (string, error) {
	a.mu.Lock()
	exact := a.tx == tx
	a.mu.Unlock()
	if !exact {
		return "", ErrRevision
	}
	if session != a.original.media || generation != a.original.mediaGeneration+1 {
		return "", ErrRevision
	}
	result, err := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET media_generation=?,generation=generation+1 WHERE id=? AND revision=? AND media_session_id=? AND media_generation=? AND ended_ms=0`, generation, a.original.id, a.original.revision, session, a.original.mediaGeneration)
	if err != nil {
		return "", err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return "", ErrRevision
	}
	return a.transition.AfterGenerationTx(ctx, tx, session, generation, resource)
}

func (a *privateSubtitleAcceptance) AcceptTx(ctx context.Context, tx *sql.Tx, plan subtitles.PlaybackPlan) error {
	bound, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return err
	}
	ctx = bound
	if err := a.CheckTx(ctx, tx); err != nil {
		return err
	}
	a.mu.Lock()
	if a.tx != nil && a.tx != tx {
		a.mu.Unlock()
		return ErrRevision
	}
	a.tx = tx
	a.mu.Unlock()
	r := a.original
	view := r.presentationView()
	result := subtitlePlanResult(plan, subtitleResult{files: []SubtitleFile{}})
	view.Subtitles, view.Subtitle = result.files, result.shown
	view.ChoiceRevision = r.choiceRevision() + 1
	position := r.position
	if a.change.PositionMs != nil {
		position = *a.change.PositionMs
	}
	if a.change.Seek != nil {
		position = a.change.Seek.PositionMs
	}
	if plan.Generation != r.mediaGeneration {
		if plan.Generation != r.mediaGeneration+1 || plan.Presentation == nil {
			return ErrRevision
		}
		a.service.adoptReplaced(ctx, &view, r.media, plan.Presentation, playback.V1Choice{AdminMaxVideoBitrateBPS: a.caller.AdminMaxVideoBitrateBPS, Remote: a.caller.Remote})
		view.StartPositionMs = position
		r.generation++
	} else if a.change.Seek != nil && view.converting() {
		r.generation++
		view.StartPositionMs = position
		a.restart = true
		a.restartPosition = position
	}
	// Persist the ACTUAL mapped child resource in the current request. It must
	// never retain the retired parent's resource ID as a future command target.
	next := a.next
	if plan.Selected != nil && next.Subtitles != nil {
		copy := *next.Subtitles
		copy.TrackID = TrackRef(plan.Selected.ID)
		next.Subtitles = &copy
	}
	request, err := json.Marshal(next)
	if err != nil {
		return err
	}
	presentation, err := json.Marshal(view)
	if err != nil {
		return err
	}
	state := r.state
	if a.change.State != "" && state != "preparing" {
		state = a.change.State
	}
	bandwidth := r.bandwidth
	if a.change.ThroughputKbps != nil {
		bandwidth = int64(*a.change.ThroughputKbps)
	}
	now := a.service.now()
	res, err := tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET state=?,request=?,presentation=?,media_generation=?,generation=?,position_ms=?,start_position_ms=?,bandwidth_kbps=?,revision=revision+1,updated_ms=?,`+pausedSinceUpdate+` WHERE id=? AND revision=? AND media_session_id=? AND media_generation=? AND ended_ms=0`, state, string(request), string(presentation), plan.Generation, r.generation, position, view.StartPositionMs, bandwidth, now.UnixMilli(), state, now.UnixMilli(), r.id, r.revision, r.media, plan.Generation)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrRevision
	}
	a.mu.Lock()
	a.request = string(request)
	a.published = true
	a.mu.Unlock()
	if err = a.CheckTx(ctx, tx); err != nil {
		return err
	}
	r.state, r.request, r.presentation, r.mediaGeneration, r.position, r.startPosition, r.bandwidth, r.updated = state, string(request), string(presentation), plan.Generation, position, view.StartPositionMs, bandwidth, now.UnixMilli()
	r.revision++
	if err = a.service.sessionEventsTx(ctx, tx, r, now, false); err != nil {
		return err
	}
	a.result = r
	return nil
}

func (s *Service) privateSubtitlePatch(ctx context.Context, c Caller, r row, change Change) (SessionView, bool, error) {
	if s.Playback == nil || s.Subtitles == nil {
		return SessionView{}, false, nil
	}
	// Freeze nested request values before preparation can perform any IO.
	raw, err := json.Marshal(change)
	if err != nil {
		return SessionView{}, true, err
	}
	if err = json.Unmarshal(raw, &change); err != nil {
		return SessionView{}, true, err
	}
	a := &privateSubtitleAcceptance{service: s, ctx: ctx, caller: c, original: r, change: change, next: r.stored()}
	transition, private, err := s.Playback.PrepareDiscSubtitleTransition(ctx, c.Principal, r.media, a.current)
	if err != nil || !private {
		return SessionView{}, private, err
	}
	a.transition = transition
	defer transition.Close()
	if c.Admit == nil || c.AuthorizeItem == nil || change.Replan || change.Audio != nil || change.Quality != nil || change.VersionID != "" || change.PartIndex != nil || change.Failure != nil {
		return SessionView{}, true, playback.ErrIncompatible
	}
	if change.Subtitles != nil {
		a.next.Subtitles = change.Subtitles
	}
	if change.Subtitles != nil || change.SubtitleOffsetMs != nil {
		a.next.SubtitleOffsetUS = ""
	}
	if change.SubtitleOffsetUS != nil {
		a.next.SubtitleOffsetUS = *change.SubtitleOffsetUS
	}
	if change.SubtitleOffsetMs != nil {
		a.next.SubtitleOffsetMs = *change.SubtitleOffsetMs
	}
	if change.State != "" {
		a.next.State = change.State
	}
	current, err := c.replanContext(ctx, r.id)
	if err != nil {
		return SessionView{}, true, err
	}
	current = transition.Context(current)
	plan, err := s.Subtitles.Plan(current, c.Principal, r.item, r.media)
	if err != nil {
		return SessionView{}, true, err
	}
	position := r.position
	if change.PositionMs != nil {
		position = *change.PositionMs
	}
	if change.Seek != nil {
		position = change.Seek.PositionMs
	}
	m := subtitles.SelectRequest{OperationID: "v1-" + r.id + "-r" + strconv.Itoa(r.revision+1), Generation: plan.Generation, ExpectedRevision: plan.Revision, Mode: "off", OffsetUS: "0", PositionUS: strconv.FormatInt(max(position, 0)*1000, 10)}
	if req := a.next.Subtitles; req != nil && req.TrackID != "" && req.TrackID != TrackOff {
		var chosen *subtitles.Resource
		for i := range plan.Resources {
			if plan.Resources[i].ID == string(req.TrackID) {
				chosen = &plan.Resources[i]
				break
			}
		}
		if chosen == nil || !chosen.Enabled {
			return SessionView{}, true, &FieldError{Path: "subtitles.trackId"}
		}
		burn := chosen.Renderer == "burn_in"
		if req.Delivery == "burn" && !burn || req.Delivery == "sidecar" && burn {
			return SessionView{}, true, &FieldError{Path: "subtitles.delivery"}
		}
		if req.Delivery == "embeddedClient" {
			m.Rendering = subtitles.RenderingFormat(chosen.Format)
		}
		m.Mode, m.ResourceID, m.ResourceRevision = "track", chosen.ID, chosen.Revision
		m.OffsetUS = strconv.FormatInt(a.next.SubtitleOffsetMs*1000, 10)
		if a.next.SubtitleOffsetUS != "" {
			m.OffsetUS = a.next.SubtitleOffsetUS
		}
	}
	if _, err = s.Subtitles.SelectAccepting(current, c.Principal, r.item, r.media, m, a); err != nil {
		return SessionView{}, true, err
	}
	if a.result.id == "" {
		return SessionView{}, true, ErrRevision
	}
	if a.restart {
		s.Playback.RestartConversion(ctx, r.media, float64(a.restartPosition)/1000)
	}
	s.cancelPreparedFor(ctx, r.id)
	s.hub.wake()
	return s.view(a.result), true, nil
}
