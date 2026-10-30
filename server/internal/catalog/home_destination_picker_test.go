package catalog

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"testing"
)

func TestHomeDestinationPickerSearchesBeyondFirstWindowAndFencesCursor(t *testing.T) {
	service, _, _, viewer, _, fixture, _ := bulkFixture(t, 1)
	owner := ResourceActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	for index := 0; index < 42; index++ {
		name := fmt.Sprintf("List %02d", index)
		if _, err := service.MutatePlaylist(owner, "", "create", "", PlaylistMutation{OperationID: fmt.Sprintf("picker-create-%d", index), Name: &name}, nil); err != nil {
			t.Fatal(err)
		}
	}
	gated, err := dbwork.BeginSnapshot(context.Background(), fixture.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer gated.Rollback()
	first, err := service.HomeDestinationPickerTx(context.Background(), gated.Tx(), "server", viewer, owner, HomeDestinationPickerRequest{Kind: "playlist", Query: "", Limit: 40})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 40 || first.NextCursor == "" {
		t.Fatalf("window not bounded: %d %q", len(first.Rows), first.NextCursor)
	}
	search, err := service.HomeDestinationPickerTx(context.Background(), gated.Tx(), "server", viewer, owner, HomeDestinationPickerRequest{Kind: "playlist", Query: "List 41", Limit: 40})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Rows) != 1 || search.Rows[0].Name != "List 41" || !search.Rows[0].CanAdd {
		t.Fatalf("server query missed later destination: %+v", search)
	}
	if _, err = service.HomeDestinationPickerTx(context.Background(), gated.Tx(), "server", viewer, owner, HomeDestinationPickerRequest{Kind: "playlist", Query: "List 41", Limit: 40, Cursor: first.NextCursor}); err == nil {
		t.Fatal("query-swapped cursor accepted")
	}
	stranger := ResourceActor{Authority: "local", AccountID: "stranger", ProfileID: "profile"}
	if _, err = service.HomeDestinationPickerTx(context.Background(), gated.Tx(), "server", viewer, stranger, HomeDestinationPickerRequest{Kind: "playlist", Query: "", Limit: 40, Cursor: first.NextCursor}); err == nil {
		t.Fatal("foreign actor cursor accepted")
	}
	empty, err := service.HomeDestinationPickerTx(context.Background(), gated.Tx(), "server", viewer, stranger, HomeDestinationPickerRequest{Kind: "playlist", Query: "", Limit: 40})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Rows) != 0 {
		t.Fatal("private destination leaked")
	}
}
