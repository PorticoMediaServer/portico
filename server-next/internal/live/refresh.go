package live

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// guideKept is how long listings are kept after they end.
const guideKept = 24 * time.Hour

// Run refreshes each source that is due, until ctx ends: on its own interval, and a failing
// one again after half an hour.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		s.refreshDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.wake:
		}
	}
}

// Wake asks the job to look for sources due now.
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) refreshDue(ctx context.Context) {
	sources, err := s.db.LiveSources(ctx)
	if err != nil {
		return
	}
	now := s.now()
	for _, src := range sources {
		var cfg LiveSourceConfig
		json.Unmarshal([]byte(src.Config), &cfg)
		every := time.Duration(max(cfg.RefreshHours, 0)) * time.Hour
		if every == 0 {
			every = 6 * time.Hour
		}
		s.mu.Lock()
		tried := s.tried[src.ID]
		s.mu.Unlock()
		if src.Paused || now.Sub(tried) < retryAfter || !src.RefreshedAt.IsZero() && now.Sub(src.RefreshedAt) < every {
			continue
		}
		if err := s.Refresh(ctx, src.PublicID); err != nil && ctx.Err() == nil {
			s.log.Warn("refreshing a Live TV source failed", "source", src.PublicID, "error", err)
		}
	}
}

// Refreshing says whether a source is being refreshed now.
func (s *Service) Refreshing(sourceID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[sourceID] != nil
}

// Refresh reads a source again: its channels and its guide. A refresh that fails keeps the
// last channels and guide and says why. One already running is waited for, not repeated.
func (s *Service) Refresh(ctx context.Context, publicID string) error {
	src, err := s.db.LiveSource(ctx, publicID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if done := s.running[src.ID]; done != nil {
		s.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan struct{})
	s.running[src.ID], s.tried[src.ID] = done, s.now()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, src.ID)
		s.mu.Unlock()
		close(done)
	}()
	report, tuners, err := s.refresh(ctx, src)
	now := s.now()
	if err != nil {
		state := StateTrouble
		switch {
		case errors.Is(err, ErrSignIn):
			state = StateSignIn
		case src.RefreshedAt.IsZero():
			state = StateBroken
		}
		if saveErr := s.db.SourceRefreshed(context.WithoutCancel(ctx), src.ID, state, err.Error(), "", 0, now); saveErr != nil {
			return saveErr
		}
		return err
	}
	raw, _ := json.Marshal(report)
	return s.db.SourceRefreshed(context.WithoutCancel(ctx), src.ID, StateUpToDate, "", string(raw), tuners, now)
}

func (s *Service) refresh(ctx context.Context, src db.LiveSource) (LiveSourceReport, int, error) {
	var cfg LiveSourceConfig
	if err := json.Unmarshal([]byte(src.Config), &cfg); err != nil {
		return LiveSourceReport{}, 0, errors.New("the source's settings could not be read")
	}
	var signIn LiveSignIn
	if src.Kind == "xtream" {
		var err error
		if signIn, err = s.SignIn(src.PublicID, src.Secret); err != nil {
			return LiveSourceReport{}, 0, ErrSignIn
		}
	}
	var upload io.Reader
	if cfg.Uploaded {
		f, err := os.Open(s.UploadPath(src.PublicID, "playlist"))
		if err != nil {
			return LiveSourceReport{}, 0, errors.New("the uploaded playlist is missing; upload it again")
		}
		defer f.Close()
		upload = f
	}
	var report LiveSourceReport
	pulled, err := s.pull(ctx, src.Kind, cfg, signIn, upload, &report)
	if err != nil {
		return report, 0, err
	}
	if src.Kind != "guide" && len(pulled.Channels) == 0 {
		return report, 0, errors.New("the source sent no channel this server can use")
	}
	// The guide: this source's own, or for one without, the first guide-only source's.
	guideSource := int64(0)
	names := map[string]string{} // a guide channel's names and numbers, to match channels sent without a guide id
	var guide io.ReadCloser
	switch {
	case cfg.GuideUploaded:
		if guide, err = os.Open(s.UploadPath(src.PublicID, "guide")); err != nil {
			return report, 0, errors.New("the uploaded guide is missing; upload it again")
		}
	case pulled.guideURL != "":
		if guide, err = s.download(ctx, pulled.guideURL, src.PublicID); err != nil {
			return report, 0, err
		}
	}
	if guide != nil {
		defer guide.Close()
		guideSource = src.ID
		now := s.now()
		guideDays := Document.Get(s.Settings).GuideDays
		err := s.db.ReplaceSourceGuide(ctx, src.ID, func(add func(db.GuideProgramme) error) error {
			return ParseGuide(guide, &report, func(c GuideChannel) error {
				for _, n := range c.Names {
					if key := norm(n); key != "" && len(names) < 4*MaxChannels {
						names[key] = c.Key
					}
				}
				return nil
			}, func(p Programme) error {
				if p.EndsAt.Before(now.Add(-guideKept)) || p.StartsAt.After(now.AddDate(0, 0, guideDays)) {
					return nil
				}
				report.Programmes++
				if report.GuideThrough == nil || p.EndsAt.After(*report.GuideThrough) {
					ends := p.EndsAt.UTC()
					report.GuideThrough = &ends
				}
				return add(db.GuideProgramme{GuideKey: p.GuideKey, StartsAt: p.StartsAt, EndsAt: p.EndsAt, ProgrammeKey: p.Key,
					SeriesKey: p.SeriesKey, Title: p.Title, EpisodeTitle: p.EpisodeTitle, Season: p.Season, Episode: p.Episode,
					Summary: p.Summary, Rating: p.Rating, RatingAge: p.RatingAge, Flags: p.Flags, ImageURL: p.Image})
			})
		})
		if err != nil {
			return report, 0, err
		}
	} else if src.Kind != "guide" {
		sources, err := s.db.LiveSources(ctx)
		if err != nil {
			return report, 0, err
		}
		for _, other := range sources {
			if other.Kind == "guide" {
				guideSource = other.ID
				break
			}
		}
	}
	if src.Kind == "guide" {
		return report, 0, nil
	}
	rows := make([]db.SourceChannel, 0, len(pulled.Channels))
	next := max(cfg.FirstNumber, 1)
	for _, c := range pulled.Channels {
		row := db.SourceChannel{Key: c.Key, Name: c.Name, Group: c.Group, GuideSource: guideSource, GuideKey: c.GuideKey, Locator: c.Locator,
			Logo: c.Logo, BroughtIn: brought(cfg, c)}
		if n, ok := ChannelNumber(c.Number); ok {
			row.SourceNumber = n
		}
		if row.GuideKey == "" && guideSource == src.ID {
			if key, ok := names[norm(c.Name)]; ok {
				row.GuideKey = key
			} else if key, ok := names[norm(c.Number)]; ok {
				row.GuideKey = key
			}
		}
		if row.GuideKey != "" {
			report.ChannelsWithGuide++
		}
		if cfg.Numbering == "sequential" || row.SourceNumber == 0 {
			row.Number = next * 1000
			next++
		} else {
			row.Number = row.SourceNumber
		}
		if row.BroughtIn {
			report.Channels++
		}
		rows = append(rows, row)
	}
	if err := s.db.ImportSourceChannels(ctx, src.ID, rows, s.now()); err != nil {
		return report, 0, err
	}
	return report, pulled.Tuners, nil
}

// Preview is what a source would bring in and leave out, and why, without saving anything.
type Preview struct {
	Report   LiveSourceReport
	Channels []LiveSentChannel // the first 100, without their addresses
	Tuners   int
}

// Preview reads a source as it is being added. playlist and guide are what the owner
// uploaded, or "".
func (s *Service) Preview(ctx context.Context, kind string, cfg LiveSourceConfig, signIn LiveSignIn, playlist, guide string) (Preview, error) {
	var out Preview
	var upload io.Reader
	if playlist != "" {
		upload = strings.NewReader(playlist)
	}
	pulled, err := s.pull(ctx, kind, cfg, signIn, upload, &out.Report)
	if err != nil {
		return out, err
	}
	out.Tuners = pulled.Tuners
	for _, c := range pulled.Channels {
		if brought(cfg, c) {
			out.Report.Channels++
		}
		if len(out.Channels) < 100 {
			c.Locator = ""
			out.Channels = append(out.Channels, c)
		}
	}
	var r io.ReadCloser
	switch {
	case guide != "":
		r = io.NopCloser(strings.NewReader(guide))
	case pulled.guideURL != "":
		if r, err = s.fetch(ctx, pulled.guideURL, maxGuideBytes); err != nil {
			return out, err
		}
	}
	if r != nil {
		defer r.Close()
		err = ParseGuide(r, &out.Report, func(GuideChannel) error { return nil }, func(Programme) error { out.Report.Programmes++; return nil })
	}
	return out, err
}

// download fetches a guide into a file in the data folder first, so the guide's writer is
// held only while programmes are written, never while a slow provider sends them. The file
// is removed when it is closed.
func (s *Service) download(ctx context.Context, address, sourceID string) (io.ReadCloser, error) {
	body, err := s.fetch(ctx, address, maxGuideBytes)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	path := s.UploadPath(sourceID, "download")
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(file, body); err != nil {
		file.Close()
		os.Remove(path)
		return nil, errors.New("the guide could not be downloaded")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	return removeOnClose{file}, nil
}

type removeOnClose struct{ *os.File }

func (f removeOnClose) Close() error {
	err := f.File.Close()
	os.Remove(f.Name())
	return err
}
