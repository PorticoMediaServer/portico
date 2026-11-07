package scan

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// Value-only W method substitute: no Scope, native owner or writer authority.
type ownedObservation struct {
	page           nativeinventory.Page
	err            error
	observed, shut int
}

func (p *ownedObservation) ObserveInventory(context.Context) (nativeinventory.Page, error) {
	p.observed++
	return p.page, p.err
}
func (p *ownedObservation) Close(context.Context) error { p.shut++; return nil }

func TestObserveN1UsesOwnedRawValuesAndLeavesCustodyWithW(t *testing.T) {
	identity, input, filename := n1Values("/media", "root", "directory", "directory-revision")
	turn := n1Turn()
	want, err := BindN1(identity, input, turn, filename)
	if err != nil {
		t.Fatal(err)
	}
	p := &ownedObservation{page: turn.Page}
	got, err := ObserveN1(t.Context(), p, identity, input, filename)
	if err != nil || got.Reference() != want.Reference() || p.observed != 1 || p.shut != 0 {
		t.Fatal("owned observation changed ORIGINAL or released W custody", err)
	}
	p.page.Entries[0].Path[0] = 'X'
	ref, err := writer.InventoryAcceptReference(got.Command())
	if err != nil || ref != want.Reference() {
		t.Fatal("owned output mutation changed frozen ORIGINAL", err)
	}
}

func TestObserveN1ErrorsAndRawPolicyRefusalNeverReleaseOrAdvance(t *testing.T) {
	identity, input, filename := n1Values("/media", "root", "directory", "directory-revision")
	for _, tc := range []struct {
		name string
		edit func(*ownedObservation)
		want error
	}{
		{"error with nonzero output", func(p *ownedObservation) { p.err = context.Canceled }, context.Canceled},
		{"raw invalid byte", func(p *ownedObservation) {
			p.page.Entries[0].Name = []byte("Film-\xff.mkv")
			p.page.Entries[0].Path = []byte("/media/Film-\xff.mkv")
		}, writer.ErrFilenameUnrepresentable},
		{"EOF", func(p *ownedObservation) { p.page.Complete = true; p.page.NextCursor = "" }, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &ownedObservation{page: n1Turn().Page}
			tc.edit(p)
			got, err := ObserveN1(t.Context(), p, identity, input, filename)
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, PreparedN1{}) || p.observed != 1 || p.shut != 0 || input.Directory.ExpectedCursor != "" {
				t.Fatal("refusal manufactured a command, progress or physical release", got, err)
			}
		})
	}
}
