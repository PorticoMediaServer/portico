package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// StateBatch is the body of POST /v1/items/state:batch: what a device kept while it could
// not reach the server, downloads played with no server above all. Edits are the state
// route's changes, each with how long ago it was made; plays are where each playing got to.
type StateBatch struct {
	Edits []StateBatchEdit `json:"edits"`
	Plays []OfflinePlay    `json:"plays"`
}

// StateBatchEdit is one title's change, as PUT /v1/items/{id}/state takes it.
type StateBatchEdit struct {
	ItemID string `json:"itemId"`
	StateRequest
}

// OfflinePlay is where one playing of a title had got to. PlayID is the device's own name
// for that playing, so a play sent twice is counted once; PlayedMs is how long it actually
// played; MadeAgoMs is how long ago, by the device's own measure, it was at PositionMs.
type OfflinePlay struct {
	ItemID     string `json:"itemId"`
	PlayID     string `json:"playId"`
	PositionMs int64  `json:"positionMs"`
	PlayedMs   int64  `json:"playedMs"`
	MadeAgoMs  int64  `json:"madeAgoMs"`
}

// StateBatchReply says, title by title, where each change left things. A change for a
// title that is no longer on the server, or no longer this profile's to see, is dropped
// quietly: it has nowhere to go and sending it again changes nothing.
type StateBatchReply struct {
	Items []StateBatchResult `json:"items"`
	Plays []OfflinePlayDone  `json:"plays"`
}

// StateBatchResult is one edit's outcome: the title's state now, or dropped.
type StateBatchResult struct {
	ItemID  string     `json:"itemId"`
	Dropped bool       `json:"dropped,omitempty"`
	State   *ItemState `json:"state,omitempty"`
}

// OfflinePlayDone says whether a playing has reached the end and been counted.
type OfflinePlayDone struct {
	PlayID   string `json:"playId"`
	Finished bool   `json:"finished"`
}

// maxBatch bounds one batch; a device with more sends several.
const maxBatch = 500

func (s *Server) stateBatchRoutes() {
	// The later change wins field by field, a play is counted once per playId and a repeat
	// changes nothing, so the batch is safe to send again without a key. Playing a download
	// uses none of the account's streams: nothing here touches playback.
	Handle(s, "POST /v1/items/state:batch", WithProfile, s.stateBatch)
}

func (s *Server) stateBatch(r *http.Request, req StateBatch) (StateBatchReply, error) {
	if len(req.Edits)+len(req.Plays) > maxBatch {
		return StateBatchReply{}, ErrInvalid("Send at most 500 changes at once.")
	}
	edits := make([]db.ItemEdit, len(req.Edits))
	for n, e := range req.Edits {
		if e.MadeAgoMs < 0 || e.MadeAgoMs > db.MaxEditAge.Milliseconds() {
			return StateBatchReply{}, ErrField("edits.madeAgoMs", "This must be between 0 and 35 days.")
		}
		edits[n] = db.ItemEdit{ItemID: e.ItemID, StateEdit: db.StateEdit{Watched: e.Watched, PositionMS: e.PositionMs, PositionText: e.Position,
			Favorite: e.Favorite, InList: e.InList, Hidden: e.NotInterested, Rating: e.Rating.Value, ClearRating: e.Rating.Set && e.Rating.Value == nil,
			DismissContinue: e.ContinueDismissed, Ago: time.Duration(e.MadeAgoMs) * time.Millisecond}}
	}
	v := viewer(r)
	out := StateBatchReply{Items: make([]StateBatchResult, len(req.Edits)), Plays: make([]OfflinePlayDone, len(req.Plays))}
	states, refusals, err := s.db.SetStates(r.Context(), db.User, v, edits)
	if err != nil {
		return StateBatchReply{}, err
	}
	for n, e := range req.Edits {
		out.Items[n].ItemID = e.ItemID
		if refusals[n] != nil {
			out.Items[n].Dropped = true
			continue
		}
		state := stateShape(states[n])
		out.Items[n].State = &state
	}
	if len(req.Plays) == 0 {
		return out, nil
	}
	var deviceID int64
	if c := CallerFrom(r.Context()); c.SessionID != 0 {
		if device, err := s.db.SessionDevice(r.Context(), c.SessionID); err == nil {
			deviceID = device.ID
		}
	}
	batch := make([]db.Progress, len(req.Plays))
	now := s.now()
	for n, p := range req.Plays {
		if p.PlayID == "" || len(p.PlayID) > 128 || p.MadeAgoMs < 0 || p.MadeAgoMs > db.MaxEditAge.Milliseconds() {
			return StateBatchReply{}, ErrField("plays", "Each play needs a playId of up to 128 characters and a madeAgoMs of 0 to 35 days.")
		}
		// A device names its own plays; two devices that chose the same name are told apart.
		batch[n] = db.Progress{Viewer: v, ItemID: p.ItemID, DeviceID: deviceID, PlayID: "offline:" + strconv.FormatInt(deviceID, 10) + ":" + p.PlayID,
			PositionMS: p.PositionMs,
			At:         now.Add(-time.Duration(p.MadeAgoMs) * time.Millisecond), WatchedMS: max(p.PlayedMs, 0)}
	}
	finished, err := s.db.SaveProgress(r.Context(), db.User, batch)
	for n, p := range req.Plays {
		out.Plays[n] = OfflinePlayDone{PlayID: p.PlayID, Finished: finished[n]}
	}
	return out, err
}
