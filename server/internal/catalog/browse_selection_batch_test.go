package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func selectionBatchFixture(t *testing.T, s *Service, v Viewer, request BrowseRequest, authority HomeActionAuthority) (*BrowseSelectionBatchState, BrowseSelectionDescriptor, SelectionBatchWitness) {
	t.Helper()
	witness := func(read *Service) (string, error) {
		return read.browseExactWitness(request.Library, v.Profile, v.Restrictions)
	}
	var state *BrowseSelectionBatchState
	var oracle BrowseSelectionDescriptor
	err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		page, err := read.BrowseEntities(v, request)
		if err != nil {
			return err
		}
		oracle, err = read.BrowseSelectionAggregate(request, v, *page.Bound, authority)
		if err != nil {
			return err
		}
		basis, err := witness(read)
		if err != nil {
			return err
		}
		state, err = read.NewBrowseSelectionBatch(request, v, *page.Bound, basis)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return state, oracle, witness
}

func finishSelectionBatch(t *testing.T, s *Service, state *BrowseSelectionBatchState, witness SelectionBatchWitness, authority HomeActionAuthority, rows int) BrowseSelectionDescriptor {
	t.Helper()
	for step := 0; !state.Complete(); step++ {
		if step > 20000 {
			t.Fatal("reducer failed to progress")
		}
		before := state.Clone()
		next, work, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Rows: rows})
		if err != nil {
			t.Fatal(err)
		}
		if work.Rows < 1 || work.Rows > rows {
			t.Fatalf("raw budget %+v", work)
		}
		if work.Bytes < next.Bytes() {
			t.Fatalf("undercharged memory %+v retained=%d", work, next.Bytes())
		}
		if !reflect.DeepEqual(state, before) {
			t.Fatal("input state mutated")
		}
		if reflect.DeepEqual(state, next) {
			t.Fatal("successful step made no progress")
		}
		state = next
	}
	var result BrowseSelectionDescriptor
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var err error
		result, err = s.WithContext(ctx).FinishBrowseSelectionBatch(state, witness, authority)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSelectionBatchExactOracleAndClone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, s, _ := browseFixture(t)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	authority := HomeActionAuthority{Personal: true, Organize: true, Bulk: true, Playback: true, Queue: true, Metadata: true, Refresh: true, Downloads: true, DeleteLibrary: func(string) (bool, error) { return true, nil }}
	queries := []string{`null`, `{"field":"subtitleLanguage","operator":"is-missing","value":null}`, `{"field":"savedCollection","operator":"not-in","value":["missing"]}`, `{"field":"match","operator":"equals","value":"unmatched"}`, `{"field":"playState","operator":"equals","value":"played"}`, `{"field":"playState","operator":"equals","value":"unplayed"}`, `{"field":"playState","operator":"not-in","value":["played"]}`, `{"field":"genre","operator":"equals","value":"Action"}`, `{"field":"genre","operator":"not-equals","value":"Action"}`, `{"field":"genre","operator":"is-missing","value":null}`, `{"field":"year","operator":"at-least","value":2000}`, `{"field":"year","operator":"at-least","value":3000}`, `{"any":[{"field":"year","operator":"at-least","value":2000},{"not":{"field":"title","operator":"contains","value":"nothing"}}]}`, `{"field":"versions","operator":"is-present","value":null}`, `{"field":"favorite","operator":"equals","value":false}`, `{"field":"personalRating","operator":"is-missing","value":null}`, `{"field":"durationMs","operator":"is-present","value":null}`}
	for _, raw := range queries {
		t.Run(raw, func(t *testing.T) {
			query, err := ParseBrowseQuery(json.RawMessage(raw), "query")
			if err != nil {
				t.Fatal(err)
			}
			request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: query, Limit: 1}
			state, oracle, witness := selectionBatchFixture(t, s, v, request, authority)
			// Changing the caller's AST after capture cannot change stored identity or intent.
			if query != nil {
				query.Field = "title"
				query.Value = "mutated"
			}
			got := finishSelectionBatch(t, s, state, witness, authority, 1)
			if !reflect.DeepEqual(got, oracle) {
				t.Fatalf("got %+v\nwant %+v", got, oracle)
			}
		})
	}
}

func TestSelectionBatchRestrictionAndAttributeRawProgress(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("a", "Movies", "movie", "/a")
	visible := c.Movie(library, "/a/visible.mkv", "Visible", 2020)
	hidden := c.Movie(library, "/a/hidden.mkv", "Hidden", 2020)
	unrated := c.Movie(library, "/a/unrated.mkv", "Unrated", 2020)
	for _, item := range []catalogtest.Item{visible, hidden} {
		c.Attributes(item.ID, "contentRating", "PG")
	}
	c.Attributes(hidden.ID, "label", "private")
	// A long rejected attribute history must progress across nested cursors.
	values := []string{}
	for i := 0; i < 80; i++ {
		values = append(values, fmt.Sprintf("unmatched-%03d", i))
	}
	c.Attributes(visible.ID, "tag", values...)
	c.Attributes(hidden.ID, "tag", "wanted")
	c.Attributes(unrated.ID, "tag", "wanted")
	c.Drain()
	s := New(c.DB)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	age := 13
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true, BlockedLabels: []string{"private"}}}
	for _, raw := range []string{`null`, `{"field":"tag","operator":"equals","value":"wanted"}`, `{"field":"tag","operator":"not-equals","value":"wanted"}`, `{"field":"tag","operator":"contains-all","value":["unmatched-000","unmatched-079"]}`} {
		query, err := ParseBrowseQuery(json.RawMessage(raw), "query")
		if err != nil {
			t.Fatal(err)
		}
		request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: query, Limit: 1}
		state, oracle, witness := selectionBatchFixture(t, s, v, request, HomeActionAuthority{Playback: true})
		got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{Playback: true}, 3)
		if !reflect.DeepEqual(got, oracle) {
			t.Fatalf("%s got %+v want %+v", raw, got, oracle)
		}
	}
}

func TestSelectionBatchLiveAuthorityDriftAndRestart(t *testing.T) {
	_, s, _ := browseFixture(t)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 1}
	granted := HomeActionAuthority{Playback: true, DeleteLibrary: func(string) (bool, error) { return true, nil }}
	state, oracle, witness := selectionBatchFixture(t, s, v, request, granted)
	next, _, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Rows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if next == state || reflect.DeepEqual(next, state) {
		t.Fatal("no independent progress")
	}
	if _, _, err = s.StepBrowseSelectionBatch(context.Background(), next, func(*Service) (string, error) { return "changed", nil }, SelectionBatchLimits{}); !errors.Is(err, ErrSelectionBatchStale) {
		t.Fatalf("drift accepted: %v", err)
	}
	denied := finishSelectionBatch(t, s, next, witness, HomeActionAuthority{}, 2)
	if denied.Total != oracle.Total || denied.Types[0].CapabilityCounts != (SelectionCapabilityCounts{}) {
		t.Fatalf("cached grants %+v", denied)
	}
	// Restart discards state; the same complete key can start a fresh reduction.
	restarted, _, _ := selectionBatchFixture(t, s, v, request, granted)
	if restarted.Key() != state.Key() || restarted.after != 0 {
		t.Fatal("identity/restart mismatch")
	}
	if _, _, err = s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{RetainedBytes: 1}); !errors.Is(err, ErrSelectionBatchCapacity) {
		t.Fatal(err)
	}
	if _, err = s.FinishBrowseSelectionBatch(state, witness, granted); !errors.Is(err, ErrSelectionBatchIncomplete) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = s.StepBrowseSelectionBatch(ctx, state, witness, SelectionBatchLimits{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSelectionBatchRejectsUnboundedAndExcludedForms(t *testing.T) {
	_, s, _ := browseFixture(t)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 1}
		page, err := read.BrowseEntities(v, request)
		if err != nil {
			return err
		}
		for _, node := range []*BrowseNode{{Field: "homeRow", Operator: "equals", Value: "new_on_server"}, {Field: "dateAdded", Operator: "within-last-days", Value: 7}} {
			bound := *page.Bound
			bound.Query = node
			if _, err = read.NewBrowseSelectionBatch(request, v, bound, "w"); !errors.Is(err, ErrSelectionBatchUnsupported) {
				t.Fatalf("%s: %v", node.Field, err)
			}
		}
		bound := *page.Bound
		bound.Sorts = []BrowseSortSelection{{Field: "random", Seed: 4}}
		if _, err = read.NewBrowseSelectionBatch(request, v, bound, "w"); !errors.Is(err, ErrSelectionBatchUnsupported) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSelectionBatchDoesNoWritesOrWholeViewAggregation(t *testing.T) {
	_, s, _ := browseFixture(t)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	state, _, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 1}, HomeActionAuthority{})
	ctx, statements := dbwork.TraceStatements(context.Background())
	for !state.Complete() {
		var err error
		state, _, err = s.StepBrowseSelectionBatch(ctx, state, witness, SelectionBatchLimits{Rows: 20})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range statements() {
		upper := strings.ToUpper(statement)
		if strings.Contains(upper, "COUNT(") || strings.Contains(upper, "GROUP BY") || strings.Contains(upper, "INSERT ") || strings.Contains(upper, "UPDATE ") || strings.Contains(upper, "DELETE ") {
			t.Fatalf("unbounded/write statement: %s", statement)
		}
	}
}

func TestSelectionBatchUnavailableAssetParity(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("mu", "Music", "music", "/mu")
	artist := c.Artist(library, "Artist")
	album := c.Album(artist, "Album", 2020)
	item := c.Song(album, 1, "/mu/a.flac", "A")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"available": false})
	})
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"mu"}}
	state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "mu", Profile: "p", ViewerFence: "f", Pivot: "songs", Limit: 1}, HomeActionAuthority{Playback: true, Queue: true, Downloads: true})
	got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{Playback: true, Queue: true, Downloads: true}, 2)
	if !reflect.DeepEqual(got, oracle) || got.Total != 1 || got.Types[0].CapabilityCounts.Play != 0 {
		t.Fatalf("got %+v want %+v", got, oracle)
	}
}

func TestSelectionBatchPublishedVocabularyOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, s, names := browseFixture(t)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	for _, field := range browseFields {
		if field.ID == "homeRow" || field.ID == "search" {
			continue
		}
		for _, operator := range field.Operators {
			if operator == "within-last-days" {
				continue
			}
			t.Run(field.ID+"/"+operator, func(t *testing.T) {
				var value any = "missing"
				if len(field.AllowedValues) > 0 {
					value = field.AllowedValues[0]
				}
				switch field.Type {
				case valueNumber:
					value = float64(1)
				case valueBoolean:
					value = false
				case valueDate:
					value = "2026-09-01"
				}
				if field.ID == "episodeGroup" {
					value = names["m1"].Public + "/all"
				}
				if field.ID == "search" {
					value = "al"
				}
				switch operator {
				case "in", "not-in", "contains-any", "contains-all":
					value = []any{value}
				case "between":
					value = []any{value, value}
				case "is-present", "is-missing":
					value = nil
				}
				raw, err := json.Marshal(BrowseNode{Field: field.ID, Operator: operator, Value: value})
				if err != nil {
					t.Fatal(err)
				}
				query, err := ParseBrowseQuery(raw, "query")
				if err != nil {
					t.Fatal(err)
				}
				request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: query, Limit: 1}
				state, oracle, witness := selectionBatchFixture(t, s, v, request, HomeActionAuthority{Playback: true})
				got := finishSelectionBatch(t, s, state, witness, HomeActionAuthority{Playback: true}, 64)
				if !reflect.DeepEqual(got, oracle) {
					t.Fatalf("got %+v want %+v", got, oracle)
				}
			})
		}
	}
}
