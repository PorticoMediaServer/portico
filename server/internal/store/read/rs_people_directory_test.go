package read

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func finishRSDirectory(t *testing.T, f *browseFixture, r *RSPeoplePageReader, in RSPeopleDirectoryInput) RSPeopleDirectory {
	t.Helper()
	in.Views = []BrowseInput{f.input}
	in.Order = "title"
	p, ref, err := r.BeginDirectory(t.Context(), 1, time.Now().Add(12*time.Second), 4<<20, in)
	if err != nil || ref != nil {
		t.Fatal(err, ref)
	}
	t.Cleanup(func() { _ = p.Close() })
	for n := 0; n < 80; n++ {
		out, _, err := r.directory(t.Context(), foregroundRequest(), time.Second, f.output, p, f.input.Now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Complete {
			return out
		}
		if !out.Pending || out.Total != 0 || len(out.People) != 0 {
			t.Fatal("capped/incomplete directory presented as exact", out)
		}
	}
	t.Fatal("directory failed to terminate")
	return RSPeopleDirectory{}
}
func TestRSPeopleDirectoryExactCountsOrderAndOrphans(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, false)
	for _, desc := range []bool{false, true} {
		out := finishRSDirectory(t, f, r, RSPeopleDirectoryInput{Limit: 1, Descending: desc})
		if out.Total != 2 || len(out.People) != 1 || out.Next == nil {
			t.Fatal(out)
		}
		first, second := int64(3), int64(1)
		if desc {
			first, second = second, first
		}
		if out.People[0].ID != first {
			t.Fatal(out)
		}
		out = finishRSDirectory(t, f, r, RSPeopleDirectoryInput{Limit: 1, Descending: desc, After: out.Next})
		if out.Total != 2 || out.People[0].ID != second || out.Next != nil {
			t.Fatal("bad continuation", out)
		}
		if second == 1 && (out.People[0].CreditCount != 9 || len(out.People[0].Roles) != 2 || out.People[0].Portrait == nil) {
			t.Fatal("raw obsolete or missing roles", out)
		}
	}
	for _, in := range []RSPeopleDirectoryInput{{Query: "ada", Order: "title", Limit: 40}, {Order: "relevance", Limit: 40}} {
		_, _, err := r.BeginDirectory(t.Context(), 1, time.Now().Add(12*time.Second), 4<<20, in)
		if !errors.Is(err, ErrUnqualified) {
			t.Fatal("native search dependency silently replaced", err)
		}
	}
}
func TestRSPeopleDirectoryLIVEAndZeroAfterRevocation(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, true)
	p, ref, err := r.BeginDirectory(t.Context(), 1, time.Now().Add(12*time.Second), 4<<20, RSPeopleDirectoryInput{Views: []BrowseInput{f.input}, Order: "title", Limit: 40})
	if err != nil || ref != nil {
		t.Fatal(err, ref)
	}
	defer p.Close()
	q, request, output := liveRequests("directory-revoke")
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		v, _, e := r.Directory(ctx, request, time.Second, out, p)
		if e != nil || !v.Pending || len(v.People) != 0 {
			t.Fatal(v, e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
	q, request, output = liveRequests("directory-revoked")
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		v, _, e := r.Directory(ctx, request, time.Second, out, p)
		if len(v.People) != 0 || v.Total != 0 || v.Complete || !errors.Is(e, ErrDenied) {
			t.Fatal(v, e)
		}
		return e
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if usage := f.authority.Usage()[governor.Foreground]; usage[governor.MemoryBytes] != 0 {
		t.Fatal("retained preparation after terminal error", usage)
	}
}
