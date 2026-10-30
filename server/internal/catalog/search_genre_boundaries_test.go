package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// Real baseline and real capture/authority, with algorithm facts isolated from
// the catalogue producer. Full real-source build/oracle coverage is separate.
func genrePinnedFacts(t *testing.T, facts []FacetBaseFact) (*GenreRuntime, *readprep.Store, Viewer) {
	t.Helper()
	c := catalogtest.Open(t)
	assertGenreBaseline(t, c)
	c.Library("movies", "Movies", "movie", "/movies")
	c.Drain()
	viewer := Viewer{Libraries: []string{"movies"}}
	var plan FacetBasePlan
	var authority string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		scopes, err := captureGenreScopes(ctx, tx, viewer)
		if err != nil {
			return err
		}
		plan, err = CaptureSearchGenreBasePlan(ctx, tx, scopes[0])
		if err != nil {
			return err
		}
		authority, err = CaptureFacetAuthority(ctx, tx, scopes[0])
		return err
	})
	b := &FacetBases{DB: c.DB, Producer: &facetFixtureProducer{facts: facts}, MaxBytes: 128 << 20}
	var base int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		base, err = b.Begin(ctx, tx, plan.Scope, plan.Witness, authority)
		return err
	})
	for {
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = b.Step(ctx, tx, base)
			return err
		})
		if done {
			break
		}
	}
	runtime, _, store := genreTestRuntime(t, c)
	return runtime, store, viewer
}
func genreAwait(t *testing.T, r *GenreRuntime, caller readprep.Caller, out SearchGenreResult) SearchGenreResult {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for out.Page == nil {
		var id string
		if out.Preparing != nil {
			id = out.Preparing.QueryID
		} else if out.Fallback != nil {
			id = out.Fallback.QueryID
		}
		out.Release()
		if id == "" || time.Now().After(deadline) {
			t.Fatal("preparation did not complete", id)
		}
		time.Sleep(time.Millisecond)
		var err error
		out, err = r.Poll(context.Background(), caller, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func TestGenreRuntimeDeliveryAuthAndOpaqueAnchors(t *testing.T) {
	r, store, v := genrePinnedFacts(t, []FacetBaseFact{{"Alpha", "Alpha", 3}, {"Beta", "Beta", 2}, {"Gamma", "Gamma", 1}})
	first := genreReady(t, r, v, SearchGenreRequest{Limit: 1})
	ctx := context.Background()
	token, err := r.decode(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.NextCursor) > 512 || strings.Contains(first.NextCursor, "Alpha") {
		t.Fatal("noncompact/text anchor")
	}
	if _, err = r.Poll(ctx, "different-session", first.QueryID); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("foreign caller saw private query", err)
	}
	if _, err = r.Continue(ctx, "caller", v, SearchGenreRequest{Limit: 2, LimitPresent: true}, first.NextCursor); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("explicit limit changed captured page", err)
	}
	if _, err = r.Continue(ctx, "caller", v, SearchGenreRequest{Limit: 0, LimitPresent: true}, first.NextCursor); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("explicit zero inherited limit", err)
	}
	corrupted := []byte(first.NextCursor)
	corrupted[30] ^= 1
	if _, err = r.Continue(ctx, "caller", v, SearchGenreRequest{}, string(corrupted)); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("tampered anchor accepted", err)
	}
	out, err := r.Continue(ctx, "caller", v, SearchGenreRequest{}, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	out = genreAwait(t, r, "caller", out)
	secondToken, err := r.decode(out.Page.ReloadCursor)
	if err != nil {
		t.Fatal(err)
	}
	if token.expires != secondToken.expires {
		t.Fatal("continuation extended original lease", token.expires, secondToken.expires)
	}
	if store.Stats().Deliveries != 1 || store.Stats().DeliveryBytes < 32768 {
		t.Fatal("ready delivery released before caller serialization", store.Stats())
	}
	out.Release()
	out.Release()
	if store.Stats().Deliveries != 0 {
		t.Fatal("delivery leak", store.Stats())
	}
	var calls atomic.Int64
	r.check = func(context.Context, readprep.Caller, FacetBaseSnapshot) error {
		if calls.Add(1) == 3 {
			return ErrFacetAuthorityUnavailable
		}
		return nil
	}
	denied, err := r.Poll(ctx, "caller", first.QueryID)
	denied.Release()
	if !errors.Is(err, ErrFacetAuthorityUnavailable) || denied.Page != nil || store.Stats().Deliveries != 0 {
		t.Fatal("post-hydration authority/release", err, denied, store.Stats())
	}
	r.check = func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil }
	if _, err = r.Poll(ctx, "caller", first.QueryID); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("denied private handle survived", err)
	}
}
func TestGenreRuntimeByteShortReverseAndLegalScalar(t *testing.T) {
	// Each ordinary row is below the scalar exception, but the page's encoded
	// estimate admits only the closest suffix/prefix. Reverse must stay adjacent.
	facts := []FacetBaseFact{}
	for _, prefix := range []string{"A", "B", "C", "D"} {
		value := prefix + strings.Repeat("x", 16<<10)
		facts = append(facts, FacetBaseFact{value, value, 1})
	}
	r, store, v := genrePinnedFacts(t, facts)
	first := genreReady(t, r, v, SearchGenreRequest{Limit: 4})
	if len(first.Genres) != 1 || first.NextCursor == "" {
		t.Fatal("fixture did not byte-shorten", len(first.Genres))
	}
	second := genreReady(t, r, v, SearchGenreRequest{Cursor: first.NextCursor})
	back := genreReady(t, r, v, SearchGenreRequest{Cursor: second.PreviousCursor})
	if !reflect.DeepEqual(first.Genres, back.Genres) {
		t.Fatal("byte-short reverse skipped adjacent page")
	}
	h, _ := genreHandle(first.QueryID)
	summary, err := store.Poll(context.Background(), "caller", h)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Payload) > 16384 || strings.Contains(string(summary.Payload), facts[0].Value) {
		t.Fatal("cache retained scalar labels", len(summary.Payload))
	}
	summary.Release()
	t.Run("oversize-uncached", func(t *testing.T) {
		value := strings.Repeat("Z", 2<<20)
		runtime, cache, viewer := genrePinnedFacts(t, []FacetBaseFact{{value, value, 1}})
		out, err := runtime.Initial(context.Background(), "caller", viewer, SearchGenreRequest{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		out = genreAwait(t, runtime, "caller", out)
		defer out.Release()
		if len(out.Page.Genres) != 1 || out.Page.Genres[0].Value != value {
			t.Fatal("legal full scalar was truncated or refused")
		}
		// The sole O(B) response is transient. Cached summary and query descriptor
		// remain IDs/policy only; no 4MiB value/label copy enters the store.
		handle, _ := genreHandle(out.Page.QueryID)
		cached, err := cache.Poll(context.Background(), "caller", handle)
		if err != nil {
			t.Fatal(err)
		}
		defer cached.Release()
		var payload genreSummary
		if err = json.Unmarshal(cached.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if len(cached.Payload) > 16384 || cache.Stats().Bytes > 1<<20 {
			t.Fatal("exceptional scalar entered ordinary query cache", len(cached.Payload), cache.Stats())
		}
	})
}

func TestGenreRuntimeColdUnknownFallbackAndCompatibilityPreparing(t *testing.T) {
	c := catalogtest.Open(t)
	assertGenreBaseline(t, c)
	c.Library("movies", "Movies", "movie", "/movies")
	c.Drain()
	store, err := readprep.New(readprep.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	check := func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil }
	bases, err := NewFacetRuntime(c.DB, store, check, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewGenreRuntime(c.DB, store, check, bases)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately do not start the base worker. Simulate the presentation clock
	// reaching the approved quiet-gap fallback without a ten-second fixture wait.
	r.now = func() time.Time { return time.Now().Add(11 * time.Second) }
	viewer := Viewer{Libraries: []string{"movies"}}
	out, err := r.Initial(context.Background(), "caller", viewer, SearchGenreRequest{Preview: "covers"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Page != nil || out.Fallback == nil || out.Fallback.HasGenresKnown || out.Fallback.HasGenres != nil || !out.Fallback.TotalIsLowerBound || out.Fallback.QueryID == "" || out.Fallback.ReloadCursor != "" {
		t.Fatal("cold fallback became ordinary empty ready", out)
	}
	id := out.Fallback.QueryID
	out.Release()
	s := New(c.DB)
	s.UseGenreRuntime(r)
	if _, err = s.SearchGenres(viewer, SearchGenreRequest{}); !errors.Is(err, ErrFacetBaseUnavailable) {
		t.Fatal("compatibility method invented caller authority", err)
	}
	s = s.WithContext(WithSearchGenreCaller(context.Background(), "caller"))
	_, err = s.SearchGenres(viewer, SearchGenreRequest{})
	var preparing *SearchGenrePreparingError
	if !errors.As(err, &preparing) || preparing.Preparation.QueryID == "" {
		t.Fatal("compatibility cold read lost typed preparation", err)
	}
	if err = r.Cancel(context.Background(), "caller", id); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Poll(context.Background(), "caller", id); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("canceled cold handle survived", err)
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("fallback delivery leak", store.Stats())
	}
}

func TestGenreColdPreparationFollowsArtEpochUntilCompleteBasis(t *testing.T) {
	c := catalogtest.Open(t)
	assertGenreBaseline(t, c)
	library := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(library, "/movies/a.mkv", "A", 2000)
	c.Genres(movie.ID, "fixture", "Genre")
	c.Drain()
	store, err := readprep.New(readprep.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	check := func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil }
	bases, err := NewFacetRuntime(c.DB, store, check, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewGenreRuntime(c.DB, store, check, bases)
	if err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Libraries: []string{"movies"}}
	initial, err := r.Initial(context.Background(), "caller", viewer, SearchGenreRequest{Preview: "backdrop"})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Preparing == nil {
		initial.Release()
		t.Fatal("fixture unexpectedly ready")
	}
	id := initial.Preparing.QueryID
	initial.Release()
	selectSearchGenreArt(t, c, movie, "item", "backdrop")
	var plan FacetBasePlan
	var authority string
	var base int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		scopes, err := captureGenreScopes(ctx, tx, viewer)
		if err != nil {
			return err
		}
		plan, err = CaptureSearchGenreBasePlan(ctx, tx, scopes[0])
		if err != nil {
			return err
		}
		authority, err = CaptureFacetAuthority(ctx, tx, scopes[0])
		return err
	})
	b := &FacetBases{DB: c.DB, Producer: plan.Producer, MaxBytes: 128 << 20}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		base, err = b.Begin(ctx, tx, plan.Scope, plan.Witness, authority)
		return err
	})
	for step := 0; ; step++ {
		if step > 100 {
			t.Fatal("source failed to finish")
		}
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = b.Step(ctx, tx, base)
			return err
		})
		if done {
			break
		}
	}
	out, err := r.Poll(context.Background(), "caller", id)
	if err != nil {
		t.Fatal("cold content drift became terminal", err)
	}
	out = genreAwait(t, r, "caller", out)
	defer out.Release()
	if out.Page.HasGenres == nil || !*out.Page.HasGenres || len(out.Page.Genres) != 1 || out.Page.Genres[0].Backdrop == nil || out.Page.Genres[0].Backdrop.ID != movie.Public {
		t.Fatal("pending query did not follow coherent newer art basis", out.Page)
	}
}

func TestGenreHydrateRejectsNonJSONSafeCountBeforeStringRead(t *testing.T) {
	for _, count := range []int64{0, -1, 9007199254740992} {
		// No DB/store: an invalid wire count must fail before scalar preprobe/read.
		_, err := (&GenreRuntime{}).hydrate(context.Background(), "", "", readprep.Result{}, genreDescriptor{}, genreSummary{Rows: []genreRow{{Count: count}}})
		if !errors.Is(err, ErrFacetBaseInput) {
			t.Fatal(count, err)
		}
	}
}
