package compactcatalog_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

// Title blocks stay exact through growth past a split, renames that move rows
// across blocks (including block starts) and removals.
func TestBrowseBlocksCountEveryRowThroughChange(t *testing.T) {
	testtier.Media(t, "4,500 titles to cross block splits")
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	random := rand.New(rand.NewSource(7))
	title := func() string {
		return fmt.Sprintf("%c%c film %06d", 'A'+random.Intn(26), 'a'+random.Intn(26), random.Intn(1000000))
	}
	items := make([]catalogtest.Item, 0, 4500)
	for i := 0; i < 4500; i++ {
		items = append(items, c.Movie(films, fmt.Sprintf("/m/%05d.mkv", i), title(), 2000))
	}
	c.Drain()
	check := func(stage string) {
		t.Helper()
		if err := compactcatalog.CheckBrowseBlocks(context.Background(), c.DB); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
	}
	check("after growth")
	var blocks int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_browse_blocks WHERE kind=1 AND ordering=0`).Scan(&blocks); err != nil || blocks < 3 {
		t.Fatalf("4,500 movies made %d blocks (%v); splits did not happen", blocks, err)
	}
	for _, i := range random.Perm(len(items))[:400] {
		c.Fields(items[i].ID, map[string]any{"title": title()})
	}
	c.Drain()
	check("after renames")
	for _, i := range random.Perm(len(items))[:300] {
		c.Delete(items[i].ID)
	}
	c.Drain()
	check("after removals")
}

// The default tier's share of the block invariants: first rows, renames that
// move a block's start, and removals (a cascade included), below a split.
func TestBrowseBlocksCountASmallLibraryThroughChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	random := rand.New(rand.NewSource(3))
	items := []catalogtest.Item{}
	for i := 0; i < 200; i++ {
		items = append(items, c.Movie(films, fmt.Sprintf("/m/%03d.mkv", i), fmt.Sprintf("%c film %03d", 'A'+random.Intn(26), i), 2000))
	}
	c.Drain()
	for _, i := range random.Perm(len(items))[:40] {
		c.Fields(items[i].ID, map[string]any{"title": fmt.Sprintf("%c renamed %03d", 'A'+random.Intn(26), i)})
	}
	for _, i := range random.Perm(len(items))[:30] {
		c.Delete(items[i].ID)
	}
	c.Drain()
	if err := compactcatalog.CheckBrowseBlocks(context.Background(), c.DB); err != nil {
		t.Fatal(err)
	}
}
