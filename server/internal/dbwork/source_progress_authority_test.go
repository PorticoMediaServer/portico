package dbwork

import "testing"

// TestLibrarySourceProgressIsNotAuthority pins the parser decision the split
// relies on: the private progress table is absent from the authority list, its
// exact writer is not an authority write, and a real source statement still is.
func TestLibrarySourceProgressIsNotAuthority(t *testing.T) {
	for _, table := range authorityTables {
		if table == "library_source_progress" {
			t.Fatal("library_source_progress is in authorityTables")
		}
	}
	const progress = `INSERT INTO library_source_progress(source_id,last_progress_at) SELECT id,? FROM library_sources WHERE id=? ON CONFLICT(source_id) DO UPDATE SET last_progress_at=excluded.last_progress_at`
	if authorityWrite(progress) {
		t.Fatal("the private progress upsert is read as an authority write")
	}
	if !authorityWrite(`UPDATE library_sources SET health='healthy' WHERE id=?`) {
		t.Fatal("a real source health statement is no longer an authority write")
	}
}
