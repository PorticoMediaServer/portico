package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestHomeManifestDefersShelvesAndComposesBoundedSpotlight(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	req := homeRequestFixture("movies", "tv", "music")
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	doc, err := s.WithContext(ctx).HomeManifest(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Rows) == 0 {
		t.Fatal("empty manifest")
	}
	for _, row := range doc.Rows {
		if !row.Deferred || len(row.Entries) != 0 || row.Endpoint == "" || row.ViewerFence != req.Viewer.Fence {
			t.Fatalf("row was composed or unfenced: %+v", row)
		}
	}
	if len(doc.Spotlight) != 5 {
		t.Fatalf("spotlight candidates: %d", len(doc.Spotlight))
	}
	for _, query := range trace() {
		if strings.Contains(query, "substr(COALESCE(sh.overview,d.overview") && !strings.Contains(query, "1,300") {
			t.Fatalf("unbounded overview: %s", query)
		}
	}

	spec, schema, err := apispec.Schema("HomeDocument")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	if issues := spec.ValidateJSON(schema, raw); len(issues) > 0 {
		t.Fatal(issues)
	}
}

func TestDeferredHomeFamilyIsBoundedAndFenced(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	req := homeRequestFixture("movies", "tv", "music")
	row, err := s.HomeSingleRow(req, "for_you", HomeRowPage{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if row.Deferred || row.ViewerFence != req.Viewer.Fence || len(row.Children) > recPersonalRows {
		t.Fatalf("family %+v", row)
	}
	for _, child := range row.Children {
		if child.Children != nil || child.Deferred || child.Family != "for_you" || child.Limit != 2 || child.ViewerFence != row.ViewerFence {
			t.Fatalf("child %+v", child)
		}
	}
	req.HiddenRowIDs = []string{"for_you"}
	if _, err = s.HomeSingleRow(req, "for_you", HomeRowPage{}); err != ErrHomeRowUnknown {
		t.Fatalf("hidden family reachable: %v", err)
	}
}

func TestContinuePreviewDoesNotReadSiblingRow(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	r := homeRequestFixture("movies", "tv", "music")
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	row, err := s.WithContext(ctx).HomeSingleRow(r, "continue", HomeRowPage{Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(row)
	if strings.Contains(string(raw), `"hero"`) {
		t.Fatal("retired hero output")
	}

	sources := 0
	for _, q := range trace() {
		if strings.Contains(q, "FROM progress_activity") {
			sources++
		}
	}
	if sources > 2 {
		t.Fatalf("preview composed its sibling: %d activity reads", sources)
	}
}

// The approved spotlight replaces the continuation-only hero. Wire responses
// must not retain the retired single-entry selector.
func TestHomeSpotlightRetiresSingleHero(t *testing.T) {
	s := New(homeFixture(t))
	doc, err := s.HomeRows(homeRequestFixture())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), `"hero"`) {
		t.Fatal("retired hero serialized")
	}
	if len(doc.Spotlight) != 5 {
		t.Fatalf("spotlight %+v", doc.Spotlight)
	}
}
