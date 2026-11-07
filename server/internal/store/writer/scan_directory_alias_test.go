package writer

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Read the actual post-ACK native snapshot, including lease and sequence. The
// immutable original scope is the same captured scope, never current parents.
func directoryAliasSnapshot(t *testing.T, e *executor, s inventoryClaimRequest) (inventoryClaimRequest, int64, [16]byte) {
	t.Helper()
	var next sql.NullInt64
	var warnings int64
	var nonce []byte
	err := e.db.QueryRow(`SELECT r.job_revision,r.policy_revision,r.job_lease_owner,r.job_lease_generation,r.job_lease_expires_ms,r.last_command_sequence,r.acknowledged_command_sequence,r.phase,r.scan_mode,
d.relative_path,d.identity,d.revision,d.cursor,d.state,d.pages,d.skipped,
r.finished_at,r.authoritative,r.root_rebound,r.discovered,r.job_attempts,r.job_next_attempt_ms,
j.status,j.processed,j.created_at,j.error,r.warnings,r.command_incarnation
FROM inventory_runs r JOIN jobs j ON j.id=r.job_id JOIN inventory_directories d ON d.job_id=r.job_id AND d.relative_path='.' WHERE r.job_id=?`, s.Original.RunID).Scan(
		&s.Run.revision, &s.Run.policyRevision, &s.Run.leaseOwner, &s.Run.leaseGeneration, &s.Run.leaseExpiresMS, &s.Run.lastSequence, &s.Run.acknowledgedSequence, &s.Run.phase, &s.Run.scanMode,
		&s.Root.Path, &s.Root.Identity, &s.Root.Revision, &s.Root.Cursor, &s.Root.State, &s.Root.Pages, &s.Root.Skipped,
		&s.FinishedAt, &s.Authoritative, &s.RootRebound, &s.Discovered, &s.Attempts, &next,
		&s.JobStatus, &s.Processed, &s.JobCreatedAt, &s.JobErrorText, &warnings, &nonce)
	if err != nil || len(nonce) != 16 || s.Run.acknowledgedSequence != s.Run.lastSequence {
		t.Fatal("actual settled snapshot required", err, len(nonce), s.Run)
	}
	s.NextAttemptMS.Present, s.NextAttemptMS.Value = next.Valid, next.Int64
	var incarnation [16]byte
	copy(incarnation[:], nonce)
	return s, warnings, incarnation
}

// Regression for #1351 round 1: refusal has no directory row, so the ordinary
// visited index alone cannot fence its identity after ACK and an alias path.
// CHANGE-011 must be embedded before these real registry/dispatcher controls.
func TestScanDirectoryRefusedIdentityFencesAliases(t *testing.T) {
	for _, purpose := range []CommandID{InventoryDirectoryDiscover, InventoryDirectoryRefuse} {
		name := "discover_alias"
		if purpose == InventoryDirectoryRefuse {
			name = "refuse_alias"
		}
		t.Run(name, func(t *testing.T) {
			w, e, authority, s := terminalDispatchFixture(t)
			id, _, page, child := terminalInput()
			first, err := newScanDirectoryRefuseCommand(id, s, s.Root, 0, page, child)
			if err != nil {
				t.Fatal(err)
			}
			refused, err := executeTerminal(t, w, first)
			if err != nil || refused.Outcome.State != Applied {
				t.Fatal("original refusal", refused, err)
			}
			q := first.reference()
			if _, floor, err := w.effective.runScanDirectoryTerminal(t.Context(), w, inventoryTestDisclosure(t, w, q), q, first.purpose, nil, true); err != nil || floor.AcknowledgedThrough != id.Sequence {
				t.Fatal("original ACK", floor, err)
			}
			var warnings int64
			var incarnation [16]byte
			s, warnings, incarnation = directoryAliasSnapshot(t, e, s)
			before := directoryCounts(t, e)
			beforeWarnings, beforeSkipped := warnings, s.Root.Skipped
			// Both attempted aliases are genuinely different native paths; the
			// changed revision must not evade identity equality either.
			rawName := []byte("valid-alias")
			if purpose == InventoryDirectoryRefuse {
				rawName = []byte{'o', 't', 'h', 'e', 'r', 0xfe}
			}
			page = nativeinventory.Page{Version: 1, RootIdentity: s.Original.RootIdentity, DirectoryIdentity: s.Root.Identity, DirectoryRevision: s.Root.Revision, NextCursor: "43", Entries: []nativeinventory.Entry{{Name: rawName, Path: append([]byte("/media/"), rawName...), Directory: true, ObjectIdentity: "bad-child"}}}
			child = nativeinventory.Page{Version: 1, RootIdentity: s.Original.RootIdentity, DirectoryIdentity: "bad-child", DirectoryRevision: "alias-revision"}
			id = CommandIdentity{Incarnation: incarnation, Sequence: s.Run.lastSequence + 1}
			id.OperationID[0] = 10
			attempt := func(wantConflict bool) {
				t.Helper()
				if purpose == InventoryDirectoryDiscover {
					c, err := newScanDirectoryDiscoverCommand(id, s, s.Root, page, child)
					if err != nil {
						t.Fatal(err)
					}
					out, err := directoryExecute(t, w, c)
					if wantConflict {
						if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, scanDirectoryCommandResult{}) {
							t.Fatal("refused identity re-entered discovery", out, err)
						}
					} else if err != nil || out.Outcome.State != Applied {
						t.Fatal("different identity was blocked", out, err)
					}
				} else {
					c, err := newScanDirectoryRefuseCommand(id, s, s.Root, warnings, page, child)
					if err != nil {
						t.Fatal(err)
					}
					out, err := executeTerminal(t, w, c)
					if wantConflict {
						if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, scanDirectoryTerminalCommandResult{}) {
							t.Fatal("refused identity acquired a second pin", out, err)
						}
					} else if err != nil || out.Outcome.State != Applied {
						t.Fatal("different refused identity was blocked", out, err)
					}
				}
			}
			attempt(true)
			if after := directoryCounts(t, e); after != before {
				t.Fatal("alias conflict changed native effects", before, after)
			}
			after, afterWarnings, _ := directoryAliasSnapshot(t, e, s)
			if afterWarnings != beforeWarnings || after.Root.Skipped != beforeSkipped {
				t.Fatal("alias conflict added warning or skipped entry", afterWarnings, after.Root.Skipped)
			}
			var receipts, pins int64
			if err := e.db.QueryRow(`SELECT (SELECT count(*) FROM scan_command_receipts WHERE run_id=? AND refused_directory_identity IS NOT NULL),(SELECT count(*) FROM inventory_scan_issues WHERE job_id=? AND refused_entry_kind='directory')`, s.Original.RunID, s.Original.RunID).Scan(&receipts, &pins); err != nil || receipts != 1 || pins != 1 {
				t.Fatal("ACK/conflict lost the run-wide fence", receipts, pins, err)
			}
			// A different identity at that same path remains eligible. This
			// kills a blanket 'any previous refusal stops the run' workaround.
			page.Entries[0].ObjectIdentity, child.DirectoryIdentity = "different-child", "different-child"
			id.OperationID[0] = 11
			attempt(false)
			if authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
				t.Fatal("alias control leaked custody")
			}
		})
	}
}
