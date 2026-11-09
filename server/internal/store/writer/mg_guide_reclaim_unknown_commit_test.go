package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// A lost COMMIT reply is never success: the private effect's returned value is
// not Applied evidence, the writer is fenced, and the durable state is read back
// through a NEW read-only connection. The child page is all-or-nothing, never a
// partially deleted range.
func TestMGGuideReclaimUnknownCommitNeverSuccess(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_commit", true: "after_commit"}[after], func(t *testing.T) {
			w, path, op := mgReclaimStagedFixture(t, 2, 10, 1)
			witness := mgReclaimWitness(7, "old", false)
			_, op = mgReclaimDiscard(t, w, op, witness)
			committed := mgReclaimReadDurable(t, mgReclaimReader(t, path), op.request)

			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: connector, replyError: true, afterCommit: after}
			installFaultPool(w, fault)

			turn := mgReclaimTurn(t, w, MGGuideReclaim)
			out, err := mgReclaimGuideNative(t.Context(), turn, mgGuideReclaimInput{request: op.request, identity: mgGuideIdentityAfter(op), witness: witness})
			if err != nil || out.removed != 8 {
				t.Fatal("slice did not reach COMMIT", out, err)
			}
			_ = turn.closeRows()
			if _, err := turn.transaction.commit(); !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCommitReply) {
				t.Fatal("uncertain COMMIT reported success", err)
			}
			if !w.ambiguous || !w.fenced {
				t.Fatal("uncertain writer was not fenced")
			}

			// Read the physical truth through the fresh read-only connection.
			got := mgReclaimReadDurable(t, mgReclaimReader(t, path), op.request)
			if after {
				if got.programmes != 2 || got.channels != 2 || got.choices != 1 ||
					got.cursor != "programme-07" || got.sequence != committed.sequence+1 {
					t.Fatal("committed slice is not whole", got, committed)
				}
			} else if got != committed {
				t.Fatal("uncommitted slice leaked", got, committed)
			}
		})
	}
}
