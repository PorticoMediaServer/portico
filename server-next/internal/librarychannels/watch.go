package librarychannels

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Preview is what a channel being edited would play: how many titles match, about how many
// hours that is, some of what plays and today's schedule exactly as saving would make it.
type Preview struct {
	Titles      int
	Unplaceable int
	Hours       int
	Sample      []db.ProgrammeTitle
	Today       []PreviewProgramme
}

// PreviewProgramme is one programme of today's schedule; Title is nil for the slate.
type PreviewProgramme struct {
	StartsAt, EndsAt time.Time
	Title            *db.ProgrammeTitle
}

// Preview builds today's schedule, from local midnight in the channel's time zone, for a
// document that has not been saved. Its cost follows the size of the chosen libraries: it is
// the owner's editor, asked again a moment after each edit.
func (s *Service) Preview(ctx context.Context, b *Builder, loc *time.Location) (Preview, error) {
	now := s.now()
	local := now.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	st := &State{}
	rows, report, err := Build(ctx, s.db, b, loc, st, midnight, midnight.AddDate(0, 0, 1), now, nil)
	if err != nil {
		return Preview{}, err
	}
	out := Preview{Titles: report.Titles, Unplaceable: report.Unplaceable}
	if out.Hours, err = s.hours(ctx, b, st); err != nil {
		return Preview{}, err
	}
	var ids []int64
	for _, r := range rows {
		if r.ItemID != 0 {
			ids = append(ids, r.ItemID)
		}
	}
	titles, err := s.db.ProgrammeTitles(ctx, ids)
	if err != nil {
		return Preview{}, err
	}
	seen := map[int64]bool{}
	for _, r := range rows {
		p := PreviewProgramme{StartsAt: r.StartsAt, EndsAt: r.EndsAt}
		if t, ok := titles[r.ItemID]; ok {
			p.Title = &t
			sample := t.ID
			if t.ShowID != 0 {
				sample = t.ShowID
			}
			if len(out.Sample) < 5 && !seen[sample] {
				seen[sample] = true
				out.Sample = append(out.Sample, t)
			}
		}
		out.Today = append(out.Today, p)
	}
	return out, nil
}

// hours is about how long everything the channel's own selection matches would take to play.
func (s *Service) hours(ctx context.Context, b *Builder, st *State) (int, error) {
	bd := &build{ctx: ctx, src: s.db, b: b, st: st, now: s.now(), pools: map[string]*pool{}}
	p, err := bd.pool("", &b.Selection)
	if err != nil {
		return 0, err
	}
	var ms int64
	var shows []int64
	for _, t := range p.titles {
		if t.show {
			shows = append(shows, t.id)
		} else {
			ms += t.ms
		}
	}
	episodes, err := s.db.ShowsRuntime(ctx, shows)
	return int((ms + episodes + 30*60_000) / 3_600_000), err
}

// CountUpTo counts the titles a selection matches, stopping at limit, so a template is
// offered or not without reading every title.
func (s *Service) CountUpTo(ctx context.Context, sel *Selection, limit int) (int, error) {
	if len(sel.Libraries) == 0 {
		return 0, nil
	}
	query := db.ChannelSelection{Libraries: sel.Libraries, Match: sel.Match, Conditions: sel.Conditions,
		Films: slices.Contains(sel.Kinds, "movie"), Shows: slices.Contains(sel.Kinds, "show")}
	if sel.AddedWithinDays > 0 {
		query.AddedSince = s.now().AddDate(0, 0, -sel.AddedWithinDays)
	}
	count := 0
	err := s.db.ChannelCandidates(ctx, query, func(db.ChannelCandidate) error {
		if count++; count >= limit {
			return errEnough
		}
		return nil
	})
	if errors.Is(err, errEnough) {
		err = nil
	}
	return count, err
}

var errEnough = errors.New("enough")
