package catalog

import "testing"

// rowTotal is a row's total, or -1 when the row carries none.
func rowTotal(r HomeRow) int {
	if r.Total == nil {
		return -1
	}
	return *r.Total
}

// New on server knows the exact count of its bounded shelf. A preview and
// See all carry the same total without counting the whole library.
func TestHomeDocumentRecentRowCarriesItsBoundedTotal(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest()
	r.Limit = 2
	r.RowOrder = []string{"new_on_server", "continue"}
	doc, err := s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	var recent *HomeRow
	for i := range doc.Rows {
		if doc.Rows[i].ID == "new_on_server" {
			recent = &doc.Rows[i]
		}
	}
	if recent == nil || recent.Total == nil || !recent.HasMore || recent.NextCursor == "" || len(recent.Entries) != 2 {
		t.Fatalf("Home lost the bounded total or its continuation: %+v", recent)
	}
	row, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 2})
	if err != nil || row.Total == nil || *row.Total <= 2 || !row.HasMore {
		t.Fatalf("the row request did not carry its exact total: %+v %v", row, err)
	}
	whole, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 60})
	if err != nil || whole.Total == nil || *whole.Total != len(whole.Entries) || *whole.Total != *row.Total {
		t.Fatalf("the row's total is not its length: %+v %v", whole, err)
	}
	r.Limit = 60
	if doc, err = s.HomeRows(r); err != nil {
		t.Fatal(err)
	}
	for _, got := range doc.Rows {
		if got.ID == "new_on_server" && (got.Total == nil || *got.Total != *row.Total || got.HasMore) {
			t.Fatalf("a row that fits its page lost the total it read: %+v", got)
		}
	}
}
