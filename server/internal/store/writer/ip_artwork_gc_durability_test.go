package writer

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// MARK (229) and FINALIZE (230) are FULL in both the compiled registry and the
// frame the body runs through. A NORMAL frame is refused even when the body is
// otherwise exact: there is no NORMAL fallback for the artwork GC handoff.
func TestIPArtworkGCDurabilityDescriptorsAndFrame(t *testing.T) {
	for _, kind := range []CommandID{IPArtworkGCMark, IPArtworkGCFinish} {
		t.Run(ipArtworkGCCommandKind(kind), func(t *testing.T) {
			want, err := Describe(kind)
			if err != nil || want.Durability != Full {
				t.Fatal("descriptor", want, err)
			}
			w, r, p := ipArtworkGCFixture(t)
			digest, _ := seedIPArtworkGCSource(t, w, 1, true)
			owner := ipArtworkGCOwner()
			var cmd *ipArtworkGCCommand
			if kind == IPArtworkGCMark {
				cmd = ipArtworkGCCommandFor(t, IPArtworkGCMark, owner, 1, 2,
					ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 1, bytes: 2 << 20})
			} else {
				marked, next := ipArtworkGCSourceMark(t, w, r, p, owner, 1, 2, digest, 2<<20)
				ipArtworkGCAck(t, w, 1)
				owner = next
				cmd = ipArtworkGCCommandFor(t, IPArtworkGCFinish, owner, 2, 3,
					ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 2,
						bytes: 2 << 20, token: marked.Value.Token})
			}
			turn, tx, b := ipPolicyTurn(t, w, r, kind)
			if tx.descriptor != want || tx.descriptor.Durability != Full ||
				tx.settings.Synchronous != int(sqliteconn.Full) || !tx.policyVerified {
				t.Fatal("frame durability", tx.descriptor, tx.settings, tx.policyVerified)
			}
			// The connected PRAGMA is the second witness. A downgraded frame is
			// refused before any custody write.
			tx.settings.Synchronous = int(sqliteconn.Normal)
			out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
			if out != (ipArtworkGCCommandResult{}) || !errors.Is(err, ErrInvalid) {
				t.Fatal("NORMAL frame admitted artwork GC", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
}

// The mark candidate becomes unlink permission only after the FULL COMMIT. A
// fresh reader sees no retiring row before commit and the exact retiring
// revision after it; a lost-response replay then serves the saved original
// while the row is still marked, which is the mark-before-unlink proof.
func TestIPArtworkGCMarkDurableBeforeUnlinkAndLostResponseReplay(t *testing.T) {
	w, r, p := ipArtworkGCFixture(t)
	digest, _ := seedIPArtworkGCSource(t, w, 1, true)
	path := ipPolicyDatabasePath(t, w)
	cmd := ipArtworkGCCommandFor(t, IPArtworkGCMark, ipArtworkGCOwner(), 1, 2,
		ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 1, bytes: 2 << 20})
	retiring := `SELECT retiring FROM artwork_sources WHERE digest='` + digest + `'`
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCMark)
	out, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	// The body wrote the row, but the caller has not committed. Nothing a driver
	// could read durably says "unlink": the token/result is only a candidate.
	if got := ipPolicyFreshInt(t, path, retiring); got != 0 {
		t.Fatal("uncommitted mark visible to a fresh reader", got)
	}
	if _, err = tx.commit(); err != nil {
		t.Fatal(err)
	}
	if got := ipPolicyFreshInt(t, path, retiring); got != 1 {
		t.Fatal("durable mark missing after FULL commit", got)
	}
	if got := ipPolicyFreshInt(t, path, `SELECT revision FROM artwork_sources WHERE digest='`+digest+`'`); got != 2 {
		t.Fatal("durable mark revision", got)
	}
	if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='gc_mark'`); got != 1 {
		t.Fatal("durable mark receipt", got)
	}

	// Lost response: the same command replayed on a fresh turn returns the saved
	// original, and the row is still marked so the one unlink retry stays exact.
	turn, tx, b = ipPolicyTurn(t, w, r, IPArtworkGCMark)
	replay, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
		t.Fatal("mark replay", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyFreshInt(t, path, retiring); got != 1 {
		t.Fatal("replay cleared the durable mark", got)
	}
	if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='gc_mark'`); got != 1 {
		t.Fatal("replay duplicated the mark receipt", got)
	}
}

// An uncertain MARK COMMIT is ResolveRequired. The in-transaction candidate is
// never returned as unlink permission; the only durable evidence is the fresh
// read of the marked row.
func TestIPArtworkGCMarkUncertainCommitResolves(t *testing.T) {
	w, r, p := ipArtworkGCFixture(t)
	digest, _ := seedIPArtworkGCSource(t, w, 1, true)
	cmd := ipArtworkGCCommandFor(t, IPArtworkGCMark, ipArtworkGCOwner(), 1, 2,
		ipArtworkGCRequest{target: ipArtworkGCSource, digest: digest, revision: 1, bytes: 2 << 20})
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPArtworkGCMark)
	candidate, err := applyIPArtworkGC(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || candidate.Outcome.State != Applied {
		t.Fatal(candidate, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("uncertain mark commit", result, err)
	}
	if again, e := applyIPArtworkGC(context.Background(), turn, p, context.Background(), b, cmd); again != (ipArtworkGCCommandResult{}) || e == nil {
		t.Fatal("quarantined frame served the mark", again, e)
	}
	if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM artwork_command_receipts WHERE command_kind='gc_mark'`); got != 1 {
		t.Fatal("fresh mark receipt", got)
	}
	if got := ipPolicyFreshInt(t, path, `SELECT retiring FROM artwork_sources WHERE digest='`+digest+`'`); got != 1 {
		t.Fatal("fresh retiring row", got)
	}
}

// A later reference cannot attach to a marked source: the selection command's
// ready fence requires a non-retiring source, so the attach is refused while the
// mark stays exactly as the unlink retry left it.
func TestIPArtworkGCMarkedSourceRefusesReferenceAttach(t *testing.T) {
	w, r, p := ipArtworkGCFixture(t)
	digest, _ := seedIPArtworkGCSource(t, w, 1, true)
	marked, owner := ipArtworkGCSourceMark(t, w, r, p, ipArtworkGCOwner(), 1, 2, digest, 2<<20)
	if marked.Outcome.State != Applied {
		t.Fatal(marked)
	}
	ipArtworkGCAck(t, w, 1)
	defs := append(usAuthorityDefinitions(), ipArtworkMaterialDefinitions()...)
	defs = append(defs, ipArtworkGCDefinitions()...)
	defs = append(defs, ipArtworkSelectionDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	union := &ipPolicyRegistry{registry, r.keeper}
	before := ipArtworkSelectionBeforeFor(t, w, owner, "poster")
	cmd := ipArtworkSelectionCommandFor(t, IPArtworkSelect, 2, 9, owner, "poster", before,
		ipArtworkSelectionFactsFor(digest))
	turn, tx, b := ipPolicyTurn(t, w, union, IPArtworkSelect)
	out, err := applyIPArtworkSelection(t.Context(), turn, p, t.Context(), b, cmd)
	if out != (ipArtworkSelectionCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("reference attached to a marked source", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	ipArtworkGCExpect(t, w, 1, `SELECT count(*) FROM artwork_sources WHERE digest=? AND retiring=1 AND revision=2`, digest)
	ipArtworkGCExpect(t, w, 0, `SELECT count(*) FROM artwork_selections WHERE digest=?`, digest)
}
