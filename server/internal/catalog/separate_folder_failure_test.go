package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func separateFailurePending(t *testing.T) (*separateAsyncFixture, string) {
	t.Helper()
	f := newSeparateAsyncFixture(t, 20)
	id, err := New(f.c.DB).SeparateFolder(WithSeparateFolderActor(context.Background(), f.principal), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if id != "" || !errors.Is(err, ErrSeparateFolderPending) {
		t.Fatalf("pending: %q %v", id, err)
	}
	root := separateAsyncRoot(t, f.c)
	for n := 0; n < 4 && separateAsyncChild(t, f.c, root) == ""; n++ {
		separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
	}
	if separateAsyncChild(t, f.c, root) == "" {
		t.Fatal("no live child")
	}
	return f, root
}

func TestSeparateFolderBackgroundPermanentFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 8.93s")
	}
	cases := []struct {
		name, code string
		corrupt    func(*separateAsyncFixture, string)
	}{
		{"ready generation", "source_changed", func(f *separateAsyncFixture, _ string) {
			f.c.Exec("UPDATE library_sources SET generation=generation+1 WHERE id=?", f.source)
		}},
		{"retired container", "source_changed", func(f *separateAsyncFixture, _ string) {
			f.c.Exec("UPDATE catalog_entities SET retired=1 WHERE id=?", f.album)
		}},
		{"unavailable file", "source_changed", func(f *separateAsyncFixture, root string) {
			f.c.Exec("UPDATE inventory_objects SET state='missing',missing_since='now' WHERE asset_id=(SELECT token FROM catalog_assets WHERE id=(SELECT current_key FROM catalog_entity_mutations WHERE mutation_id=(SELECT current_child FROM catalog_entity_mutations WHERE mutation_id=?)))", root)
		}},
		{"missing file link", "source_changed", func(f *separateAsyncFixture, root string) {
			f.c.Exec("DELETE FROM catalog_asset_links WHERE entity_id=(SELECT subject_id FROM catalog_entity_mutations WHERE mutation_id=(SELECT current_child FROM catalog_entity_mutations WHERE mutation_id=?)) AND asset_id=(SELECT current_key FROM catalog_entity_mutations WHERE mutation_id=(SELECT current_child FROM catalog_entity_mutations WHERE mutation_id=?))", root, root)
		}},
		{"cursor mismatch", "invalid_cursor", func(f *separateAsyncFixture, root string) {
			// Explicit negative control: corrupt the durable raw cursor.
			f.c.Exec("UPDATE catalog_entity_mutations SET cursor=-1 WHERE mutation_id=?", root)
		}},
		{"invalid binding", "invalid_cursor", func(f *separateAsyncFixture, root string) {
			f.c.Exec("UPDATE catalog_entity_mutations SET accepted_revision='invalid' WHERE mutation_id=?", root)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, root := separateFailurePending(t)
			tc.corrupt(f, root)
			before := separateAsyncEffects(t, f)
			for n := 0; n < 3; n++ {
				out := separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
				if out.ErrorCode != tc.code || (n > 0 && out.State != "failed") {
					t.Fatalf("turn %d: %+v", n, out)
				}
				if after := separateAsyncEffects(t, f); after != before {
					t.Fatalf("effects: %+v -> %+v", before, after)
				}
			}
			if n := rows(t, f.c, "SELECT count(*) FROM catalog_entity_mutations WHERE status<>'terminal'"); n != 0 {
				t.Fatalf("held: %d", n)
			}
		})
	}
}

func TestSeparateFolderBackgroundTransientFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 4.23s")
	}
	for _, moving := range []bool{false, true} {
		t.Run(map[bool]string{false: "offline", true: "moving"}[moving], func(t *testing.T) {
			f, root := separateFailurePending(t)
			if moving {
				f.c.Exec("INSERT INTO identity_move_waiting(account_id,move_id,state,created_ms) VALUES(?,'move','waiting',1)", f.account)
			} else {
				f.c.Exec("UPDATE library_sources SET health='offline' WHERE id=?", f.source)
			}
			before := separateAsyncEffects(t, f)
			out := separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
			want := "waiting for source"
			if moving {
				want = "waiting for account move"
			}
			if out.Phase != want || out.State != "running" {
				t.Fatalf("%+v", out)
			}
			if after := separateAsyncEffects(t, f); after != before {
				t.Fatalf("effects: %+v -> %+v", before, after)
			}
			if moving {
				f.c.Exec("UPDATE direct_memberships SET disabled=1 WHERE account_id=?", f.account)
				out = separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
				if out.ErrorCode != "authority_revoked" {
					t.Fatalf("moving masked revoked authority: %+v", out)
				}
			}
		})
	}
}

func TestSeparateFolderBackgroundResumesAfterAccountMove(t *testing.T) {
	f, root := separateFailurePending(t)
	f.c.Exec("INSERT INTO identity_move_waiting(account_id,move_id,state,created_ms) VALUES(?,'move','waiting',1)", f.account)
	before := separateAsyncEffects(t, f)
	out := separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
	if out.State != "running" || out.Phase != "waiting for account move" {
		t.Fatalf("transient move left the scheduler's runnable states: %+v", out)
	}
	if after := separateAsyncEffects(t, f); after != before {
		t.Fatalf("waiting changed catalogue/claims: %+v -> %+v", before, after)
	}
	f.c.Exec("DELETE FROM identity_move_waiting WHERE account_id=?", f.account)
	for turn := 0; turn < 240; turn++ {
		out = separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
		if out.State == "failed" || out.State == "paused" {
			t.Fatalf("move completion did not resume the same command: %+v", out)
		}
		if out.State == "succeeded" {
			return
		}
	}
	t.Fatal("same command did not finish after account move cleared")
}

func TestSeparateFolderIdentityRefusalCompatibility(t *testing.T) {
	for _, err := range []error{identity.ErrForbidden, identity.ErrNotVisible} {
		if !errors.Is(err, identity.ErrUnauthorized) {
			t.Fatalf("%v does not match unauthorized", err)
		}
	}
}

// Inject a permanent failure after a real hook write, or corrupt a real
// retirement job's binding. Neither negative control fabricates completion.
func TestSeparateFolderBackgroundRetirementFailureRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 2.23s")
	}
	for _, planMismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "late hook failure", true: "plan mismatch"}[planMismatch], func(t *testing.T) {
			f, root := separateFailurePending(t)
			child := separateAsyncChild(t, f.c, root)
			entity := separateAsyncInt(t, f.c, "SELECT subject_id FROM catalog_entity_mutations WHERE mutation_id=?", child)
			token := separateAsyncText(t, f.c, "SELECT token FROM catalog_assets WHERE id=(SELECT current_key FROM catalog_entity_mutations WHERE mutation_id=?)", child)
			f.c.Exec("INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES('failure-job',?,?,'music','profile','target','{}','revision','{}','running','running',1,2)", entity, token)
			_, hooks := separateAsyncRetirement(t, f)
			if !planMismatch {
				hooks.CancelJob = func(ctx context.Context, tx *sql.Tx, id string) error {
					if _, err := tx.ExecContext(ctx, "UPDATE prepared_media_jobs SET phase='must roll back' WHERE id=?", id); err != nil {
						return err
					}
					return compactcatalog.ErrMutationStale
				}
			}
			corrupted, failed := false, false
			for n := 0; n < 100; n++ {
				if planMismatch && !corrupted && rows(t, f.c, "SELECT count(*) FROM catalog_purge_jobs WHERE mutation_id=?", child) > 0 {
					f.c.Exec("UPDATE catalog_purge_jobs SET plan_digest='0000000000000000000000000000000000000000000000000000000000000000' WHERE mutation_id=?", child)
					corrupted = true
				}
				out := separateAsyncAdapterStep(t, f.c.DB, hooks, root)
				if out.ErrorCode != "" {
					want := "source_changed"
					if planMismatch {
						want = "invalid_cursor"
					}
					if out.ErrorCode != want {
						t.Fatalf("%+v", out)
					}
					if !planMismatch && separateAsyncText(t, f.c, "SELECT phase FROM prepared_media_jobs WHERE id='failure-job'") != "running" {
						t.Fatal("failed quantum committed hook write")
					}
					failed = true
					break
				}
			}
			if !failed || planMismatch && !corrupted {
				t.Fatal("negative control not reached")
			}
		})
	}
}
