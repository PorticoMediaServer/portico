//go:build linux

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// budgetRoot makes folders F00, F01, … under a new root, folder i holding
// sizes[i] files, and returns the root and one request per folder.
func budgetRoot(t *testing.T, sizes []int) (string, []InventoryRequest) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	requests := []InventoryRequest{}
	for i, n := range sizes {
		folder := fmt.Sprintf("F%02d", i)
		if err = os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < n; j++ {
			if err = os.WriteFile(filepath.Join(root, folder, fmt.Sprintf("n%03d.mkv", j)), []byte("m"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		requests = append(requests, InventoryRequest{Root: root, RelativePath: folder, Limit: 64})
	}
	page, err := LocalInventoryPage(InventoryRequest{Root: root, RelativePath: ".", Limit: 64})
	if err != nil {
		t.Fatal(err)
	}
	for i := range requests {
		requests[i].RootIdentity = page.RootIdentity
	}
	return root, requests
}

func repeat(n, size int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = size
	}
	return out
}

// budgetBoth answers the batch through the real helper process and through the
// in-process path, requires them to agree and to pass the response check.
func budgetBoth(t *testing.T, requests []InventoryRequest, budget int) []BudgetEnvelope {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper, err := New(binary).InventoryPagesBudget(context.Background(), "budget", requests, budget)
	if err != nil {
		t.Fatal(err)
	}
	local, err := LocalInventoryPagesBudget(requests, budget)
	if err != nil {
		t.Fatal(err)
	}
	// The JSON round trip turns empty slices into nil; compare as JSON.
	a, _ := json.Marshal(helper)
	b, _ := json.Marshal(local)
	if string(a) != string(b) {
		t.Fatalf("the helper and the in-process path disagree:\nhelper %s\nlocal  %s", a, b)
	}
	if err = CheckBudgetResponse(requests, budget, helper); err != nil {
		t.Fatalf("a genuine answer failed the check: %v", err)
	}
	return helper
}

type budgetShape struct {
	pages, raw, skipped, entries, errors int
}

func shapeOf(envelopes []BudgetEnvelope) budgetShape {
	var s budgetShape
	for _, e := range envelopes {
		switch e.Outcome {
		case BudgetPage:
			s.pages++
			s.raw += e.RawUsed
			s.entries += len(e.Page.Entries)
		case BudgetSkipped:
			s.skipped++
		case BudgetError:
			s.errors++
		}
	}
	return s
}

// TestBudgetedBatchMatchesTheSpecifiedShapes is DESIGN-PERF-SRV-029 r3's
// expectation table, each row through the helper and the in-process path.
func TestBudgetedBatchMatchesTheSpecifiedShapes(t *testing.T) {
	t.Run("30 folders of 12, budget 64", func(t *testing.T) {
		_, requests := budgetRoot(t, repeat(30, 12))
		envelopes := budgetBoth(t, requests, 64)
		if got := shapeOf(envelopes); got != (budgetShape{pages: 6, raw: 64, skipped: 24, entries: 64}) {
			t.Fatalf("shape %+v", got)
		}
		for i := 0; i < 5; i++ {
			if p := envelopes[i].Page; envelopes[i].RawUsed != 12 || !p.Complete || p.NextCursor != "" {
				t.Fatalf("folder %d: %+v", i, envelopes[i])
			}
		}
		cut := envelopes[5]
		if cut.RawUsed != 4 || cut.Page.Complete || cut.Page.NextCursor == "" {
			t.Fatalf("the cut folder: %+v", cut)
		}
		// Next quantum the cut folder comes first with the full limit: its last
		// eight names and EOF in the same call.
		next := requests[5:]
		next[0].Cursor = cut.Page.NextCursor
		again := budgetBoth(t, next, 64)
		if p := again[0].Page; again[0].RawUsed != 8 || !p.Complete || len(p.Entries) != 8 {
			t.Fatalf("the cut folder's continuation: %+v", again[0])
		}
		seen := map[string]bool{}
		for _, e := range append(cut.Page.Entries, again[0].Page.Entries...) {
			if seen[e.Name] {
				t.Fatalf("%s listed twice across the cut", e.Name)
			}
			seen[e.Name] = true
		}
		if len(seen) != 12 {
			t.Fatalf("the cut folder lost names: %d of 12", len(seen))
		}
	})
	t.Run("32 folders of 12, budget 64", func(t *testing.T) {
		_, requests := budgetRoot(t, repeat(32, 12))
		if got := shapeOf(budgetBoth(t, requests, 64)); got != (budgetShape{pages: 6, raw: 64, skipped: 26, entries: 64}) {
			t.Fatalf("shape %+v", got)
		}
	})
	t.Run("32 empty folders", func(t *testing.T) {
		_, requests := budgetRoot(t, repeat(32, 0))
		envelopes := budgetBoth(t, requests, 64)
		if got := shapeOf(envelopes); got != (budgetShape{pages: 32}) {
			t.Fatalf("shape %+v", got)
		}
		for i, e := range envelopes {
			if !e.Page.Complete {
				t.Fatalf("empty folder %d is not complete", i)
			}
		}
	})
	t.Run("a 200-name folder first", func(t *testing.T) {
		_, requests := budgetRoot(t, append([]int{200}, repeat(31, 3)...))
		envelopes := budgetBoth(t, requests, 64)
		if got := shapeOf(envelopes); got != (budgetShape{pages: 1, raw: 64, skipped: 31, entries: 64}) {
			t.Fatalf("shape %+v", got)
		}
		single, err := LocalInventoryPage(requests[0])
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(single.Entries, envelopes[0].Page.Entries) || single.NextCursor != envelopes[0].Page.NextCursor || single.Complete {
			t.Fatal("the first folder's page differs from the single-folder page")
		}
	})
	t.Run("an error after five folders", func(t *testing.T) {
		root, requests := budgetRoot(t, repeat(32, 12))
		if err := os.RemoveAll(filepath.Join(root, "F05")); err != nil {
			t.Fatal(err)
		}
		envelopes := budgetBoth(t, requests, 64)
		if got := shapeOf(envelopes); got != (budgetShape{pages: 5, raw: 60, skipped: 26, entries: 60, errors: 1}) {
			t.Fatalf("shape %+v", got)
		}
		if envelopes[5].Outcome != BudgetError || envelopes[5].ErrorCode != "not_found" {
			t.Fatalf("the missing folder: %+v", envelopes[5])
		}
	})
	t.Run("ignored names are charged and still advance", func(t *testing.T) {
		root, requests := budgetRoot(t, []int{0, 2})
		for j := 0; j < 5; j++ {
			if err := os.WriteFile(filepath.Join(root, "F00", fmt.Sprintf(".hidden%d", j)), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		requests[0].Limit, requests[1].Limit = 3, 3
		envelopes := budgetBoth(t, requests, 4)
		first := envelopes[0]
		if first.RawUsed != 3 || len(first.Page.Entries) != 0 || first.Page.Complete || first.Page.NextCursor == "" {
			t.Fatalf("an all-ignored folder: %+v", first)
		}
		if envelopes[1].RawUsed != 1 || envelopes[1].Page.Complete {
			t.Fatalf("the folder after it got the rest of the budget: %+v", envelopes[1])
		}
	})
}

// TestBudgetResponseCheckRefusesEveryMalformedShape: each mutation of a
// genuine answer is refused whole.
func TestBudgetResponseCheckRefusesEveryMalformedShape(t *testing.T) {
	_, requests := budgetRoot(t, repeat(8, 12))
	genuine, err := LocalInventoryPagesBudget(requests, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckBudgetResponse(requests, 64, genuine); err != nil {
		t.Fatal(err)
	}
	clone := func() []BudgetEnvelope {
		raw, _ := json.Marshal(genuine)
		var out []BudgetEnvelope
		_ = json.Unmarshal(raw, &out)
		return out
	}
	page := func(e BudgetEnvelope) *InventoryPage { p := *e.Page; return &p }
	for name, mutate := range map[string]func([]BudgetEnvelope) []BudgetEnvelope{
		"wrong folder":     func(e []BudgetEnvelope) []BudgetEnvelope { e[1].RelativePath = "F07"; return e },
		"extra envelope":   func(e []BudgetEnvelope) []BudgetEnvelope { return append(e, e[7]) },
		"missing envelope": func(e []BudgetEnvelope) []BudgetEnvelope { return e[:7] },
		"page after a skip": func(e []BudgetEnvelope) []BudgetEnvelope {
			e[7] = BudgetEnvelope{RelativePath: "F07", Outcome: BudgetPage, RawUsed: 0, Page: page(e[0])}
			e[7].Page.Entries = nil
			e[7].Page.Complete = true
			e[7].Page.NextCursor = ""
			return e
		},
		"page after an error": func(e []BudgetEnvelope) []BudgetEnvelope {
			e[0] = BudgetEnvelope{RelativePath: "F00", Outcome: BudgetError, ErrorCode: "changed"}
			return e
		},
		"page after unstable": func(e []BudgetEnvelope) []BudgetEnvelope { e[0].Page.Unstable = true; return e },
		"skip with a page":    func(e []BudgetEnvelope) []BudgetEnvelope { e[7].Page = page(e[0]); return e },
		"skip with a code":    func(e []BudgetEnvelope) []BudgetEnvelope { e[7].ErrorCode = "changed"; return e },
		"skip with raw names": func(e []BudgetEnvelope) []BudgetEnvelope { e[7].RawUsed = 1; return e },
		"error with a page": func(e []BudgetEnvelope) []BudgetEnvelope {
			e[5] = BudgetEnvelope{RelativePath: "F05", Outcome: BudgetError, ErrorCode: "changed", Page: page(e[0])}
			e[6], e[7] = BudgetEnvelope{RelativePath: "F06", Outcome: BudgetSkipped}, BudgetEnvelope{RelativePath: "F07", Outcome: BudgetSkipped}
			return e
		},
		"unknown error code": func(e []BudgetEnvelope) []BudgetEnvelope {
			e[5] = BudgetEnvelope{RelativePath: "F05", Outcome: BudgetError, ErrorCode: "gone"}
			return e[:6]
		},
		"unknown outcome":     func(e []BudgetEnvelope) []BudgetEnvelope { e[7].Outcome = "later"; return e },
		"negative raw":        func(e []BudgetEnvelope) []BudgetEnvelope { e[0].RawUsed = -1; return e },
		"raw over its limit":  func(e []BudgetEnvelope) []BudgetEnvelope { e[0].RawUsed = 65; return e },
		"sum over the budget": func(e []BudgetEnvelope) []BudgetEnvelope { e[5].RawUsed = 5; return e },
		"entries over raw":    func(e []BudgetEnvelope) []BudgetEnvelope { e[0].RawUsed = 11; return e },
		"complete at its limit": func(e []BudgetEnvelope) []BudgetEnvelope {
			e[5].Page.Complete = true
			e[5].Page.NextCursor = ""
			return e
		},
		"incomplete, no cursor":  func(e []BudgetEnvelope) []BudgetEnvelope { e[5].Page.NextCursor = ""; return e },
		"another root":           func(e []BudgetEnvelope) []BudgetEnvelope { e[0].Page.RootIdentity = "other"; return e },
		"page carrying an error": func(e []BudgetEnvelope) []BudgetEnvelope { e[0].Page.ErrorCode = "changed"; return e },
	} {
		if err := CheckBudgetResponse(requests, 64, mutate(clone())); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
