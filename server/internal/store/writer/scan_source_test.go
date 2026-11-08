package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func scanSourceFixture(t *testing.T) (*executor, *stmt.Registry, *inventoryCreateCommand) {
	t.Helper()
	w, _, command := scanLifecycleExecutionFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET root='/retained-library-identity' WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO library_scan_policies(library_id,revision) VALUES('lifecycle-library',7)`); err != nil {
		t.Fatal(err)
	}
	registry := cachedNativeInventoryFixtureRegistry(t, w, scanSourceDefinitions(), "scan_source_test.go", "scan_run_lifecycle_test.go")
	return w, registry, command
}

func TestScanCurrentSourceCaptureAndExactRecheck(t *testing.T) {
	w, registry, command := scanSourceFixture(t)
	turn := scanLifecycleExecutionTurn(t, w, registry, InventoryCreate, commandMemoryBudget{4 << 20, 4 << 20})
	r := command.request
	f, err := captureScanSource(t.Context(), turn, r.Scope.libraryID, r.Scope.libraryKey, r.Source.id)
	if err != nil || f.policyRevision != r.PolicyRevision || !reflect.DeepEqual(f.source, r.Source) || string(f.identityRoot) != "/retained-library-identity" || string(f.identityRoot) == string(f.source.resolvedRoot) {
		t.Fatal(f, err)
	}
	_, _ = turn.transaction.rollback(nil)
	o := scanRunOriginalScope{LibraryID: r.Scope.libraryID, LibraryKey: r.Scope.libraryKey, SourceID: r.Source.id,
		SourceGeneration: r.Source.generation, SourceIncarnation: r.Source.incarnation, RootIdentity: r.Source.rootIdentity,
		ResolvedRoot: r.Source.resolvedRoot, FollowSymlinks: r.Source.followSymlinks, PolicyRevision: r.PolicyRevision}
	if !f.matches(o) {
		t.Fatal("native captured original changed meaning")
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE library_sources SET generation=generation+1 WHERE id=?`, r.Source.id); err != nil {
		t.Fatal(err)
	}
	turn = scanLifecycleExecutionTurn(t, w, registry, InventoryClaim, commandMemoryBudget{4 << 20, 4 << 20})
	changed, err := captureScanSource(t.Context(), turn, r.Scope.libraryID, r.Scope.libraryKey, r.Source.id)
	if err != nil || changed.matches(o) || changed.source.generation != f.source.generation+1 {
		t.Fatal("current source silently rebased original", changed, err)
	}
}

func TestScanCurrentSourceRefusesInactiveAndUnfunded(t *testing.T) {
	for _, fault := range []string{"retired", "disabled", "memory"} {
		t.Run(fault, func(t *testing.T) {
			w, registry, command := scanSourceFixture(t)
			budget := commandMemoryBudget{4 << 20, 4 << 20}
			switch fault {
			case "retired":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET retired=1 WHERE id=7`); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE library_sources SET enabled=0 WHERE id=?`, command.request.Source.id); err != nil {
					t.Fatal(err)
				}
			case "memory":
				budget = commandMemoryBudget{}
			}
			turn := scanLifecycleExecutionTurn(t, w, registry, InventoryCreate, budget)
			r := command.request
			got, err := captureScanSource(t.Context(), turn, r.Scope.libraryID, r.Scope.libraryKey, r.Source.id)
			if err == nil || !reflect.DeepEqual(got, scanSourceFacts{}) {
				t.Fatal("ineligible source became authority", got, err)
			}
		})
	}
}

func TestScanCurrentSourcePreservesEmptyLegacyIdentityRoot(t *testing.T) {
	w, registry, command := scanSourceFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET root='' WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	turn := scanLifecycleExecutionTurn(t, w, registry, InventoryCreate, commandMemoryBudget{4 << 20, 4 << 20})
	r := command.request
	got, err := captureScanSource(t.Context(), turn, r.Scope.libraryID, r.Scope.libraryKey, r.Source.id)
	if err != nil || len(got.identityRoot) != 0 || !reflect.DeepEqual(got.source, r.Source) {
		t.Fatal("physical source root replaced the saved legacy root", got, err)
	}
}

func TestScanCurrentSourceLateCloseCancellationClearsFacts(t *testing.T) {
	w, registry, command := scanSourceFixture(t)
	var seq int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w.openPool(base, func(conn driver.Conn) driver.Conn {
		return &inventoryCancelReadConn{writerDriverConn: conn.(writerDriverConn), target: "FROM library_scan_policies", point: "close", cancel: cancel}
	})
	turn := scanLifecycleExecutionTurn(t, w, registry, InventoryCreate, commandMemoryBudget{4 << 20, 4 << 20})
	r := command.request
	got, err := captureScanSource(ctx, turn, r.Scope.libraryID, r.Scope.libraryKey, r.Source.id)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, scanSourceFacts{}) {
		t.Fatal("late cancel released current source", got, err)
	}
}

func TestScanCurrentSourceMemoryPrecedesNativePayload(t *testing.T) {
	for _, population := range []string{"accept-covered", "accept-unfunded", "oversized", "invalid-utf8", "nul", "wrong-purpose"} {
		t.Run(population, func(t *testing.T) {
			w, registry, command := scanSourceFixture(t)
			purpose := InventoryCreate
			if strings.HasPrefix(population, "accept-") {
				purpose = InventoryAccept
			}
			if population == "wrong-purpose" {
				purpose = BasicAccept
			}
			root := ""
			switch population {
			case "oversized":
				root = strings.Repeat("x", 200000)
			case "invalid-utf8":
				root = "/media/\xff"
			case "nul":
				root = "/media/\x00"
			}
			if root != "" {
				if _, err := w.db.ExecContext(t.Context(), `UPDATE library_sources SET root=? WHERE id=?`, root, command.request.Source.id); err != nil {
					t.Fatal(err)
				}
			}
			turn := scanLifecycleExecutionTurn(t, w, registry, purpose, commandMemoryBudget{4 << 20, 4 << 20})
			if population == "accept-covered" {
				turn.transaction.custody.(*governedTurn).inventoryMemory = inventoryMemoryBudget{4 << 20, 4 << 20}
			}
			r := command.request
			got, err := captureScanSource(t.Context(), turn, r.Scope.libraryID, r.Scope.libraryKey, r.Source.id)
			if population == "accept-covered" {
				if err != nil || !reflect.DeepEqual(got.source, r.Source) {
					t.Fatal(got, err)
				}
				return
			}
			if err == nil || !reflect.DeepEqual(got, scanSourceFacts{}) {
				t.Fatal("source proof escaped failed preparation", got, err)
			}
			var refusal *Refusal
			if population == "oversized" && (!errors.As(err, &refusal) || refusal.Code != RefusalCapacity) {
				t.Fatal("unfunded payload did not refuse before copy", err)
			}
			if population == "accept-unfunded" && (!errors.As(err, &refusal) || refusal.Code != RefusalUnqualified) {
				t.Fatal("N1 borrowed lifecycle partition", err)
			}
			if population == "invalid-utf8" || population == "nul" || population == "wrong-purpose" {
				if !errors.Is(err, ErrInvalid) {
					t.Fatal(err)
				}
			}
		})
	}
}
