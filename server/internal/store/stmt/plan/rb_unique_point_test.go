package plan

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestRBUniquePointKeepsClosedCompleteKeyBoundary(t *testing.T) {
	d := stmt.Definition{ID: "read.browse.rb.channel.check"}
	a := AutoIndexAudit{Binding: stmt.AutoIndexBinding{Columns: []stmt.IndexColumn{{Name: "library_id"}, {Name: "id"}}}}
	point := []keyCondition{{"library_id", "="}, {"id", "="}, {"rowid", "="}}
	if !rbAdditionalRowIDPoint(d, a, point) {
		t.Fatal("complete unique point with extra equality refused")
	}
	for _, bad := range [][]keyCondition{
		{{"library_id", "="}, {"rowid", "="}},
		{{"library_id", "="}, {"id", ">"}, {"rowid", "="}},
		{{"library_id", "="}, {"id", "="}, {"rowid", ">"}},
		{{"library_id", "="}, {"id", "="}, {"other", "="}},
		{{"library_id", "="}, {"id", "="}, {"rowid", "="}, {"rowid", "="}},
	} {
		if rbAdditionalRowIDPoint(d, a, bad) {
			t.Fatal("incomplete/range/unrecognized trace admitted", bad)
		}
	}
	d.ID = "writer.n1.point"
	if rbAdditionalRowIDPoint(d, a, point) {
		t.Fatal("existing command qualification changed")
	}
	d.ID = "read.browse.rb.channel.check"
	a.WithoutRowid = true
	if rbAdditionalRowIDPoint(d, a, point) {
		t.Fatal("WITHOUT ROWID trace admitted")
	}
}
