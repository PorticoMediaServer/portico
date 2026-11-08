// Package workload supplies opt-in, composition-owned qualification drivers.
// It opens no database, issues no authority and is not wired into the server.
package workload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

const Version = "portico-sustained-workload-v1"

var errActionRefused = errors.New("workload action refused")

// Config binds ONE already prepared database and host/cache/cadence cell to the
// external immutable QL manifest. These declarations do not prove provenance.
// Duration starts AFTER fixture preparation; it may cover tens of minutes.
type Config struct {
	RunID    string
	Cell     qual.RestartCell
	Duration time.Duration
}

// Stream has one serial worker and no work queue. Pattern repeats in order;
// duplicate entries specify a deterministic mix. Use one stream for writer
// cycles and independent streams for foreground reads and PASSIVE checkpoints.
// A slow call consumes its slot; overdue slots are recorded, never caught up in
// a burst or retried to fill a successful population. Max 8 streams/32 entries.
type Stream struct {
	Name          string
	Every, Offset time.Duration
	Pattern       []Step
}

type Step struct {
	Name string
	Run  Action
}

type Offer struct {
	Stream, Operation string
	Slot              uint64
	ScheduledAt       time.Time
}

// maxActionObservations bounds one synchronous action. The real WC protocol
// emits a before-snapshot, ACCEPT, RESOLVE, ACK and after-snapshot, and a native
// binding may add one caller-side bracket: six records. A wrapper that also
// records an offered-but-no-fresh-commit fact only does so when ACCEPT was not
// observed, so the bound still holds. It remains a hard cap; an action that
// loops or drifts phases fails instead of growing the raw stream without limit.
const maxActionObservations = 6

// Action emits at most maxActionObservations records (the native WC protocol's
// five phases plus one caller-side bracket), immediately after each synchronous
// facade call returns. It must honour cancellation and join its own work. The
// driver never launches a goroutine around a native call. Any error/panic stops
// all new offers; returned partial observations survive.
type Action func(context.Context, Offer, func(Observation) error) error

type Record struct {
	Version, Label, RunID string
	Sequence              uint64
	UnitID                string
	Qualification         bool
	Cell                  qual.RestartCell
	Kind                  string        // start, operation, missed, stream_end, final
	Duration              time.Duration `json:",omitempty"` // declared window on start
	At                    time.Time
	Streams               []Schedule      `json:",omitempty"`
	Offer                 *Offer          `json:",omitempty"`
	Observation           *Observation    `json:",omitempty"`
	Missed                uint64          `json:",omitempty"`
	Progress              *StreamProgress `json:",omitempty"`
	Error                 string          `json:",omitempty"`
	// DriversReturned is NOT native cleanup proof. QL must append the owning
	// composition's independent writer/reader/checkpointer Close/join receipt.
	DriversReturned  bool `json:",omitempty"`
	ScheduleComplete bool `json:",omitempty"`
}

type Schedule struct {
	Name          string
	Every, Offset time.Duration
	Pattern       []string
}

type Sink interface{ Append(Record) error }

type StreamProgress struct {
	Name                                        string
	Scheduled, Offered, Calls, Missed, Unserved uint64
}

// JSONSink encodes incrementally; Run serializes calls. Supply a fresh owned
// output file. Sync (when available) keeps completed records on disk; failures
// stop offers. Serialization and sync happen after facade return, outside HOLD,
// and contribute to elapsed time and any missed schedule slots.
type JSONSink struct{ Output io.Writer }

func (s JSONSink) Append(r Record) error {
	if s.Output == nil {
		return errors.New("nil workload output")
	}
	if err := json.NewEncoder(s.Output).Encode(r); err != nil {
		return err
	}
	if file, ok := s.Output.(interface{ Sync() error }); ok {
		return file.Sync()
	}
	return nil
}

func validate(c Config, streams []Stream, sink Sink) error {
	if sink == nil || c.RunID == "" || len(c.RunID) > 128 || c.Duration <= 0 || c.Duration > 2*time.Hour ||
		c.Cell.ID == "" || c.Cell.HostID == "" || c.Cell.DatabaseID == "" || c.Cell.Profile == "" ||
		(c.Cell.CacheState != "cold" && c.Cell.CacheState != "warm") ||
		(c.Cell.CadenceMode != "realistic" && c.Cell.CadenceMode != "accelerated") || len(c.Cell.ManifestSHA256) != 64 ||
		len(streams) == 0 || len(streams) > 8 {
		return errors.New("invalid workload configuration")
	}
	for _, b := range c.Cell.ManifestSHA256 {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return errors.New("invalid manifest hash")
		}
	}
	names := map[string]bool{}
	for _, s := range streams {
		if s.Name == "" || len(s.Name) > 128 || names[s.Name] || s.Every < time.Millisecond || s.Offset < 0 || s.Offset >= c.Duration || len(s.Pattern) == 0 || len(s.Pattern) > 32 {
			return errors.New("invalid workload stream")
		}
		names[s.Name] = true
		for _, step := range s.Pattern {
			if step.Name == "" || len(step.Name) > 128 || step.Run == nil {
				return errors.New("invalid workload step")
			}
		}
	}
	return nil
}

// Run starts every stream against the same monotonic epoch. No work starts
// before its offer or after cancellation/deadline is observed. It waits for all
// active actions even when their native work outlives cancellation. No timeout
// is treated as physical return. Caller owns resource construction and closure.
func Run(parent context.Context, c Config, streams []Stream, sink Sink) error {
	if parent == nil {
		return errors.New("nil workload context")
	}
	if err := validate(c, streams, sink); err != nil {
		return err
	}
	start := time.Now()
	end := start.Add(c.Duration)
	ctx, cancel := context.WithDeadline(parent, end)
	defer cancel()
	var mu sync.Mutex
	var runErr error
	var sequence uint64
	complete := true
	appendRecord := func(r Record) error {
		mu.Lock()
		defer mu.Unlock()
		r.Version, r.Label, r.RunID, r.Cell = Version, "DIRECTIONAL", c.RunID, c.Cell
		sequence++
		r.Sequence, r.UnitID = sequence, fmt.Sprintf("%s:%d", c.RunID, sequence)
		r.At = time.Now()
		if r.Missed > 0 || r.Error != "" || r.Observation != nil && (r.Observation.Stub || r.Observation.RefusalError) || r.Progress != nil && r.Progress.Unserved != 0 {
			complete = false
		}
		if r.Observation != nil && r.Observation.Progress != nil {
			complete = false
		} // values/lineage stage; no native composition yet
		if r.Error != "" || r.Observation != nil && r.Observation.RefusalError {
			cancel()
		}
		err := sink.Append(r)
		if err != nil {
			complete = false
			runErr = errors.Join(runErr, err)
			cancel()
		}
		return err
	}
	schedules := make([]Schedule, len(streams))
	for i, s := range streams {
		schedules[i] = Schedule{Name: s.Name, Every: s.Every, Offset: s.Offset}
		for _, step := range s.Pattern {
			schedules[i].Pattern = append(schedules[i].Pattern, step.Name)
		}
	}
	if err := appendRecord(Record{Kind: "start", Streams: schedules, Duration: c.Duration}); err != nil {
		return err
	}
	var workers sync.WaitGroup
	for _, stream := range streams {
		workers.Go(func() {
			next := start.Add(stream.Offset)
			progress := StreamProgress{Name: stream.Name, Scheduled: missedSlots(next, end, stream.Every)}
			defer func() {
				progress.Unserved = progress.Scheduled - progress.Offered - progress.Missed
				_ = appendRecord(Record{Kind: "stream_end", Progress: &progress})
			}()
			var slot uint64
			for next.Before(end) {
				if !waitUntil(ctx, next) {
					return
				}
				if ctx.Err() != nil || !time.Now().Before(end) {
					return
				}
				step := stream.Pattern[slot%uint64(len(stream.Pattern))]
				offer := Offer{stream.Name, step.Name, slot + 1, next}
				progress.Offered++
				count := 0
				var emitErr error
				emit := func(o Observation) error {
					count++
					if count > maxActionObservations {
						emitErr = fmt.Errorf("action exceeded %d observations", maxActionObservations)
						return emitErr
					}
					progress.Calls++
					if o.CallReturned != nil && !o.CallReturned.IsZero() && !o.CallReturned.Before(offer.ScheduledAt) {
						duration := o.CallReturned.Sub(offer.ScheduledAt)
						o.ReadyNS = &duration
					}
					err := appendRecord(Record{Kind: "operation", Offer: &offer, Observation: &o, Error: o.CallError})
					if o.RefusalError {
						err = errors.Join(err, errActionRefused)
					}
					emitErr = errors.Join(emitErr, err)
					return err
				}
				actionErr := invoke(ctx, step.Run, offer, emit)
				actionErr = errors.Join(actionErr, emitErr)
				if count == 0 {
					actionErr = errors.Join(actionErr, errors.New("action emitted no observation"))
				}
				if actionErr != nil {
					_ = appendRecord(Record{Kind: "operation", Offer: &offer, Error: actionErr.Error()})
					mu.Lock()
					runErr = errors.Join(runErr, actionErr)
					mu.Unlock()
					cancel()
					return
				}
				slot++
				next = next.Add(stream.Every)
				// Do not count offers outside the declared window, or manufacture
				// misses for streams stopped by an error/cancellation.
				if parent.Err() != nil {
					return
				}
				missed := missedSlots(next, minTime(time.Now(), end), stream.Every)
				if missed > 0 {
					progress.Missed += missed
					if err := appendRecord(Record{Kind: "missed", Offer: &Offer{Stream: stream.Name, Slot: slot + 1, ScheduledAt: next}, Missed: missed}); err != nil {
						return
					}
					slot += missed
					next = next.Add(time.Duration(missed) * stream.Every)
				}
			}
		})
	}
	workers.Wait()
	// Short last calls do not shorten the predeclared observation window.
	if ctx.Err() == nil {
		waitUntil(ctx, end)
	}
	mu.Lock()
	if err := parent.Err(); err != nil {
		runErr = errors.Join(runErr, err)
		complete = false
	}
	if time.Now().Before(end) {
		complete = false
	}
	finalErr, scheduleComplete := runErr, complete
	mu.Unlock()
	final := Record{Kind: "final", DriversReturned: true, ScheduleComplete: scheduleComplete}
	if finalErr != nil {
		final.Error = finalErr.Error()
	}
	_ = appendRecord(final)
	return runErr
}

func invoke(ctx context.Context, action Action, offer Offer, emit func(Observation) error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("workload action panic: %v", value)
		}
	}()
	return action(ctx, offer, emit)
}

func missedSlots(next, now time.Time, every time.Duration) uint64 {
	if !next.Before(now) {
		return 0
	}
	return uint64((now.Sub(next)-1)/every) + 1
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func waitUntil(ctx context.Context, at time.Time) bool {
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
