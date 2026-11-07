package read

import "testing"

// An uncomposed library family cannot become a successful empty recent shelf.
func TestRBHomeFixedReadingScope(t *testing.T) {
	homeFixedCase(t, "reading_recent_pending", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) { f.exec(t, `UPDATE libraries SET kind='book' WHERE id='movies'`) })
		p, c, e := homeFixedRead(t, f, r, "new_on_server")
		if e != nil || !p.Pending || p.Exact || p.Exhausted || p.PendingReason != "recent_library_policy_uncomposed" {
			t.Fatal(p, c, e)
		}
	})
}
