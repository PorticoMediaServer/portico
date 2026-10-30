package metadata

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mbaccounting"
)

func TestMBAccountingPublicationAndCustodiedUnpinUseSameExactOracle(t *testing.T) {
	s, j, stage, proof := mbFinalTypicalFixture(t)
	ctx := context.Background()
	if e := s.mbPublishAlbumReady(ctx, j, stage, proof, "now"); e != nil {
		t.Fatal(e)
	}
	assertMBAccounting(t, s.db)
	driveArtistHandoffOperations(t, s.db)
	var ref compactcatalog.MutationRef
	e := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		var e error
		ref, e = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: j.base.Entity, OwnerKind: "title", OwnerID: "accounting-unpin", OwnerGeneration: 1, Mode: "members"})
		if e != nil {
			return e
		}
		return compactcatalog.SetMutationPhaseTx(ctx, tx, ref, "drain")
	})
	if e != nil {
		t.Fatal(e)
	}
	finalized := false
	for turn := 0; turn < 32; turn++ {
		e = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			if _, e := compactcatalog.DrainEntityMBPinTx(ctx, tx, ref); e != nil {
				return e
			}
			m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
			finalized = m.Phase == "finalize"
			return e
		})
		if e != nil {
			t.Fatal(turn, e)
		}
		assertMBAccounting(t, s.db)
		if finalized {
			break
		}
	}
	if !finalized {
		t.Fatal("actual unpin did not finish")
	}
	var pins, next int
	if e = s.db.QueryRow(`SELECT count(*) FROM mb_album_links WHERE album_id=?`, j.base.Entity).Scan(&pins); e != nil {
		t.Fatal(e)
	}
	if e = s.db.QueryRow(`SELECT count(*) FROM mb_recording_accounting_next`).Scan(&next); e != nil {
		t.Fatal(e)
	}
	if pins != 0 || next != 0 {
		t.Fatal("pin or private facts escaped drain", pins, next)
	}
}

func TestMBAccountingFinalFailureReleasesPrivateOwner(t *testing.T) {
	s, j, stage, proof := mbFinalTypicalFixture(t)
	ctx := context.Background()
	change, e := s.mbPrepareAccounting(ctx, j, "album", stage.value.ID, "", proof.revision, proof.digest)
	if e != nil {
		t.Fatal(e)
	}
	sentinel := errors.New("simulated final refusal")
	if got := s.mbAbortPrivateAccounting(ctx, change.Owner, sentinel); !errors.Is(got, sentinel) {
		t.Fatal(got)
	}
	for turn := 0; turn < 8; turn++ {
		held, worked, e := s.mbAccountingMaintenance(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if !held {
			break
		}
		if !worked {
			t.Fatal("aborted private work stopped progressing")
		}
	}
	e = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return mbaccounting.IdleTx(ctx, tx) })
	if e != nil {
		t.Fatal("failed publication stranded producers", e)
	}
	var pins int
	if e = s.db.QueryRow(`SELECT count(*) FROM mb_album_links WHERE album_id=?`, j.base.Entity).Scan(&pins); e != nil || pins != 0 {
		t.Fatal("private failure published pin", pins, e)
	}
	assertMBAccounting(t, s.db)
}
