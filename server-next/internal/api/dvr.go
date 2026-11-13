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

// DvrRecording is one booking, recording or line of History. A recorded programme is also a
// title in the Recorded TV library (ItemID), listed, played and marked like any title.
type DvrRecording struct {
	ID string `json:"id"`
	// Channel is the channel's name and ChannelID its id, left out once the channel is gone.
	Channel      string    `json:"channel"`
	ChannelID    string    `json:"channelId,omitempty"`
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
	// LibraryID is the Recorded TV library the title is in, sent with ItemID.
	LibraryID string `json:"libraryId,omitempty"`
	// Bytes is what the recording takes on disk now, and what deleting it frees: its file and
	// the guide's details beside it. It grows while it records.
	Bytes int64 `json:"bytes"`
	// PositionMs, DurationMs and Watched are this profile's place in the recorded title, sent
	// with ItemID.
	PositionMs int64 `json:"positionMs,omitempty"`
	DurationMs int64 `json:"durationMs,omitempty"`
	Watched    bool  `json:"watched,omitempty"`
	// PlayingNow is how many are playing it now, whom deleting it stops. Only the read of one
	// recording sends it.
	PlayingNow int `json:"playingNow,omitempty"`
	// Tuners and TunersUsedBy come with a conflict: how many tuners the source has, and what
	// holds them at its time, the account's own recordings by title and anyone else's as
	// "A recording".
	Tuners       int      `json:"tuners,omitempty"`
	TunersUsedBy []string `json:"tunersUsedBy,omitempty"`
	// MayChange says this profile may change it (padding, priority, Keep, Record this instead
	// or Skip this one), stop it, or delete what was recorded: the booker, the owner and
	// administrators. MayCancel, on a booking not yet started, says it may cancel it: a profile
	// that asked for it lets go of its own ask, and the owner or an administrator cancels it
	// for everyone.
	MayChange bool `json:"mayChange"`
	MayCancel bool `json:"mayCancel,omitempty"`
}

// DvrRecordingBook asks to record one airing: Programme is a programme's id from the guide.
type DvrRecordingBook struct {
	Programme   string `json:"programme"`
	PadBeforeMs *int64 `json:"padBeforeMs,omitempty"`
	PadAfterMs  *int64 `json:"padAfterMs,omitempty"`
	Priority    *int   `json:"priority,omitempty"`
}

// DvrRecordingChange changes one booking or recording: only what is sent changes. Choice is
// record ("Record this instead") or skip ("Skip this one").
type DvrRecordingChange struct {
	PadBeforeMs *int64  `json:"padBeforeMs,omitempty"`
	PadAfterMs  *int64  `json:"padAfterMs,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
	Keep        *bool   `json:"keep,omitempty"`
	Choice      *string `json:"choice,omitempty"`
}

// DvrOverview is the Recordings header: counts, space and the keep rule in force.
type DvrOverview struct {
	Rules     int    `json:"rules"`
	Groups    int    `json:"groups"`
	Upcoming  int    `json:"upcoming"`
	Recorded  int    `json:"recorded"`
	Unwatched int    `json:"unwatched"`
	Conflicts int    `json:"conflicts"`
	FreeBytes int64  `json:"freeBytes"`
	HoursLeft int    `json:"hoursLeft"`
	KeepRule  string `json:"keepRule"`
	FolderOK  bool   `json:"folderOk"`
	CanRecord bool   `json:"canRecord"`
	// FreeOnDriveBytes is free drive space before applying the recording floor and cap.
	FreeOnDriveBytes int64 `json:"freeOnDriveBytes"`
	// UsedBytes is what the recordings take together.
	UsedBytes int64 `json:"usedBytes"`
	// FloorBytes is the free space recording always leaves on the drive; CapBytes, above 0, is
	// the most the recordings may take together.
	FloorBytes int64 `json:"floorBytes"`
	CapBytes   int64 `json:"capBytes"`
	// KeepDays and KeepNewest are the keep rule behind KeepRule: 0 and 0 keep every recording
	// until it is deleted.
	KeepDays   int `json:"keepDays"`
	KeepNewest int `json:"keepNewest"`
	// EveryonesRecordings says this profile may see everyone's recordings (it may see the
	// Recorded TV library); without it Everyone's is the same list as its own.
	EveryonesRecordings bool `json:"everyonesRecordings"`
	// UpcomingBytes and UpcomingHours are about what every booking of the next 14 days will
	// take, at the same rate as HoursLeft, so it can be set against the space left.
	UpcomingBytes int64 `json:"upcomingBytes"`
	UpcomingHours int   `json:"upcomingHours"`
}

// DvrTunerUse is what holds a tuned channel: a viewer (named only for the owner's own) or a recording.
type DvrTunerUse struct {
	Channel string `json:"channel"`
	Using   string `json:"using"`
}

// DvrTunerSource counts channel inputs and lists their current and upcoming uses.
type DvrTunerSource struct {
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	TunerCount int    `json:"tunerCount"`
	Paused     bool   `json:"paused"`
	// InUse counts distinct channel inputs, not viewers sharing them.
	InUse     int            `json:"inUse"`
	Conflicts int            `json:"conflicts"`
	Active    []DvrTunerUse  `json:"active"`
	Upcoming  []DvrRecording `json:"upcoming"`
}

// DvrTuners is the owner's Tuners page: what is using them now, and the clashes ahead.
type DvrTuners struct {
	Sources   []DvrTunerSource `json:"sources"`
	InUse     []DvrTunerUse    `json:"inUse"`
	Conflicts []DvrRecording   `json:"conflicts"`
}

func (s *Server) dvrRoutes() {
	s.dvrGroupRoutes()
	Handle(s, "GET /v1/admin/dvr/folder", Owner, s.readDvrFolder)
	s.dvrPermissionRoutes()
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

func (s *Server) recordingOut(ctx context.Context, r db.Recording, conflict bool, items map[int64]db.RecordedTitle) DvrRecording {
	title := items[r.ItemID]
	out := DvrRecording{ID: r.PublicID, Channel: r.ChannelName, Title: r.Title, EpisodeTitle: r.EpisodeTitle, Season: r.Season, Episode: r.Episode,
		StartsAt: r.StartsAt.UTC(), EndsAt: r.EndsAt.UTC(), PadBeforeMs: r.PadBefore.Milliseconds(), PadAfterMs: r.PadAfter.Milliseconds(),
		Priority: r.Priority, State: r.State, Reason: r.Reason, BookedBy: r.BookedBy, Conflict: conflict, Choice: r.Choice, InGuide: r.InGuide,
		Keep: r.Keep, FromRule: r.RuleID != 0, ItemID: title.PublicID, LibraryID: title.LibraryID, PositionMs: title.PositionMS,
		DurationMs: title.DurationMS, Watched: title.Watched}
	if r.State != db.RecordingScheduled {
		out.Bytes = s.recorder.Bytes(r)
	}
	if r.ChannelID != 0 {
		out.ChannelID, _ = s.db.ChannelPublicID(ctx, r.ChannelID) // a channel that has gone has no id to give
	}
	return out
}

// clashes are the conflicts among the bookings of the next 14 days.
func (s *Server) clashes(ctx context.Context) (map[int64]dvr.Clash, error) {
	upcoming, err := s.db.Upcoming(ctx, s.now().AddDate(0, 0, 14))
	if err != nil {
		return nil, err
	}
	return s.recorder.ClashDetails(ctx, upcoming)
}

// ownProfiles are the caller's account's profiles, whose recordings a conflict names by title.
func (s *Server) ownProfiles(r *http.Request) (map[int64]bool, error) {
	profiles, err := s.db.Profiles(r.Context(), CallerFrom(r.Context()).UserID)
	own := make(map[int64]bool, len(profiles))
	for _, p := range profiles {
		own[p.ID] = true
	}
	return own, err
}

// withClash marks a booking that loses a tuner with how many tuners there are and what holds
// them (rule 42), naming only the account's own recordings (area 7 rule 38) and of those only
// the ones within this profile's rating limit.
func (s *Server) withClash(lv db.LiveViewer, out DvrRecording, clashes map[int64]dvr.Clash, id int64, own map[int64]bool) DvrRecording {
	clash, ok := clashes[id]
	if !ok {
		return out
	}
	out.Conflict, out.Tuners, out.TunersUsedBy = true, clash.Tuners, []string{}
	for _, w := range clash.Using {
		if own[w.ProfileID] && rated(lv, w.RatingAge) {
			out.TunersUsedBy = append(out.TunersUsedBy, w.Title)
		} else {
			out.TunersUsedBy = append(out.TunersUsedBy, "A recording")
		}
	}
	return out
}

func (s *Server) liveAllowed(r *http.Request, record bool) (db.LiveViewer, error) {
	lv, err := s.liveViewer(r)
	if err == nil && record && !lv.Record {
		return lv, ErrNotPermitted()
	}
	return lv, err
}

func (s *Server) dvrSummary(r *http.Request, _ None) (DvrOverview, error) {
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return DvrOverview{}, err
	}
	ctx := r.Context()
	scope, err := s.recordingReadScope(r, lv)
	if err != nil {
		return DvrOverview{}, err
	}
	counts, err := s.db.RecordingOverviewTotals(ctx, scope)
	if err != nil {
		return DvrOverview{}, err
	}
	upcoming, err := s.db.Upcoming(ctx, s.now().AddDate(0, 0, 14))
	if err != nil {
		return DvrOverview{}, err
	}
	losers, err := s.recorder.Clashes(ctx, upcoming)
	if err != nil {
		return DvrOverview{}, err
	}
	opts := s.dvrOptions()
	out := DvrOverview{Recorded: counts.Recorded, Unwatched: counts.Unwatched, Rules: counts.Rules, Groups: counts.Groups, KeepRule: keepRule(opts), CanRecord: lv.Record,
		FloorBytes: opts.Floor, CapBytes: opts.Cap, KeepDays: opts.KeepDays, KeepNewest: opts.KeepNewest}
	if lib, err := s.recorder.Library(ctx); err == nil {
		if out.UsedBytes, err = s.db.RecordedBytes(ctx, lib.ID); err != nil {
			return DvrOverview{}, err
		}
		// The same test maySeeRecording applies to someone else's recording.
		if _, err := s.db.Library(ctx, viewer(r), lib.PublicID); err == nil {
			out.EveryonesRecordings = true
		} else if !errors.Is(err, db.ErrNotFound) {
			return DvrOverview{}, err
		}
	}
	out.EveryonesRecordings = out.EveryonesRecordings || lv.Owner
	// The forecast is space, not titles: every booking counts, as every booking takes room.
	var upcomingMs int64
	for _, rec := range upcoming {
		if rec.State == db.RecordingScheduled {
			upcomingMs += (rec.EndsAt.Sub(rec.StartsAt) + rec.PadBefore + rec.PadAfter).Milliseconds()
		}
	}
	out.UpcomingBytes = upcomingMs * bytesPerHour / int64(time.Hour/time.Millisecond)
	out.UpcomingHours = int((upcomingMs + int64(time.Hour/time.Millisecond) - 1) / int64(time.Hour/time.Millisecond))
	// Upcoming and conflicts count only the bookings this profile may see.
	for _, rec := range upcoming {
		if !scope.Everyone && rec.ProfileID != scope.Viewer.ProfileID && !slices.Contains(rec.AlsoWantedBy, scope.ProfilePub) {
			continue
		}
		if ok, err := s.maySeeRecording(ctx, r, lv, rec); err != nil {
			return DvrOverview{}, err
		} else if ok {
			out.Upcoming++
			if losers[rec.ID] {
				out.Conflicts++
			}
		}
	}
	if free, err := diskspace.Free(opts.Folder); err == nil {
		out.FreeOnDriveBytes, out.FreeBytes, out.FolderOK = free, max(free-opts.Floor, 0), true
		if opts.Cap > 0 {
			out.FreeBytes = min(out.FreeBytes, max(opts.Cap-out.UsedBytes, 0))
		}
		out.HoursLeft = int(out.FreeBytes / bytesPerHour)
	}
	return out, nil
}

// bytesPerHour is about what an hour of broadcast HD takes, for hours left and the forecast.
const bytesPerHour = 3 << 30

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

// itemIDs reads the recordings' titles that the viewer may see, with its place in each.
func (s *Server) itemIDs(ctx context.Context, v db.Viewer, rows []db.Recording) (map[int64]db.RecordedTitle, error) {
	var ids []int64
	for _, r := range rows {
		if r.ItemID != 0 {
			ids = append(ids, r.ItemID)
		}
	}
	return s.db.RecordedTitles(ctx, v, ids)
}

func (s *Server) listRecordings(r *http.Request, _ None) (Page[DvrRecording], error) {
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return Page[DvrRecording]{}, err
	}
	ctx := r.Context()
	query := r.URL.Query()
	state := query.Get("state")
	q := db.RecordingQuery{Upcoming: state != "history" && state != "recorded", Made: state == "recorded", Limit: 50}
	if query.Has("group") {
		group := query.Get("group")
		q.Group = &group
	}
	scope, err := s.recordingReadScope(r, lv)
	if err != nil {
		return Page[DvrRecording]{}, err
	}
	q.Scope = &scope
	var c struct {
		After int64  `json:"a"`
		ID    int64  `json:"i"`
		Title string `json:"t,omitempty"`
	}
	if text := query.Get("cursor"); text != "" && decodeOpaque(text, &c) != nil {
		return Page[DvrRecording]{}, ErrField("cursor", "This cursor is not one this list gave.")
	}
	q.After, q.AfterID, q.AfterTitle = c.After, c.ID, c.Title
	rows, err := s.db.Recordings(ctx, q)
	if err != nil {
		return Page[DvrRecording]{}, err
	}
	var clashes map[int64]dvr.Clash
	if q.Upcoming {
		if clashes, err = s.clashes(ctx); err != nil {
			return Page[DvrRecording]{}, err
		}
	}
	own, err := s.ownProfiles(r)
	if err != nil {
		return Page[DvrRecording]{}, err
	}
	items, err := s.itemIDs(ctx, viewer(r), rows)
	if err != nil {
		return Page[DvrRecording]{}, err
	}
	out := Page[DvrRecording]{Items: []DvrRecording{}}
	for _, row := range rows {
		out.Items = append(out.Items, s.withMay(r, s.withClash(lv, s.recordingOut(ctx, row, false, items), clashes, row.ID, own), row, scope.ProfilePub))
	}
	if len(rows) == q.Limit {
		last := rows[len(rows)-1]
		after := last.StartsAt.UnixMilli()
		if !q.Upcoming && !q.Made {
			after = last.UpdatedAt.UnixMilli()
		}
		next := map[string]any{"a": after, "i": last.ID}
		if q.Made {
			next["t"] = last.Title
		}
		out.NextCursor = encodeOpaque(next)
	}
	return out, nil
}

func (s *Server) bookRecording(r *http.Request, req DvrRecordingBook) (DvrRecording, error) {
	lv, err := s.liveAllowed(r, true)
	if err != nil {
		return DvrRecording{}, err
	}
	ctx := r.Context()
	ch, p, err := s.programme(ctx, viewer(r), lv, req.Programme)
	if err != nil {
		return DvrRecording{}, err
	}
	switch {
	case ch.Kind != db.ChannelFromSource:
		return DvrRecording{}, ErrConflict("cannot_record", "This program can't be recorded.")
	case !rated(lv, p.RatingAge):
		return DvrRecording{}, ErrNotFound()
	case !p.EndsAt.After(s.now()):
		return DvrRecording{}, ErrConflict("programme_over", "This program has ended.")
	}
	names, err := s.db.ProfileNames(ctx, viewer(r).ProfileID)
	if err != nil {
		return DvrRecording{}, err
	}
	if _, err := s.recorder.Library(ctx); err != nil {
		return DvrRecording{}, ErrConflict("recordings_folder", "The recordings folder can't be written.")
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
		return DvrRecording{}, err
	}
	out := s.withMay(r, s.recordingOut(ctx, rec, false, nil), rec, names.PublicID)
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

// askedFor says the viewer's profile booked a booking or also asked for it; profilePub is its
// public id.
func askedFor(r *http.Request, rec db.Recording, profilePub string) bool {
	return rec.ProfileID == CallerFrom(r.Context()).ProfileID || slices.Contains(rec.AlsoWantedBy, profilePub)
}

// mayCancel says the viewer may cancel a booking not yet started: a profile that asked for it
// (it lets go of its own ask), or the owner or an administrator (for everyone).
func mayCancel(r *http.Request, rec db.Recording, profilePub string) bool {
	c := CallerFrom(r.Context())
	return askedFor(r, rec, profilePub) || c.Role == "owner" || c.Role == "admin"
}

// withMay says on a row what the viewer may do with it, by the same rules the change and
// delete routes apply, so the app and the routes never disagree.
func (s *Server) withMay(r *http.Request, out DvrRecording, rec db.Recording, profilePub string) DvrRecording {
	out.MayChange = s.mayChange(r, rec)
	if rec.State == db.RecordingScheduled {
		out.MayCancel = mayCancel(r, rec, profilePub)
	}
	return out
}

func (s *Server) getRecording(r *http.Request, _ None) (DvrRecording, error) {
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return DvrRecording{}, err
	}
	rec, err := s.db.Recording(r.Context(), r.PathValue("id"))
	if err != nil {
		return DvrRecording{}, sourceError(err)
	}
	if ok, err := s.maySeeRecording(r.Context(), r, lv, rec); err != nil || !ok {
		if err != nil {
			return DvrRecording{}, err
		}
		return DvrRecording{}, ErrNotFound()
	}
	items, err := s.itemIDs(r.Context(), viewer(r), []db.Recording{rec})
	if err != nil {
		return DvrRecording{}, err
	}
	clashes, err := s.clashes(r.Context())
	if err != nil {
		return DvrRecording{}, err
	}
	own, err := s.ownProfiles(r)
	if err != nil {
		return DvrRecording{}, err
	}
	names, err := s.db.ProfileNames(r.Context(), viewer(r).ProfileID)
	if err != nil {
		return DvrRecording{}, err
	}
	out := s.withMay(r, s.withClash(lv, s.recordingOut(r.Context(), rec, false, items), clashes, rec.ID, own), rec, names.PublicID)
	if rec.ItemID != 0 {
		for _, session := range s.playback.All() {
			if session.Item.ID == rec.ItemID {
				out.PlayingNow++
			}
		}
	}
	return out, nil
}

func (s *Server) changeRecording(r *http.Request, req DvrRecordingChange) (DvrRecording, error) {
	ctx := r.Context()
	lv, err := s.liveAllowed(r, false)
	if err != nil {
		return DvrRecording{}, err
	}
	rec, err := s.db.Recording(ctx, r.PathValue("id"))
	if err != nil {
		return DvrRecording{}, sourceError(err)
	}
	if ok, err := s.maySeeRecording(ctx, r, lv, rec); err != nil || !ok {
		if err != nil {
			return DvrRecording{}, err
		}
		return DvrRecording{}, ErrNotFound()
	}
	if !s.mayChange(r, rec) {
		return DvrRecording{}, ErrNotPermitted()
	}
	if req.Choice != nil && *req.Choice != "record" && *req.Choice != "skip" && *req.Choice != "" {
		return DvrRecording{}, ErrField("choice", "Choose record or skip.")
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
		return DvrRecording{}, err
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
		if !mayCancel(r, rec, names.PublicID) {
			return None{}, ErrNotPermitted()
		}
		everyone := !askedFor(r, rec, names.PublicID)
		return None{}, s.db.CancelRecording(ctx, rec.PublicID, c.ProfileID, names.PublicID, everyone, s.now())
	case !s.mayChange(r, rec):
		return None{}, ErrNotPermitted()
	case rec.State == db.RecordingRecording && r.URL.Query().Get("action") == "stop":
		s.recorder.Stop(rec.ID)
		return None{}, nil
	}
	return None{}, s.recorder.Delete(ctx, rec) // which stops anyone playing it
}

func (s *Server) dvrTuners(r *http.Request, _ None) (DvrTuners, error) {
	ctx := r.Context()
	sources, err := s.db.LiveSources(ctx)
	if err != nil {
		return DvrTuners{}, err
	}
	out := DvrTuners{InUse: []DvrTunerUse{}, Conflicts: []DvrRecording{}, Sources: []DvrTunerSource{}}
	bySource := map[int64]int{}
	for _, src := range sources {
		if src.Kind == "guide" {
			continue
		}
		using := s.tuners.Using(src.ID)
		bySource[src.ID] = len(out.Sources)
		out.Sources = append(out.Sources, DvrTunerSource{SourceID: src.PublicID, SourceName: src.Name, TunerCount: src.TunerCount, Paused: src.Paused,
			InUse: len(using), Active: []DvrTunerUse{}, Upcoming: []DvrRecording{}})
		index := len(out.Sources) - 1
		for channel, uses := range using {
			name, err := s.db.ChannelName(ctx, channel)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return DvrTuners{}, err
			}
			for _, u := range uses {
				using := u.Name
				if !u.Record {
					using = "A viewer: " + u.Name
				}
				entry := DvrTunerUse{Channel: name, Using: using}
				out.InUse = append(out.InUse, entry)
				out.Sources[index].Active = append(out.Sources[index].Active, entry)
			}
		}
	}
	upcoming, err := s.db.Upcoming(ctx, s.now().Add(12*time.Hour))
	if err != nil {
		return DvrTuners{}, err
	}
	losers, err := s.recorder.Clashes(ctx, upcoming)
	if err != nil {
		return DvrTuners{}, err
	}
	channelSources := map[int64]int64{}
	for _, rec := range upcoming {
		row := s.withMay(r, s.recordingOut(ctx, rec, true, nil), rec, "")
		source, known := channelSources[rec.ChannelID]
		if !known {
			_, source, _, err = s.db.ChannelLocator(ctx, rec.ChannelID)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return DvrTuners{}, err
			}
			channelSources[rec.ChannelID] = source
		}
		if index, present := bySource[source]; present {
			out.Sources[index].Upcoming = append(out.Sources[index].Upcoming, row)
			if losers[rec.ID] {
				out.Sources[index].Conflicts++
			}
		}
		if losers[rec.ID] {
			out.Conflicts = append(out.Conflicts, row)
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
