package api

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The owner's activity panel and Now playing read again when told something they show has
// changed, instead of asking every few seconds. What they show changes when a stream starts
// or ends, is paused or resumed, starts or stops buffering or becomes a problem, and when a
// scan or another task is asked for, starts or ends. Once a second the server compares a short
// summary of those with the last one and, when it differs, tells the owner's devices once:
// a burst (a hundred streams ending at a restart, a scan's thousand files) is one hint, and a
// task's progress, a stream's position and its bitrate change nothing.
const activityHintEvery = time.Second

// startActivity starts the hints. New calls it once, after the live feed exists.
func (s *Server) startActivity(ctx context.Context) error {
	last := s.activityNow()
	s.goBackground(func() { s.watchActivity(ctx, last) })
	return nil
}

func (s *Server) watchActivity(ctx context.Context, last string) {
	tick := time.NewTicker(activityHintEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := s.activityNow()
		if now == last {
			continue
		}
		last = now
		owner, err := s.db.OwnerUserID(ctx)
		if err != nil {
			continue // no owner yet (setup not done): nobody to tell
		}
		s.live.ToAccount(owner, "activity", "updated", "")
	}
}

// activityNow is the short summary of what the activity panel shows: each stream with its
// state and whether it is a problem, each live channel watched, and the tasks running and
// waiting. Positions, bitrates and progress are left out on purpose.
func (s *Server) activityNow() string {
	var parts []string
	now := s.now()
	for _, v := range s.playback.All() {
		problem := !v.BufferingSince.IsZero() && now.Sub(v.BufferingSince) >= ProblemAfter
		parts = append(parts, v.ID+" "+v.State+" "+strconv.FormatBool(!v.BufferingSince.IsZero())+" "+strconv.FormatBool(problem))
	}
	if s.viewings != nil {
		for _, v := range s.viewings.All() {
			parts = append(parts, v.ID+" "+strconv.FormatBool(v.Paused))
		}
	}
	slices.Sort(parts)
	for _, state := range s.jobs.Busy() {
		parts = append(parts, "job "+state)
	}
	return strings.Join(parts, "\n")
}
