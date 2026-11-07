package writer

import (
	"database/sql"
	"testing"
)

func TestUOJobLeaseClaimRenewAndStaleWorker(t *testing.T) {
	old := uoJobLease{nextMS: sql.NullInt64{Int64: 100, Valid: true}, revision: 1}
	claimed, err := old.transition(uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 7, durationMS: 1000})
	if err != nil || claimed.owner != 7 || claimed.generation != 1 || claimed.expiresMS != 1100 || claimed.attempts != 1 {
		t.Fatal(claimed, err)
	}
	renewed, err := claimed.transition(uoLeaseChange{action: uoLeaseRenew, nowMS: 200, owner: 7, generation: 1, durationMS: 2000})
	if err != nil || renewed.expiresMS != 2200 {
		t.Fatal(renewed, err)
	}
	if _, err := renewed.transition(uoLeaseChange{action: uoLeaseFinish, nowMS: 300, owner: 8, generation: 1}); err == nil {
		t.Fatal("foreign worker completed lease")
	}
	if _, err := renewed.transition(uoLeaseChange{action: uoLeaseFinish, nowMS: 300, owner: 7, generation: 2}); err == nil {
		t.Fatal("successor worker completed lease")
	}
	finished, err := renewed.transition(uoLeaseChange{action: uoLeaseFinish, nowMS: 300, owner: 7, generation: 1})
	if err != nil || finished.owner != 0 || finished.expiresMS != 0 || finished.nextMS.Valid {
		t.Fatal(finished, err)
	}
	if _, err := finished.transition(uoLeaseChange{action: uoLeaseClaim, nowMS: 400, owner: 9, durationMS: 100}); err == nil {
		t.Fatal("held job became runnable")
	}
}
func TestUOJobLeaseExpiryAndRetry(t *testing.T) {
	old := uoJobLease{owner: 1, generation: 2, expiresMS: 100, attempts: 3, revision: 4, nextMS: sql.NullInt64{Int64: 10, Valid: true}}
	if _, err := old.transition(uoLeaseChange{action: uoLeaseRenew, nowMS: 100, owner: 1, generation: 2, durationMS: 100}); err == nil {
		t.Fatal("expired renewal")
	}
	next, err := old.transition(uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 2, durationMS: 100})
	if err != nil || next.generation != 3 || next.attempts != 4 {
		t.Fatal(next, err)
	}
	retry, err := next.transition(uoLeaseChange{action: uoLeaseFinish, nowMS: 150, owner: 2, generation: 3, nextMS: sql.NullInt64{Int64: 500, Valid: true}})
	if err != nil || retry.nextMS.Int64 != 500 || retry.owner != 0 {
		t.Fatal(retry, err)
	}
	if _, err := retry.transition(uoLeaseChange{action: uoLeaseClaim, nowMS: 499, owner: 3, durationMS: 100}); err == nil {
		t.Fatal("retry became due early")
	}
}
