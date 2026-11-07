package read_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

func workloadInput[I, P any](id string, input I, consume func(context.Context, P) error) workload.ForegroundInput[I, P] {
	q, request, output := read.ForegroundWorkloadRequests(id)
	entry := time.Now()
	// Functional adapter controls retain real admission/custody, not a ready SLA.
	q.ReadyDeadline, q.ProtectedDeadline = entry.Add(10*time.Second), entry.Add(20*time.Second)
	request.Deadline, output.Deadline = q.ProtectedDeadline, q.ProtectedDeadline
	return workload.ForegroundInput[I, P]{Request: request, Budget: 10 * time.Second, Foreground: q, OutputRequest: output, Input: input, Consume: consume}
}

func requireConsumerCustody(t *testing.T, f read.ForegroundWorkloadFixture, ctx context.Context) {
	t.Helper()
	u := f.Authority.Usage()[governor.Foreground]
	if f.Authority.ForegroundUsage().Live != 1 || u[governor.Reader] != 0 || u[governor.MemoryBytes] != (8<<20)+4096 {
		t.Error("consumer lost LIVE/output or kept native window", u)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Error("consumer lost protected context")
	}
}

func requireRetired(t *testing.T, f read.ForegroundWorkloadFixture) {
	t.Helper()
	if f.Authority.ForegroundUsage().Live != 0 || f.Authority.Usage()[governor.Foreground] != (governor.Amounts{}) {
		t.Error("observation preceded output/LIVE cleanup")
	}
}

func TestForegroundWorkloadActualRecipes(t *testing.T) {
	for _, recipe := range []string{"BROWSE", "TITLE_DETAIL", "PLAYBACK_LOOKUP", "CONTINUE_WATCHING"} {
		t.Run(recipe, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := read.NewForegroundWorkloadFixture(t)
				consumed := 0
				check := func(ctx context.Context) { consumed++; requireConsumerCustody(t, f, ctx) }
				var action workload.Action
				switch recipe {
				case "BROWSE":
					action = workload.Browse(f.Browse, f.Governor, f.Authority, f.Snapshots, func(context.Context, workload.Offer) (workload.BrowseInput, error) {
						return workloadInput(recipe, f.Input, func(ctx context.Context, p read.BrowsePage) error {
							check(ctx)
							if len(p.Items) != 40 {
								t.Error("browse page")
							}
							return nil
						}), nil
					})
				case "TITLE_DETAIL":
					action = workload.Detail(f.Foreground, f.Governor, f.Authority, f.Snapshots, func(context.Context, workload.Offer) (workload.DetailInput, error) {
						return workloadInput(recipe, read.TitleInput{View: f.Input, Item: 1, Asset: 1}, func(ctx context.Context, p read.TitleDetail) error {
							check(ctx)
							if len(p.Cast) != 100 || len(p.Crew) != 50 || p.File == nil {
								t.Error("detail page")
							}
							return nil
						}), nil
					})
				case "PLAYBACK_LOOKUP":
					action = workload.PlaybackLookup(f.Foreground, f.Governor, f.Authority, f.Snapshots, func(context.Context, workload.Offer) (workload.PlaybackInput, error) {
						return workloadInput(recipe, read.TitleInput{View: f.Input, Item: 1, Asset: 1}, func(ctx context.Context, p read.PlaybackFile) error {
							check(ctx)
							if p.Path != "/media/title.mkv" {
								t.Error("playback file")
							}
							return nil
						}), nil
					})
				case "CONTINUE_WATCHING":
					action = workload.ContinueWatching(f.Foreground, f.Governor, f.Authority, f.Snapshots, func(context.Context, workload.Offer) (workload.ContinueInput, error) {
						return workloadInput(recipe, read.ContinueInput{View: f.Input}, func(ctx context.Context, p read.ContinuePage) error {
							check(ctx)
							if len(p.Items) != 40 {
								t.Error("continue page")
							}
							return nil
						}), nil
					})
				}
				err := action(t.Context(), workload.Offer{}, func(o workload.Observation) error {
					requireRetired(t, f)
					if o.Phase != recipe || o.HoldNS != nil || o.CommitNS != nil || o.Evidence != nil || o.RefusalError || o.CallError != "" {
						t.Error("invented writer/error evidence", o)
					}
					data, err := json.Marshal(o.Result)
					if err != nil {
						t.Fatal(err)
					}
					var result struct {
						Summary                                        json.RawMessage
						Evidence                                       *workload.ReadEvidence
						Consumption                                    *read.ForegroundConsumption
						ReadyBudget                                    time.Duration
						ReadyDeadline, ProtectedDeadline, ReadDeadline time.Time
						ReadError, ConsumerError, ScopeError           *string
					}
					if err = json.Unmarshal(data, &result); err != nil {
						t.Fatal(err)
					}
					if string(result.Summary) == "null" || result.Evidence == nil || result.Consumption == nil || result.ReadError == nil || *result.ReadError != "" || result.ConsumerError == nil || *result.ConsumerError != "" || result.ScopeError == nil || *result.ScopeError != "" {
						t.Fatal("missing successful evidence", string(data))
					}
					cost := result.Evidence
					want := map[string]int{"BROWSE": 11, "TITLE_DETAIL": 26, "PLAYBACK_LOOKUP": 19, "CONTINUE_WATCHING": 11}[recipe]
					if cost.LookupStatements+cost.PublicationStatements != want || cost.Attempts != 1 || cost.AdmissionElapsed != result.Consumption.AdmissionElapsed || cost.AdmissionElapsed < 0 || cost.Elapsed < cost.AdmissionElapsed || result.Consumption.Elapsed < cost.Elapsed || o.WallNS == nil || *o.WallNS < result.Consumption.Elapsed {
						t.Error("lost whole-operation cost", cost)
					}
					for _, w := range append(cost.LookupWindows, cost.PublicationWindows...) {
						if w == nil || w.Started == nil || w.Ended == nil || w.Settings == nil || w.Deadline.After(result.ReadyDeadline) {
							t.Error("missing actual window/deadline", w)
						}
					}
					for _, secret := range []string{"/media/title.mkv", `"PublicID"`, `"ResumePosition"`, `"Title":`, `"Token":`} {
						if strings.Contains(string(data), secret) {
							t.Error("payload escaped custody", secret)
						}
					}
					return nil
				})
				if err != nil || consumed != 1 {
					t.Fatal("actual adapter", consumed, err)
				}
			})
		})
	}
}

func TestForegroundWorkloadReadRefusalAndConsumerErrors(t *testing.T) {
	f := read.NewForegroundWorkloadFixture(t)
	consumerFailure, sinkFailure := errors.New("consumer failed"), errors.New("sink failed")
	for _, failure := range []string{"denied", "admission", "consumer"} {
		t.Run(failure, func(t *testing.T) {
			consumed := false
			action := workload.Browse(f.Browse, f.Governor, f.Authority, f.Snapshots, func(context.Context, workload.Offer) (workload.BrowseInput, error) {
				in := workloadInput(failure, f.Input, func(ctx context.Context, _ read.BrowsePage) error {
					consumed = true
					requireConsumerCustody(t, f, ctx)
					return consumerFailure
				})
				if failure == "denied" {
					in.Input.TokenHash = "missing"
				}
				if failure == "admission" {
					in.Foreground.ReadyDeadline = time.Now().Add(-time.Second)
				}
				return in, nil
			})
			err := action(t.Context(), workload.Offer{}, func(o workload.Observation) error {
				requireRetired(t, f)
				v := o.Result.(workload.ForegroundResult[workload.BrowseSummary])
				if o.CallError == "" || v.ScopeError == nil || *v.ScopeError == "" {
					t.Error("failure lost")
				}
				switch failure {
				case "denied":
					if v.Summary != nil || v.ReadError == nil || !strings.Contains(*v.ReadError, read.ErrDenied.Error()) || v.ConsumerError != nil || v.Evidence == nil {
						t.Error("read denial became success", v)
					}
				case "admission":
					if v.Evidence != nil || v.ReadError != nil || v.ConsumerError != nil || v.Summary != nil || !o.RefusalError || v.Consumption.Refusal == nil {
						t.Error("admission allocated/read payload", v)
					}
				case "consumer":
					if v.Summary == nil || v.ReadError == nil || *v.ReadError != "" || v.ConsumerError == nil || *v.ConsumerError != consumerFailure.Error() {
						t.Error("consumer error misclassified", v)
					}
				}
				return sinkFailure
			})
			if !errors.Is(err, sinkFailure) || consumed != (failure == "consumer") {
				t.Fatal(consumed, err)
			}
			if failure == "consumer" && !errors.Is(err, consumerFailure) {
				t.Fatal("lost consumer failure", err)
			}
		})
	}
}

type pairedReadSink struct {
	observed  atomic.Bool
	read      workload.Observation
	published chan struct{}
}

func (s *pairedReadSink) Append(r workload.Record) error {
	if r.Observation != nil && r.Observation.Phase == "BROWSE" {
		s.read = *r.Observation
		s.observed.Store(true)
		close(s.published)
	}
	return nil
}

func TestForegroundWorkloadPairedStopJoinsConsumer(t *testing.T) {
	synctest.Test(t, testForegroundWorkloadPairedStopJoinsConsumer)
}

func testForegroundWorkloadPairedStopJoinsConsumer(t *testing.T) {
	f := read.NewForegroundWorkloadFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	writerEntered, consumerEntered := make(chan struct{}), make(chan struct{})
	var consumerContext context.Context
	var preparationStarted time.Time
	writerReturn, consumerReturn := make(chan struct{}), make(chan struct{})
	var writerOnce, consumerOnce sync.Once
	releaseWriter := func() { writerOnce.Do(func() { close(writerReturn) }) }
	releaseConsumer := func() { consumerOnce.Do(func() { close(consumerReturn) }) }
	sink := &pairedReadSink{published: make(chan struct{})}
	browse := workload.Browse(f.Browse, f.Governor, f.Authority, f.Snapshots, func(ctx context.Context, _ workload.Offer) (workload.BrowseInput, error) {
		preparationStarted = time.Now()
		select {
		case <-writerEntered:
		case <-ctx.Done():
			return workload.BrowseInput{}, ctx.Err()
		}
		return workloadInput("paired", f.Input, func(ctx context.Context, _ read.BrowsePage) error {
			consumerContext = ctx
			close(consumerEntered)
			<-consumerReturn
			return ctx.Err()
		}), nil
	})
	// A blocked action models a writer owner only; it performs NO native write.
	writer := func(ctx context.Context, _ workload.Offer, emit func(workload.Observation) error) error {
		close(writerEntered)
		<-writerReturn
		return ctx.Err()
	}
	cfg := workload.Config{RunID: "paired-control", Duration: time.Minute, Cell: qual.RestartCell{ID: "cell", HostID: "synthetic", Profile: "test-only", DatabaseID: "one-db", ManifestSHA256: strings.Repeat("a", 64), CacheState: "warm", CadenceMode: "accelerated"}}
	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = workload.Run(ctx, cfg, []workload.Stream{{Name: "writer-control", Every: time.Minute, Pattern: []workload.Step{{Name: "blocked-control", Run: writer}}}, {Name: "read", Every: time.Minute, Pattern: []workload.Step{{Name: "browse", Run: browse}}}}, sink)
		close(done)
	}()
	defer func() { cancel(); releaseWriter(); releaseConsumer(); <-done }()
	select {
	case <-consumerEntered:
	case <-sink.published:
		t.Fatal("read failed before consumer", sink.read.CallError)
	case <-done:
		t.Fatal("read never overlapped writer", runErr)
	}
	requireConsumerCustody(t, f, consumerContext)
	if sink.observed.Load() {
		t.Fatal("emitted before consumer/cleanup")
	}
	cancel()
	releaseWriter()
	requireConsumerCustody(t, f, consumerContext)
	select {
	case <-done:
		t.Fatal("cancel refunded retained consumer")
	default:
	}
	consumerRelease := time.Now()
	releaseConsumer()
	<-done
	if runErr == nil || !sink.observed.Load() {
		t.Fatal("stop/evidence lost", runErr)
	}
	requireRetired(t, f)
	o := sink.read
	if o.CallStarted.After(preparationStarted) || o.CallReturned.Before(consumerRelease) || o.ReadyNS == nil || o.WallNS == nil || *o.ReadyNS < *o.WallNS {
		t.Fatal("lost scheduled/consumer interval", o)
	}
}
