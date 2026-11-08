package writer_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

var pairedEnabled = flag.Bool("n1-paired", false, "opt-in same-DB N1/foreground component")
var pairedWarmup = flag.Int("n1-paired-warmup", 1, "actual retained N1 warmup originals,1..64")
var pairedEvery = flag.Duration("n1-paired-every", 100*time.Millisecond, "declared offer cadence shared by bounded serial writer/read streams")
var pairedRoute = flag.String("n1-paired-route", "browse", "browse,detail,playback,continue or all (four-entry pattern)")
var pairedCadence = flag.String("n1-paired-cadence", "realistic", "declared realistic or accelerated; not inferred from ready/protected ceilings")

var pairedCohort = flag.String("n1-paired-cohort", "timing", "timing or native-counters; never pooled")

type pairedSink struct {
	c                   *writer.N1PairedComponent
	finalSeen, complete bool
	drivers             bool
	progress            map[string]workload.StreamProgress
	search              []workload.Observation
}

func (s *pairedSink) Append(record workload.Record) error {
	if record.Kind == "final" {
		s.finalSeen = true
		s.drivers = record.DriversReturned
		s.complete = record.ScheduleComplete && record.DriversReturned && record.Error == ""
	}
	if record.Progress != nil {
		if s.progress == nil {
			s.progress = map[string]workload.StreamProgress{}
		}
		s.progress[record.Progress.Name] = *record.Progress
	}
	if record.Offer != nil && record.Offer.Operation == "full_search" && record.Observation != nil {
		s.search = append(s.search, *record.Observation)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.c.EmitScheduler(raw)
}

// streamGap names the first required stream that missed a scheduled offer or
// left one unserved. Supporting streams are recorded gaps, not verdicts.
func (s *pairedSink) streamGap(names ...string) string {
	for _, name := range names {
		p, ok := s.progress[name]
		if !ok {
			return name + " stream missing its terminal progress"
		}
		if p.Missed+p.Unserved != 0 {
			return fmt.Sprintf("%s stream missed %d and left %d unserved; retain the raw gaps", name, p.Missed, p.Unserved)
		}
	}
	return ""
}

func encodePaired[P any](ctx context.Context, page P) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Actual bounded JSON encoding/consumer work, joined before return. This is
	// not an HTTP transfer, player start or first-frame measurement.
	return errors.Join(json.NewEncoder(io.Discard).Encode(page), ctx.Err())
}
func pairedInput[I, P any](c *writer.N1PairedComponent, offer workload.Offer, input I, consume func(context.Context, P) error) workload.ForegroundInput[I, P] {
	q, request, output := readfixture.Requests(fmt.Sprintf("%s/%s/%d", c.DatabaseID, offer.Operation, offer.Slot), offer.ScheduledAt)
	return workload.ForegroundInput[I, P]{Request: request, Budget: time.Second, Foreground: q, OutputRequest: output, Input: input, Consume: consume}
}
func pairedAction(c *writer.N1PairedComponent, route string, consumeBrowse func(context.Context, read.BrowsePage) error) workload.Action {
	switch route {
	case "browse":
		if consumeBrowse == nil {
			consumeBrowse = encodePaired[read.BrowsePage]
		}
		return workload.Browse(c.Browse, c.Governor, c.Authority, c.Snapshots, func(ctx context.Context, offer workload.Offer) (workload.BrowseInput, error) {
			view := c.View
			view.Now = offer.ScheduledAt
			return pairedInput(c, offer, view, consumeBrowse), nil
		})
	case "detail":
		return workload.Detail(c.Foreground, c.Governor, c.Authority, c.Snapshots, func(ctx context.Context, offer workload.Offer) (workload.DetailInput, error) {
			view := c.PointView
			view.Now = offer.ScheduledAt
			return pairedInput(c, offer, read.TitleInput{View: view, Item: 1, Asset: 1}, encodePaired[read.TitleDetail]), nil
		})
	case "playback":
		return workload.PlaybackLookup(c.Foreground, c.Governor, c.Authority, c.Snapshots, func(ctx context.Context, offer workload.Offer) (workload.PlaybackInput, error) {
			view := c.PointView
			view.Now = offer.ScheduledAt
			return pairedInput(c, offer, read.TitleInput{View: view, Item: 1, Asset: 1}, encodePaired[read.PlaybackFile]), nil
		})
	case "continue":
		return workload.ContinueWatching(c.Foreground, c.Governor, c.Authority, c.Snapshots, func(ctx context.Context, offer workload.Offer) (workload.ContinueInput, error) {
			view := c.PointView
			view.Now = offer.ScheduledAt
			return pairedInput(c, offer, read.ContinueInput{View: view}, encodePaired[read.ContinuePage]), nil
		})
	}
	return nil
}
func recordPaired(c *writer.N1PairedComponent, action workload.Action) workload.Action {
	return func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		return action(ctx, offer, func(observation workload.Observation) error {
			if observation.CallReturned != nil && !observation.CallReturned.IsZero() && !observation.CallReturned.Before(offer.ScheduledAt) {
				ready := observation.CallReturned.Sub(offer.ScheduledAt)
				observation.ReadyNS = &ready
			}
			raw, err := json.Marshal(observation)
			if err != nil {
				return err
			}
			id := fmt.Sprintf("%s/foreground/%d", c.DatabaseID, offer.Slot)
			return errors.Join(c.EmitRead(id, offer.ScheduledAt, raw), emit(observation))
		})
	}
}

func TestN1PairedMeasurement(t *testing.T) {
	if !*pairedEnabled {
		if os.Getenv("PORTICO_WRITER_N1_PAIRED") != "" {
			t.Fatal("companion flag required")
		}
		t.Skip("opt-in paired target")
	}
	if os.Getenv("PORTICO_WRITER_N1_PAIRED") != "1" {
		t.Fatal("explicit paired flag requires PORTICO_WRITER_N1_PAIRED=1")
	}
	if *pairedCadence != "realistic" && *pairedCadence != "accelerated" {
		t.Fatal("explicit cadence population required")
	}
	if *pairedEvery < time.Millisecond || *pairedEvery > time.Second {
		t.Fatal("bounded declared cadence required")
	}
	routes := []string{*pairedRoute}
	if *pairedRoute == "all" {
		routes = []string{"browse", "detail", "playback", "continue"}
	}
	for _, route := range routes {
		if route != "browse" && route != "detail" && route != "playback" && route != "continue" {
			t.Fatal("unsupported closed route")
		}
	}
	file, err := os.Open(os.Getenv("PORTICO_WRITER_N1_PAIRED_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 16385))
	if err := errors.Join(readErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	c := writer.NewN1PairedComponent(t, data, *pairedWarmup, *pairedCohort)
	digest := sha256.Sum256(data)
	cfg := workload.Config{RunID: c.RunID, Duration: time.Duration(c.Measured) * *pairedEvery, Cell: qual.RestartCell{ID: c.RunID, HostID: c.HostEpoch, DatabaseID: c.DatabaseID, Profile: "existing N1/RD2 test profiles, NOT qualified", ManifestSHA256: hex.EncodeToString(digest[:]), CacheState: "warm", CadenceMode: *pairedCadence}}
	n1 := func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		started := time.Now()
		err := c.RunOriginal(t, ctx, offer.ScheduledAt, false, *pairedCohort)
		returned := time.Now()
		wall := returned.Sub(started)
		o := workload.Observation{Phase: "COMPONENT_N1_CHAIN", CallStarted: &started, CallReturned: &returned, WallNS: &wall}
		if err != nil {
			o.CallError = err.Error()
		}
		return errors.Join(err, emit(o))
	}
	reads := workload.Stream{Name: "foreground", Every: *pairedEvery}
	for _, route := range routes {
		reads.Pattern = append(reads.Pattern, workload.Step{Name: route, Run: recordPaired(c, pairedAction(c, route, nil))})
	}
	signalContext, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalContext, 5*time.Minute)
	defer cancel()
	sink := &pairedSink{c: c}
	streams := []workload.Stream{{Name: "n1", Every: *pairedEvery, Pattern: []workload.Step{{Name: "fresh_begin_resolve_ack", Run: n1}}}, reads}
	if c.MemoryEnabled() {
		streams = append(streams, ec5MemoryStream(c))
	}
	runErr := workload.Run(ctx, cfg, streams, sink)
	if runErr == nil && (!sink.finalSeen || !sink.complete) {
		runErr = errors.New("incomplete declared paired schedule; retain missed/unserved offers")
	}
	if err := c.Close(runErr); err != nil {
		t.Fatal(err)
	}
}

func TestN1PairedSameDBAndLIVE(t *testing.T) {
	if !*pairedEnabled {
		t.Skip("opt-in same-DB owner/reader control")
	}
	data, _ := json.Marshal(map[string]any{"run_id": "PAIR_CONTROL", "host_epoch": "CONTROL", "source_commit": strings.Repeat("a", 40), "output_path": filepath.Join(t.TempDir(), "paired.ndjson"), "fixtures": 1})
	c := writer.NewN1PairedComponent(t, data, 1, "timing")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	consumerStarted, writerReturned := make(chan struct{}), make(chan struct{})
	readDone := make(chan error, 1)
	entry := time.Now()
	action := pairedAction(c, "browse", func(ctx context.Context, page read.BrowsePage) error {
		if len(page.Items) == 0 {
			return errors.New("missing actual foreground page")
		}
		if c.Authority.ForegroundUsage().Live != 1 || c.Authority.Usage()[governor.Foreground][governor.MemoryBytes] < (8<<20) {
			return errors.New("consumer not in SAME governor LIVE/output custody")
		}
		close(consumerStarted)
		select {
		case <-writerReturned:
		case <-ctx.Done():
			return ctx.Err()
		}
		return encodePaired(ctx, page)
	})
	go func() {
		readDone <- recordPaired(c, action)(ctx, workload.Offer{Stream: "foreground", Operation: "browse", Slot: 0, ScheduledAt: entry}, func(o workload.Observation) error {
			result, ok := o.Result.(workload.ForegroundResult[workload.BrowseSummary])
			if !ok || o.ReadyNS == nil || o.CallReturned == nil || *o.ReadyNS != o.CallReturned.Sub(entry) || result.Summary == nil || result.ReadError == nil || *result.ReadError != "" || result.ConsumerError == nil || *result.ConsumerError != "" || result.ScopeError == nil || *result.ScopeError != "" || !result.ReadyDeadline.Equal(entry.Add(time.Second)) || !result.ProtectedDeadline.Equal(entry.Add(3*time.Second)) {
				return errors.New("separate result/deadline facts lost")
			}
			if result.Evidence == nil || len(result.Evidence.LookupWindows) == 0 || len(result.Evidence.PublicationWindows) == 0 {
				return errors.New("actual read windows missing")
			}
			for _, window := range append(result.Evidence.LookupWindows, result.Evidence.PublicationWindows...) {
				if window == nil || window.Settings == nil || window.Deadline == nil || window.Ended == nil || window.Deadline.After(result.ReadyDeadline) {
					return errors.New("native facts/deadline were defaulted or extended")
				}
			}
			return nil
		})
	}()
	select {
	case <-consumerStarted:
	case err := <-readDone:
		t.Fatal("reader failed before consumer", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	writerErr := c.RunOriginal(t, ctx, time.Now(), false, "timing")
	close(writerReturned)
	readerErr := <-readDone
	if err := errors.Join(writerErr, readerErr); err != nil {
		t.Fatal(err)
	}
	for i, route := range []string{"detail", "playback", "continue"} {
		if err := recordPaired(c, pairedAction(c, route, nil))(ctx, workload.Offer{Stream: "foreground", Operation: route, Slot: uint64(i + 1), ScheduledAt: time.Now()}, func(workload.Observation) error { return nil }); err != nil {
			t.Fatal(route, err)
		}
	}
	if c.Authority.ForegroundUsage().Live != 0 || c.Authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("owners not joined")
	}
	if err := c.Close(nil); err != nil {
		t.Fatal(err)
	}
}
