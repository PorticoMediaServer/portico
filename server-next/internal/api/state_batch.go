package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// maxBatchEdits is the most edits and plays one batch carries together. A selection or an
// offline queue larger than this is sent in several batches.
const maxBatchEdits = 500

// StateBatch is several state edits at once: what a device queued while out of reach, or one
// action on several selected titles. Each edit is the body of PUT /v1/items/{id}/state with
// the item's id.
type StateBatch struct {
	Items []StateBatchEdit `json:"items"`
	// Plays are where each playing a device made while out of reach had got to: a download
	// watched or listened to with no server. Each is counted as a play once, however often
	// the batch is sent.
	Plays []OfflinePlay `json:"plays,omitempty"`
}

// OfflinePlay is one playing of a title made out of reach. PlayID is the device's own name
// for that playing, the same each time it is sent; it need be unique only on that device.
// PositionMs is where it got to, PlayedMs how long it actually played, and MadeAgoMs how long
// ago, by the device's own measure, it was there (0 to 35 days).
type OfflinePlay struct {
	ItemID     string `json:"itemId"`
	PlayID     string `json:"playId"`
	PositionMs int64  `json:"positionMs"`
	PlayedMs   int64  `json:"playedMs"`
	MadeAgoMs  int64  `json:"madeAgoMs"`
}

// OfflinePlayResult is one play's outcome: whether it reached the end and was counted, or why
// it was refused (not_found: the title is gone, or not this profile's to see; invalid_request).
type OfflinePlayResult struct {
	ItemID   string      `json:"itemId"`
	PlayID   string      `json:"playId"`
	Finished bool        `json:"finished"`
	Error    *BatchError `json:"error,omitempty"`
}

// StateBatchEdit is one title's edit in a batch.
type StateBatchEdit struct {
	ItemID string `json:"itemId"`
	StateRequest
}

// StateBatchReply says what happened title by title, in the order sent: the state as it now
// stands, or why that one edit was refused. The rest of the batch is done either way.
type StateBatchReply struct {
	Items []StateBatchResult  `json:"items"`
	Plays []OfflinePlayResult `json:"plays,omitempty"`
}

// StateBatchResult is one title's outcome. Error is absent when the edit applied (or lost to
// a later edit, which is not a refusal); State is absent when it was refused.
type StateBatchResult struct {
	ItemID string      `json:"itemId"`
	State  *ItemState  `json:"state,omitempty"`
	Error  *BatchError `json:"error,omitempty"`
}

// BatchError is why one edit in a batch was refused: not_found (gone, or not this profile's
// to see) or invalid_request.
type BatchError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) stateBatchRoutes() {
	// Every edit is applied in one transaction. Edits set state rather than add to it, so a
	// batch sent again after a lost reply changes nothing more; an Idempotency-Key is accepted
	// and not needed. A show or season marked here offers no undo.
	Handle(s, "POST /v1/items/state:batch", WithProfile, func(r *http.Request, req StateBatch) (StateBatchReply, error) {
		if n := len(req.Items) + len(req.Plays); len(req.Items) == 0 && len(req.Plays) == 0 || n > maxBatchEdits {
			return StateBatchReply{}, ErrField("items", "Send 1 to 500 edits and plays at a time.")
		}
		out := StateBatchReply{Items: make([]StateBatchResult, len(req.Items))}
		edits := make([]db.ItemEdit, 0, len(req.Items))
		positions := make([]int, 0, len(req.Items))
		for n, e := range req.Items {
			out.Items[n].ItemID = e.ItemID
			if e.MadeAgoMs < 0 || e.MadeAgoMs > (35*24*time.Hour).Milliseconds() {
				out.Items[n].Error = &BatchError{Code: "invalid_request", Message: "An edit must be between now and 35 days old."}
				continue
			}
			positions = append(positions, n)
			edits = append(edits, db.ItemEdit{ItemID: e.ItemID, StateEdit: db.StateEdit{Watched: e.Watched, PositionMS: e.PositionMs,
				PositionText: e.Position, Favorite: e.Favorite, InList: e.InList, Hidden: e.NotInterested,
				DismissContinue: e.ContinueDismissed, Rating: e.Rating.Value,
				ClearRating: e.Rating.Set && e.Rating.Value == nil, Ago: time.Duration(e.MadeAgoMs) * time.Millisecond}})
		}
		states, refusals, err := s.db.SetStates(r.Context(), db.User, viewer(r), edits)
		if err != nil {
			return StateBatchReply{}, err
		}
		if out.Plays, err = s.offlinePlays(r, req.Plays); err != nil {
			return StateBatchReply{}, err
		}
		for n, refusal := range refusals {
			result := &out.Items[positions[n]]
			switch {
			case errors.Is(refusal, db.ErrNotFound):
				result.Error = &BatchError{Code: "not_found", Message: "This title is no longer here."}
			case errors.Is(refusal, db.ErrInvalid):
				result.Error = &BatchError{Code: "invalid_request",
					Message: strings.TrimPrefix(refusal.Error(), db.ErrInvalid.Error()+": ")}
			default:
				state := stateShape(states[n])
				result.State = &state
			}
		}
		return out, nil
	})
}

// offlinePlays saves the plays of a batch, a few hundred to a write, and answers each in the
// order sent. A play's id is kept with the device's, so two devices that chose the same id
// each count. A play's time is never more than the title lasts. Playing a download uses none
// of the account's streams: nothing here touches playback.
func (s *Server) offlinePlays(r *http.Request, plays []OfflinePlay) ([]OfflinePlayResult, error) {
	if len(plays) == 0 {
		return nil, nil
	}
	var device int64
	if c := CallerFrom(r.Context()); c.SessionID != 0 {
		if d, err := s.db.SessionDevice(r.Context(), c.SessionID); err == nil {
			device = d.ID
		}
	}
	out := make([]OfflinePlayResult, len(plays))
	batch := make([]db.Progress, 0, len(plays))
	positions := make([]int, 0, len(plays))
	now := s.now()
	for n, p := range plays {
		out[n] = OfflinePlayResult{ItemID: p.ItemID, PlayID: p.PlayID}
		switch {
		case p.PlayID == "" || len(p.PlayID) > 128:
			out[n].Error = &BatchError{Code: "invalid_request", Message: "A play needs a playId of up to 128 characters."}
			continue
		case p.MadeAgoMs < 0 || p.MadeAgoMs > db.MaxEditAge.Milliseconds():
			out[n].Error = &BatchError{Code: "invalid_request", Message: "A play must be between now and 35 days old."}
			continue
		}
		positions = append(positions, n)
		batch = append(batch, db.Progress{Viewer: viewer(r), ItemID: p.ItemID, DeviceID: device,
			PlayID: "offline:" + strconv.FormatInt(device, 10) + ":" + p.PlayID, PositionMS: max(p.PositionMs, 0),
			At: now.Add(-time.Duration(p.MadeAgoMs) * time.Millisecond), WatchedMS: max(p.PlayedMs, 0)})
	}
	finished, gone, err := s.db.SavePlays(r.Context(), db.User, batch)
	for n, at := range positions {
		if gone[n] {
			out[at].Error = &BatchError{Code: "not_found", Message: "This title is no longer here."}
		}
		out[at].Finished = finished[n]
	}
	return out, err
}
