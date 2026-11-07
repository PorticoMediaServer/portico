package read

import (
	"strings"
	"testing"
)

func newRSSavedCollectionSource(t *testing.T) *browseFixture {
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO saved_resource_generation(id,revision) VALUES(1,1)`)
	f.exec(t, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES('saved','collection','ignored','local','a','p','Saved','2026-11-07')`)
	return f
}
func TestRSSavedCollectionAuthorityOracle(t *testing.T) {
	f := newRSSavedCollectionSource(t)
	check := func() {
		t.Helper()
		var want int64
		err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM saved_resources r WHERE r.id='saved' AND r.kind='collection' AND r.deleted=0 AND ((r.owner_authority='local' AND r.owner_account='a' AND r.owner_profile='p') OR r.visibility='server' OR EXISTS(SELECT 1 FROM saved_resource_shares s WHERE s.resource_id=r.id AND s.authority='local' AND s.account_id='a' AND s.profile_id='p'))`).Scan(&want)
		if err != nil {
			t.Fatal(err)
		}
		present := false
		for _, query := range []string{rsSavedCollectionSourceSQL, rsSavedCollectionShareSQL, rsSavedCollectionServerSQL} {
			args := []any{"token", "saved"}
			if query == rsSavedCollectionServerSQL {
				args = []any{"saved"}
			}
			rows, err := f.conn.QueryContext(t.Context(), query, args...)
			if err != nil {
				t.Fatal(err)
			}
			present = rows.Next() || present
			rows.Close()
		}
		if present != (want == 1) {
			t.Fatalf("oracle mismatch %v want %d", present, want)
		}
	}
	check()
	for _, sql := range []string{`UPDATE saved_resources SET owner_account='other'`, `INSERT INTO saved_resource_shares VALUES('saved','local','a','p','viewer')`, `DELETE FROM saved_resource_shares`, `UPDATE saved_resources SET visibility='server'`, `UPDATE saved_resources SET owner_account='a',kind='view'`, `UPDATE saved_resources SET kind='collection',deleted=1`} {
		f.exec(t, sql)
		check()
	}
}
func TestRSSavedCollectionActualOctets(t *testing.T) {
	f := newRSSavedCollectionSource(t)
	name := "雪\x00é"
	summary := strings.Repeat("界", 32768)
	f.exec(t, `UPDATE saved_resources SET name=?,summary=? WHERE id='saved'`, name, summary)
	var n, s, c int64
	if err := f.conn.QueryRowContext(t.Context(), rsSavedCollectionOctetsSQL, "saved").Scan(&n, &s, &c); err != nil {
		t.Fatal(err)
	}
	if n != int64(len(name)) || s != int64(len(summary)) || c != 10 {
		t.Fatalf("actual UTF8/NUL bytes %d %d %d", n, s, c)
	}
}
