package access

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

func TestPersonModesUseLiveHardCeilingAndSamePagedFacts(t *testing.T) {
	store, db, _ := fixture(t)
	ctx := context.Background()
	if _, e := db.Exec(`UPDATE playback_owner_policy SET per_account_cap=1 WHERE singleton=1;
 INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms) VALUES('playing','tv','member','another-profile','local','key','digest','channel','member','playing','{}',1,1,?,0,0)`, time.Now().Add(time.Minute).UnixMilli()); e != nil {
		t.Fatal(e)
	}
	enforcer := &Enforcer{Store: store}
	member := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "member-profile", Role: "member"}}
	for i, mode := range []streamlimits.Mode{streamlimits.Inherit, streamlimits.Unlimited, streamlimits.Limit} {
		doc, e := store.Limits(ctx, nil, "member")
		if e != nil {
			t.Fatal(e)
		}
		doc.Limits.MaxStreamsMode = mode
		doc.Limits.MaxStreams = 0
		if mode == streamlimits.Limit {
			doc.Limits.MaxStreams = 5
		}
		out, e := store.SetLimits(ctx, nil, "member", LimitsChange{ExpectedRevision: doc.Revision, OperationID: []string{"mode-inherit-0001", "mode-unlimited-0001", "mode-limit-0001"}[i], Limits: doc.Limits})
		if e != nil {
			t.Fatal(e)
		}
		if out.ServerMaxStreams == nil || *out.ServerMaxStreams != 1 || out.EffectiveMaxStreams == nil || *out.EffectiveMaxStreams != 1 {
			t.Fatalf("ceiling facts %+v", out)
		}
		page, e := store.Members(ctx, nil, MemberQuery{Search: "member"})
		if e != nil || len(page.Items) != 1 {
			t.Fatalf("page %+v %v", page, e)
		}
		row := page.Items[0]
		if row.MaxStreamsMode != mode || row.LimitsRevision != out.Revision || row.EffectiveMaxStreams == nil || *row.EffectiveMaxStreams != 1 {
			t.Fatalf("page/detail disagree: %+v / %+v", row, out)
		}
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		_, e = enforcer.AdmitPlayback(ctx, tx, member, Admission{})
		tx.Rollback()
		if !errors.Is(e, ErrStreamLimit) {
			t.Fatalf("%s bypassed hard ceiling: %v", mode, e)
		}
	}
	// Lowering the ceiling takes effect without rewriting a person's mode or
	// revision. Removing it still leaves an explicit person cap in force.
	if _, e := db.Exec(`UPDATE playback_owner_policy SET per_account_cap=NULL WHERE singleton=1`); e != nil {
		t.Fatal(e)
	}
	doc, e := store.Limits(ctx, nil, "member")
	if e != nil || doc.Limits.MaxStreamsMode != streamlimits.Limit || doc.EffectiveMaxStreams == nil || *doc.EffectiveMaxStreams != 5 {
		t.Fatalf("person cap lost %+v %v", doc, e)
	}
}

func TestBulkStreamModeValidationPrecedesEveryMutation(t *testing.T) {
	store, db, _ := fixture(t)
	ctx := context.Background()
	issuer, e := identity.New(db, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('second','second',x'','second-main')`); e != nil {
		t.Fatal(e)
	}
	mode := streamlimits.Unlimited
	positive := 3
	q := MemberBulkRequest{OperationID: "stream-bulk-invalid-0001", Action: "access", Targets: []MemberBulkTarget{{"member", 1, 1}, {"second", 1, 1}}, Access: &MemberAccessPatch{MaxStreamsMode: &mode, MaxStreams: &positive}}
	if _, e = store.BulkMembers(ctx, nil, issuer, owner(), q); !errors.Is(e, ErrInvalid) {
		t.Fatalf("contradictory modes accepted: %v", e)
	}
	q.Access.MaxStreams = nil
	q.Targets[1].ExpectedLimitsRevision = 0
	if _, e = store.BulkMembers(ctx, nil, issuer, owner(), q); !errors.Is(e, ErrInvalid) {
		t.Fatalf("missing limits revision accepted: %v", e)
	}
	var changed int
	if e = db.QueryRow(`SELECT (SELECT count(*) FROM account_stream_limits)+(SELECT count(*) FROM access_limits)+(SELECT count(*) FROM access_receipts)`).Scan(&changed); e != nil || changed != 0 {
		t.Fatalf("partial invalid bulk: %d %v", changed, e)
	}
	q.Targets[1].ExpectedLimitsRevision = 1
	q.OperationID = "stream-bulk-valid-0001"
	if _, e = store.BulkMembers(ctx, nil, issuer, owner(), q); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM account_stream_limits WHERE mode='unlimited' AND stream_limit IS NULL`).Scan(&changed); e != nil || changed != 2 {
		t.Fatalf("one-transaction bulk modes: %d %v", changed, e)
	}
	// A live transaction reads exactly the normalized authority used to write.
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	out, e := streamlimits.ReadTx(ctx, tx, "member")
	if e != nil || out.Mode != streamlimits.Unlimited {
		t.Fatalf("normalized read %+v %v", out, e)
	}
}
