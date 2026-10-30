package catalog

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestSelectionLocalSearchBrowseOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	library := c.Library("a", "Movies", "movie", "/a")
	for i, title := range []string{"Alphabet soup", "Beta", "東京都", "東京A", "abcdefghijklmno", "1234567890", "Café WALL·E Spider-Man", "foo bar", "foo𞤀bar", "한글 한국", "カタカナ ひらがな"} {
		c.Movie(library, fmt.Sprintf("/a/%d.mkv", i), title, 2000+i)
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	authority := HomeActionAuthority{Personal: true, Playback: true, Queue: true}
	for _, text := range []string{"a", "alpha", "東京", "東", "東京都", "東京A", "東京a", "abcdefghijklm", "1234567", "cafe", "wall e", "spiderman", "foo𞤀bar", "한", "カ", "k1", "l1", "missing"} {
		for _, wrap := range []string{"leaf", "not", "any"} {
			t.Run(text+"/"+wrap, func(t *testing.T) {
				query := &BrowseNode{Field: "search", Operator: "contains", Value: text}
				switch wrap {
				case "not":
					query = &BrowseNode{Not: query}
				case "any":
					query = &BrowseNode{Any: []BrowseNode{*query, {Field: "year", Operator: "equals", Value: float64(2001)}}}
				}
				request := BrowseRequest{Library: "a", Pivot: "movies", Query: query, Limit: 1}
				state, oracle, witness := selectionBatchFixture(t, s, v, request, authority)
				got := finishSelectionBatch(t, s, state, witness, authority, 1)
				if !reflect.DeepEqual(got, oracle) {
					t.Fatalf("local %+v != actual page oracle %+v", got, oracle)
				}
			})
		}
	}
}

func TestSelectionLocalSearchBytesTokensAndMissingDocument(t *testing.T) {
	db, s, names := browseFixture(t)
	p, err := newSelectionBatchSearch("a")
	if err != nil {
		t.Fatal(err)
	}
	check := func(id int64) (bool, int, selectionSearchWork) {
		t.Helper()
		var yes bool
		var bytes int
		var work selectionSearchWork
		err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
			var err error
			yes, err = s.WithContext(ctx).selectionBatchSearchMatches(p, id, &bytes, &work, nil)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return yes, bytes, work
	}
	yes, beforeBytes, before := check(names["m1"].ID)
	if !yes || beforeBytes <= 0 || before.documentTokens <= 0 || before.queryTokens != 1 || before.tokenBytes <= 0 || before.transientBytes < beforeBytes {
		t.Fatalf("work: %v %d %+v", yes, beforeBytes, before)
	}
	// Unrelated vocabulary cannot increase a singleton's tokenization work.
	var vocabulary strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&vocabulary, "alphabetical%06d ", i)
	}
	if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `UPDATE catalog_search_documents SET search=? WHERE entity_id=?`, vocabulary.String(), names["m2"].ID); err != nil {
		t.Fatal(err)
	}
	// Missing document must be false, including the zero-byte/zero-token probe.
	yes, bytes, work := check(999999)
	if yes || bytes != 0 || work != (selectionSearchWork{}) {
		t.Fatalf("missing %v %d %+v", yes, bytes, work)
	}
	_, afterBytes, after := check(names["m1"].ID)
	if beforeBytes != afterBytes || before != after {
		t.Fatalf("work changed: %d %+v / %d %+v", beforeBytes, before, afterBytes, after)
	}
}

func TestSelectionLocalSearchColdMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	library := c.Library("a", "Movies", "movie", "/a")
	// A bounded light fixture, not the campaign's deferred 1M/5M fixture.
	const titles = 512
	var title strings.Builder
	for i := 0; i < 96; i++ {
		fmt.Fprintf(&title, "alpha%03d ", i)
	}
	for i := 0; i < titles; i++ {
		c.Movie(library, fmt.Sprintf("/a/%d.mkv", i), fmt.Sprintf("%s %d", title.String(), i), 2000)
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	query := &BrowseNode{Field: "search", Operator: "contains", Value: "a"}
	state, oracle, witness := selectionBatchFixture(t, s, v, BrowseRequest{Library: "a", Pivot: "movies", Query: query, Limit: 1}, HomeActionAuthority{})
	started := time.Now()
	var source, examined, tokens int64
	turns := 0
	for !state.Complete() {
		if turns > 20000 {
			t.Fatal("no EOF")
		}
		next, work, err := s.StepBrowseSelectionBatch(context.Background(), state, witness, SelectionBatchLimits{Bytes: 16384})
		if err != nil {
			t.Fatal(err)
		}
		if work.Rows < 1 || work.Rows > SelectionBatchMaximumRows {
			t.Fatalf("rows %+v", work)
		}
		if work.ExaminedBytes > 16384+selectionBatchProbeBytes && work.TransientScalarBytes == 0 {
			t.Fatalf("unaccounted overrun %+v", work)
		}
		source += int64(work.DecodedBytes)
		examined += int64(work.ExaminedBytes)
		tokens += work.DocumentTokens
		state = next
		turns++
	}
	elapsed := time.Since(started)
	if state.counts[1][0] != oracle.Total || oracle.Total != titles || tokens <= 0 || source < titles*1000 {
		t.Fatalf("measurement mismatch counts=%+v oracle=%+v source=%d tokens=%d", state.counts, oracle, source, tokens)
	}
	t.Logf("cold local search: titles=%d wall=%s decodedSourceBytes=%d examinedBytes=%d documentTokens=%d turns=%d", titles, elapsed, source, examined, tokens, turns)
}
