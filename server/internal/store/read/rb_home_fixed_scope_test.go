package read

import "testing"

// Reading libraries are outside New on Server's video source universe.
func TestRBHomeFixedReadingScope(t *testing.T) {
	homeFixedCase(t, "reading_excluded_from_video", func(t *testing.T) {
		f, r := homeFixedFixture(t, func(f *browseFixture) { f.exec(t, `UPDATE libraries SET kind='book' WHERE id='movies'`) })
		p, c, e := homeFixedRead(t, f, r, "new_on_server")
		if e != nil || p.Pending || !p.Exact || !p.Exhausted || len(p.Items) != 0 || p.RawPositions != 0 {
			t.Fatal(p, c, e)
		}
	})
}
