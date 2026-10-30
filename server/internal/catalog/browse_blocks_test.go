package catalog

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

// A title page at any position, answered from the counted blocks, is the
// page an OFFSET over the same order gives: across block boundaries, at the
// start of a block, and at the end.
func TestBrowsePositionsFromBlocksMatchTheOrder(t *testing.T) {
	testtier.Media(t, "4,500 titles across several blocks")
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	random := rand.New(rand.NewSource(11))
	for i := 0; i < 4500; i++ {
		// Repeated titles put ties inside and across blocks.
		c.Movie(films, fmt.Sprintf("/m/%05d.mkv", i), fmt.Sprintf("%c film %03d", 'A'+random.Intn(26), random.Intn(300)), 2000)
	}
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	var blocks int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_browse_blocks WHERE kind=1 AND ordering=0`).Scan(&blocks); err != nil || blocks < 3 {
		t.Fatalf("%d blocks (%v)", blocks, err)
	}
	for _, start := range []int{1, 59, 1023, 1024, 1025, 2047, 2048, 2049, 3000, 4439, 4499} {
		page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "m", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 60, Range: &BrowseRange{Start: start}})
		if err != nil {
			t.Fatal(start, err)
		}
		rows, err := c.DB.Query(`SELECT pid(ce.public_id) FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id
		 WHERE e.library_id=(SELECT id FROM catalog_libraries WHERE library_id='m') AND e.kind=1 ORDER BY e.sort_key COLLATE NOCASE,e.entity_id LIMIT 60 OFFSET ?`, start)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			want = append(want, id)
		}
		rows.Close()
		got := []string{}
		for _, e := range page.Entries {
			got = append(got, e.ID)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("start %d: blocks gave %d entries starting %v, the order gives %d starting %v", start, len(got), first(got), len(want), first(want))
		}
	}
}

func first(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// A fact order's direct position (file size, critic rating) comes from its
// counted blocks and is the page an OFFSET over the order gives, both ways.
func TestFactOrderPositionsFromBlocksMatchTheOrder(t *testing.T) {
	testtier.Media(t, "4,500 titles across several fact-order blocks")
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 4500; i++ {
		c.Movie(films, fmt.Sprintf("/m/%05d.mkv", i), fmt.Sprintf("Film %05d", i), 2000)
	}
	c.Drain()
	// Values move after the rows exist: the move triggers keep the blocks.
	c.Exec(`UPDATE catalog_browse_rows SET file_size=(entity_id*7919)%500,critic_key=(entity_id*104729)%97/10.0 WHERE kind=1`)
	if err := compactcatalog.CheckBrowseBlocks(context.Background(), c.DB); err != nil {
		t.Fatal(err)
	}
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	for _, order := range []struct{ field, column string }{{"fileSize", "file_size"}, {"criticRating", "critic_key"}} {
		for _, direction := range []string{"asc", "desc"} {
			for _, start := range []int{1, 1024, 2049, 3000, 4499} {
				page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Limit: 40, Sort: []BrowseSortSelection{{Field: order.field, Direction: direction}}, Range: &BrowseRange{Start: start}})
				if err != nil {
					t.Fatal(order.field, start, err)
				}
				dir := "ASC"
				if direction == "desc" {
					dir = "DESC"
				}
				rows, err := c.DB.Query(`SELECT pid(ce.public_id) FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.kind=1 ORDER BY e.`+order.column+` `+dir+`,e.entity_id `+dir+` LIMIT 40 OFFSET ?`, start)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{}
				for rows.Next() {
					var id string
					rows.Scan(&id)
					want = append(want, id)
				}
				rows.Close()
				got := []string{}
				for _, e := range page.Entries {
					got = append(got, e.ID)
				}
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("%s %s start %d: blocks gave %d entries starting %v, the order gives %d starting %v", order.field, direction, start, len(got), first(got), len(want), first(want))
				}
			}
		}
	}
}
