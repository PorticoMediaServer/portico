package eventfeed

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestCapacityHintsRequireExactAdmittedAudienceAndEmptyMetadata(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "mine", ProfileID: "one"}}
	own := apievents.CapacityAudience(p.Authority, p.AccountID)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ audience, kind, resource string }{{own, "capacity.changed", ""}, {apievents.CapacityAudience("local", "other"), "capacity.changed", ""}, {"server", "capacity.changed", ""}, {own, "session.updated", ""}, {own, "capacity.changed", "private-id"}} {
		if err = apievents.Append(tx, row.audience, row.kind, "", row.resource, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	h := New(db)
	for _, admin := range []bool{false, true} {
		page, err := h.readAt(context.Background(), p, admin, 0, true)
		if err != nil || len(page.Events) != 0 {
			t.Fatal("ordinary/admin scope received capacity hint", page, err)
		}
	}
	ctx := WithCapacityRecipient(context.Background(), func(_ context.Context, _ *sql.Tx, live identity.Principal) error {
		if live.Viewer != p.Viewer {
			return identity.ErrUnauthorized
		}
		return nil
	})
	page, err := h.readAt(ctx, p, false, 0, true)
	if err != nil || len(page.Events) != 1 || page.Events[0].Type != "capacity.changed" || page.Events[0].Resource != (Resource{}) || page.Events[0].Revision != "" || len(page.Events[0].Data) != 0 {
		t.Fatal("capacity projection", page, err)
	}
	retired := errors.New("original recipient retired")
	stale := WithCapacityRecipient(context.Background(), func(context.Context, *sql.Tx, identity.Principal) error { return retired })
	if _, err = h.readAt(stale, p, false, 0, true); !errors.Is(err, retired) {
		t.Fatal("queued reader adopted successor admission", err)
	}
	ordinary, closeOrdinary := h.SubscribeFor(p, false)
	defer closeOrdinary()
	admitted, closeAdmitted := h.SubscribeForContext(ctx, p, false)
	defer closeAdmitted()
	h.wakeAudiences(map[string]bool{own: true})
	select {
	case <-ordinary:
		t.Fatal("unadmitted reader woke for private account capacity")
	default:
	}
	select {
	case <-admitted:
	default:
		t.Fatal("admitted reader missed own account capacity")
	}
}
