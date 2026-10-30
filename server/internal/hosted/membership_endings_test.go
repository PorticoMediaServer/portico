package hosted

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFullMemberPublicationRetainsExplicitEndingsWithoutInferringACause(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	const account = "acc_removed"
	incarnation := strings.Repeat("a", 32)
	var sequence int64
	if err := f.db.QueryRow(`INSERT INTO hosted_membership_journal(hosted_account_id) VALUES(?) RETURNING sequence`, account).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO hosted_membership_endings(hosted_account_id,incarnation,sequence,cause,ended_ms,expires_ms) VALUES(?,?,?,'owner_removed',?,?)`, account, incarnation, sequence, now, now+2592000000); err != nil {
		t.Fatal(err)
	}
	find := func(push MemberPush) *MemberStanding {
		for i := range push.Members {
			if push.Members[i].AccountID == account {
				return &push.Members[i]
			}
		}
		return nil
	}
	for _, q := range []memberSync{{full: true, head: sequence}, {acked: sequence - 1, head: sequence}} {
		push, err := f.service.buildMemberPush(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		member := find(push)
		if member == nil || member.Active || member.MembershipID != incarnation || member.Ending == nil || member.Ending.Sequence != sequence || member.Ending.Cause != "owner_removed" || push.Through != sequence {
			t.Fatal("explicit ending was lost from the sequenced publication")
		}
	}
	// An acknowledged/pruned journal does not erase the full-recovery tombstone.
	if _, err := f.db.Exec(`DELETE FROM hosted_membership_journal WHERE sequence<=?`, sequence); err != nil {
		t.Fatal(err)
	}
	push, err := f.service.buildMemberPush(ctx, memberSync{full: true, head: sequence})
	if err != nil || find(push) == nil {
		t.Fatal("lost delta could not be reconciled", err)
	}
	// A later transition must never be attached to an older full snapshot.
	var later int64
	if err = f.db.QueryRow(`INSERT INTO hosted_membership_journal(hosted_account_id) VALUES(?) RETURNING sequence`, account).Scan(&later); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE hosted_membership_endings SET sequence=?,cause='' WHERE hosted_account_id=?`, later, account); err != nil {
		t.Fatal(err)
	}
	push, err = f.service.buildMemberPush(ctx, memberSync{full: true, head: sequence})
	if err != nil || find(push) != nil {
		t.Fatal("future transition entered an older publication", err)
	}
	push, err = f.service.buildMemberPush(ctx, memberSync{full: true, head: later})
	if err != nil {
		t.Fatal(err)
	}
	member := find(push)
	if member == nil || member.MembershipID != incarnation || member.Ending != nil {
		t.Fatal("neutral standing manufactured a cause or lost its incarnation")
	}
	expired := now - 31*24*60*60*1000
	if _, err = f.db.Exec(`UPDATE hosted_membership_endings SET ended_ms=?,expires_ms=? WHERE hosted_account_id=?`, expired, expired+2592000000, account); err != nil {
		t.Fatal(err)
	}
	push, err = f.service.buildMemberPush(ctx, memberSync{full: true, head: later})
	if err != nil || find(push) != nil {
		t.Fatal("expired ending was republished", err)
	}
}
