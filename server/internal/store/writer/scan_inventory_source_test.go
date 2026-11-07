package writer

import (
	"bytes"
	"errors"
	"testing"
)

func TestN1FreshSourceKeepsBothRootMeanings(t *testing.T) {
	in, id := inventoryPublicInput(t)
	command, err := NewInventoryAcceptCommand(id, in)
	if err != nil {
		t.Fatal(err)
	}
	c := command.(*inventoryAcceptCommand)
	want := scanSourceFacts{libraryID: in.Scope.LibraryID, libraryKey: in.Scope.LibraryKey,
		policyRevision: in.Run.PolicyRevision, source: c.request.source, identityRoot: []byte("/legacy-name-root")}
	if !inventoryDriverSourceMatches(c, want, want) {
		t.Fatal("exact retained/current native source refused")
	}
	for _, change := range []struct {
		name string
		edit func(*scanSourceFacts)
	}{
		{"logical_root", func(f *scanSourceFacts) { f.identityRoot = []byte("/replacement-name-root") }},
		{"physical_root", func(f *scanSourceFacts) { f.source.resolvedRoot = []byte("/replacement-physical-root") }},
		{"policy", func(f *scanSourceFacts) { f.policyRevision++ }},
		{"generation", func(f *scanSourceFacts) { f.source.generation++ }},
	} {
		t.Run(change.name, func(t *testing.T) {
			actual := want
			actual.source.resolvedRoot = bytes.Clone(want.source.resolvedRoot)
			change.edit(&actual)
			if inventoryDriverSourceMatches(c, want, actual) {
				t.Fatal("fresh source changed after observation/freeze")
			}
		})
	}
	wrongPrepared := want
	wrongPrepared.policyRevision++
	if inventoryDriverSourceMatches(c, wrongPrepared, want) {
		t.Fatal("current source concealed a mismatched retained preparation")
	}
}

// Component only: the existing owned fixture supplies the real Capture,
// same-governor frame and memory partition. These private effect values do not
// issue a driver, source capability, production profile or fresh permission.
func TestN1FreshSourceRequiresCurrentNativeRootAndActiveRun(t *testing.T) {
	for _, mode := range []string{"current", "logical_root_changed", "active_slot_missing", "active_slot_other_run", "foreign_root_alias", "foreign_root_child", "foreign_root_parent"} {
		t.Run(mode, func(t *testing.T) {
			w, _, c, registry := inventoryFreshFixtureDefinitions(t, inventoryDriverCommandDefinitions())
			// Same physical folders may be configured in other libraries.
			// Native identity is library/source scoped, not globally path-scoped.
			foreignRoot := map[string]string{"foreign_root_alias": "/media", "foreign_root_child": "/media/nested", "foreign_root_parent": "/"}[mode]
			if foreignRoot != "" {
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_libraries(id,library_id,root) VALUES(8,'other-library',?)`, foreignRoot); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "active_slot_missing" {
				run := "fresh-run"
				if mode == "active_slot_other_run" {
					run = "other-active-run"
					if _, err := w.db.ExecContext(t.Context(), `INSERT INTO jobs(id,library_id,status,created_at) VALUES('other-active-run','fresh-library','running','2026-11-06T00:00:00Z')`); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO inventory_source_active(source_id,job_id) VALUES('fresh-source',?)`, run); err != nil {
					t.Fatal(err)
				}
			}
			prepared := scanSourceFacts{libraryID: c.request.scope.libraryID, libraryKey: c.request.scope.libraryKey,
				policyRevision: c.request.run.policyRevision, source: c.request.source, identityRoot: []byte("/media")}
			if mode == "logical_root_changed" {
				prepared.identityRoot = []byte("/old-logical-root")
			}
			ref, err := c.receiptRequest()
			if err != nil {
				t.Fatal(err)
			}
			retained := &scanPreparation{source: &prepared}
			n := &N1Preparation{preparation: retained, command: c, reference: ref}
			retained.inventory = n
			turn, _ := inventoryMemoryFixtureTurn(t, w, registry, c, inventoryMemoryBudget{4 << 20, 4 << 20})
			in := &inventoryInvocation{input: c, request: ref, issuer: &scanIssuerInvocation{action: scanIssuerN1Apply, frame: turn.transaction, retained: retained}}
			err = checkInventoryDriverFreshSource(t.Context(), turn, in)
			allowed := mode == "current" || foreignRoot != ""
			if allowed && err != nil {
				t.Fatal("current same-snapshot source refused", err)
			}
			if !allowed && !errors.Is(err, ErrConflict) {
				t.Fatal("changed native source/active slot accepted", mode, err)
			}
			if _, rollbackErr := turn.transaction.rollback(nil); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
		})
	}
}

// Canonical paths are per-library identities. Other libraries may refer to the
// very same file. Actual tokens/row IDs are separately allocated and the fresh
// effects must neither reuse nor mutate that other library's native asset.
func TestN1OverlappingLibraryFoldersKeepNativeAssetNamespaces(t *testing.T) {
	for _, root := range []string{"/media", "/media/nested", "/"} {
		t.Run(root, func(t *testing.T) {
			w, _, c, registry := inventoryFreshFixtureDefinitions(t, inventoryDriverCommandDefinitions())
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_libraries(id,library_id,root) VALUES(8,'other-library',?)`, root); err != nil {
				t.Fatal(err)
			}
			path := c.request.files[0].assetPath
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(901,'foreign-asset',8,?,999,0,'','','',0,0,0)`, path); err != nil {
				t.Fatal(err)
			}
			turn, _ := inventoryMemoryFixtureTurn(t, w, registry, c, inventoryMemoryBudget{4 << 20, 4 << 20})
			value, err := applyInventoryAccept(t.Context(), turn, c)
			if err != nil || value.Outcome != (ReceiptOutcome{}) || len(value.Members) != 1 || value.Members[0].LibraryID != 7 || value.Members[0].AssetID == 901 {
				t.Fatal("overlap escaped own library identity", value, err)
			}
			var library, size int64
			var token, actualPath string
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT library_id,token,path,size FROM catalog_assets WHERE id=901`).Scan(&library, &token, &actualPath, &size); err != nil {
				t.Fatal(err)
			}
			if library != 8 || token != "foreign-asset" || actualPath != path || size != 999 {
				t.Fatal("another library's same-path asset changed", library, token, actualPath, size)
			}
			var copies int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_assets WHERE path=?`, path).Scan(&copies); err != nil || copies != 2 {
				t.Fatal("per-library path uniqueness became global", copies, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
