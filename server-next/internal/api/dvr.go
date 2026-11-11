package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/dvr"
)

// Recording is one booking, recording or line of History. A recorded programme is also a
// title in the Recorded TV library (ItemID), listed, played and marked like any title.
type Recording struct {
	ID           string    `json:"id"`
	Channel      string    `json:"channel"`
	Title        string    `json:"title"`
	EpisodeTitle string    `json:"episodeTitle,omitempty"`
	Season       *int      `json:"season,omitempty"`
	Episode      *int      `json:"episode,omitempty"`
	StartsAt     time.Time `json:"startsAt"`
	EndsAt       time.Time `json:"endsAt"`
	PadBeforeMs  int64     `json:"padBeforeMs"`
	PadAfterMs   int64     `json:"padAfterMs"`
	Priority     int       `json:"priority"`
	// State is scheduled, recording, recorded, partial, failed, cancelled or skipped, and
	// Reason says why one is partial, failed or skipped.
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	BookedBy string `json:"bookedBy,omitempty"`
	// Conflict says every tuner is busy at its time: it waits for one and may be partly recorded.
	Conflict bool   `json:"conflict,omitempty"`
	Choice   string `json:"choice,omitempty"`
	InGuide  bool   `json:"inGuide"`
	Keep     bool   `json:"keep"`
	FromRule bool   `json:"fromRule"`
	ItemID   string `json:"itemId,omitempty"`
}

// RecordingBook asks to record one airing: Programme is a programme's id from the guide.
type RecordingBook struct {
	Programme   string `json:"programme"`
	PadBeforeMs *int64 `json:"padBeforeMs,omitempty"`
	PadAfterMs  *int64 `json:"padAfterMs,omitempty"`
	Priority    *int   `json:"priority,omitempty"`
}

// RecordingChange changes one booking or recording: only what is sent changes. Choice is
// record ("Record this instead") or skip ("Skip this one").
type RecordingChange struct {
	PadBeforeMs *int64  `json:"padBeforeMs,omitempty"`
	PadAfterMs  *int64  `json:"padAfterMs,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
	Keep        *bool   `json:"keep,omitempty"`
	Choice      *string `json:"choice,omitempty"`
}

// DVR is the Recordings header: counts, space and the keep rule in force.
type DVR struct {
	Upcoming  int    `json:"upcoming"`
	Recorded  int    `json:"recorded"`
	Unwatched int    `json:"unwatched"`
	Conflicts int    `json:"conflicts"`
	FreeBytes int64  `json:"freeBytes"`
	HoursLeft int    `json:"hoursLeft"`
	KeepRule  string `json:"keepRule"`
	FolderOK  bool   `json:"folderOk"`
	CanRecord bool   `json:"canRecord"`
}

// Rule is a series rule.
type Rule struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Source      string `json:"source"`
	Channel     string `json:"channel,omitempty"`
	ByTitle     bool   `json:"byTitle"` // the guide has no series id: it matches by title on the same channel
	NewOnly     bool   `json:"newOnly"`
	KeepNewest  *int   `json:"keepNewest,omitempty"`
	KeepDays    *int   `json:"keepDays,omitempty"`
	Priority    int    `json:"priority"`
	PadBeforeMs *int64 `json:"padBeforeMs,omitempty"`
	PadAfterMs  *int64 `json:"padAfterMs,omitempty"`
	Paused      bool   `json:"paused"`
	status      int
}

func (r Rule) reply(http.Header) int { return r.status }

// RuleSave makes a rule from a programme in the guide, or changes one.
type RuleSave struct {
	Programme   string `json:"programme,omitempty"`
	AnyChannel  *bool  `json:"anyChannel,omitempty"`
	NewOnly     *bool  `json:"newOnly,omitempty"`
	KeepNewest  *int   `json:"keepNewest,omitempty"`
	KeepDays    *int   `json:"keepDays,omitempty"`
	Priority    *int   `json:"priority,omitempty"`
	PadBeforeMs *int64 `json:"padBeforeMs,omitempty"`
	PadAfterMs  *int64 `json:"padAfterMs,omitempty"`
	Paused      *bool  `json:"paused,omitempty"`
}

// TunerUse is what holds a tuned channel: a viewer (named only for the owner's own) or a recording.
type TunerUse struct {
	Channel string `json:"channel"`
	Using   string `json:"using"`
}

// Tuners is the owner's Tuners page: what is using them now, and the clashes ahead.
type Tuners struct {
	InUse     []TunerUse  `json:"inUse"`
	Conflicts []Recording `json:"conflicts"`
}

func (s *Server) dvrRoutes() {
	Handle(s, "GET /v1/dvr", WithProfile, s.dvrSummary)
	Handle(s, "GET /v1/dvr/recordings", WithProfile, s.listRecordings)
	Handle(s, "POST /v1/dvr/recordings", WithProfile, s.bookRecording)
	Handle(s, "GET /v1/dvr/recordings/{id}", WithProfile, s.getRecording)
	Handle(s, "PATCH /v1/dvr/recordings/{id}", WithProfile, s.changeRecording)
	Handle(s, "DELETE /v1/dvr/recordings/{id}", WithProfile, s.deleteRecording)
	Handle(s, "GET /v1/dvr/rules", WithProfile, s.listRules)
	Handle(s, "POST /v1/dvr/rules", WithProfile, s.createRule)
	Handle(s, "PATCH /v1/dvr/rules/{id}", WithProfile, s.changeRule)
	Handle(s, "DELETE /v1/dvr/rules/{id}", WithProfile, s.deleteRule)
	Handle(s, "GET /v1/admin/dvr/tuners", Owner, s.dvrTuners)
}

// programme finds a guide programme the viewer may see by its id: a source's channel the
// viewer may watch, and a programme within its rating limit. Anything else is not found.
func (s *Server) programme(ctx context.Context, v db.Viewer, lv db.LiveViewer, id string) (db.LiveChannel, db.GuideProgramme, error) {
	var ref struct {
		Channel string `json:"c"`
		Start   int64  `json:"s"`
	}
	if decodeOpaque(id, &ref) != nil {
		return db.LiveChannel{}, db.GuideProgramme{}, ErrNotFound()
	}
	ch, err := s.db.LiveChannelByID(ctx, v, ref.Channel)
	if errors.Is(err, db.ErrNotFound) || err == nil && !mayWatch(lv, ch) {
		return ch, db.GuideProgramme{}, ErrNotFound()
	}
	if err != nil {
		return ch, db.GuideProgramme{}, err
	}
	at := time.UnixMilli(ref.Start)
	if ch.Kind == db.ChannelFromLibrary {
		ch.GuideSource, ch.GuideKey = db.LibraryGuideSource, ch.PublicID
	}
	if ch.Kind == db.ChannelFromSource && ch.GuideSource == 0 {
		return ch, db.GuideProgramme{}, ErrNotFound()
	}
	rows, err := s.db.GuideProgrammes(ctx, ch.GuideSource, ch.GuideKey, at, at.Add(time.Millisecond), 2)
	if err != nil {
		return ch, db.GuideProgramme{}, err
	}
	for _, p := range rows {
		if p.StartsAt.Equal(at) {
			return ch, p, nil
		}
	}
	return ch, db.GuideProgramme{}, ErrNotFound()
}

func (s *Server) recordingOut(r db.Recording, conflict bool, items map[int64]string) Recording {
	out := Recording{ID: r.PublicID, Channel: r.ChannelName, Title: r.Title, EpisodeTitle: r.EpisodeTitle, Season: r.Season, Episode: r.Episode,
		StartsAt: r.StartsAt.UTC(), EndsAt: r.EndsAt.UTC(), PadBeforeMs: r.PadBefore.Milliseconds(), PadAfterMs: r.PadAfter.Milliseconds(),
		Priority: r.Priority, State: r.State, Reason: r.Reason, BookedBy: r.BookedBy, Conflict: conflict, Choice: r.Choice, InGuide: r.InGuide,
		Keep: r.Keep, FromRule: r.RuleID != 0, ItemID: items[r.ItemID]}
	return out
}

func (s *Server) liveAllowed(r *http.Request, record bool) (db.LiveViewer, error) {
	lv, err := s.liveViewer(r)
	if err == nil && record && !lv.Record {
		return lv, ErrNotPermitted()
	}
	return lv, err
}

func (s *Server) dvrSummary(r *http.Request, _ None) (DVR, error) {
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return DVR{}, err
	}
	ctx := r.Context()
	counts, err := s.db.RecordingCounts(ctx, viewer(r))
	if err != nil {
		return DVR{}, err
	}
	upcoming, err := s.db.Upcoming(ctx, s.now().AddDate(0, 0, 14))
	if err != nil {
		return DVR{}, err
	}
	losers, err := s.recorder.Clashes(ctx, upcoming)
	if err != nil {
		return DVR{}, err
	}
	opts := s.dvrOptions()
	out := DVR{Recorded: counts.Recorded, Unwatched: counts.Unwatched, KeepRule: keepRule(opts), CanRecord: lv.Record}
	// Upcoming and conflicts count only the bookings this profile may see.
	for _, rec := range upcoming {
		if ok, err := s.maySeeRecording(ctx, r, lv, rec); err != nil {
			return DVR{}, err
		} else if ok {
			out.Upcoming++
			if losers[rec.ID] {
				out.Conflicts++
			}
		}
	}
	if free, err := diskspace.Free(opts.Folder); err == nil {
		out.FreeBytes, out.FolderOK = max(free-opts.Floor, 0), true
		out.HoursLeft = int(out.FreeBytes / (3 << 30)) // about 3 GB an hour of broadcast HD
	}
	return out, nil
}

// keepRule is the owner's default keep rule in words.
func keepRule(o dvr.Options) string {
	switch {
	case o.KeepNewest > 0:
		return fmt.Sprintf("The newest %d of each show", o.KeepNewest)
	case o.KeepDays > 0:
		return fmt.Sprintf("%d days", o.KeepDays)
	}
	return "Until deleted"
}

// maySeeRecording says whether a profile may know of a booking or recording: the programme is
// within its rating limit, and it may watch the channel (the owner always). Anything else is
// absent from its lists and not found by its id.
func (s *Server) maySeeRecording(ctx context.Context, r *http.Request, lv db.LiveViewer, rec db.Recording) (bool, error) {
	if !rated(lv, rec.RatingAge) {
		return false, nil
	}
	if lv.Owner {
		return true, nil
	}
	// Someone else's booking or recording only to a profile that may see Recorded TV.
	if names, err := s.db.ProfileNames(ctx, viewer(r).ProfileID); err != nil {
		return false, err
	} else if rec.ProfileID != viewer(r).ProfileID && !slices.Contains(rec.AlsoWantedBy, names.PublicID) {
		lib, err := s.recorder.Library(ctx)
		if err != nil {
			return false, err
		}
		if _, err := s.db.Library(ctx, viewer(r), lib.PublicID); errors.Is(err, db.ErrNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
	}
	public, err := s.db.ChannelPublicID(ctx, rec.ChannelID)
	if errors.Is(err, db.ErrNotFound) {
		return rec.ProfileID == viewer(r).ProfileID, nil // its channel has gone: its own booker still sees it
	}
	if err != nil {
		return false, err
	}
	ch, err := s.db.LiveChannelByID(ctx, viewer(r), public)
	if errors.Is(err, db.ErrNotFound) {
		return rec.ProfileID == viewer(r).ProfileID, nil
	}
	return err == nil && mayWatch(lv, ch), err
}

// itemIDs reads the public ids of recordings' titles that the viewer may see.
func (s *Server) itemIDs(ctx context.Context, v db.Viewer, rows []db.Recording) (map[int64]string, error) {
	var ids []int64
	for _, r := range rows {
		if r.ItemID != 0 {
			ids = append(ids, r.ItemID)
		}
	}
	return s.db.VisibleItems(ctx, v, ids)
}

func (s *Server) listRecordings(r *http.Request, _ None) (Page[Recording], error) {
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return Page[Recording]{}, err
	}
	ctx := r.Context()
	query := r.URL.Query()
	q := db.RecordingQuery{Upcoming: query.Get("state") != "history", Limit: 50}
	if query.Get("scope") != "everyone" {
		names, err := s.db.ProfileNames(ctx, viewer(r).ProfileID)
		if err != nil {
			return Page[Recording]{}, err
		}
		q.ProfileID, q.ProfilePub = viewer(r).ProfileID, names.PublicID
	}
	var c struct {
		After int64 `json:"a"`
		ID    int64 `json:"i"`
	}
	if text := query.Get("cursor"); text != "" && decodeOpaque(text, &c) != nil {
		return Page[Recording]{}, ErrField("cursor", "This cursor is not one this list gave.")
	}
	q.After, q.AfterID = c.After, c.ID
	rows, err := s.db.Recordings(ctx, q)
	if err != nil {
		return Page[Recording]{}, err
	}
	var losers map[int64]bool
	if q.Upcoming {
		upcoming, err := s.db.Upcoming(ctx, s.now().AddDate(0, 0, 14))
		if err != nil {
			return Page[Recording]{}, err
		}
		if losers, err = s.recorder.Clashes(ctx, upcoming); err != nil {
			return Page[Recording]{}, err
		}
	}
	items, err := s.itemIDs(ctx, viewer(r), rows)
	if err != nil {
		return Page[Recording]{}, err
	}
	out := Page[Recording]{Items: []Recording{}}
	for _, row := range rows {
		if ok, err := s.maySeeRecording(ctx, r, lv, row); err != nil {
			return Page[Recording]{}, err
		} else if ok {
			out.Items = append(out.Items, s.recordingOut(row, losers[row.ID], items))
		}
	}
	if len(rows) == q.Limit {
		last := rows[len(rows)-1]
		after := last.StartsAt.UnixMilli()
		if !q.Upcoming {
			after = last.UpdatedAt.UnixMilli()
		}
		out.NextCursor = encodeOpaque(map[string]any{"a": after, "i": last.ID})
	}
	return out, nil
}

func (s *Server) bookRecording(r *http.Request, req RecordingBook) (Recording, error) {
	lv, err := s.liveAllowed(r, true)
	if err != nil {
		return Recording{}, err
	}
	ctx := r.Context()
	ch, p, err := s.programme(ctx, viewer(r), lv, req.Programme)
	if err != nil {
		return Recording{}, err
	}
	switch {
	case ch.Kind != db.ChannelFromSource:
		return Recording{}, ErrConflict("cannot_record", "This program can't be recorded.")
	case !rated(lv, p.RatingAge):
		return Recording{}, ErrNotFound()
	case !p.EndsAt.After(s.now()):
		return Recording{}, ErrConflict("programme_over", "This program has ended.")
	}
	names, err := s.db.ProfileNames(ctx, viewer(r).ProfileID)
	if err != nil {
		return Recording{}, err
	}
	if _, err := s.recorder.Library(ctx); err != nil {
		return Recording{}, ErrConflict("recordings_folder", "The recordings folder can't be written.")
	}
	opts := s.dvrOptions()
	b := db.Booking{ProfileID: viewer(r).ProfileID, ProfileName: names.Name, ProfilePub: names.PublicID, ChannelID: ch.ID, ChannelName: ch.Name,
		ProgrammeKey: p.ProgrammeKey, SeriesKey: p.SeriesKey, Title: p.Title, EpisodeTitle: p.EpisodeTitle, Season: p.Season, Episode: p.Episode,
		Details: p.Summary, Rating: p.Rating, RatingAge: p.RatingAge, StartsAt: p.StartsAt, EndsAt: p.EndsAt, PadBefore: opts.PadBefore,
		PadAfter: opts.PadAfter, Now: s.now()}
	if req.PadBeforeMs != nil {
		b.PadBefore = time.Duration(max(*req.PadBeforeMs, 0)) * time.Millisecond
	}
	if req.PadAfterMs != nil {
		b.PadAfter = time.Duration(max(*req.PadAfterMs, 0)) * time.Millisecond
	}
	if req.Priority != nil {
		b.Priority = max(min(*req.Priority, 1), -1)
	}
	rec, created, err := s.db.BookAiring(ctx, b)
	if err != nil {
		return Recording{}, err
	}
	out := s.recordingOut(rec, false, nil)
	if created {
		s.goBackground(func() { s.recorder.Tick(s.liveContext) })
	}
	return out, nil
}

// mayChange says the viewer may change or delete a recording: the profile that booked it, the
// owner and administrators.
func (s *Server) mayChange(r *http.Request, rec db.Recording) bool {
	c := CallerFrom(r.Context())
	return c.Role == "owner" || c.Role == "admin" || rec.ProfileID == c.ProfileID
}

func (s *Server) getRecording(r *http.Request, _ None) (Recording, error) {
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return Recording{}, err
	}
	rec, err := s.db.Recording(r.Context(), r.PathValue("id"))
	if err != nil {
		return Recording{}, sourceError(err)
	}
	if ok, err := s.maySeeRecording(r.Context(), r, lv, rec); err != nil || !ok {
		if err != nil {
			return Recording{}, err
		}
		return Recording{}, ErrNotFound()
	}
	items, err := s.itemIDs(r.Context(), viewer(r), []db.Recording{rec})
	if err != nil {
		return Recording{}, err
	}
	upcoming, err := s.db.Upcoming(r.Context(), s.now().AddDate(0, 0, 14))
	if err != nil {
		return Recording{}, err
	}
	losers, err := s.recorder.Clashes(r.Context(), upcoming)
	if err != nil {
		return Recording{}, err
	}
	return s.recordingOut(rec, losers[rec.ID], items), nil
}

func (s *Server) changeRecording(r *http.Request, req RecordingChange) (Recording, error) {
	ctx := r.Context()
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return Recording{}, err
	}
	rec, err := s.db.Recording(ctx, r.PathValue("id"))
	if err != nil {
		return Recording{}, sourceError(err)
	}
	if ok, err := s.maySeeRecording(ctx, r, lv, rec); err != nil || !ok {
		if err != nil {
			return Recording{}, err
		}
		return Recording{}, ErrNotFound()
	}
	if !s.mayChange(r, rec) {
		return Recording{}, ErrNotPermitted()
	}
	if req.Choice != nil && *req.Choice != "record" && *req.Choice != "skip" && *req.Choice != "" {
		return Recording{}, ErrField("choice", "Choose record or skip.")
	}
	c := db.RecordingChange{Priority: req.Priority, Keep: req.Keep, Choice: req.Choice}
	if req.PadBeforeMs != nil {
		d := time.Duration(max(*req.PadBeforeMs, 0)) * time.Millisecond
		c.PadBefore = &d
	}
	if req.PadAfterMs != nil {
		d := time.Duration(max(*req.PadAfterMs, 0)) * time.Millisecond
		c.PadAfter = &d
	}
	if err := s.db.ChangeRecording(ctx, rec.PublicID, c, s.now()); err != nil {
		return Recording{}, err
	}
	return s.getRecording(r, None{})
}

// deleteRecording cancels a booking (it passes to the next person who asked), stops one that
// is recording with action=stop (what was captured is kept), or deletes a recording and its
// file, stopping anyone playing it.
func (s *Server) deleteRecording(r *http.Request, _ None) (None, error) {
	ctx := r.Context()
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return None{}, err
	}
	rec, err := s.db.Recording(ctx, r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return None{}, nil
	}
	if err != nil {
		return None{}, err
	}
	if ok, err := s.maySeeRecording(ctx, r, lv, rec); err != nil || !ok {
		return None{}, err // one it may not know of is as if it were gone
	}
	c := CallerFrom(ctx)
	names, err := s.db.ProfileNames(ctx, c.ProfileID)
	if err != nil {
		return None{}, err
	}
	switch {
	case rec.State == db.RecordingScheduled:
		// The booker passes it on and someone who also asked is taken off it; the owner or an
		// administrator who asked for neither cancels it for everyone.
		asked := rec.ProfileID == c.ProfileID || slices.Contains(rec.AlsoWantedBy, names.PublicID)
		everyone := !asked && (c.Role == "owner" || c.Role == "admin")
		if !asked && !everyone {
			return None{}, ErrNotPermitted()
		}
		return None{}, s.db.CancelRecording(ctx, rec.PublicID, c.ProfileID, names.PublicID, everyone, s.now())
	case !s.mayChange(r, rec):
		return None{}, ErrNotPermitted()
	case rec.State == db.RecordingRecording && r.URL.Query().Get("action") == "stop":
		s.recorder.Stop(rec.ID)
		return None{}, nil
	}
	return None{}, s.recorder.Delete(ctx, rec) // which stops anyone playing it
}

func ruleOut(rule db.RecordingRule, source, channel string) Rule {
	out := Rule{ID: rule.PublicID, Title: rule.Title, Source: source, Channel: channel, ByTitle: rule.SeriesKey == "", NewOnly: rule.NewOnly,
		KeepNewest: rule.KeepNewest, KeepDays: rule.KeepDays, Priority: rule.Priority, Paused: rule.Paused}
	if rule.PadBefore != nil {
		ms := rule.PadBefore.Milliseconds()
		out.PadBeforeMs = &ms
	}
	if rule.PadAfter != nil {
		ms := rule.PadAfter.Milliseconds()
		out.PadAfterMs = &ms
	}
	return out
}

func (s *Server) listRules(r *http.Request, _ None) (Page[Rule], error) {
	if _, err := s.liveAllowed(r, true); err != nil {
		return Page[Rule]{}, err
	}
	profile := viewer(r).ProfileID
	if r.URL.Query().Get("scope") == "everyone" && CallerFrom(r.Context()).Role == "owner" {
		profile = 0
	}
	rules, err := s.db.Rules(r.Context(), profile)
	if err != nil {
		return Page[Rule]{}, err
	}
	out := Page[Rule]{Items: []Rule{}}
	for _, rule := range rules {
		out.Items = append(out.Items, ruleOut(rule, "", ""))
	}
	return out, nil
}

func applyRule(rule *db.RecordingRule, req RuleSave) error {
	if req.NewOnly != nil {
		rule.NewOnly = *req.NewOnly
	}
	if req.KeepNewest != nil {
		if *req.KeepNewest < 1 {
			return ErrField("keepNewest", "Keep at least one episode.")
		}
		rule.KeepNewest = req.KeepNewest
	}
	if req.KeepDays != nil {
		if *req.KeepDays < 1 {
			return ErrField("keepDays", "Keep for at least a day.")
		}
		rule.KeepDays = req.KeepDays
	}
	if req.Priority != nil {
		rule.Priority = max(min(*req.Priority, 1), -1)
	}
	if req.PadBeforeMs != nil {
		d := time.Duration(max(*req.PadBeforeMs, 0)) * time.Millisecond
		rule.PadBefore = &d
	}
	if req.PadAfterMs != nil {
		d := time.Duration(max(*req.PadAfterMs, 0)) * time.Millisecond
		rule.PadAfter = &d
	}
	if req.Paused != nil {
		rule.Paused = *req.Paused
	}
	return nil
}

// createRule makes a series rule from a programme: it follows the guide's series id, or where
// the guide has none, the same title on the same channel. With ?preview=true it answers what
// it would record and makes nothing.
func (s *Server) createRule(r *http.Request, req RuleSave) (Rule, error) {
	lv, err := s.liveAllowed(r, true)
	if err != nil {
		return Rule{}, err
	}
	ctx := r.Context()
	ch, p, err := s.programme(ctx, viewer(r), lv, req.Programme)
	if err != nil {
		return Rule{}, err
	}
	if ch.Kind != db.ChannelFromSource {
		return Rule{}, ErrConflict("cannot_record", "This program can't be recorded.")
	}
	rule := db.RecordingRule{ProfileID: viewer(r).ProfileID, SourceID: ch.SourceID, ChannelID: ch.ID, SeriesKey: p.SeriesKey, Title: p.Title}
	if req.AnyChannel != nil && *req.AnyChannel && p.SeriesKey != "" {
		rule.ChannelID = 0
	}
	if req.NewOnly != nil && *req.NewOnly && p.Flags == 0 {
		return Rule{}, ErrField("newOnly", "This guide doesn't say which episodes are new.")
	}
	if err := applyRule(&rule, req); err != nil {
		return Rule{}, err
	}
	if r.URL.Query().Get("preview") == "true" {
		return ruleOut(rule, "", ch.PublicID), nil
	}
	saved, err := s.db.SaveRule(ctx, rule, s.now())
	if err != nil {
		return Rule{}, err
	}
	if err := s.recorder.FollowRules(ctx); err != nil {
		return Rule{}, err
	}
	out := ruleOut(saved, "", ch.PublicID)
	out.status = http.StatusCreated
	return out, nil
}

func (s *Server) changeRule(r *http.Request, req RuleSave) (Rule, error) {
	if _, err := s.liveAllowed(r, true); err != nil {
		return Rule{}, err
	}
	ctx := r.Context()
	rule, err := s.db.Rule(ctx, r.PathValue("id"))
	if err != nil {
		return Rule{}, sourceError(err)
	}
	if rule.ProfileID != viewer(r).ProfileID && CallerFrom(ctx).Role != "owner" {
		return Rule{}, ErrNotPermitted()
	}
	if err := applyRule(&rule, req); err != nil {
		return Rule{}, err
	}
	saved, err := s.db.SaveRule(ctx, rule, s.now())
	if err != nil {
		return Rule{}, err
	}
	return ruleOut(saved, "", ""), nil
}

func (s *Server) deleteRule(r *http.Request, _ None) (None, error) {
	if _, err := s.liveAllowed(r, true); err != nil {
		return None{}, err
	}
	ctx := r.Context()
	rule, err := s.db.Rule(ctx, r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return None{}, nil
	}
	if err != nil {
		return None{}, err
	}
	if rule.ProfileID != viewer(r).ProfileID && CallerFrom(ctx).Role != "owner" {
		return None{}, ErrNotPermitted()
	}
	return None{}, s.db.DeleteRule(ctx, rule.PublicID, r.URL.Query().Get("bookings") == "cancel", s.now())
}

func (s *Server) dvrTuners(r *http.Request, _ None) (Tuners, error) {
	ctx := r.Context()
	sources, err := s.db.LiveSources(ctx)
	if err != nil {
		return Tuners{}, err
	}
	out := Tuners{InUse: []TunerUse{}, Conflicts: []Recording{}}
	for _, src := range sources {
		for channel, uses := range s.tuners.Using(src.ID) {
			name, err := s.db.ChannelName(ctx, channel)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return Tuners{}, err
			}
			for _, u := range uses {
				using := u.Name
				if !u.Record {
					using = "A viewer: " + u.Name
				}
				out.InUse = append(out.InUse, TunerUse{Channel: name, Using: using})
			}
		}
	}
	upcoming, err := s.db.Upcoming(ctx, s.now().Add(12*time.Hour))
	if err != nil {
		return Tuners{}, err
	}
	losers, err := s.recorder.Clashes(ctx, upcoming)
	if err != nil {
		return Tuners{}, err
	}
	for _, rec := range upcoming {
		if losers[rec.ID] {
			out.Conflicts = append(out.Conflicts, s.recordingOut(rec, true, nil))
		}
	}
	return out, nil
}

// stopPlaying ends every session playing a deleted recording, with the reason.
func (s *Server) stopPlaying(ctx context.Context, itemID int64) {
	for _, session := range s.playback.All() {
		if session.Item.ID == itemID {
			s.playback.Terminate(ctx, session.ID, "This recording was deleted.")
		}
	}
}
