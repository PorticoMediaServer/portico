package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"testing"
)

func TestPeopleHTTPTableAndAtomicBulk(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	for i := 0; i < 120; i++ {
		id := fmt.Sprintf("paged-%03d", i)
		if _, e := f.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,X'00',?)`, id, id, id+"-profile"); e != nil {
			t.Fatal(e)
		}
	}
	w := f.raw("GET", "/v1/admin/access/members?q=paged&sort=person&direction=asc&limit=31", f.owner.AccessToken, nil, nil)
	var page access.MemberPage
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || w.Code != 200 || page.MatchedCount != 120 || len(page.Items) != 31 || page.NextCursor == "" {
		t.Fatalf("page %d %s %v", w.Code, w.Body.String(), e)
	}
	req := access.MemberBulkRequest{OperationID: "http-bulk-0001", Action: "disable", Targets: []access.MemberBulkTarget{{AccountID: "paged-001", ExpectedRevision: 1}, {AccountID: "paged-002", ExpectedRevision: 999}}}
	w = f.raw("POST", "/v1/admin/access/members/bulk", f.owner.AccessToken, nil, req)
	if w.Code != 409 {
		t.Fatalf("stale %d %s", w.Code, w.Body.String())
	}
	var off bool
	if e := f.db.QueryRow(`SELECT disabled FROM direct_memberships WHERE account_id='paged-001'`).Scan(&off); e != nil || off {
		t.Fatal("partial", off, e)
	}
	req.Targets[1].ExpectedRevision = 1
	w = f.raw("POST", "/v1/admin/access/members/bulk", f.owner.AccessToken, nil, req)
	if w.Code != 200 {
		t.Fatalf("bulk %d %s", w.Code, w.Body.String())
	}
	w = f.raw("GET", "/v1/admin/access-tokens", f.owner.AccessToken, nil, nil)
	if w.Code != 404 {
		t.Fatalf("owner token inventory survived %d %s", w.Code, w.Body.String())
	}
}
