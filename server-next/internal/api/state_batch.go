package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// maxBatchEdits is the most edits one batch carries. A selection or an offline queue larger
// than this is sent in several batches.
const maxBatchEdits = 500

// StateBatch is several state edits at once: what a device queued while out of reach, or one
// action on several selected titles. Each edit is the body of PUT /v1/items/{id}/state with
// the item's id.
type StateBatch struct {
	Items []StateBatchEdit `json:"items"`
}

// StateBatchEdit is one title's edit in a batch.
type StateBatchEdit struct {
	ItemID string `json:"itemId"`
	StateRequest
}

// StateBatchReply says what happened title by title, in the order sent: the state as it now
// stands, or why that one edit was refused. The rest of the batch is done either way.
type StateBatchReply struct {
	Items []StateBatchResult `json:"items"`
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
		if len(req.Items) == 0 || len(req.Items) > maxBatchEdits {
			return StateBatchReply{}, ErrField("items", "Send 1 to 500 edits at a time.")
		}
		edits := make([]db.ItemEdit, len(req.Items))
		for n, e := range req.Items {
			if e.MadeAgoMs < 0 {
				return StateBatchReply{}, ErrField("items.madeAgoMs", "This cannot be negative.")
			}
			edits[n] = db.ItemEdit{ItemID: e.ItemID, StateEdit: db.StateEdit{Watched: e.Watched, PositionMS: e.PositionMs,
				PositionText: e.Position, Favorite: e.Favorite, InList: e.InList, Hidden: e.Hidden, Rating: e.Rating.Value,
				ClearRating: e.Rating.Set && e.Rating.Value == nil, Ago: time.Duration(e.MadeAgoMs) * time.Millisecond}}
		}
		states, refusals, err := s.db.SetStates(r.Context(), db.User, viewer(r), edits)
		if err != nil {
			return StateBatchReply{}, err
		}
		out := StateBatchReply{Items: make([]StateBatchResult, len(edits))}
		for n, refusal := range refusals {
			out.Items[n].ItemID = req.Items[n].ItemID
			switch {
			case errors.Is(refusal, db.ErrNotFound):
				out.Items[n].Error = &BatchError{Code: "not_found", Message: "This title is no longer here."}
			case errors.Is(refusal, db.ErrInvalid):
				out.Items[n].Error = &BatchError{Code: "invalid_request",
					Message: strings.TrimPrefix(refusal.Error(), db.ErrInvalid.Error()+": ")}
			default:
				state := stateShape(states[n])
				out.Items[n].State = &state
			}
		}
		return out, nil
	})
}
