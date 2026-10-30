package metadata

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestBulkMetadataContainerFencesStayPerTarget(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	c := catalogtest.New(t, db)
	other := c.Show(c.Handle("tv"), "Other show", 2021)
	season := c.Season(other, 1)
	c.Episode(other, season, 1, "/tv/other.mkv")
	c.Drain()
	ctx := context.Background()
	targets := []BulkTarget{}
	for _, id := range []string{names["show"].Public, other.Public} {
		state, err := s.RepairState(ctx, RepairTarget{Kind: "show", ID: id}, allowAll)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, BulkTarget{Kind: "show", ID: id, ExpectedRevision: state.Revision})
	}
	changed := "Changed independently"
	editField(t, s, RepairTarget{Kind: "show", ID: targets[0].ID}, "network", RepairFieldEdit{Value: &changed})
	network := "Bulk network"
	result, err := s.BulkEdit(ctx, BulkEdit{OperationID: "per-target-shows", Targets: targets, Fields: map[string]RepairFieldEdit{"network": {Value: &network}}}, editorActor(), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 || result.Failed != 1 || result.Results[0].Code != "metadata_conflict" || !result.Results[1].OK {
		t.Fatalf("independent fences %+v", result)
	}
	// Source changes still invalidate the affected target even when all its
	// editable values are unchanged.
	state, err := s.RepairState(ctx, RepairTarget{Kind: "show", ID: other.Public}, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE catalog_assets SET size=size+1 WHERE path='/tv/other.mkv'`); err != nil {
		t.Fatal(err)
	}
	result, err = s.BulkEdit(ctx, BulkEdit{OperationID: "changed-show-file", Targets: []BulkTarget{{Kind: "show", ID: other.Public, ExpectedRevision: state.Revision}}, Fields: map[string]RepairFieldEdit{"network": {Value: &network}}}, editorActor(), allowAll)
	if err != nil || result.Failed != 1 || result.Results[0].Code != "metadata_conflict" {
		t.Fatalf("source fence %+v %v", result, err)
	}
}
