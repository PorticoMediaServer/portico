package catalog

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
)

func exactWitnessForTest(t *testing.T, s *Service, profile string, r identity.ContentRestrictions) string {
	t.Helper()
	var out string
	err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var e error
		out, e = s.WithContext(ctx).browseExactWitness("reading", profile, r)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBrowseExactWitnessTransactionMissingAndPolicy(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	if _, err := s.browseExactWitness("reading", v.Profile, v.Restrictions); err == nil {
		t.Fatal("unbound witness accepted")
	}
	before := exactWitnessForTest(t, s, v.Profile, v.Restrictions)
	for _, r := range []identity.ContentRestrictions{{Revision: 1}, {MemberRevision: 1}, {BlockRecordings: true}, {BlockedLabels: []string{"a,b"}}, {BlockedLabels: []string{"a", "b"}}} {
		if exactWitnessForTest(t, s, v.Profile, r) == before {
			t.Fatal("policy omitted", r)
		}
	}
	if exactWitnessForTest(t, s, "other-profile", v.Restrictions) == before {
		t.Fatal("profile omitted")
	}
	// Both explicit transaction carriers are supported by selection admission.
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		clone := *s
		clone.readTx = tx
		got, e := clone.browseExactWitness("reading", v.Profile, v.Restrictions)
		if e != nil || got != before {
			t.Fatal(got, e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM catalog_browse_predicate_epoch`, `DELETE FROM catalog_summary_classification_revision`, `DELETE FROM catalog_generations`, `DELETE FROM catalog_derivations WHERE domain=19`, `UPDATE catalog_derivations SET rebuilding=1,first_build=1 WHERE domain=18`} {
		tx, e := c.DB.Begin()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(query); e != nil {
			t.Fatal(e)
		}
		clone := *s
		clone.readTx = tx
		_, err := clone.browseExactWitness("reading", v.Profile, v.Restrictions)
		tx.Rollback()
		if err == nil {
			t.Fatal("unproved witness accepted", query)
		}
	}
}

func TestBrowseExactWitnessSnapshotAndRuntimeFormats(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	before := exactWitnessForTest(t, s, v.Profile, v.Restrictions)
	s.ConfigureReadingMobi(true)
	if exactWitnessForTest(t, s, v.Profile, v.Restrictions) == before {
		t.Fatal("format omitted")
	}
	s.ConfigureReadingMobi(false)
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		first, e := read.browseExactWitness("reading", v.Profile, v.Restrictions)
		if e != nil {
			return e
		}
		// A write on the WAL writer must not leak into this held read snapshot.
		c.Exec(`INSERT INTO content_rating_ages VALUES('snapshot-witness',7)`)
		second, e := read.browseExactWitness("reading", v.Profile, v.Restrictions)
		if e != nil {
			return e
		}
		if first != second {
			t.Fatal("snapshot drifted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if exactWitnessForTest(t, s, v.Profile, v.Restrictions) == before {
		t.Fatal("new snapshot missed write")
	}
}
