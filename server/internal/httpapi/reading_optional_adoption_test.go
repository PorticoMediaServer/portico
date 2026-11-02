package httpapi

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A book whose descriptive facts can't be kept still opens (RD-4): the type,
// credits and counts an open adopts are for the catalogue, not for reading.
func TestReadingOpenSurvivesAFailedFactAdoption(t *testing.T) {
	for _, tc := range []struct{ kind, format string }{{"book", "epub"}, {"comic", "cbz"}} {
		t.Run(tc.format, func(t *testing.T) {
			r := newReadingFixture(t, tc.kind, tc.format)
			if _, err := dbwork.ExecWrite(context.Background(), r.s.DB, dbwork.ClassMaintenance, `CREATE TRIGGER test_refuse_publication_type BEFORE INSERT ON reading_publication_types BEGIN SELECT RAISE(ABORT, 'test refusal'); END`); err != nil {
				t.Fatal(err)
			}
			session, _, err := r.s.Prepare(context.Background(), r.p, r.item, r.source)
			if err != nil {
				t.Fatalf("a failed publication-type adoption failed the open: %v", err)
			}
			if err = r.s.CloseSession(r.p, session.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
