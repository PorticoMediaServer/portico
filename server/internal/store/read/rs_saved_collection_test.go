package read

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRSSavedMembersBeginGuards(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	deadline := time.Now().Add(12 * time.Second)
	for _, c := range []struct {
		name string
		in   SavedCollectionInput
	}{
		{"empty-resource", SavedCollectionInput{View: f.input}},
		{"long-resource", SavedCollectionInput{View: f.input, Resource: strings.Repeat("x", 257)}},
		{"zero-limit", SavedCollectionInput{View: func() BrowseInput { v := f.input; v.Limit = 0; return v }(), Resource: "saved"}},
		{"descending", SavedCollectionInput{View: func() BrowseInput { v := f.input; v.Descending = true; return v }(), Resource: "saved"}},
		{"keep-articles", SavedCollectionInput{View: func() BrowseInput { v := f.input; v.KeepArticles = true; return v }(), Resource: "saved"}},
		{"short-lease", SavedCollectionInput{View: f.input, Resource: "saved"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			bytes := int64(8 << 20)
			if c.name == "short-lease" {
				bytes = 1024
			}
			p, _, e := r.BeginMembers(t.Context(), f.authority, 1, deadline, bytes, c.in)
			if !errors.Is(e, ErrInput) || p != nil {
				t.Fatalf("guard %s: %v", c.name, e)
			}
		})
	}
	p, ref, e := r.BeginMembers(t.Context(), f.authority, 1, deadline, 8<<20, SavedCollectionInput{View: f.input, Resource: "saved"})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	if e = p.Close(); e != nil {
		t.Fatal(e)
	}
	if e = p.Close(); e != nil {
		t.Fatal("close is not idempotent", e)
	}
	if e = savedMembersError(t, f, r, SavedCollectionInput{View: f.input, Resource: "saved", After: &SavedCollectionCursor{}}); !errors.Is(e, ErrStale) {
		t.Fatal("Members accepted a foreign cursor", e)
	}
	viewAfter := f.input
	viewAfter.After = &BrowseCursor{}
	if e = savedMembersError(t, f, r, SavedCollectionInput{View: viewAfter, Resource: "saved"}); !errors.Is(e, ErrInput) {
		t.Fatal("Members accepted a view cursor", e)
	}
}
