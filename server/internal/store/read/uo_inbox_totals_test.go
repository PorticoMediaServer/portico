package read

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestUOInboxTotalsExactCountsWithoutPayload(t *testing.T) {
	f, base, _ := uoInboxFixture(t)
	f.exec(t, `UPDATE notification_acknowledgments SET read_ms=NULL,archived_ms=50,revision=7 WHERE notice_sequence=2`)
	f.exec(t, `UPDATE notification_records SET body=?,expires_ms=100 WHERE sequence=1`, strings.Repeat("x", 70000))
	f.exec(t, `INSERT INTO notification_records(sequence,id,scope,audience,severity,source,category,title,body,dedupe_key,revision,created_ms,updated_ms,expires_ms) VALUES(4,'d','shared:admins','server-admin','info','storage','storage.low','D','New','d',7,300,300,10000)`)
	payload := false
	query := func(id stmt.ID, args ...any) ([][]any, error) {
		if id == uoInboxPayload {
			payload = true
		}
		return base(id, args...)
	}
	q := uoInboxInput{scope: "shared:admins", audience: "server-admin", principal: "principal:one:shared-admins", state: "all", nowMS: 300, limit: 40, byteBudget: 64 << 10}
	totals, err := readUOInboxTotals(t.Context(), query, q)
	if err != nil || payload || totals.total != 3 || totals.counts != (uoInboxCounts{read: 1, archived: 1, unread: 1}) || totals.rawRows != 4 {
		t.Fatal(totals, payload, err)
	}
	// A closure spanning different snapshots must never publish mixed totals.
	loads := 0
	drift := func(id stmt.ID, args ...any) ([][]any, error) {
		rows, err := base(id, args...)
		if id == uoInboxHead {
			loads++
			if loads == 2 && len(rows) == 1 {
				rows[0][0] = int64(999)
			}
		}
		return rows, err
	}
	if out, err := readUOInboxTotals(t.Context(), drift, q); !errors.Is(err, ErrStale) || out != (uoInboxTotals{}) {
		t.Fatal("mixed snapshot totals", out, err)
	}
}

func TestUOInboxTotalsRefusesAckChangeMidTraversal(t *testing.T) {
	f, base, _ := uoInboxFixture(t)
	// Three full scalar windows: the ACK change lands after the first, on a row
	// already counted, which would otherwise publish a torn read/unread split.
	f.exec(t, `INSERT INTO notification_records(sequence,id,scope,audience,severity,source,category,title,body,dedupe_key,revision,created_ms,updated_ms,expires_ms)
WITH RECURSIVE n(x) AS (VALUES(100) UNION ALL SELECT x+1 FROM n WHERE x<349)
SELECT x,'x'||x,'shared:admins','server-admin','info','storage','storage.low','T','B','x'||x,1,10,10,10000 FROM n`)
	f.exec(t, `INSERT INTO notification_revision(singleton,value) VALUES(1,300)`)
	changed := false
	query := func(id stmt.ID, args ...any) ([][]any, error) {
		if id == uoInboxCountTail && !changed {
			changed = true
			// One individual ACK write reserves the next shared ordinal and marks
			// sequence 300 read after its window was already counted.
			f.exec(t, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,read_ms,revision) VALUES('principal:one:shared-admins',300,50,9)`)
			f.exec(t, `UPDATE notification_revision SET value=301 WHERE singleton=1`)
		}
		return base(id, args...)
	}
	if out, err := readUOInboxTotals(t.Context(), query, uoInboxTestInput()); !errors.Is(err, ErrStale) || out != (uoInboxTotals{}) {
		t.Fatal("torn ACK total published", out, err)
	}
	// Control: the same stored state without a concurrent ACK write is exact.
	totals, err := readUOInboxTotals(t.Context(), base, uoInboxTestInput())
	if err != nil || totals.total != 253 || totals.rawRows != 253 || totals.counts != (uoInboxCounts{read: 4, unread: 249}) {
		t.Fatal(totals, err)
	}
}

func TestUOInboxTotalsRefusesOverRetainedBound(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	// One row beyond the published retained cohort. The reader binds one extra
	// row per 100-row window and refuses rather than publishing a partial total.
	f.exec(t, `INSERT INTO notification_records(sequence,id,scope,audience,severity,source,category,title,body,dedupe_key,revision,created_ms,updated_ms,expires_ms)
WITH RECURSIVE n(x) AS (VALUES(100) UNION ALL SELECT x+1 FROM n WHERE x<10100)
SELECT x,'x'||x,'shared:admins','server-admin','info','storage','storage.low','T','B','x'||x,1,10,10,10000 FROM n`)
	q := uoInboxTestInput()
	if out, err := readUOInboxTotals(t.Context(), query, q); !errors.Is(err, ErrUnqualified) || out != (uoInboxTotals{}) {
		t.Fatal("over-bound totals published", out, err)
	}
}
