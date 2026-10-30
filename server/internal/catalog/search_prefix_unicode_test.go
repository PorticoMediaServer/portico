package catalog

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"testing"
)

func TestNumericPrefixesKeepUnicodeAndTypingSteps(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	arabic := c.Movie(lib, "/m/a.mkv", "Serial ١٢٣٤٥٦٧٨٩", 2000)
	padded := c.Movie(lib, "/m/b.mkv", "Title 000000000001", 2000)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	for _, test := range []struct{ q, id string }{{"١", arabic.Public}, {"١٢", arabic.Public}, {"Title 0", padded.Public}, {"Title 00", padded.Public}, {"Title 000", padded.Public}, {"Title 0000", padded.Public}, {"Title 00000", padded.Public}, {"Title 000000", padded.Public}} {
		out, err := s.Search(context.Background(), SearchRequest{Viewer: viewer, Q: test.q, Group: "movies", Limit: 10})
		if err != nil || len(out.Groups) != 1 || out.Groups[0].Status != "success" || len(out.Groups[0].Items) != 1 || out.Groups[0].Items[0].ID != test.id {
			t.Fatalf("q=%s out=%+v err=%v", test.q, out.Groups, err)
		}
	}
}

func TestAmbiguousLabelsNeverClaimPermissionCounts(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 20; i++ {
		item := c.Movie(lib, fmt.Sprintf("/m/%d.mkv", i), "Harbor", 2000)
		if i%2 == 0 {
			c.Attributes(item.ID, "label", "Hidden|Private")
		}
	}
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"Hidden|Private"}}}
	out, err := s.Search(context.Background(), SearchRequest{Viewer: viewer, Q: "Harbor", Group: "movies", Limit: 3})
	if err != nil || out.Groups[0].Status != "success" || len(out.Groups[0].Items) != 3 || out.Groups[0].TotalCountExact {
		t.Fatalf("out=%+v err=%v", out.Groups, err)
	}
	for _, item := range out.Groups[0].Items {
		var denied bool
		if err = c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_item_attribute_edges x ON x.item_id=e.id JOIN catalog_attribute_terms t ON t.id=x.term_id WHERE e.public_id=pid_blob(?) AND t.value_key='hidden|private')`, item.ID).Scan(&denied); err != nil || denied {
			t.Fatalf("denied=%v err=%v", denied, err)
		}
	}
}

func TestPrefixCursorSurvivesDictionaryGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 10; i++ {
		c.Movie(lib, fmt.Sprintf("/m/first-%d.mkv", i), fmt.Sprintf("Title 100000%06d", i), 2000)
	}
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	request := SearchRequest{Viewer: viewer, Q: "1", Group: "movies", Limit: 3}
	first, err := s.Search(context.Background(), request)
	if err != nil || first.Groups[0].Status != "success" || !first.Groups[0].HasMore {
		t.Fatalf("first=%+v err=%v", first.Groups, err)
	}
	for i := 1; i <= 150; i++ {
		c.Movie(lib, fmt.Sprintf("/m/new-%d.mkv", i), fmt.Sprintf("Title %06d000000", 100000+i), 2000)
	}
	c.Drain()
	request.Cursor = first.Groups[0].NextCursor
	next, err := s.Search(context.Background(), request)
	if err != nil || next.Groups[0].Status != "success" || len(next.Groups[0].Items) != 3 {
		t.Fatalf("next=%+v err=%v", next.Groups, err)
	}
	seen := map[string]bool{}
	for _, item := range first.Groups[0].Items {
		seen[item.ID] = true
	}
	for _, item := range next.Groups[0].Items {
		if seen[item.ID] {
			t.Fatal("dictionary growth repeated a result")
		}
	}
	for _, direction := range []string{"asc", "desc"} {
		ctx, trace := dbwork.TraceQueryPlans(context.Background())
		mixed := SearchRequest{Viewer: viewer, Q: "Title 1", Group: "movies", Limit: 3, Direction: direction}
		page, err := s.WithContext(ctx).Search(ctx, mixed)
		if err != nil || page.Groups[0].Status != "success" || len(page.Groups[0].Items) != 3 || !page.Groups[0].HasMore {
			t.Fatalf("wide numeric page=%+v err=%v", page.Groups, err)
		}
		bounded := false
		for _, query := range trace() {
			if strings.Contains(query, "WITH page AS MATERIALIZED") {
				bounded = strings.Contains(query, "catalog_search_prefix_rows") && !strings.Contains(query, "catalog_search_titles MATCH")
			}
		}
		if !bounded {
			t.Fatal("wide numeric intersection returned to full FTS ranking")
		}
		mixed.Cursor = page.Groups[0].NextCursor
		continued, err := s.Search(context.Background(), mixed)
		if err != nil || continued.Groups[0].Status != "success" || len(continued.Groups[0].Items) != 3 {
			t.Fatalf("wide numeric continuation=%+v err=%v", continued.Groups, err)
		}
		seen := map[string]bool{}
		for _, item := range page.Groups[0].Items {
			seen[item.ID] = true
		}
		for _, item := range continued.Groups[0].Items {
			if seen[item.ID] {
				t.Fatal("wide numeric intersection repeated a result")
			}
		}
	}
}
