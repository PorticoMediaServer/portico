package mediaanalysis

import (
	"context"
	"testing"
)

func TestChapterPolicyUsesStoredOwnerEditAndKeepsEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := viewerMarkerDB(t)
	ctx := context.Background()
	owner := viewerMarkerAccess(f.item)
	owner.Owner = true
	s := &Service{db: f.db}
	write, err := s.begin(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.resolve(ctx, write.Tx(), owner, Target{})
	write.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	// Use the actual owner edit writer: it retains chapter provenance but writes
	// approved=1 and edited=1, the fields every current viewer consumer trusts.
	_, err = s.Mutate(ctx, owner, Mutation{Target: Target{SourceID: source.ID, SourceRevision: source.Revision, MappingRevision: source.MappingRevision}, ExpectedRevision: 7, RequestID: "chapter-owner-edit-0001", Action: "edit", MarkerID: "titled-recap", Kind: "recap", StartUS: "0", EndUS: "3000000"})
	if err != nil {
		t.Fatal(err)
	}
	var provenance string
	var approved, edited bool
	if err = f.db.QueryRow(`SELECT provenance,approved,edited FROM analysis_markers WHERE id='titled-recap'`).Scan(&provenance, &approved, &edited); err != nil || provenance != "embedded_chapter_label:v1" || !approved || !edited {
		t.Fatal(provenance, approved, edited, err)
	}
	// A second untouched chapter suggestion, on a nonoverlapping interval.
	if _, err = f.db.Exec(`INSERT INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id) SELECT 'unapproved-chapter',object_id,source_revision,source_binding,'recap',50000000,55000000,.85,provenance,'' FROM analysis_markers WHERE id='titled-recap'`); err != nil {
		t.Fatal(err)
	}
	before, err := ViewerMarkers(ctx, f.db, owner, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO server_settings(singleton,"segments.useChapterNames") VALUES(1,0) ON CONFLICT(singleton) DO UPDATE SET "segments.useChapterNames"=0`); err != nil {
		t.Fatal(err)
	}
	after, err := ViewerMarkers(ctx, f.db, owner, "")
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision == after.Revision {
		t.Fatal("chapter policy did not fence projection")
	}
	ids := map[string]bool{}
	for _, marker := range after.Markers {
		ids[marker.ID] = true
	}
	if !ids["titled-recap"] || ids["unapproved-chapter"] || !ids["measured-credits"] || !ids["single-file-guess"] {
		t.Fatal("wrong toggle boundary", after.Markers)
	}
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM analysis_markers WHERE id IN('titled-recap','unapproved-chapter') AND deleted=0`).Scan(&count); err != nil || count != 2 {
		t.Fatal("toggle destroyed evidence", count, err)
	}
	if _, err = f.db.Exec(`UPDATE server_settings SET "segments.useChapterNames"=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	restored, err := ViewerMarkers(ctx, f.db, owner, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, marker := range restored.Markers {
		if marker.ID == "unapproved-chapter" {
			found = true
		}
	}
	if !found {
		t.Fatal("reenabling required reanalysis", restored.Markers)
	}
}
