package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// AirPlayPrepare fences preparation by choices, not by observation/lease revisions.
type AirPlayPrepare struct {
	Generation     int   `json:"generation"`
	ChoiceRevision int   `json:"choiceRevision"`
	PositionMs     int64 `json:"positionMs"`
}
type AirPlayPresentation struct {
	SessionID      string       `json:"sessionId"`
	Generation     int          `json:"generation"`
	ChoiceRevision int          `json:"choiceRevision"`
	Presentation   Presentation `json:"presentation"`
}

func (r row) choiceRevision() int {
	n := r.presentationView().ChoiceRevision
	if n < 1 {
		return 1
	}
	return n
}

// PrepareAirPlay leaves the local presentation and its choices untouched. The native HLS
// source contains the exact selected audio and text, or a burn of that selected drawn track.
// It shares the live owner's stream slot and cannot outlive or out-authorize its owner.
func (s *Service) PrepareAirPlay(ctx context.Context, c Caller, id string, in AirPlayPrepare) (AirPlayPresentation, error) {
	release := s.admission.hold(c.Principal.AccountID)
	defer release()
	r, err := s.session(ctx, c, id)
	if err != nil {
		return AirPlayPresentation{}, err
	}
	if r.ended > 0 {
		return AirPlayPresentation{}, ErrEnded
	}
	if r.device != c.DeviceID || r.kind != "vod" || r.media == "" {
		return AirPlayPresentation{}, identity.ErrForbidden
	}
	if in.Generation != r.generation || in.ChoiceRevision != r.choiceRevision() {
		return AirPlayPresentation{}, &RevisionError{Current: s.view(r)}
	}
	if in.PositionMs < 0 || in.PositionMs > maxStartPositionMs {
		return AirPlayPresentation{}, &FieldError{Path: "positionMs"}
	}
	var prior string
	var generation, revision int
	var raw string
	err = dbwork.QueryRow(ctx, s.DB, `SELECT a.media_session_id,a.generation,a.choice_revision,a.presentation FROM playback_airplay_presentations a JOIN playback_sessions p ON p.id=a.media_session_id WHERE a.session_id=? AND p.state NOT IN('stopped','ended','failed')`, id).Scan(&prior, &generation, &revision, &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AirPlayPresentation{}, err
	}
	if generation == r.generation && revision == r.choiceRevision() {
		var p Presentation
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return AirPlayPresentation{}, err
		}
		return AirPlayPresentation{id, r.generation, r.choiceRevision(), p}, nil
	}
	ctx, err = c.replanContext(ctx, id)
	if err != nil {
		return AirPlayPresentation{}, err
	}
	request := r.stored()
	// Read the actual current selection, including automatic/default selection and its timing;
	// a stored omitted request is not proof that the viewer currently has subtitles off.
	view := r.presentationView()
	request.VersionID = r.version
	if view.Quality != nil {
		request.Quality = *view.Quality
	}
	request.Audio = &AudioRequest{TrackID: view.AudioTrackID}
	if view.AudioTrackID == "" {
		request.Audio = r.stored().Audio
	}
	request.Subtitles = &SubtitleRequest{TrackID: TrackOff}
	exactOffset := ""
	if s.Subtitles == nil && view.Subtitle != nil {
		return AirPlayPresentation{}, playback.ErrIncompatible
	}
	if s.Subtitles != nil {
		plan, e := s.Subtitles.Plan(ctx, c.Principal, r.item, r.media)
		if e != nil {
			return AirPlayPresentation{}, e
		}
		if plan.Mode == "track" && plan.Selected != nil {
			delivery := "sidecar"
			if plan.Renderer == "client" || plan.Renderer == "burn_in" {
				delivery = "burn"
			}
			request.Subtitles = &SubtitleRequest{TrackID: TrackRef(plan.Selected.ID), Delivery: delivery}
			// Keep the server's exact microsecond timing through the receiver preparation.
			var micros int64
			if micros, e = strconv.ParseInt(plan.OffsetUS, 10, 64); e != nil {
				return AirPlayPresentation{}, &FieldError{Path: "subtitleOffsetMs"}
			}
			request.SubtitleOffsetMs = micros / 1000
			exactOffset = plan.OffsetUS
		}
	}
	media, p, err := s.present(ctx, c, presentationTarget{item: r.item, version: r.version, part: r.partIndex, request: request, startMs: in.PositionMs, requestID: newID("airplay_"), subtitleOp: newID("airplay_sub_"), privateFor: r.media, privateUntilMs: r.lease, airPlay: true, subtitleOffsetUS: exactOffset})
	if err != nil {
		if media.ID != "" {
			_ = s.Playback.StopV1(context.WithoutCancel(ctx), media.ID)
		}
		return AirPlayPresentation{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.Playback.StopV1(context.WithoutCancel(ctx), media.ID)
		}
	}()
	if request.Subtitles.TrackID != TrackOff && (p.Subtitle == nil || p.Subtitle.TrackID != string(request.Subtitles.TrackID) || p.Subtitle.Delivery != request.Subtitles.Delivery) {
		return AirPlayPresentation{}, playback.ErrIncompatible
	}
	if request.Subtitles.TrackID == TrackOff && p.Subtitle != nil {
		return AirPlayPresentation{}, playback.ErrIncompatible
	}
	if p.Mode != "stream" {
		return AirPlayPresentation{}, playback.ErrIncompatible
	}
	p.Generation = r.generation
	p.ChoiceRevision = r.choiceRevision()
	encoded, err := json.Marshal(p)
	if err != nil {
		return AirPlayPresentation{}, err
	}
	err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassEstablishedPlayback, func(ctx context.Context, tx *sql.Tx) error {
		now, e := loadRow(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = c.authorizeSession(ctx, now); e != nil {
			return e
		}
		if now.ended > 0 || now.generation != r.generation || now.choiceRevision() != r.choiceRevision() || now.media != r.media {
			return &RevisionError{Current: s.view(now)}
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO playback_airplay_presentations VALUES(?,?,?,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET generation=excluded.generation,choice_revision=excluded.choice_revision,media_session_id=excluded.media_session_id,media_generation=excluded.media_generation,presentation=excluded.presentation,created_ms=excluded.created_ms`, id, r.generation, r.choiceRevision(), media.ID, media.Generation, string(encoded), s.now().UnixMilli())
		return e
	})
	if err != nil {
		return AirPlayPresentation{}, err
	}
	committed = true
	if prior != "" {
		_ = s.Playback.StopV1(context.WithoutCancel(ctx), prior)
	}
	return AirPlayPresentation{id, r.generation, r.choiceRevision(), p}, nil
}

func (s *Service) retireAirPlayTx(ctx context.Context, tx *sql.Tx, id string) error {
	var media string
	err := tx.QueryRowContext(ctx, `SELECT media_session_id FROM playback_airplay_presentations WHERE session_id=?`, id).Scan(&media)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE playback_sessions SET state='stopped' WHERE id=?`, media); err != nil {
		return err
	}
	return dbwork.AfterCommit(tx, func() { s.Playback.StopFencedV1(media) })
}
