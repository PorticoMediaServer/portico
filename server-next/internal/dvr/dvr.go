// Package dvr records Live TV: bookings and series rules, the recorder that takes a tuner for
// each airing and writes it into the one Recorded TV library, and tuner clashes. Only
// internal/db runs SQL; the stream comes from internal/live's shared inputs.
package dvr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// Options are the owner's recording settings. Until the dvr settings document exists the
// server runs on these defaults.
type Options struct {
	// Folder is where recordings are written: never inside Portico's data folder.
	Folder              string
	PadBefore, PadAfter time.Duration
	// Floor is the free space recording stops before.
	Floor int64
	// StopViewers is the owner's tuner clash choice: false keeps the viewer watching (the
	// default), true stops their stream after a countdown and records.
	StopViewers bool
	Countdown   time.Duration
	// CountdownFloor is the least a viewer is warned for: 10 seconds.
	CountdownFloor time.Duration
	// KeepDays and KeepNewest are the owner's default keep rule for recordings a series rule's
	// own choice does not cover: 0 and 0 keep every recording until it is deleted.
	KeepDays, KeepNewest int
	// Cap, when above 0, is the most the recordings may take together.
	Cap int64
}

// Defaults are the owner's recording settings until changed.
func Defaults(home string) Options {
	return Options{Folder: filepath.Join(home, "Recorded TV"), PadBefore: time.Minute, PadAfter: 3 * time.Minute, Floor: 10 << 30,
		Countdown: 60 * time.Second, CountdownFloor: 10 * time.Second}
}

// Viewers is what the recorder needs of the live sessions: to warn one before its tuner is
// taken, and to end it.
type Viewers interface {
	OnSource(sourceID, exceptChannel int64) []string
	Warn(id string, until time.Time, reason string)
	End(id string)
}

// Service books, records and keeps the recordings.
type Service struct {
	db      *db.DB
	tuners  *live.Tuners
	sources *live.Service
	viewers Viewers
	log     *slog.Logger
	now     func() time.Time
	options func() Options
	scanner *scan.Scanner

	// ticking makes one Tick at a time, so one booking is never started twice.
	ticking sync.Mutex
	mu      sync.Mutex
	running map[int64]*running
	warned  map[string]time.Time // a viewing told it will be stopped, and when
	// Deleted is told the title of each recording deleted, so anyone playing it is stopped,
	// whether the owner deleted it or a keep rule did.
	Deleted func(ctx context.Context, itemID int64)
	// busy holds the bookings this run of the server tried and found every tuner taken: one
	// whose time passed without being tried was missed because the server was off.
	busy map[int64]bool
}

type running struct {
	stop   context.CancelFunc
	byHand bool
}

// New makes the service; Run starts the recorder.
func New(d *db.DB, tuners *live.Tuners, sources *live.Service, viewers Viewers, log *slog.Logger, now func() time.Time,
	options func() Options) *Service {
	return &Service{db: d, tuners: tuners, sources: sources, viewers: viewers, log: log, now: now, options: options,
		scanner: &scan.Scanner{DB: d}, running: map[int64]*running{}, warned: map[string]time.Time{}, busy: map[int64]bool{}}
}

// Library is the one Recorded TV library, made with the first booking.
func (s *Service) Library(ctx context.Context) (db.Library, error) {
	folder := s.options().Folder
	id, err := s.db.SettingOrCreate(ctx, "dvr.library", func() string { return "" })
	if err != nil {
		return db.Library{}, err
	}
	if id != "" {
		libraries, err := s.db.AllLibraries(ctx)
		if err != nil {
			return db.Library{}, err
		}
		for _, l := range libraries {
			if l.PublicID == id {
				return l, nil
			}
		}
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return db.Library{}, fmt.Errorf("the recordings folder can't be made: %w", err)
	}
	lib, err := s.db.CreateLibrary(ctx, "Recorded TV", db.LibraryShows, "", []string{folder})
	if err != nil {
		return db.Library{}, err
	}
	return lib, s.db.SaveSetting(ctx, "dvr.library", lib.PublicID, s.now())
}

// Run records until ctx ends. At start, a recording the server stopped in the middle of
// carries on as Partly recorded, and a booking whose whole time passed while the server was
// off goes to History.
func (s *Service) Run(ctx context.Context) {
	s.Resume(ctx)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	rulesAt := time.Time{}
	for {
		if s.now().Sub(rulesAt) >= 10*time.Minute {
			rulesAt = s.now()
			if err := s.FollowRules(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("following series rules failed", "error", err)
			}
			s.db.ForgetOldHistory(ctx, s.now())
			s.db.ForgetEndedReminders(ctx, s.now())
			if err := s.Keep(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("applying keep rules failed", "error", err)
			}
		}
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Resume settles what a stop of the server left: see Run.
func (s *Service) Resume(ctx context.Context) {
	upcoming, err := s.db.Upcoming(ctx, s.now().Add(time.Minute))
	if err != nil {
		return
	}
	for _, r := range upcoming {
		end := r.EndsAt.Add(r.PadAfter)
		switch {
		case r.State == db.RecordingScheduled && end.Before(s.now()):
			s.db.RecordingFinished(ctx, r.ID, db.RecordingFailed, "The server was off", s.now())
		case r.State == db.RecordingRecording && end.Before(s.now()):
			s.db.RecordingFinished(ctx, r.ID, db.RecordingPartial, "The server was restarted", s.now())
		case r.State == db.RecordingRecording:
			s.db.RecordingNote(ctx, r.ID, "The server was restarted", s.now())
		}
	}
}

// Tick starts what is due and ends what has passed. It is safe to call at any time.
func (s *Service) Tick(ctx context.Context) {
	s.ticking.Lock()
	defer s.ticking.Unlock()
	now := s.now()
	upcoming, err := s.db.Upcoming(ctx, now.Add(12*time.Hour))
	if err != nil {
		return
	}
	losers, err := s.Clashes(ctx, upcoming)
	if err != nil {
		return
	}
	for _, r := range upcoming {
		start, end := r.StartsAt.Add(-r.PadBefore), r.EndsAt.Add(r.PadAfter)
		s.mu.Lock()
		_, busy := s.running[r.ID]
		s.mu.Unlock()
		switch {
		case busy, start.After(now):
		case r.Choice == "skip":
			s.db.RecordingFinished(ctx, r.ID, db.RecordingSkipped, "Skipped", now)
		case !r.InGuide && r.State == db.RecordingScheduled && !end.After(now):
			s.db.RecordingFinished(ctx, r.ID, db.RecordingFailed, "No longer in the guide", now)
		case !r.InGuide && r.State == db.RecordingScheduled:
			// The guide no longer lists it: nothing is recorded in its place.
		case !end.After(now):
			s.mu.Lock()
			reason := "The server was off"
			if s.busy[r.ID] {
				reason = "Every tuner was busy"
			}
			delete(s.busy, r.ID)
			s.mu.Unlock()
			if r.State == db.RecordingRecording {
				reason = ""
			}
			state := db.RecordingFailed
			if r.State == db.RecordingRecording {
				state = db.RecordingPartial
			}
			s.db.RecordingFinished(ctx, r.ID, state, reason, now)
		case losers[r.ID] && r.State == db.RecordingScheduled:
			// Lost a clash: it waits for a tuner and is partly recorded if one comes free.
			s.mu.Lock()
			s.busy[r.ID] = true
			s.mu.Unlock()
		default:
			s.start(ctx, r, end)
		}
	}
}

// start takes a tuner for a recording and writes the stream until end.
func (s *Service) start(ctx context.Context, r db.Recording, end time.Time) {
	opts := s.options()
	if s.full(ctx) {
		s.db.RecordingFinished(ctx, r.ID, db.RecordingFailed, "Space ran out", s.now())
		return
	}
	locator, sourceID, tuners, err := s.db.ChannelLocator(ctx, r.ChannelID)
	if err != nil {
		s.db.RecordingFinished(ctx, r.ID, db.RecordingFailed, "The channel is no longer available", s.now())
		return
	}
	address, err := s.address(ctx, sourceID, locator)
	if err != nil {
		s.db.RecordingFinished(ctx, r.ID, db.RecordingFailed, "The source needs its sign-in again", s.now())
		return
	}
	use := &live.Use{Record: true, Name: r.Title}
	in, err := s.tuners.Tune(r.ChannelID, sourceID, tuners, address, use)
	var busy *live.NoTuner
	if errors.As(err, &busy) {
		s.mu.Lock()
		s.busy[r.ID] = true
		s.mu.Unlock()
		s.clash(sourceID, r)
		return
	}
	if err != nil {
		return
	}
	lib, err := s.Library(ctx)
	if err != nil {
		in.Leave(use)
		s.db.RecordingFinished(ctx, r.ID, db.RecordingFailed, "The recordings folder can't be written", s.now())
		return
	}
	dir, name := Layout(r)
	file, err := s.openFile(opts.Folder, dir, name, r)
	if err != nil {
		in.Leave(use)
		s.db.RecordingFinished(ctx, r.ID, db.RecordingFailed, "The recordings folder can't be written", s.now())
		return
	}
	reason := ""
	if s.now().After(r.StartsAt.Add(time.Minute)) && r.State == db.RecordingScheduled {
		reason = "Started late"
	}
	s.db.RecordingStarted(ctx, r.ID, reason, s.now())
	work, cancel := context.WithDeadline(context.WithoutCancel(ctx), end)
	run := &running{stop: cancel}
	s.mu.Lock()
	s.running[r.ID] = run
	s.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			in.Leave(use)
			file.Close()
			s.mu.Lock()
			delete(s.running, r.ID)
			s.mu.Unlock()
		}()
		title := func() {
			if _, err := s.scanner.Scan(context.WithoutCancel(ctx), lib); err != nil {
				s.log.Warn("scanning Recorded TV failed", "error", err)
				return
			}
			item, err := s.db.ItemForFile(context.WithoutCancel(ctx), lib.ID, dir, name)
			if err != nil {
				s.log.Warn("the scan made no title of a recording", "file", name, "error", err)
				return
			}
			s.db.RecordingTitle(context.WithoutCancel(ctx), r.ID, item)
		}
		// The title appears a moment after recording begins, so it can be watched from its start.
		go func() {
			select {
			case <-work.Done():
			case <-time.After(3 * time.Second):
				title()
			}
		}()
		err := in.Read(work, time.Time{}, &roomCheck{w: file, full: func() bool { return s.full(context.WithoutCancel(ctx)) }})
		state, why := db.RecordingRecorded, ""
		current, _ := s.db.Recording(context.WithoutCancel(ctx), r.PublicID)
		switch {
		case errors.Is(err, errNoRoom):
			state, why = db.RecordingPartial, "Space ran out"
		case run.byHand:
			state, why = db.RecordingPartial, "Stopped by hand"
		case errors.Is(err, context.DeadlineExceeded) || work.Err() == context.DeadlineExceeded:
			if current.Reason != "" {
				state = db.RecordingPartial
			}
		case ctx.Err() != nil:
			return // the server is stopping: the restart carries on and marks it partly recorded
		default:
			state, why = db.RecordingPartial, "The source stopped"
		}
		file.Sync()
		title()
		s.db.RecordingFinished(context.WithoutCancel(ctx), r.ID, state, why, s.now())
	}()
}

// full says recording must stop: the recordings folder's disk is below the owner's free-space
// floor, or the recordings have reached the owner's cap.
func (s *Service) full(ctx context.Context) bool {
	opts := s.options()
	if !diskspace.Room(opts.Folder, opts.Floor) {
		return true
	}
	if opts.Cap > 0 {
		lib, err := s.Library(ctx)
		if err != nil {
			return true
		}
		used, err := s.db.RecordedBytes(ctx, lib.ID)
		return err != nil || used >= opts.Cap
	}
	return false
}

var errNoRoom = errors.New("space ran out")

// roomCheck writes a recording and checks for room every 64 MB and every two seconds, so a
// recording stops before the disk falls below the floor rather than filling it.
type roomCheck struct {
	w       io.Writer
	full    func() bool
	written int64
	checked time.Time
}

func (c *roomCheck) Write(p []byte) (int, error) {
	if c.written >= 64<<20 || time.Since(c.checked) >= 2*time.Second {
		c.written, c.checked = 0, time.Now()
		if c.full() {
			return 0, errNoRoom
		}
	}
	c.written += int64(len(p))
	return c.w.Write(p)
}

func (s *Service) address(ctx context.Context, sourceID int64, locator string) (string, error) {
	src, err := s.db.LiveSourceByID(ctx, sourceID)
	if err != nil {
		return "", err
	}
	var cfg live.Config
	json.Unmarshal([]byte(src.Config), &cfg)
	var signIn live.SignIn
	if src.Kind == "xtream" {
		if signIn, err = s.sources.SignIn(src.PublicID, src.Secret); err != nil {
			return "", err
		}
	}
	return live.StreamAddress(src.Kind, cfg, signIn, locator)
}

// openFile opens the recording's file to add to (after a restart it carries on in the same
// file) and writes the guide's details beside it, where the scanner reads them.
func (s *Service) openFile(folder, dir, name string, r db.Recording) (*os.File, error) {
	path := filepath.Join(folder, filepath.FromSlash(dir))
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	// The show's own details beside its folder, so the show carries the guide's rating too and
	// a profile that hides unrated titles is treated as for any rated show.
	show := filepath.Join(folder, filepath.FromSlash(strings.Split(dir, "/")[0]), "tvshow.nfo")
	if _, err := os.Stat(show); errors.Is(err, os.ErrNotExist) {
		os.WriteFile(show, []byte(ShowNFO(r)), 0o644)
	}
	nfo := filepath.Join(path, strings.TrimSuffix(name, ".ts")+".nfo")
	if _, err := os.Stat(nfo); errors.Is(err, os.ErrNotExist) {
		os.WriteFile(nfo, []byte(NFO(r)), 0o644)
	}
	return os.OpenFile(filepath.Join(path, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// clash is a recording that found every tuner busy: with the owner's choice to stop a viewer,
// the viewers of one input on the source are warned together, and stopped when the countdown
// ends, which gives its tuner to the recording.
func (s *Service) clash(sourceID int64, r db.Recording) {
	opts := s.options()
	if !opts.StopViewers || s.viewers == nil {
		return // the viewer keeps watching; the recording starts as soon as a tuner is free
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	viewers := s.viewers.OnSource(sourceID, r.ChannelID)
	for id := range s.warned {
		if !slices.Contains(viewers, id) {
			delete(s.warned, id) // stopped or gone meanwhile
		}
	}
	if len(viewers) == 0 {
		return
	}
	if at, ok := s.warned[viewers[0]]; ok {
		if !now.Before(at) {
			for _, id := range viewers {
				delete(s.warned, id)
				s.viewers.End(id)
			}
		}
		return
	}
	wait := max(min(opts.Countdown, r.EndsAt.Sub(now)), opts.CountdownFloor)
	for _, id := range viewers {
		s.warned[id] = now.Add(wait)
		s.viewers.Warn(id, now.Add(wait), "This stream will stop so that "+r.Title+" can be recorded.")
	}
}

// Stop ends a recording by hand; what was captured is kept.
func (s *Service) Stop(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.running[id]; run != nil {
		run.byHand = true
		run.stop()
	}
}

// Recording says whether a recording is being written now.
func (s *Service) Recording(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id] != nil
}

// Layout is where a recording is written, so another tool reads it too: a folder per show
// with season folders, and the date in the name where the guide gives no episode number. Each
// airing has its own file: the name ends with when and on which channel it aired, worked out
// from the booking alone so a restart finds the same file. Each part of a name is cut to fit
// what a disk accepts.
func Layout(r db.Recording) (dir, name string) {
	show := clean(r.Title)
	channel := clean(r.ChannelName)
	if r.ChannelName == "" {
		channel = fmt.Sprintf("channel %d", r.ChannelID)
	}
	aired := r.StartsAt.Format("2006-01-02 15.04") + " " + cut(channel, 40) + fmt.Sprintf(" %d", r.ChannelID)
	if r.Season != nil && r.Episode != nil {
		name = fmt.Sprintf("%s - S%02dE%02d", cut(show, 100), *r.Season, *r.Episode)
		if r.EpisodeTitle != "" {
			name += " - " + cut(clean(r.EpisodeTitle), 60)
		}
		return fmt.Sprintf("%s/Season %02d", cut(show, 200), *r.Season), name + " - " + aired + ".ts"
	}
	return cut(show, 200), cut(show, 160) + " - " + aired + ".ts"
}

// cut shortens text to at most n bytes, on a character boundary.
func cut(text string, n int) string {
	if len(text) <= n {
		return text
	}
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return strings.TrimRight(text[:n], " .")
}

func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return '-'
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	if s == "" {
		return "Recording"
	}
	// Names Windows keeps for devices, as a whole name or before a dot, cannot be files there.
	stem, _, _ := strings.Cut(strings.ToUpper(s), ".")
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		s = "_" + s
	}
	return s
}

// ShowNFO is a show's own details, written once in its folder: its title and the guide's rating.
func ShowNFO(r db.Recording) string {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
	return "<tvshow><title>" + esc(r.Title) + "</title><mpaa>" + esc(r.Rating) + "</mpaa></tvshow>\n"
}

// NFO is the guide's details in the form the scanner reads beside a file.
func NFO(r db.Recording) string {
	esc := func(s string) string {
		return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
	}
	if r.Season != nil && r.Episode != nil {
		title := r.EpisodeTitle
		if title == "" {
			title = fmt.Sprintf("Episode %d", *r.Episode)
		}
		return fmt.Sprintf("<episodedetails><title>%s</title><showtitle>%s</showtitle><season>%d</season><episode>%d</episode>"+
			"<plot>%s</plot><aired>%s</aired><mpaa>%s</mpaa></episodedetails>\n",
			esc(title), esc(r.Title), *r.Season, *r.Episode, esc(r.Details), r.StartsAt.Format("2006-01-02"), esc(r.Rating))
	}
	return fmt.Sprintf("<episodedetails><title>%s</title><showtitle>%s</showtitle>"+
		"<plot>%s</plot><aired>%s</aired><mpaa>%s</mpaa></episodedetails>\n",
		esc(r.Title+" "+r.StartsAt.Format("2006-01-02 15.04")), esc(r.Title), esc(r.Details), r.StartsAt.Format("2006-01-02"), esc(r.Rating))
}

// Clashes works out which upcoming recordings lose a tuner: on each source, wherever more
// airings overlap than it has tuners, the winners are a person's "Record this instead",
// then single bookings before series rules, the higher priority, the earlier start and the
// booking made first. Start early and finish late give way before any programme's own time.
func (s *Service) Clashes(ctx context.Context, upcoming []db.Recording) (map[int64]bool, error) {
	type airing struct {
		r      db.Recording
		source int64
		tuners int
	}
	bySource := map[int64][]airing{}
	for _, r := range upcoming {
		if r.Choice == "skip" {
			continue
		}
		_, source, tuners, err := s.db.ChannelLocator(ctx, r.ChannelID)
		if err != nil {
			continue
		}
		bySource[source] = append(bySource[source], airing{r, source, tuners})
	}
	losers := map[int64]bool{}
	for _, list := range bySource {
		sort.SliceStable(list, func(i, j int) bool {
			a, b := list[i].r, list[j].r
			switch {
			case (a.Choice == "record") != (b.Choice == "record"):
				return a.Choice == "record"
			case (a.RuleID == 0) != (b.RuleID == 0):
				return a.RuleID == 0
			case a.Priority != b.Priority:
				return a.Priority > b.Priority
			case !a.StartsAt.Equal(b.StartsAt):
				return a.StartsAt.Before(b.StartsAt)
			}
			return a.CreatedAt.Before(b.CreatedAt)
		})
		var winners []db.Recording
		for _, a := range list {
			if inUse(winners, a.r) >= max(a.tuners, 1) {
				losers[a.r.ID] = true
				continue
			}
			winners = append(winners, a.r)
		}
	}
	return losers, nil
}

// inUse is the most tuners the winners use at once during a booking's own time, not counting
// its channel: one channel is one tuner however many of its programmes are booked. The most
// is reached at the booking's start or at a winner's start within it. Padding gives way.
func inUse(winners []db.Recording, a db.Recording) int {
	moments := []time.Time{a.StartsAt}
	for _, w := range winners {
		if w.StartsAt.After(a.StartsAt) && w.StartsAt.Before(a.EndsAt) {
			moments = append(moments, w.StartsAt)
		}
	}
	most := 0
	for _, at := range moments {
		channels := map[int64]bool{}
		for _, w := range winners {
			if !w.StartsAt.After(at) && at.Before(w.EndsAt) && w.ChannelID != a.ChannelID {
				channels[w.ChannelID] = true
			}
		}
		most = max(most, len(channels))
	}
	return most
}

// FollowRules books, for every rule that is not paused, each airing in the next two weeks of
// the guide that it asks for and has not been recorded.
func (s *Service) FollowRules(ctx context.Context) error {
	rules, err := s.db.Rules(ctx, 0)
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.follow(ctx, now); err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.Paused {
			continue
		}
		if err := s.followRule(ctx, rule, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) followRule(ctx context.Context, rule db.RecordingRule, now time.Time) error {
	airings, err := s.db.GuideAirings(ctx, rule.SeriesKey, rule.Title, now, now.AddDate(0, 0, 14), 2000)
	if err != nil {
		return err
	}
	profile, err := s.db.ProfileNames(ctx, rule.ProfileID)
	if err != nil {
		return err
	}
	opts := s.options()
	for _, p := range airings {
		if rule.NewOnly && p.Flags&live.FlagNew == 0 {
			continue
		}
		if rule.SeriesKey != "" && p.Season != nil && p.Episode != nil {
			if had, err := s.db.RecordedEpisode(ctx, rule.SeriesKey, *p.Season, *p.Episode); err != nil || had {
				continue
			}
		}
		channels, err := s.db.ChannelsForListings(ctx, p.SourceID, p.GuideKey)
		if err != nil {
			return err
		}
		for _, c := range channels {
			if c.SourceID != rule.SourceID || rule.ChannelID != 0 && c.ID != rule.ChannelID {
				continue
			}
			if had, err := s.db.AiringRecording(ctx, c.ID, p.ProgrammeKey); err == nil && had.State != db.RecordingCancelled {
				break // booked already: nothing to write
			}
			b := db.Booking{ProfileID: rule.ProfileID, ProfileName: profile.Name, ProfilePub: profile.PublicID, RuleID: rule.ID, ChannelID: c.ID,
				ChannelName: c.Name, ProgrammeKey: p.ProgrammeKey, SeriesKey: p.SeriesKey, Title: p.Title, EpisodeTitle: p.EpisodeTitle,
				Season: p.Season, Episode: p.Episode, Details: p.Summary, Rating: p.Rating, RatingAge: p.RatingAge, StartsAt: p.StartsAt,
				EndsAt: p.EndsAt, PadBefore: opts.PadBefore,
				PadAfter: opts.PadAfter, Priority: rule.Priority, Now: now}
			if rule.PadBefore != nil {
				b.PadBefore = *rule.PadBefore
			}
			if rule.PadAfter != nil {
				b.PadAfter = *rule.PadAfter
			}
			if _, _, err := s.db.Book(ctx, b); err != nil {
				return err
			}
			break // one airing of a programme is recorded once
		}
	}
	return nil
}

// follow moves each upcoming booking with its programme when the guide has moved it, by the
// guide's own key, and marks one the guide no longer lists, which is then never recorded:
// a different programme is never recorded in its place.
func (s *Service) follow(ctx context.Context, now time.Time) error {
	upcoming, err := s.db.Upcoming(ctx, now.AddDate(0, 0, 15))
	if err != nil {
		return err
	}
	for _, r := range upcoming {
		if r.State != db.RecordingScheduled || r.ChannelID == 0 {
			continue
		}
		source, key, err := s.db.ChannelListings(ctx, r.ChannelID)
		if err != nil || source == 0 {
			continue
		}
		p, err := s.db.GuideProgrammeByKey(ctx, source, key, r.ProgrammeKey, r, now)
		switch {
		case errors.Is(err, db.ErrNotFound):
			if r.InGuide {
				err = s.db.MoveBooking(ctx, r.ID, r.ProgrammeKey, r.StartsAt, r.EndsAt, false, now)
			} else {
				err = nil
			}
		case err == nil && (!r.InGuide || p.ProgrammeKey != r.ProgrammeKey || !p.StartsAt.Equal(r.StartsAt) || !p.EndsAt.Equal(r.EndsAt)):
			err = s.db.MoveBooking(ctx, r.ID, p.ProgrammeKey, p.StartsAt, p.EndsAt, true, now)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Rescan scans the Recorded TV library, so a deleted recording's title goes with its file.
func (s *Service) Rescan(ctx context.Context) {
	lib, err := s.Library(ctx)
	if err == nil {
		_, err = s.scanner.Scan(ctx, lib)
	}
	if err != nil && ctx.Err() == nil {
		s.log.Warn("scanning Recorded TV failed", "error", err)
	}
}

// Delete removes a recording: its file and the guide's details beside it, then its row, and
// scans the library so its title goes too. Whoever plays it is stopped by the caller.
func (s *Service) Delete(ctx context.Context, r db.Recording) error {
	s.Stop(r.ID)
	if r.ItemID != 0 && s.Deleted != nil {
		s.Deleted(ctx, r.ItemID)
	}
	dir, name := Layout(r)
	folder := filepath.Join(s.options().Folder, filepath.FromSlash(dir))
	os.Remove(filepath.Join(folder, name))
	os.Remove(filepath.Join(folder, strings.TrimSuffix(name, filepath.Ext(name))+".nfo"))
	if err := s.db.DeleteRecording(ctx, r.ID); err != nil {
		return err
	}
	go s.Rescan(context.WithoutCancel(ctx))
	return nil
}

// Keep deletes what a keep choice no longer keeps: a series rule's own (its newest few, or its
// days), and for every other recording the owner's default (the newest few of each show, or
// so many days). Keep on a recording protects it; nothing is deleted to make room.
func (s *Service) Keep(ctx context.Context) error {
	if err := s.keepDefault(ctx); err != nil {
		return err
	}
	rules, err := s.db.Rules(ctx, 0)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.KeepNewest == nil && rule.KeepDays == nil {
			continue
		}
		recorded, err := s.db.RuleRecordings(ctx, rule.ID)
		if err != nil {
			return err
		}
		for n, r := range recorded {
			old := rule.KeepNewest != nil && n >= *rule.KeepNewest ||
				rule.KeepDays != nil && r.StartsAt.Before(s.now().AddDate(0, 0, -*rule.KeepDays))
			if old {
				if err := s.Delete(ctx, r); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) keepDefault(ctx context.Context) error {
	opts := s.options()
	if opts.KeepDays == 0 && opts.KeepNewest == 0 {
		return nil // until deleted
	}
	recorded, err := s.db.KeepCandidates(ctx)
	if err != nil {
		return err
	}
	show, n := "", 0
	for _, r := range recorded {
		key := r.SeriesKey
		if key == "" {
			key = r.Title
		}
		if key != show {
			show, n = key, 0
		}
		n++
		old := opts.KeepNewest > 0 && n > opts.KeepNewest || opts.KeepDays > 0 && r.StartsAt.Before(s.now().AddDate(0, 0, -opts.KeepDays))
		if old {
			if err := s.Delete(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}
