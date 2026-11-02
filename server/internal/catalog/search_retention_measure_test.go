package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Evidence only. Both revisions use the same seeded database and public IDs.
func TestSearchRetentionMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_SEARCH_PROFILE_ITEMS"))
	if n == 0 {
		t.Skip("hosted retention probe")
	}
	db, err := persistence.Open(filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("search-realistic-films-%d.sqlite", n)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := os.ReadFile(filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("search-realistic-films-%d.sqlite.corpus.json", n)))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Queries []struct {
			Name string
			Q    string
		}
	}
	if err = json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	queries := map[string]string{}
	for _, q := range corpus.Queries {
		queries[q.Name] = q.Q
	}
	var title string
	if err = db.QueryRow(`SELECT title FROM catalog_search_documents ORDER BY entity_id LIMIT 1`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	queries["exact-title"] = title
	words := strings.Fields(title)
	if len(words) > 1 {
		queries["starts-with"] = strings.Join(words[:2], " ")
	} else {
		queries["starts-with"] = title
	}
	results := map[string]any{}
	for name, q := range queries {
		ctx, cost := dbwork.Measure(context.Background())
		ctx, trace := dbwork.TraceQueryPlans(ctx)
		out, err := New(db).Search(ctx, SearchRequest{Viewer: Viewer{Profile: "F-empty-profile", Fence: "F-search", Libraries: []string{"movies"}}, Q: q, Group: "movies", Limit: 10, Sort: "relevance"})
		if err != nil || len(out.Groups) != 1 || out.Groups[0].Status != "success" {
			t.Fatalf("%s: %+v %v", name, out, err)
		}
		results[name] = map[string]any{"q": q, "items": out.Groups[0].Items, "total": out.Groups[0].TotalCount, "exact": out.Groups[0].TotalCountExact, "vm": cost().VMSteps, "queries": trace()}
	}
	raw, err = json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(os.Getenv("PORTICO_SCALE_REPORT"), "retention.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
