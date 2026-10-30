package access

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
)

func TestQualityDefaultsR25PersonInheritsServer(t *testing.T) {
	store, db, _ := fixture(t)
	ctx := context.Background()
	doc, err := store.Limits(ctx, nil, "member")
	if err != nil || doc.Limits.RemoteBitrateKbps != 0 {
		t.Fatal(doc, err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM access_limits WHERE account_id='member'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	limits := DefaultLimits()
	limits.RemoteBitrateKbps = 2000
	if _, err = store.SetLimits(ctx, nil, "member", LimitsChange{ExpectedRevision: doc.Revision, OperationID: "explicit-cap", Limits: limits}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	p := identity.Principal{Viewer: identity.Viewer{AccountID: "member"}}
	if cap, err := (&Enforcer{Store: store}).RemoteBitrateCap(ctx, tx, p); err != nil || cap != 2000000 {
		t.Fatal(cap, err)
	}
}
