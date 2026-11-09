package writer

import (
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The closed statement inventory a seal or publication frame compiles: the
// bounded staging components the operation was created from plus this task's
// point effects. The staging inventory keeps its own exact plan capture.
func mgGuidePublicationTestDefinitions() []stmt.Definition {
	return append(mgGuideDefinitions(), mgGuidePublicationDefinitions()...)
}

// The native file behind the writer pool, so a test can observe durability
// through a connection the writer never opened.
func mgGuideDatabasePath(t *testing.T, w *executor) string {
	t.Helper()
	rows, err := w.db.QueryContext(t.Context(), `SELECT name,file FROM pragma_database_list`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, file string
		if err := rows.Scan(&name, &file); err != nil {
			t.Fatal(err)
		}
		if name == "main" {
			if file == "" {
				t.Fatal("fixture database is not a native file")
			}
			return file
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("no main database")
	return ""
}

// A genuinely separate read-only connection, never the writer pool or a reopened
// writer. Real-clock assertions are absent: the reader only proves durable state.
func mgGuideFreshReader(t *testing.T, path string) *sql.DB {
	t.Helper()
	reader, _, err := sqliteconn.Open(t.Context(), path, sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

// Completes one guide birth and every bounded stage, ACKing each original result.
func mgGuideCompleteCommitted(t *testing.T, w *executor, b mgGuideBirth, stages []mgGuideFrozenStage) mgGuideNative {
	t.Helper()
	before := mgGuideBeginCommitted(t, w, b)
	for _, stage := range stages {
		_, before, _ = mgGuideStageCommitted(t, w, before, stage)
	}
	if before.progress.staged != before.progress.manifest.expected || before.progress.chain != before.progress.manifest.expectedChain {
		t.Fatal("incomplete guide", before.progress)
	}
	return before
}

// Seals a complete staging operation, saves and ACKs the original seal receipt,
// commits, and returns the durable receipt plus the post-reopen native row.
func mgGuideSealCommitted(t *testing.T, w *executor, before mgGuideNative) (mgGuideReceipt, mgGuideNative) {
	t.Helper()
	defs := mgGuidePublicationTestDefinitions()
	identity := mgGuideIdentityAfter(before)
	turn := mgGuideTestTurn(t, w, MGGuideSeal, defs)
	result, err := mgSealGuideNative(t.Context(), turn, before, identity)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptSeal, mgGuideProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	acked, err := mgACKGuideReceipt(t.Context(), turn, result.operation, saved.key, saved.resultDigest)
	if err != nil {
		t.Fatal(err)
	}
	mgGuideCommit(t, turn)
	reopen := mgGuideTestTurn(t, w, MGGuideSeal, defs)
	after, err := mgReadGuideNative(t.Context(), reopen, before.request)
	if err != nil {
		t.Fatal(err)
	}
	mgGuideRollback(t, reopen)
	if after != acked {
		t.Fatal("committed seal did not read back", after, acked)
	}
	return saved, after
}

// Publishes one sealed generation, saves and ACKs the original publication
// receipt, commits, and returns the durable receipt plus the effect result.
func mgGuidePublishCommitted(t *testing.T, w *executor, before mgGuideNative, refresh bool) (mgGuideReceipt, mgGuideNativeResult) {
	t.Helper()
	defs := mgGuidePublicationTestDefinitions()
	identity := mgGuideIdentityAfter(before)
	turn := mgGuideTestTurn(t, w, MGGuidePublish, defs)
	result, err := mgPublishGuideNative(t.Context(), turn, before, identity, refresh)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptPublish, mgGuideProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	acked, err := mgACKGuideReceipt(t.Context(), turn, result.operation, saved.key, saved.resultDigest)
	if err != nil {
		t.Fatal(err)
	}
	mgGuideCommit(t, turn)
	reopen := mgGuideTestTurn(t, w, MGGuidePublish, defs)
	after, err := mgReadGuideNative(t.Context(), reopen, before.request)
	if err != nil {
		t.Fatal(err)
	}
	head, err := mgGuideCurrentHead(t.Context(), reopen, before.progress.manifest.source)
	if err != nil {
		t.Fatal(err)
	}
	mgGuideRollback(t, reopen)
	if after != acked || head != result.head {
		t.Fatal("committed publication did not read back", after, acked, head, result.head)
	}
	durable := result
	durable.operation = after
	durable.head = head
	return saved, durable
}
