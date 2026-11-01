package httpapi

import (
	"strings"
	"testing"
	"time"
)

// The owner's remote-security summary and its one action are administrator
// routes: a member is refused, an anonymous caller is refused, and the owner
// reads the blocks in force and can clear one.
func TestAdminSecuritySummaryAndUnblockAreAdministratorsOnly(t *testing.T) {
	f := newV1Fixture(t, 0)
	member, _ := f.member()

	if w := f.raw("GET", "/v1/admin/security/summary", "", nil, nil); w.Code != 401 {
		t.Fatalf("anonymous summary: %d %s", w.Code, w.Body.String())
	}
	if w := f.raw("GET", "/v1/admin/security/summary", member, nil, nil); w.Code != 403 {
		t.Fatalf("member summary: %d %s", w.Code, w.Body.String())
	}
	if w := f.raw("POST", "/v1/admin/security/blocks/x/unblock", member, nil, nil); w.Code != 403 {
		t.Fatalf("member unblock: %d %s", w.Code, w.Body.String())
	}

	// A block in force, as the sign-in ledger records one.
	id := strings.Repeat("b", 43)
	until := time.Now().Add(10 * time.Minute).Unix()
	last := time.Now().Add(-30 * time.Second).Unix()
	if _, err := f.db.Exec(`INSERT INTO identity_source_attempts(source_bucket,source_id,failed_attempts,last_failure,blocked_until,block_count) VALUES('203.0.113.9',?,30,?,?,2)`, id, last, until); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO identity_security_counts(hour,kind,count) VALUES(?,?,1),(?,?,3)`, time.Now().Unix()/3600, "blocked_source", time.Now().Unix()/3600, "failed_sign_in"); err != nil {
		t.Fatal(err)
	}

	var summary struct {
		Counts struct {
			Last24Hours struct {
				FailedSignIns  int64 `json:"failedSignIns"`
				BlockedSources int64 `json:"blockedSources"`
			} `json:"last24Hours"`
			Last7Days struct {
				FailedSignIns int64 `json:"failedSignIns"`
			} `json:"last7Days"`
		} `json:"counts"`
		Blocks []struct {
			ID           string `json:"id"`
			Network      string `json:"network"`
			BlockCount   int64  `json:"blockCount"`
			BlockedUntil string `json:"blockedUntil"`
		} `json:"blocks"`
	}
	f.call("GET", "/v1/admin/security/summary", nil, nil, 200, &summary)
	if len(summary.Blocks) != 1 || summary.Blocks[0].ID != id || summary.Blocks[0].Network != "203.0.113.0/24" || summary.Blocks[0].BlockCount != 2 {
		t.Fatalf("summary blocks: %+v", summary.Blocks)
	}
	if summary.Counts.Last24Hours.FailedSignIns < 3 || summary.Counts.Last24Hours.BlockedSources < 1 || summary.Counts.Last7Days.FailedSignIns < 3 {
		t.Fatalf("summary counts: %+v", summary.Counts)
	}

	f.call("POST", "/v1/admin/security/blocks/"+id+"/unblock", nil, nil, 204, nil)
	var remaining int
	if err := f.db.QueryRow(`SELECT count(*) FROM identity_source_attempts WHERE source_id=?`, id).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("unblock left the row: %d %v", remaining, err)
	}
	if w := f.raw("POST", "/v1/admin/security/blocks/"+id+"/unblock", f.owner.AccessToken, nil, nil); w.Code != 404 || v1Code(w) != "not_found" {
		t.Fatalf("second unblock: %d %s", w.Code, w.Body.String())
	}
	f.call("GET", "/v1/admin/security/summary", nil, nil, 200, &summary)
	if len(summary.Blocks) != 0 {
		t.Fatalf("summary still lists %+v", summary.Blocks)
	}
}

// A blocked source is refused on the sign-in route before any account lookup,
// as a 429 with the exact wait.
func TestBlockedSourceAnswersSourceBlockedWithRetryAfter(t *testing.T) {
	f := newV1Fixture(t, 0)
	until := time.Now().Add(5 * time.Minute).Unix()
	if _, err := f.db.Exec(`INSERT INTO identity_source_attempts(source_bucket,source_id,failed_attempts,last_failure,blocked_until,block_count) VALUES('192.0.2.1',?,30,?,?,1)`, strings.Repeat("c", 43), time.Now().Unix(), until); err != nil {
		t.Fatal(err)
	}
	w := f.raw("POST", "/v1/direct/sign-in", "", nil, map[string]string{"username": "owner", "password": "long-test-password"})
	if w.Code != 429 || v1Code(w) != "source_blocked" {
		t.Fatalf("blocked sign-in: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("a blocked source got no Retry-After")
	}
}

// An invitation code is a credential that creates an account, so a guess at it
// is charged to the source like a password, and a blocked source is refused
// before the code is read.
func TestInvitationCodeGuessesChargeTheSource(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	code := strings.Repeat("a", 43)
	w := f.raw("POST", "/v1/access/invitations/preview", "", nil, map[string]string{"code": code})
	if w.Code != 404 || v1Code(w) != "invitation_not_found" {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var attempts int
	if err := f.db.QueryRow(`SELECT failed_attempts FROM identity_source_attempts WHERE source_bucket='192.0.2.1'`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("preview guess charged %d: %v", attempts, err)
	}
	w = f.raw("POST", "/v1/access/invitations/accept", "", nil, map[string]string{"code": code, "username": "newcomer", "password": "long-password-1"})
	if w.Code != 410 || v1Code(w) != "invitation_expired" {
		t.Fatalf("accept: %d %s", w.Code, w.Body.String())
	}
	if err := f.db.QueryRow(`SELECT failed_attempts FROM identity_source_attempts WHERE source_bucket='192.0.2.1'`).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("accept guess charged %d: %v", attempts, err)
	}
	// A blocked source does not get as far as reading a code.
	if _, err := f.db.Exec(`UPDATE identity_source_attempts SET blocked_until=? WHERE source_bucket='192.0.2.1'`, time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	w = f.raw("POST", "/v1/access/invitations/preview", "", nil, map[string]string{"code": code})
	if w.Code != 429 || v1Code(w) != "source_blocked" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked preview: %d %s", w.Code, w.Body.String())
	}
}
