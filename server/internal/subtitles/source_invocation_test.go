package subtitles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func capturePlaybackInvocation(t *testing.T, f *subtitleFixture) PlaybackSourceInvocation {
	t.Helper()
	ctx := context.Background()
	snapshot, p, err := f.s.readTx(ctx, f.p, f.item.Public)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	scope, err := sessionTx(ctx, snapshot.Tx(), p, f.item.Public, "session")
	if err != nil {
		t.Fatal(err)
	}
	i, ok := PlaybackSourceInvocationFromContext(f.s.playbackSourceContext(ctx, p, f.item.Public, "session", scope))
	if !ok {
		t.Fatal("authorized session invocation was not captured")
	}
	return i
}

func checkPlaybackInvocation(ctx context.Context, db *sql.DB, i PlaybackSourceInvocation) error {
	snapshot, err := dbwork.BeginSnapshot(ctx, db)
	if err != nil {
		return err
	}
	defer snapshot.Rollback()
	return i.CheckTx(ctx, snapshot.Tx())
}

func TestPlaybackSourceInvocationRetainsOnlyCapturedSessionFacts(t *testing.T) {
	f := newSubtitleFixture(t)
	i := capturePlaybackInvocation(t, f)
	facts := i.Facts()
	if facts.Principal != f.p || facts.ItemID != f.item.Public || facts.AssetID != f.item.Token || facts.SessionID != "session" || facts.SessionGeneration != 1 || facts.SourceSize != 1000 || facts.SourceModifiedNS != 1 {
		t.Fatal("captured session/pin facts changed", facts)
	}
	// Accessors return copies. Changing a caller's copy cannot retarget the
	// continuation or turn the context into a different actor/source grant.
	facts.Principal.Hash, facts.AssetID, facts.SessionID, facts.SessionGeneration = "foreign", "foreign", "foreign", 99
	if got := i.Facts(); got.Principal != f.p || got.AssetID != f.item.Token || got.SessionID != "session" || got.SessionGeneration != 1 {
		t.Fatal("caller retargeted captured authority", got)
	}
	if err := checkPlaybackInvocation(context.Background(), f.db, i); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{i, i.Facts()} {
		raw, err := json.Marshal(value)
		if err != nil || string(raw) != "{}" {
			t.Fatal("private actor/session facts entered JSON", string(raw), err)
		}
	}
	if _, ok := PlaybackSourceInvocationFromContext(context.Background()); ok {
		t.Fatal("missing invocation fabricated context authority")
	}
	if err := (PlaybackSourceInvocation{}).CheckTx(context.Background(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("empty invocation accepted", err)
	}
}

func TestPlaybackSourceInvocationRechecksCurrentActorSessionAndPin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for name, change := range map[string]func(*subtitleFixture){
		"actor denied": func(f *subtitleFixture) { f.allowed = false },
		"generation": func(f *subtitleFixture) {
			f.catalog.Exec(`UPDATE playback_sessions SET generation=2 WHERE id='session'`)
		},
		"stopped": func(f *subtitleFixture) {
			f.catalog.Exec(`UPDATE playback_sessions SET state='stopped' WHERE id='session'`)
		},
		"expired": func(f *subtitleFixture) {
			f.catalog.Exec(`UPDATE playback_sessions SET expires_at='2000-01-01T00:00:00Z' WHERE id='session'`)
		},
		"foreign principal": func(f *subtitleFixture) {
			f.catalog.Exec(`UPDATE playback_sessions SET session_hash='foreign' WHERE id='session'`)
		},
		"unavailable asset": func(f *subtitleFixture) {
			f.catalog.Exec(`UPDATE catalog_assets SET available=0 WHERE token=?`, f.item.Token)
		},
		"changed source pin": func(f *subtitleFixture) {
			f.catalog.Exec(`UPDATE catalog_assets SET size=1001 WHERE token=?`, f.item.Token)
			f.catalog.Exec(`UPDATE playback_source_pins SET size=1001 WHERE session_id='session'`)
		},
		"foreign pin": func(f *subtitleFixture) {
			library := f.catalog.Library("foreign-library", "Foreign", "movie", "/foreign")
			other := f.catalog.Movie(library, "/foreign/other.mp4", "Foreign", 2024)
			f.catalog.Exec(`UPDATE playback_source_pins SET asset_id=? WHERE session_id='session'`, other.Token)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSubtitleFixture(t)
			i := capturePlaybackInvocation(t, f)
			change(f)
			err := checkPlaybackInvocation(context.Background(), f.db, i)
			if err == nil {
				t.Fatal("changed actor/session/source pin accepted")
			}
			if (name == "actor denied" || name == "foreign pin") && !errors.Is(err, identity.ErrUnauthorized) {
				t.Fatal("current actor/pin refusal lost its authority error", err)
			}
		})
	}
}

type invocationSourceInputs struct {
	open func(context.Context, string, string, string) (RenderInput, error)
}

type selectedCacheExtractor struct {
	extract func(context.Context) ([]byte, string, error)
}

func (e selectedCacheExtractor) Probe(context.Context, string, string) (*Inventory, string, error) {
	return nil, "", ErrUnsupported
}
func (e selectedCacheExtractor) Extract(ctx context.Context, _ string, _ string, _ int, _ string) ([]byte, string, error) {
	return e.extract(ctx)
}

func TestSelectionRefusesBeforeDiscoveredBurnCacheExtraction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"session", "generation", "revision", "authority"} {
		t.Run(change, func(t *testing.T) {
			f := newSubtitleFixture(t)
			persistDiscovered(t, f, []Track{{Origin: "embedded", StreamIndex: 3, Format: "ass", Language: "en"}})
			catalog, err := f.s.List(context.Background(), f.p, f.item.Public)
			if err != nil || len(catalog.Resources) != 1 {
				t.Fatal(catalog, err)
			}
			calls := 0
			f.s.Extractor = selectedCacheExtractor{extract: func(context.Context) ([]byte, string, error) {
				calls++
				return nil, "", ErrUnsupported
			}}
			m := SelectRequest{OperationID: "early-refusal", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: catalog.Resources[0].ID, ResourceRevision: catalog.Resources[0].Revision, PositionUS: "0", OffsetUS: "0"}
			session := "session"
			switch change {
			case "session":
				session = "foreign-session"
			case "generation":
				m.Generation++
			case "revision":
				m.ExpectedRevision++
			case "authority":
				f.allowed = false
			}
			if _, err := f.s.Select(context.Background(), f.p, f.item.Public, session, m); err == nil || calls != 0 {
				t.Fatal("refused selection reached extraction", err, calls)
			}
		})
	}
}

func TestSelectedCacheKeepsCallerSealAndCancellation(t *testing.T) {
	f := newSubtitleFixture(t)
	persistDiscovered(t, f, []Track{{Origin: "embedded", StreamIndex: 3, Format: "srt", Language: "en"}})
	catalog, err := f.s.List(context.Background(), f.p, f.item.Public)
	if err != nil || len(catalog.Resources) != 1 {
		t.Fatal(catalog, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshot, p, err := f.s.readTx(ctx, f.p, f.item.Public)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := sessionTx(ctx, snapshot.Tx(), p, f.item.Public, "session")
	snapshot.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	selected := f.s.playbackSourceContext(ctx, p, f.item.Public, "session", scope)
	defer closePlaybackSourceContext(selected)
	entered := make(chan struct{})
	f.s.Extractor = selectedCacheExtractor{extract: func(work context.Context) ([]byte, string, error) {
		invocation, ok := PlaybackSourceInvocationFromContext(work)
		if !ok || invocation.Lifetime() != ctx || invocation.Facts().SessionID != "session" {
			t.Error("selected extraction detached or lost its seal")
		}
		close(entered)
		<-work.Done()
		return nil, "", work.Err()
	}}
	done := make(chan error, 1)
	go func() {
		done <- f.s.ensureCached(selected, p, f.item.Public, catalog.Resources[0].ID, catalog.Resources[0].Revision)
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("selected work outlived caller", err)
	}
	f.s.cacheMu.Lock()
	detached := len(f.s.cacheFills)
	f.s.cacheMu.Unlock()
	if detached != 0 {
		t.Fatal("selected extraction entered detached cache work")
	}
}

func (i invocationSourceInputs) OpenSubtitleInput(ctx context.Context, item, source, session string) (RenderInput, error) {
	return i.open(ctx, item, source, session)
}

type invocationRenderInput struct {
	validate   func(context.Context) error
	closed     *int
	afterClose func()
}

func (*invocationRenderInput) Evidence() string { return "representation" }
func (*invocationRenderInput) Size() int64      { return 1000 }
func (*invocationRenderInput) ReadExtent(context.Context, int64, int64) ([]byte, error) {
	return nil, ErrUnsupported
}
func (i *invocationRenderInput) Validate(ctx context.Context) error { return i.validate(ctx) }
func (i *invocationRenderInput) Close() error {
	*i.closed++
	if i.afterClose != nil {
		i.afterClose()
	}
	return nil
}

func TestSelectedPlaybackSourceCarriesActualInvocationAfterCommit(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	receipt, err := f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("source-invocation", plainSubtitle, f.item.Token))
	if err != nil {
		t.Fatal(err)
	}
	f.catalog.Exec(`UPDATE subtitle_render_revisions SET source_evidence='representation' WHERE resource_id=?`, receipt.ResourceID)
	f.catalog.Exec(`INSERT INTO subtitle_remote_sessions(session_id,evidence) VALUES('session','representation')`)
	opens, validations, closes := 0, 0, 0
	f.s.SourceInputs = invocationSourceInputs{open: func(read context.Context, item, source, session string) (RenderInput, error) {
		opens++
		i, ok := PlaybackSourceInvocationFromContext(read)
		if !ok || read.Done() != ctx.Done() || item != f.item.Public || source != f.item.Token || session != "session" {
			t.Fatal("source input lost the authorized invocation/lifetime")
		}
		facts := i.Facts()
		if facts.Principal != f.p || facts.SessionGeneration != 1 || facts.AssetID != source || facts.ItemID != item || facts.SessionID != session {
			t.Fatal("caller fields were guessed or retargeted", facts)
		}
		if err := checkPlaybackInvocation(read, f.db, i); err != nil {
			t.Fatal("continuation failed outside the committed command snapshot", err)
		}
		return &invocationRenderInput{closed: &closes, validate: func(check context.Context) error {
			validations++
			if check != ctx {
				t.Fatal("source validation detached from the original invocation")
			}
			return checkPlaybackInvocation(check, f.db, i)
		}}, nil
	}}
	plan, err := f.s.Select(ctx, f.p, f.item.Public, "session", SelectRequest{OperationID: "select-source-invocation", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: receipt.ResourceID, ResourceRevision: receipt.Revision, OffsetUS: "0"})
	if err != nil || plan.Selected == nil || plan.Selected.ID != receipt.ResourceID || opens != 1 || validations != 1 || closes != 1 {
		t.Fatal("selected source propagation failed", plan, err, opens, validations, closes)
	}
}

func TestPlaybackSourceInvocationCancellationDoesNotAuthorize(t *testing.T) {
	f := newSubtitleFixture(t)
	i := capturePlaybackInvocation(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkPlaybackInvocation(ctx, f.db, i); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled invocation became a read grant", err)
	}
}

func TestPlaybackSourceRetainsInputAuthorityThroughPublication(t *testing.T) {
	f := newSubtitleFixture(t)
	i := capturePlaybackInvocation(t, f)
	closed := 0
	current := true
	err := i.Retain(func(context.Context, *sql.Tx) error {
		if !current {
			return ErrConflict
		}
		return nil
	}, func() error { closed++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), playbackSourceInvocationKey{}, i)
	check := func() error {
		snapshot, err := dbwork.BeginSnapshot(ctx, f.db)
		if err != nil {
			return err
		}
		defer snapshot.Rollback()
		return checkPlaybackSourcePublicationTx(ctx, snapshot.Tx())
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	if closed != 0 {
		t.Fatal("input lease closed before publication")
	}
	current = false
	if err := check(); !errors.Is(err, ErrConflict) {
		t.Fatal("publication lost physical continuation", err)
	}
	// A permissive physical continuation still cannot replace actor authority.
	current, f.allowed = true, false
	if err := check(); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("physical continuation replaced actor authority", err)
	}
	closePlaybackSourceContext(ctx)
	closePlaybackSourceContext(ctx)
	if closed != 1 {
		t.Fatal("lease cleanup was not exactly once", closed)
	}
	if err := i.Retain(func(context.Context, *sql.Tx) error { return nil }, func() error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed invocation retained a new lease", err)
	}
}

func TestPlaybackSourceCancellationClosesRetainedChildren(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	snapshot, p, err := f.s.readTx(ctx, f.p, f.item.Public)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := sessionTx(ctx, snapshot.Tx(), p, f.item.Public, "session")
	snapshot.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	selected := f.s.playbackSourceContext(ctx, p, f.item.Public, "session", scope)
	i, _ := PlaybackSourceInvocationFromContext(selected)
	closed := make(chan struct{})
	var closes atomic.Int32
	if err := i.Retain(func(context.Context, *sql.Tx) error { return nil }, func() error {
		if closes.Add(1) == 1 {
			close(closed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close retained children")
	}
	closePlaybackSourceContext(selected)
	if closes.Load() != 1 {
		t.Fatal("cancellation cleanup repeated", closes.Load())
	}
	if err := checkPlaybackInvocation(context.Background(), f.db, i); !errors.Is(err, ErrUnavailable) {
		t.Fatal("cancelled owner lifetime revived through a new child context", err)
	}
}

func TestSelectedPlaybackSourceRechecksLeaseAfterDecoderInputCloses(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "changed before publication"}[changed], func(t *testing.T) {
			f := newSubtitleFixture(t)
			ctx := context.Background()
			receipt, err := f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("retained-source", plainSubtitle, f.item.Token))
			if err != nil {
				t.Fatal(err)
			}
			f.catalog.Exec(`UPDATE subtitle_render_revisions SET source_evidence='representation' WHERE resource_id=?`, receipt.ResourceID)
			f.catalog.Exec(`INSERT INTO subtitle_remote_sessions(session_id,evidence) VALUES('session','representation')`)
			inputClosed, leaseClosed, checks := 0, 0, 0
			current := true
			f.s.SourceInputs = invocationSourceInputs{open: func(read context.Context, item, source, session string) (RenderInput, error) {
				i, ok := PlaybackSourceInvocationFromContext(read)
				if !ok {
					t.Fatal("missing selected invocation")
				}
				if err := i.Retain(func(context.Context, *sql.Tx) error {
					checks++
					if inputClosed != 1 || leaseClosed != 0 {
						t.Error("publication lost real lease lifetime", inputClosed, leaseClosed)
					}
					if !current {
						return ErrConflict
					}
					return nil
				}, func() error { leaseClosed++; return nil }); err != nil {
					t.Fatal(err)
				}
				return &invocationRenderInput{closed: &inputClosed, validate: func(context.Context) error { return nil }, afterClose: func() { current = !changed }}, nil
			}}
			plan, err := f.s.Select(ctx, f.p, f.item.Public, "session", SelectRequest{OperationID: "select-retained-source", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: receipt.ResourceID, ResourceRevision: receipt.Revision, OffsetUS: "0"})
			if changed {
				if !errors.Is(err, ErrConflict) {
					t.Fatal("retired source published a selection", plan, err)
				}
				var operations int
				if err := f.db.QueryRow(`SELECT count(*) FROM subtitle_selection_operations WHERE operation_id='select-retained-source'`).Scan(&operations); err != nil || operations != 0 {
					t.Fatal("refused selection published a receipt", operations, err)
				}
			} else if err != nil || plan.Selected == nil || plan.Selected.ID != receipt.ResourceID {
				t.Fatal("current retained source failed", plan, err)
			}
			if checks < 1 || inputClosed != 1 || leaseClosed != 1 {
				t.Fatal("source continuation/cleanup not retained through publication", checks, inputClosed, leaseClosed)
			}
		})
	}
}
