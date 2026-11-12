package librarychannels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// How far the schedule reaches. The job tops a channel up once less than topUpBelow is
// scheduled, so each channel is added to about once a day, and keeps a day of what aired.
const (
	ahead      = 7 * 24 * time.Hour
	topUpBelow = 6 * 24 * time.Hour
	keepAired  = 24 * time.Hour
	checkEvery = 10 * time.Minute
)

// Service builds and tops up every Library Channel's schedule in the background, copies it
// into the guide, and answers what is on. It keeps no channel in memory: each read takes what
// it needs from the database.
type Service struct {
	db   *db.DB
	log  *slog.Logger
	now  func() time.Time
	wake chan struct{}
	// building is held while one channel's schedule is built, so two builds never race.
	building sync.Mutex
	// failed is when each channel's last build failed: it is tried again an hour later, or as
	// soon as the owner saves anything.
	failed sync.Map
}

// New makes the service. Run starts its background work.
func New(d *db.DB, log *slog.Logger, now func() time.Time) *Service {
	return &Service{db: d, log: log, now: now, wake: make(chan struct{}, 1)}
}

// Run builds what is due until ctx ends: at start (after copying into the guide whatever its
// copy lacks), every few minutes, and as soon as Wake is called.
func (s *Service) Run(ctx context.Context) {
	if err := s.copyMissing(ctx); err != nil && ctx.Err() == nil {
		s.log.Error("copying Library Channel schedules into the guide failed", "error", err)
	}
	tick := time.NewTicker(checkEvery)
	defer tick.Stop()
	for {
		s.scheduleDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.wake:
		}
	}
}

// Wake asks the job to look for work now: a channel was saved, turned on or regenerated.
func (s *Service) Wake() {
	s.failed.Clear()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) scheduleDue(ctx context.Context) {
	ids, err := s.db.LibraryChannelsToSchedule(ctx, s.now().Add(topUpBelow))
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("listing Library Channels to schedule failed", "error", err)
		}
		return
	}
	for _, id := range ids {
		if at, ok := s.failed.Load(id); ok && s.now().Sub(at.(time.Time)) < time.Hour {
			continue
		}
		if err := s.Schedule(ctx, id); err != nil && ctx.Err() == nil {
			s.failed.Store(id, s.now())
			s.log.Error("building a Library Channel's schedule failed", "channel", id, "error", err)
		}
	}
	// A copy into the guide that failed is made again here, not only at the next start.
	if err := s.copyMissing(ctx); err != nil && ctx.Err() == nil {
		s.log.Error("copying Library Channel schedules into the guide failed", "error", err)
	}
}

// Schedule builds one channel's schedule: the rest of it again from the end of the programme
// now on when it was saved or Regenerate was asked for, otherwise on from where it ends. What
// has aired and the programme now on never change.
func (s *Service) Schedule(ctx context.Context, publicID string) error {
	s.building.Lock()
	defer s.building.Unlock()
	ch, err := s.db.LibraryChannelForSchedule(ctx, publicID)
	if errors.Is(err, db.ErrNotFound) || err == nil && !ch.On {
		return nil
	}
	if err != nil {
		return err
	}
	now := s.now().Truncate(time.Second)
	b, loc, st, from, discarded, err := s.plan(ctx, ch, now)
	var rows []db.ScheduleRow
	var report Report
	if err == nil {
		rows, report, err = Build(ctx, s.db, b, loc, &st, from, now.Add(ahead), now, discarded)
	}
	if err != nil {
		if ctx.Err() == nil {
			// Say why on the channel's row instead of "building" for good.
			s.db.LibraryChannelFailed(context.WithoutCancel(ctx), ch.ChannelID, ch.Revision,
				"This channel's schedule couldn't be made. Check its conditions, then save it again.")
		}
		return err
	}
	st.Titles, st.Unplaceable = report.Titles, report.Unplaceable
	stored, _ := json.Marshal(st)
	through := from
	if len(rows) > 0 {
		through = rows[len(rows)-1].EndsAt
	}
	word, problem := report.Word()
	err = s.db.SaveChannelSchedule(ctx, db.ScheduleWrite{ChannelID: ch.ChannelID, Revision: ch.Revision, ReadThrough: ch.ScheduledThrough, From: from, Rows: rows,
		State: string(stored), Through: through, Word: word, Problem: problem, ForgetEnd: now.Add(-keepAired)})
	if errors.Is(err, db.ErrChannelChanged) {
		return nil // saved again meanwhile: the next pass builds from the newer settings
	}
	if err != nil {
		return err
	}
	return s.copyGuide(ctx, publicID, from, now, rows)
}

// plan reads what building a channel's schedule starts from: its document, its time zone,
// its stored orders, where the new part begins (the end of the programme now on, for a
// rebuild or a schedule that has run out) and the programmes a rebuild throws away.
func (s *Service) plan(ctx context.Context, ch db.LibraryChannelSchedule, now time.Time) (*LibraryChannelBuilder, *time.Location, State, time.Time,
	[]Discarded, error) {
	var st State
	b, err := Parse(ch.Builder)
	if err != nil {
		return nil, nil, st, time.Time{}, nil, err
	}
	loc, err := LoadZone(ch.TimeZone)
	if err != nil {
		return nil, nil, st, time.Time{}, nil, err
	}
	if ch.State != "" && json.Unmarshal([]byte(ch.State), &st) != nil {
		st = State{} // a state that cannot be read starts the orders again
	}
	from := ch.ScheduledThrough
	var discarded []Discarded
	if ch.Rebuild || from.Before(now) {
		from = now
		current, err := s.db.ChannelProgrammes(ctx, ch.ChannelID, now, now.Add(time.Millisecond))
		if err != nil {
			return nil, nil, st, time.Time{}, nil, err
		}
		if len(current) > 0 && !current[0].StartsAt.After(now) {
			from = current[0].EndsAt
		}
		if ch.Rebuild && ch.ScheduledThrough.After(from) {
			if discarded, err = s.discarded(ctx, ch.ChannelID, from, ch.ScheduledThrough); err != nil {
				return nil, nil, st, time.Time{}, nil, err
			}
		}
	}
	return b, loc, st, from, discarded, nil
}

func (s *Service) discarded(ctx context.Context, channelID int64, from, through time.Time) ([]Discarded, error) {
	rows, err := s.db.ChannelProgrammes(ctx, channelID, from, through.Add(time.Millisecond))
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range rows {
		if r.ItemID != 0 && !r.StartsAt.Before(from) {
			ids = append(ids, r.ItemID)
		}
	}
	titles, err := s.db.ProgrammeTitles(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Discarded, 0, len(ids))
	for _, id := range ids {
		t := titles[id]
		d := Discarded{ItemID: id, ShowID: t.ShowID}
		if t.Season != nil && t.Episode != nil {
			d.Season, d.Episode = *t.Season, *t.Episode
		}
		out = append(out, d)
	}
	return out, nil
}

// copyGuide writes a channel's new programmes into the guide, with each title's details.
func (s *Service) copyGuide(ctx context.Context, publicID string, from, now time.Time, rows []db.ScheduleRow) error {
	var ids []int64
	for _, r := range rows {
		if r.ItemID != 0 {
			ids = append(ids, r.ItemID)
		}
	}
	titles, err := s.db.ProgrammeTitles(ctx, ids)
	if err != nil {
		return err
	}
	out := make([]db.GuideProgramme, 0, len(rows))
	for _, r := range rows {
		p := db.GuideProgramme{StartsAt: r.StartsAt, EndsAt: r.EndsAt, ProgrammeKey: "slate", ItemID: r.ItemID}
		if t, ok := titles[r.ItemID]; ok {
			p.ProgrammeKey, p.Title, p.EpisodeTitle, p.Season, p.Episode = t.PublicID, t.Title, t.EpisodeTitle, t.Season, t.Episode
			p.Summary, p.Rating, p.RatingAge = t.Summary, t.ContentRating, t.RatingAge
			if t.ShowID != 0 {
				p.SeriesKey = fmt.Sprintf("show:%d", t.ShowID)
			}
		} else {
			p.ItemID = 0
		}
		out = append(out, p)
	}
	return s.db.CopyChannelGuide(ctx, publicID, from, now.Add(-keepAired), out)
}

// copyMissing copies again each channel whose copy in the guide is missing or behind, as
// after the guide file was deleted.
func (s *Service) copyMissing(ctx context.Context) error {
	now := s.now()
	since := now.Add(-keepAired)
	after := 0
	for {
		page, err := s.db.LibraryChannels(ctx, after, 100, false)
		if err != nil || len(page) == 0 {
			return err
		}
		for _, ch := range page {
			after = ch.Number
			same, err := s.db.GuideCopyMatches(ctx, ch.ID, ch.PublicID, since)
			if err != nil {
				return err
			}
			if same {
				continue
			}
			rows, err := s.db.ChannelProgrammes(ctx, ch.ID, since, now.Add(2*ahead))
			if err != nil {
				return err
			}
			rows = slices.DeleteFunc(rows, func(r db.ScheduleRow) bool { return r.StartsAt.Before(since) })
			if err := s.copyGuide(ctx, ch.PublicID, since, now, rows); err != nil {
				return err
			}
		}
	}
}

// Dropped removes a deleted channel's programmes from the guide.
func (s *Service) Dropped(ctx context.Context, publicID string) error {
	return s.db.DropChannelGuide(ctx, db.LibraryGuideSource, publicID)
}

// Describe is one sentence saying what a channel plays, for its row in the list.
func Describe(b *LibraryChannelBuilder) string {
	what := "Movies and TV shows"
	switch {
	case len(b.Kinds) == 1 && b.Kinds[0] == "movie":
		what = "Movies"
	case len(b.Kinds) == 1 && b.Kinds[0] == "show":
		what = "TV shows"
	}
	var parts []string
	if n := len(b.Conditions); n == 1 {
		parts = append(parts, "that match 1 condition")
	} else if n > 1 {
		parts = append(parts, fmt.Sprintf("that match %d conditions", n))
	}
	if b.AddedWithinDays > 0 {
		parts = append(parts, fmt.Sprintf("added in the last %d days", b.AddedWithinDays))
	}
	order := map[string]string{"": "shuffled", "shuffle": "shuffled", "random": "at random", "inOrder": "in order",
		"inOrderThenShuffle": "in order, then shuffled", "leastPlayed": "least played first"}[b.Order]
	sentence := strings.TrimSpace(what + " " + strings.Join(parts, ", ") + ", " + order)
	if n := len(b.Blocks); n > 0 {
		sentence += fmt.Sprintf(", with %d scheduled block", n)
		if n > 1 {
			sentence += "s"
		}
	}
	return sentence + "."
}
