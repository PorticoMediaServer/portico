package librarychannels

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Tuned is what a viewer joins: the title on now and how far into it, or the slate.
type Tuned struct {
	ItemID           string // "" for the slate
	Offset           time.Duration
	StartsAt, EndsAt time.Time
	Quality          Quality
}

// ErrSlate: the channel shows its slate now: nothing is scheduled, the title has gone, or the
// viewer may not see it. Next is when the next programme begins.
type ErrSlate struct{ Next time.Time }

func (e *ErrSlate) Error() string { return "the channel shows its slate" }

// Tune finds what a viewer joins on a channel now. A channel the viewer may not see, or one
// that is off, is not found, exactly like one that does not exist.
func (s *Service) Tune(ctx context.Context, v db.Viewer, channelID string) (Tuned, error) {
	viewer, err := s.db.ViewerLive(ctx, v)
	if err != nil {
		return Tuned{}, err
	}
	ch, err := s.db.LibraryChannel(ctx, channelID, true)
	if err != nil {
		return Tuned{}, err
	}
	if !ch.On || !Visible(viewer, ch.Libraries) {
		return Tuned{}, db.ErrNotFound
	}
	b, err := Parse(ch.Builder)
	if err != nil {
		return Tuned{}, err
	}
	now := s.now()
	rows, err := s.db.ChannelProgrammes(ctx, ch.ID, now, now.Add(12*time.Hour))
	if err != nil {
		return Tuned{}, err
	}
	slate := &ErrSlate{}
	for n, r := range rows {
		if r.StartsAt.After(now) {
			slate.Next = r.StartsAt
			break
		}
		if n+1 < len(rows) {
			slate.Next = rows[n+1].StartsAt
		} else {
			slate.Next = r.EndsAt
		}
		if r.ItemID == 0 {
			break
		}
		seen, err := s.db.VisibleItems(ctx, v, []int64{r.ItemID})
		if err != nil {
			return Tuned{}, err
		}
		public, ok := seen[r.ItemID]
		if !ok {
			break // above the profile's limits: the slate plays
		}
		return Tuned{ItemID: public, Offset: now.Sub(r.StartsAt), StartsAt: r.StartsAt, EndsAt: r.EndsAt, Quality: b.Quality}, nil
	}
	return Tuned{}, slate
}

// IsSlate reports whether err says the channel shows its slate.
func IsSlate(err error) (*ErrSlate, bool) {
	var slate *ErrSlate
	ok := errors.As(err, &slate)
	return slate, ok
}
