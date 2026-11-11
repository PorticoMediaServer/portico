package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// QueueCurrent is the entry being played and where it stands in the queue, from 0.
type QueueCurrent struct {
	EntryID  string `json:"entryId"`
	Position int    `json:"position"`
}

// Queue is a play queue. Its revision moves with every change and is also its ETag: an edit
// sends it back in If-Match, and one made on an out-of-date copy is refused with 412.
type Queue struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	// Medium is audio or video. A device keeps one queue of each.
	Medium string `json:"medium"`
	// Total is how many entries the queue holds that this profile may see.
	Total   int           `json:"total"`
	Current *QueueCurrent `json:"current,omitempty"`
	// RemainingCount and RemainingMs are what is left after the current entry.
	RemainingCount int   `json:"remainingCount"`
	RemainingMs    int64 `json:"remainingMs"`
	// Repeat is off, one or all.
	Repeat  string `json:"repeat"`
	Shuffle bool   `json:"shuffle"`
	// NextEntryID is what plays when the current entry ends. Absent at the end of the queue.
	NextEntryID string `json:"nextEntryId,omitempty"`
	// Refills is true for a queue built from more than it holds at once: it takes more by
	// itself as the listener nears the end, so total and remaining are of what it holds now.
	Refills bool `json:"refills,omitempty"`
	// AutomaticAdvances counts the entries that played to their end one after another without
	// the person touching the queue: what "Still watching?" goes by.
	AutomaticAdvances int `json:"automaticAdvances"`
}

func etag(h http.Header, revision string) { h.Set("ETag", `"`+revision+`"`) }

func (q Queue) reply(h http.Header) int { etag(h, q.Revision); return 0 }

// QueueEntry is one entry of a queue.
type QueueEntry struct {
	EntryID  string `json:"entryId"`
	Position int    `json:"position"`
	ItemID   string `json:"itemId"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	// Added is true for what the person added, as against what came with the album, show or
	// list being played.
	Added bool `json:"added"`
	// Available is false for a title whose file is missing: it says so and the next one plays.
	Available   bool              `json:"available"`
	DurationMs  int64             `json:"durationMs,omitempty"`
	Year        int               `json:"year,omitempty"`
	Index       *int              `json:"index,omitempty"`
	ParentIndex *int              `json:"parentIndex,omitempty"`
	ParentID    string            `json:"parentId,omitempty"`
	ParentTitle string            `json:"parentTitle,omitempty"`
	RootID      string            `json:"rootId,omitempty"`
	RootTitle   string            `json:"rootTitle,omitempty"`
	Images      map[string]string `json:"images"`
}

// QueueEntries is a run of a queue's entries. Start is the position of the first.
type QueueEntries struct {
	Items    []QueueEntry `json:"items"`
	Start    int          `json:"start"`
	Total    int          `json:"total"`
	Revision string       `json:"revision"`
}

func (q QueueEntries) reply(h http.Header) int { etag(h, q.Revision); return 0 }

// QueueCreate builds a queue from what is being played. The first entry, or StartAt, becomes
// current; the app then starts it with POST /v1/playback/sessions.
type QueueCreate struct {
	Source  playqueue.QueueSource `json:"source"`
	Shuffle bool                  `json:"shuffle,omitempty"`
	// StartAt is the title to play first, when it is not the first of the source.
	StartAt string `json:"startAt,omitempty"`
	// Resume starts a show at its first episode not yet watched.
	Resume bool `json:"resume,omitempty"`
}

// QueueCreated is a new queue with the entries around its current one.
type QueueCreated struct {
	Queue  Queue        `json:"queue"`
	Window []QueueEntry `json:"window"`
}

func (q QueueCreated) reply(h http.Header) int { etag(h, q.Queue.Revision); return http.StatusCreated }

// QueueAdd adds to a queue. Placement is next (after the current entry) or end; After names
// an entry to add after instead.
type QueueAdd struct {
	Source    playqueue.QueueSource `json:"source"`
	Placement string                `json:"placement,omitempty"`
	After     string                `json:"after,omitempty"`
}

// QueueMove moves an entry to just before or just after another.
type QueueMove struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// QueueChange sets repeat (off, one, all) and turns shuffle on or off.
type QueueChange struct {
	Repeat  string `json:"repeat,omitempty"`
	Shuffle *bool  `json:"shuffle,omitempty"`
}

// QueueAdvance moves the queue on. Reason is completion (the entry played to its end), next,
// previous, or entry with the EntryID to jump to.
type QueueAdvance struct {
	Reason  string `json:"reason"`
	EntryID string `json:"entryId,omitempty"`
}

// QueuePrepare asks for the next entry to be got ready. SessionID is what is playing now.
type QueuePrepare struct {
	SessionID string `json:"sessionId"`
}

// PreparedNext is the next entry with its stream already running, for a join without a gap.
type PreparedNext struct {
	Token        string       `json:"token"`
	ExpiresAt    time.Time    `json:"expiresAt"`
	EntryID      string       `json:"entryId"`
	ItemID       string       `json:"itemId"`
	Presentation Presentation `json:"presentation"`
}

type QueueCommit struct {
	Token string `json:"token"`
}

// QueueCommitted is the queue moved on to the prepared entry, and the session now playing it.
type QueueCommitted struct {
	Queue   Queue           `json:"queue"`
	Session PlaybackSession `json:"session"`
}

func (q QueueCommitted) reply(h http.Header) int { etag(h, q.Queue.Revision); return 0 }

// QueueSave saves what the queue holds as a playlist. Send an Idempotency-Key.
type QueueSave struct {
	Name string `json:"name"`
}

type SavedPlaylist struct {
	PlaylistID string `json:"playlistId"`
	Entries    int    `json:"entries"`
}

func (SavedPlaylist) reply(http.Header) int { return http.StatusCreated }

func (s *Server) queueRoutes() {
	for _, route := range []*Route{
		Handle(s, "POST /v1/queues", WithProfile, s.createQueue),
		Handle(s, "GET /v1/queues", WithProfile, s.listQueues),
		Handle(s, "GET /v1/queues/{id}", WithProfile, s.getQueue),
		Handle(s, "PATCH /v1/queues/{id}", WithProfile, s.changeQueue),
		Handle(s, "DELETE /v1/queues/{id}", WithProfile, s.deleteQueue),
		Handle(s, "GET /v1/queues/{id}/entries", WithProfile, s.queueEntries),
		Handle(s, "POST /v1/queues/{id}/entries", WithProfile, s.addToQueue),
		Handle(s, "DELETE /v1/queues/{id}/entries/{entryId}", WithProfile, s.removeFromQueue),
		Handle(s, "POST /v1/queues/{id}/entries/{entryId}/move", WithProfile, s.moveInQueue),
		Handle(s, "POST /v1/queues/{id}/advance", WithProfile, s.advanceQueue),
		Handle(s, "POST /v1/queues/{id}/prepare-next", WithProfile, s.prepareNext),
		Handle(s, "POST /v1/queues/{id}/commit-next", WithProfile, s.commitNext),
		Handle(s, "POST /v1/queues/{id}/playlist", WithProfile, s.saveQueue),
	} {
		route.Scope = ScopeSession
	}
}

func queueError(err error) error {
	switch {
	case errors.Is(err, db.ErrStale):
		return refusal(http.StatusPreconditionFailed, "queue_changed", "This queue changed. Reload it and try again.", false)
	case errors.Is(err, db.ErrQueueFull):
		return refusal(http.StatusUnprocessableEntity, "queue_too_large", "This queue is too long. Remove some of it or start a new one.", false)
	case errors.Is(err, db.ErrQueueEnded):
		return ErrConflict("queue_ended", "There is nothing after this in the queue.")
	case errors.Is(err, db.ErrConflict):
		return ErrConflict("entry_is_playing", "This is playing now. Skip to another entry before removing it.")
	case errors.Is(err, playqueue.ErrUnsupported):
		return refusal(http.StatusUnprocessableEntity, "unsupported_selector", "This selection can't be played as a queue.", false)
	case errors.Is(err, playqueue.ErrNotPrepared):
		return refusal(http.StatusGone, "prepared_expired", "The prepared next item is gone.", false)
	case errors.Is(err, db.ErrInvalid):
		return ErrInvalid("Check the request and try again.")
	case errors.Is(err, db.ErrKeyReused):
		return errKeyReused()
	}
	return playbackError(err)
}

func (s *Server) queueOut(q db.Queue) Queue {
	out := Queue{ID: q.PublicID, Revision: strconv.FormatInt(q.Revision, 10), Medium: q.Medium, Total: q.Total,
		RemainingCount: q.Remaining, RemainingMs: q.RemainingMS, Repeat: q.Repeat, Shuffle: q.Shuffled, Refills: q.Selection != "",
		AutomaticAdvances: s.queues.Advances(q.PublicID)}
	if q.CurrentEntry != 0 {
		out.Current = &QueueCurrent{EntryID: playqueue.EntryID(q.CurrentEntry), Position: q.CurrentIndex}
	}
	if q.NextEntry != 0 {
		out.NextEntryID = playqueue.EntryID(q.NextEntry)
	}
	return out
}

func entriesOut(entries []db.QueueEntry) []QueueEntry {
	out := make([]QueueEntry, 0, len(entries))
	for _, e := range entries {
		images := map[string]string{}
		for _, image := range e.Item.Images {
			if image.FileHash != "" {
				images[image.Role.String()] = "/v1/images/" + image.FileHash
			}
		}
		out = append(out, QueueEntry{EntryID: playqueue.EntryID(e.EntryID), Position: e.Index, ItemID: e.Item.PublicID,
			Kind: e.Item.Kind.String(), Title: e.Item.Title, Added: e.Added, Available: e.Available, DurationMs: e.Item.DurationMS,
			Year: e.Item.Year, Index: e.Item.IndexNumber, ParentIndex: e.Item.ParentIndex, ParentID: e.Item.ParentPublicID,
			ParentTitle: e.ParentTitle, RootID: e.Item.RootPublicID, RootTitle: e.RootTitle, Images: images})
	}
	return out
}

// ifMatch reads the revision an edit was made on. With required, an edit that names none is
// refused: it could land in the wrong place.
func ifMatch(r *http.Request, required bool) (int64, error) {
	header := strings.Trim(strings.TrimPrefix(r.Header.Get("If-Match"), "W/"), `"`)
	if header == "" || header == "*" {
		if required && header == "" {
			return 0, refusal(http.StatusPreconditionRequired, "if_match_required", "Send the queue's revision in If-Match.", false)
		}
		return 0, nil
	}
	revision, err := strconv.ParseInt(header, 10, 64)
	if err != nil || revision <= 0 {
		return 0, queueError(db.ErrStale)
	}
	return revision, nil
}

// The entries around the current one: twenty before it and fifty after.
const (
	windowBefore = 20
	windowAfter  = 50
)

func (s *Server) window(r *http.Request, q db.Queue, before, after int) ([]QueueEntry, int, error) {
	from := max(q.CurrentIndex-before, 0)
	entries, _, err := s.db.QueueEntries(r.Context(), s.playbackCaller(r).Viewer, q.PublicID, from, before+after+1)
	return entriesOut(entries), from, err
}

func (s *Server) createQueue(r *http.Request, req QueueCreate) (QueueCreated, error) {
	key, err := idempotencyKey(r)
	if err != nil {
		return QueueCreated{}, err
	}
	q, err := s.queues.Create(r.Context(), s.playbackCaller(r), playqueue.CreateInput{Source: req.Source, Shuffle: req.Shuffle,
		StartAt: req.StartAt, Resume: req.Resume, Key: key, Hash: requestHash(req)})
	if err != nil {
		return QueueCreated{}, queueError(err)
	}
	window, _, err := s.window(r, q, windowBefore, windowAfter)
	return QueueCreated{Queue: s.queueOut(q), Window: window}, err
}

// listQueues answers the queues this profile has on the device asking: what an app shows
// when it is opened again.
func (s *Server) listQueues(r *http.Request, _ None) (Page[Queue], error) {
	c := s.playbackCaller(r)
	device, err := s.db.SessionDevice(r.Context(), c.SignInID)
	if err != nil {
		return Page[Queue]{}, playbackError(err)
	}
	queues, err := s.db.DeviceQueues(r.Context(), c.Viewer, device.ID)
	out := Page[Queue]{Items: make([]Queue, 0, len(queues))}
	for _, q := range queues {
		out.Items = append(out.Items, s.queueOut(q))
	}
	return out, err
}

func (s *Server) getQueue(r *http.Request, _ None) (Queue, error) {
	q, err := s.db.Queue(r.Context(), s.playbackCaller(r).Viewer, r.PathValue("id"))
	return s.queueOut(q), queueError(err)
}

// queueEntries pages through a queue: from and limit, or around=current with before and after.
func (s *Server) queueEntries(r *http.Request, _ None) (QueueEntries, error) {
	query := r.URL.Query()
	number := func(name string, fallback int) int {
		n, err := strconv.Atoi(query.Get(name))
		if err != nil || n < 0 {
			return fallback
		}
		return n
	}
	viewer := s.playbackCaller(r).Viewer
	q, err := s.db.Queue(r.Context(), viewer, r.PathValue("id"))
	if err != nil {
		return QueueEntries{}, queueError(err)
	}
	out := QueueEntries{Total: q.Total, Revision: strconv.FormatInt(q.Revision, 10)}
	if query.Get("around") == "current" {
		out.Items, out.Start, err = s.window(r, q, min(number("before", windowBefore), 100), min(number("after", windowAfter), 100))
		return out, err
	}
	out.Start = number("from", 0)
	entries, _, err := s.db.QueueEntries(r.Context(), viewer, q.PublicID, out.Start, max(number("limit", 50), 1))
	out.Items = entriesOut(entries)
	return out, err
}

func (s *Server) addToQueue(r *http.Request, req QueueAdd) (Queue, error) {
	revision, err := ifMatch(r, true)
	if err != nil {
		return Queue{}, err
	}
	place := db.QueuePlace{Next: req.Placement == "next"}
	if req.After != "" {
		place.After = playqueue.ParseEntryID(req.After)
	}
	q, err := s.queues.Add(r.Context(), s.playbackCaller(r), r.PathValue("id"), revision, req.Source, place)
	return s.queueOut(q), queueError(err)
}

func (s *Server) removeFromQueue(r *http.Request, _ None) (Queue, error) {
	revision, err := ifMatch(r, true)
	if err != nil {
		return Queue{}, err
	}
	q, err := s.queues.Remove(r.Context(), s.playbackCaller(r), r.PathValue("id"), revision, playqueue.ParseEntryID(r.PathValue("entryId")))
	return s.queueOut(q), queueError(err)
}

func (s *Server) moveInQueue(r *http.Request, req QueueMove) (Queue, error) {
	revision, err := ifMatch(r, true)
	if err != nil {
		return Queue{}, err
	}
	if (req.Before == "") == (req.After == "") {
		return Queue{}, ErrInvalid("Say which entry to move it before, or which to move it after.")
	}
	var before, after int64
	if req.Before != "" {
		before = playqueue.ParseEntryID(req.Before)
	} else {
		after = playqueue.ParseEntryID(req.After)
	}
	q, err := s.queues.Move(r.Context(), s.playbackCaller(r), r.PathValue("id"), revision, playqueue.ParseEntryID(r.PathValue("entryId")), before, after)
	return s.queueOut(q), queueError(err)
}

func (s *Server) changeQueue(r *http.Request, req QueueChange) (Queue, error) {
	revision, err := ifMatch(r, false)
	if err != nil {
		return Queue{}, err
	}
	q, err := s.queues.Change(r.Context(), s.playbackCaller(r), r.PathValue("id"), revision, req.Repeat, req.Shuffle)
	return s.queueOut(q), queueError(err)
}

func (s *Server) advanceQueue(r *http.Request, req QueueAdvance) (Queue, error) {
	revision, err := ifMatch(r, false)
	if err != nil {
		return Queue{}, err
	}
	var entry int64
	switch req.Reason {
	case "completion", "next", "previous":
	case "entry":
		entry = playqueue.ParseEntryID(req.EntryID)
	default:
		return Queue{}, ErrField("reason", "The reason is completion, next, previous or entry.")
	}
	q, err := s.queues.Advance(r.Context(), s.playbackCaller(r), r.PathValue("id"), revision, req.Reason, entry)
	return s.queueOut(q), queueError(err)
}

func (s *Server) deleteQueue(r *http.Request, _ None) (None, error) {
	return None{}, s.queues.Delete(r.Context(), s.playbackCaller(r), r.PathValue("id"))
}

func (s *Server) prepareNext(r *http.Request, req QueuePrepare) (PreparedNext, error) {
	p, err := s.queues.PrepareNext(r.Context(), s.playbackCaller(r), r.PathValue("id"), req.SessionID)
	if err != nil {
		return PreparedNext{}, queueError(err)
	}
	preview := sessionOut(playback.Session{Plan: p.Stream.Plan, Stream: p.Stream.Stream, Generation: 1})
	return PreparedNext{Token: p.Token, ExpiresAt: p.ExpiresAt.UTC(), EntryID: playqueue.EntryID(p.EntryID), ItemID: p.Stream.ItemID,
		Presentation: preview.Presentation}, nil
}

func (s *Server) commitNext(r *http.Request, req QueueCommit) (QueueCommitted, error) {
	q, session, err := s.queues.CommitNext(r.Context(), s.playbackCaller(r), r.PathValue("id"), req.Token)
	if err != nil {
		return QueueCommitted{}, queueError(err)
	}
	return QueueCommitted{Queue: s.queueOut(q), Session: sessionOut(session)}, nil
}

func (s *Server) saveQueue(r *http.Request, req QueueSave) (SavedPlaylist, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 200 {
		return SavedPlaylist{}, ErrField("name", "A playlist name has 1 to 200 characters.")
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return SavedPlaylist{}, ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	c := s.playbackCaller(r)
	id, entries, err := s.db.SaveQueueAsPlaylist(r.Context(), c.Viewer, r.PathValue("id"), name, sorttext.Key(name, "", ""), key,
		requestHash(struct{ Queue, Name string }{r.PathValue("id"), name}), s.now())
	if errors.Is(err, db.ErrKeyReused) {
		return SavedPlaylist{}, errKeyReused()
	}
	return SavedPlaylist{PlaylistID: id, Entries: entries}, queueError(err)
}
