package httpapi

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
	"time"
)

func TestLibraryRemovalInvalidatesCachedAuthority(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "authority.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"library_sources", "libraries"} {
		t.Run(table, func(t *testing.T) {
			if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `INSERT INTO libraries(id,name,kind,root) VALUES(?,'test','movie',?)`, table, "/"+table); err != nil {
				t.Fatal(err)
			}
			// Library creation installs its default source through the existing trigger.
			var sources int
			if err := db.QueryRow(`SELECT count(*) FROM library_sources WHERE library_id=?`, table).Scan(&sources); err != nil || sources == 0 {
				t.Fatal("missing source fixture", sources, err)
			}
			if table == "libraries" {
				for _, child := range []string{"library_sources", "library_scan_policies", "inventory_policy_pending"} {
					if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `DELETE FROM `+child+` WHERE library_id=?`, table); err != nil {
						t.Fatal(err)
					}
				}
			}
			now, generation := time.Now(), dbwork.AuthorityGeneration()
			access, principals, restrictions := newAccessCache(), newPrincipalCache(), newRestrictionCache()
			access.grant("grant", now, generation)
			principals.store("principal", identity.Principal{}, now, generation)
			restrictions.store("profile", identity.ContentRestrictions{}, now, generation)
			if !access.allowed("grant", now) {
				t.Fatal("grant not cached")
			}
			column := "id"
			if table == "library_sources" {
				column = "library_id"
			}
			if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `DELETE FROM `+table+` WHERE `+column+`=?`, table); err != nil {
				t.Fatal(err)
			}
			if access.allowed("grant", now) {
				t.Fatal("removed library/source retained grant")
			}
			if _, ok := principals.lookup("principal", now); ok {
				t.Fatal("stale principal reused")
			}
			if _, ok := restrictions.lookup("profile", now); ok {
				t.Fatal("stale visibility restrictions reused")
			}
		})
	}
}
