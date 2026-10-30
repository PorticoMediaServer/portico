package playbackv1

// Exact channel SessionView tags for the observation ack (O-091).
//
// A channel player reports about once a second, and its ack carries the current
// SessionView only while the tag it holds is stale. Producing that tag by
// reading and hashing the whole view every second read the session twice: once
// in endIfChannelRefused and once in channelPresentation, then again in the
// snapshot Get. The tag is a change detector, so this file derives it from the
// same maintained state the view is made from, in one read snapshot:
//
//   - the v1 row, including every field view() reads and the stored request
//     whose selected title and preparing state reach the view;
//   - the channel state the linear runtime already exposes through ReadTx
//     (programme, window, clocks, seek, confirmed position, stream), which also
//     rechecks the viewer's family authority, lease and selected source; and
//   - the observed-output snapshot the view carries, read from the same
//     snapshot (a new report of identical facts does not change the view).
//
// A refusal or read error never becomes a partial tag: the producer returns an
// error and the route falls back to the full Get it has always done, which ends
// a refused channel or shows the ended/default view. A tuner deadline adds the
// server clock (NowMS) to the stamp, so that view keeps changing exactly as it
// does today.
//
// When the tag differs the view is rebuilt from this same snapshot (the row,
// channel state and observed output already read), never from a later second
// read, so the tag and the Session it is sent with always describe one state.
//
// The tag is opaque to clients: they keep the last one the server sent and
// compare it, so a tag over these sources is equivalent to a hash of the
// rendered JSON as long as it changes whenever any rendered field can.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// errChannelViewFallback asks the route for the ordinary full view read.
var errChannelViewFallback = errors.New("playbackv1: channel view needs the full session read")

// channelViewStamp is every input to a channel's SessionView. The channel state
// is kept whole: including a field the view ignores can only cost an extra
// view, never hide a change.
type channelViewStamp struct {
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	Kind      string `json:"kind"`
	Role      string `json:"role"`
	State     string `json:"state"`
	Ended     int64  `json:"ended"`
	EndReason string `json:"endReason"`
	Message   string `json:"message"`
	ItemID    string `json:"itemId"`
	VersionID string `json:"versionId"`
	ChannelID string `json:"channelId"`
	QueueID   string `json:"queueId"`
	EntryID   string `json:"entryId"`
	Request   string `json:"request"`
	Lease     int64  `json:"lease"`
	// Generation is the session generation observed-output rows are bound to.
	Generation int `json:"generation"`
	// Default says channelPresentation returns before it reads the channel
	// (an ended session or no configured engine).
	Default bool `json:"default"`
	// Channel is nil exactly when Default is true.
	Channel *playback.ChannelState `json:"channel,omitempty"`
	// Observed is the exact renderer snapshot the view carries, not its seq:
	// a new report of the same facts does not change the view.
	Observed *playback.ObservedPlayback `json:"observed,omitempty"`
	// Now is set only when the view carries NowMS (a tuner deadline).
	Now int64 `json:"now,omitempty"`
}

// ChannelObservationView returns a channel's current SessionView only when the
// caller's held tag is stale, together with the exact tag of that view. A nil
// view with a non-empty tag means the caller already holds the current view.
//
// Every fallback (wrong owner, not a channel, a lapsed lease, a refused or
// failed channel read) returns an error, and the route then takes the ordinary
// Get path; no partial or cached view is ever returned from here.
func (s *Service) ChannelObservationView(ctx context.Context, c Caller, id, held string) (*SessionView, string, error) {
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return nil, "", err
	}
	defer done()
	r, err := loadRow(ctx, tx, id)
	if err != nil {
		return nil, "", err
	}
	if err := c.authorizeSession(ctx, r); err != nil {
		return nil, "", err
	}
	// The ack belongs to the player's own device (Observe has already checked
	// this); never hand another device's session a tag.
	if r.device != c.DeviceID {
		return nil, "", ErrNotFound
	}
	if !isChannel(r) {
		return nil, "", ErrNotFound
	}
	// Get ends a lapsed lease before it reads; let that path do it here too.
	if r.ended == 0 && r.lease <= s.now().UnixMilli() {
		return nil, "", errChannelViewFallback
	}
	stamp := channelViewStamp{
		ID: r.id, Revision: strconv.Itoa(r.revision), Kind: r.kind, Role: r.role, State: r.state,
		Ended: r.ended, EndReason: r.endReason, Message: r.message,
		ItemID: r.item, VersionID: r.version, ChannelID: r.channel,
		QueueID: r.queueID, EntryID: r.entryID, Request: r.request, Lease: r.lease,
		Generation: r.generation,
	}
	var state *playback.ChannelState
	if r.ended == 0 && s.Channels != nil {
		st, err := s.Channels.ReadTx(ctx, tx, rowPrincipal(r), r.id)
		if err != nil {
			// The full view would default or the session would end; never
			// turn a refusal into a tag.
			return nil, "", err
		}
		state = &st
		stamp.Channel = state
		if st.Media.ReservationRequiredAtMS != nil {
			stamp.Now = s.now().UnixMilli()
		}
	} else {
		stamp.Default = true
	}
	var observed *playback.ObservedPlayback
	if state != nil {
		generation, _ := strconv.Atoi(state.Media.BufferGeneration)
		if observed, err = playback.ReadObservedPlayback(ctx, tx, r.id, r.generation, generation); err != nil {
			return nil, "", err
		}
	}
	stamp.Observed = observed
	raw, err := json.Marshal(stamp)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	tag := hex.EncodeToString(sum[:16])
	if tag == held {
		return nil, tag, nil
	}
	view := s.channelViewFrom(r, state, observed, stamp.Now)
	return &view, tag, nil
}

// channelViewFrom is view() for a channel whose channel state and observed
// output were already read: the same renderer, from the same snapshot instead of
// opening a second read after the tag was computed. nowMS is the stamp's clock
// for NowMS (0: no tuner deadline).
func (s *Service) channelViewFrom(r row, state *playback.ChannelState, observed *playback.ObservedPlayback, nowMS int64) SessionView {
	v := s.sessionView(r, state, nowMS)
	if r.ended == 0 {
		// Get reads this only for a live session; a channel has no PlaybackInfo.
		v.ObservedPlayback = observed
	}
	return v
}

// channelPresentationFrom is channelPresentation's projection once ReadTx has
// returned the state, so a caller that already read it does not read again.
func (s *Service) channelPresentationFrom(st playback.ChannelState, nowMS int64) Presentation {
	p := Presentation{Mode: "stream", Subtitles: []SubtitleFile{}}
	gen, _ := strconv.Atoi(st.Media.BufferGeneration)
	p.Generation, p.URL = gen, st.Media.StreamURL
	// The engine the producer made the stream for; native until it has.
	p.Engine = playback.EngineNative
	if st.Media.Engine == playback.EngineMPV {
		p.Engine = playback.EngineMPV
	}
	ms := func(us string) int64 { n, _ := strconv.ParseInt(us, 10, 64); return n / 1000 }
	l := &LinearView{ReservationRequiredAtMS: st.Media.ReservationRequiredAtMS, Name: st.Name, OriginMs: st.Media.OriginMS, WindowStartMs: ms(st.Media.WindowStartUS), WindowEndMs: ms(st.Media.WindowEndUS), LiveEdgeMs: ms(st.Media.LiveEdgeUS), State: st.Media.State, ErrorCode: st.Media.ErrorCode}
	if l.ReservationRequiredAtMS != nil {
		if nowMS == 0 {
			nowMS = s.now().UnixMilli()
		}
		l.NowMS = &nowMS
	}
	if l.State == "" {
		l.State = "preparing"
	}
	for _, c := range st.Media.Clocks {
		l.Clocks = append(l.Clocks, LinearClockView{StartMs: ms(c.StartUS), EndMs: ms(c.EndUS), OffsetMs: ms(c.OffsetUS)})
	}
	if st.Programme != nil {
		l.Programme = &LinearProgrammeView{ID: st.Programme.ID, ItemID: st.Programme.ItemID, Title: st.Programme.Title, StartMs: st.Programme.StartMS, EndMs: st.Programme.EndMS}
	}
	if st.Next != nil {
		l.Next = &LinearProgrammeView{ID: st.Next.ID, ItemID: st.Next.ItemID, Title: st.Next.Title, StartMs: st.Next.StartMS, EndMs: st.Next.EndMS}
	}
	if q := st.Desired.Seek; q != nil {
		seek := &LinearSeekView{ID: q.ID, Live: q.Kind == "live", Acknowledged: st.AcknowledgedSeekID == q.ID, Error: st.SeekError}
		if q.PositionUS != nil {
			v := ms(*q.PositionUS)
			seek.PositionMs = &v
		}
		l.Seek = seek
	}
	if st.ConfirmedPositionUS != nil {
		v := *st.ConfirmedPositionUS / 1000
		l.ConfirmedPositionMs = &v
	}
	p.Linear = l
	return p
}
