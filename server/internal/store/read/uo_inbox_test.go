package read

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Native registered-query component controls ONLY. This deliberately supplies
// no GV admission/current US authority/publication fence or qualification.
func uoInboxFixture(t *testing.T) (*browseFixture, uoInboxQuery, *[]stmt.ID) {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO notification_inbox(scope,audience,revision,updated_ms) VALUES('shared:admins','server-admin',6,200),('shared:admins','profile',1,200)`)
	f.exec(t, `INSERT INTO notification_records(sequence,id,scope,audience,severity,source,category,title,body,dedupe_key,revision,created_ms,updated_ms,expires_ms) VALUES(1,'a','shared:admins','server-admin','warning','storage','storage.low','空き容量','Low space','a',1,10,10,10000),(2,'c','shared:admins','server-admin','warning','storage','storage.low','C','Low space','c',2,10,10,10000),(3,'b','shared:admins','server-admin','info','storage','storage.low','B','Recovered','b',5,10,10,10000)`)
	f.exec(t, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,read_ms,revision) VALUES('principal:one:shared-admins',2,50,3)`)
	f.exec(t, `INSERT INTO notification_ack_heads(principal_key,read_through_sequence,revision,read_through_ms) VALUES('principal:one:shared-admins',3,6,200)`)
	defs := append(uoInboxDefinitions(), uoInboxCountDefinitions()...)
	for i := range defs {
		var args []any
		for _, p := range defs[i].Params {
			if p.Type == stmt.Text {
				args = append(args, "fixture")
			} else {
				args = append(args, int64(1))
			}
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "synthetic-component-only", Bindings: args}}
	}
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	var calls []stmt.ID
	query := func(id stmt.ID, args ...any) (out [][]any, err error) {
		calls = append(calls, id)
		statement, err := registry.Lookup(id)
		if err != nil {
			return nil, err
		}
		bound, err := statement.Bind(args...)
		if err != nil {
			return nil, err
		}
		rows, err := bound.QueryContext(t.Context(), f.conn)
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, rows.Close()) }()
		columns, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				return nil, err
			}
			out = append(out, values)
		}
		return out, rows.Err()
	}
	return f, query, &calls
}

func uoInboxTestInput() uoInboxInput {
	return uoInboxInput{scope: "shared:admins", audience: "server-admin", principal: "principal:one:shared-admins", state: "all", nowMS: 500, limit: 100, byteBudget: 64 << 10}
}

func TestUOInboxEffectiveReadAtAndSparseStates(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	q := uoInboxTestInput()
	page, err := readUOInbox(t.Context(), query, q)
	if err != nil || len(page.items) != 3 || page.counts.read != 3 || !page.exhausted {
		t.Fatal(page, err)
	}
	want := map[string]int64{"a": 200, "b": 200, "c": 50}
	for _, item := range page.items {
		if !item.readMS.Valid || item.readMS.Int64 != want[item.id] {
			t.Fatal("bulk/individual readAt", item)
		}
	}
	// A later read-all moves ONLY bulk timestamps. C remains an exact read.
	f.exec(t, `UPDATE notification_ack_heads SET revision=8,read_through_ms=300`)
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.items {
		ms := int64(300)
		if item.id == "c" {
			ms = 50
		}
		if item.readMS.Int64 != ms {
			t.Fatal(item)
		}
	}
	f.exec(t, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,archived_ms,revision) VALUES('principal:one:shared-admins',1,100,7)`)
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || page.counts.archived != 1 {
		t.Fatal(page, err)
	}
	for _, item := range page.items {
		if item.id == "a" && (item.readMS.Valid || !item.archivedMS.Valid) {
			t.Fatal("archived unread included in read-all", item)
		}
	}
	// A producer restatement's newer notice revision invalidates the old ACK.
	f.exec(t, `UPDATE notification_records SET revision=9 WHERE sequence=1`)
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || page.counts.unread != 1 || page.counts.archived != 0 {
		t.Fatal("restatement did not reset readAt", page, err)
	}
	// Another principal's acknowledgement and head are never consulted.
	q.principal = "principal:other:shared-admins"
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || page.counts.unread != 3 {
		t.Fatal("principal ACK leaked", page, err)
	}
}

func TestUOInboxEmptyAndUninitializedBulkHeads(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	q := uoInboxTestInput()
	f.exec(t, `UPDATE notification_ack_heads SET read_through_sequence=0,read_through_ms=0`)
	page, err := readUOInbox(t.Context(), query, q)
	if err != nil || page.counts.unread != 2 || page.counts.read != 1 {
		t.Fatal("empty head became bulk coverage", page, err)
	}
	f.exec(t, `UPDATE notification_ack_heads SET read_through_sequence=3`)
	if page, err := readUOInbox(t.Context(), query, q); !errors.Is(err, errUOInboxResync) || !reflect.DeepEqual(page, uoInboxPage{}) {
		t.Fatal("uninitialized bulk became epoch readAt", page, err)
	}
	f.exec(t, `UPDATE notification_ack_heads SET read_through_ms=200,archive_through_sequence=2`)
	if _, err := readUOInbox(t.Context(), query, q); !errors.Is(err, errUOInboxResync) {
		t.Fatal("unsupported archive-all silently read", err)
	}
}

func TestUOInboxHiddenExpiredAndRawCursor(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	q := uoInboxTestInput()
	q.limit = 2
	f.exec(t, `UPDATE notification_records SET withdrawn_ms=100 WHERE sequence=3`)
	f.exec(t, `UPDATE notification_records SET expires_ms=100 WHERE sequence=2`)
	q.state = "unread"
	page, err := readUOInbox(t.Context(), query, q)
	if err != nil || len(page.items) != 0 || page.rawRows != 2 || page.next != 2 || page.exhausted {
		t.Fatal("invisible page must advance its raw cursor", page, err)
	}
	probe, err := readUOInboxUnread(t.Context(), query, q)
	if err != nil || probe.found || probe.exhausted || probe.next != 2 {
		t.Fatal("partial unread probe claimed none", probe, err)
	}
	q.before = page.next
	f.exec(t, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,revision) VALUES('principal:one:shared-admins',1,7)`)
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || len(page.items) != 1 || page.items[0].id != "a" || !page.exhausted || page.items[0].readMS.Valid {
		t.Fatal("explicit unread row hidden", page, err)
	}
	probe, err = readUOInboxUnread(t.Context(), query, q)
	if err != nil || !probe.found || !probe.exhausted {
		t.Fatal(probe, err)
	}
	// The same prefix also carries the record under state=all.
	q.state = "all"
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || len(page.items) != 1 || page.items[0].id != "a" || !page.exhausted {
		t.Fatal("visible retained row hidden", page, err)
	}
}

func TestUOInboxAudienceChangeNeverLeaks(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	q := uoInboxTestInput()
	q.limit = 2
	first, err := readUOInbox(t.Context(), query, q)
	if err != nil || len(first.items) != 2 || first.items[0].id != "b" || first.exhausted {
		t.Fatal("first page", first, err)
	}
	// A record that moves to another audience mid-traversal is no longer part of
	// this authorized inbox, and its old raw position is not replayed.
	f.exec(t, `UPDATE notification_records SET audience='profile' WHERE sequence=3`)
	q.before = first.next
	second, err := readUOInbox(t.Context(), query, q)
	if err != nil || len(second.items) != 1 || second.items[0].id != "a" || !second.exhausted {
		t.Fatal("audience change leaked or stalled", second, err)
	}
	// The record is visible only in its new audience, still as one shared row.
	q.audience = "profile"
	q.before, q.state = 0, "all"
	moved, err := readUOInbox(t.Context(), query, q)
	if err != nil || len(moved.items) != 1 || moved.items[0].id != "b" {
		t.Fatal("moved record lost", moved, err)
	}
	var shared int
	if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM notification_records WHERE id='b'`).Scan(&shared); err != nil || shared != 1 {
		t.Fatal("audience move copied a record", shared, err)
	}
}

func TestUOInboxActualOctetsBeforePayload(t *testing.T) {
	_, query, calls := uoInboxFixture(t)
	q := uoInboxTestInput()
	q.limit = 1
	page, err := readUOInbox(t.Context(), query, q)
	if err != nil || page.actualBytes <= 0 {
		t.Fatal(page, err)
	}
	q.byteBudget = page.actualBytes - 1
	*calls = nil
	page, err = readUOInbox(t.Context(), query, q)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, uoInboxPage{}) {
		t.Fatal("oversize payload copied", page, err)
	}
	for _, id := range *calls {
		if id == uoInboxPayload {
			t.Fatal("payload selected before admission")
		}
	}
	// Admit exactly the observed byte width. UTF-8 octets, not rune counts.
	q.byteBudget++
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || page.actualBytes != q.byteBudget {
		t.Fatal(page, err)
	}
	q = uoInboxTestInput()
	q.before = 2
	q.limit = 1
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || len(page.items) != 1 || page.items[0].title != "空き容量" {
		t.Fatal(page, err)
	}
	actual := int64(len(page.items[0].id) + len(page.items[0].audience) + len(page.items[0].severity) + len(page.items[0].source) + len(page.items[0].category) + len(page.items[0].title) + len(page.items[0].body) + len(page.items[0].arguments) + len(page.items[0].actions) + len(page.items[0].dedupe))
	if page.actualBytes != actual {
		t.Fatal("not actual UTF8 octets", page.actualBytes, actual)
	}
}

func TestUOInboxOperationStatusOneSharedRecord(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	f.exec(t, `INSERT INTO notice_broadcast_work(scope,operation_id,digest,upper_account,upper_profile,last_account,delivered,created,updated,revision,ids,expires_ms) VALUES('shared:admins','op','digest','uo.shared.v1','server-admin','complete',1,1,0,6,'["a"]',1000)`)
	status, err := readUOInboxOperation(t.Context(), query, "shared:admins", "server-admin", "op", 500)
	if err != nil || status.id != "a" || !status.created || status.revision != 6 {
		t.Fatal(status, err)
	}
	var rows int
	if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM notice_broadcast_work`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("operation expanded into recipient rows", rows, err)
	}
	if _, err = readUOInboxOperation(t.Context(), query, "shared:admins", "server-admin", "op", 1000); !errors.Is(err, ErrNotReady) {
		t.Fatal("expired receipt disclosed", err)
	}
	if _, err = readUOInboxOperation(t.Context(), query, "other:scope", "server-admin", "op", 500); !errors.Is(err, ErrNotReady) {
		t.Fatal("operation scope leaked", err)
	}
	if _, err = readUOInboxOperation(t.Context(), query, "shared:admins", "profile", "op", 500); !errors.Is(err, errUOInboxResync) {
		t.Fatal("audience mismatch accepted", err)
	}
	f.exec(t, `UPDATE notice_broadcast_work SET upper_account='old-recipient-cursor'`)
	if _, err = readUOInboxOperation(t.Context(), query, "shared:admins", "server-admin", "op", 500); !errors.Is(err, errUOInboxResync) {
		t.Fatal("legacy recipient work claimed completed", err)
	}
}

func TestUOInboxRawPageCapPlusOne(t *testing.T) {
	_, query, _ := uoInboxFixture(t)
	q := uoInboxTestInput()
	wide := func(id stmt.ID, args ...any) ([][]any, error) {
		if id == uoInboxFirstOctets {
			return make([][]any, q.limit+2), nil
		}
		return query(id, args...)
	}
	page, err := readUOInbox(t.Context(), wide, q)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, uoInboxPage{}) {
		t.Fatal("raw window over its cap accepted", page, err)
	}
}

func TestUOInboxCancellationAndInputBounds(t *testing.T) {
	_, query, _ := uoInboxFixture(t)
	q := uoInboxTestInput()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	page, err := readUOInbox(ctx, query, q)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(page, uoInboxPage{}) {
		t.Fatal(page, err)
	}
	for _, edit := range []func(*uoInboxInput){func(q *uoInboxInput) { q.limit = 101 }, func(q *uoInboxInput) { q.byteBudget = 65537 }, func(q *uoInboxInput) { q.state = "archive-all" }, func(q *uoInboxInput) { q.nowMS = 0 }} {
		copy := q
		edit(&copy)
		page, err = readUOInbox(t.Context(), query, copy)
		if !errors.Is(err, ErrInput) || !reflect.DeepEqual(page, uoInboxPage{}) {
			t.Fatal(page, err)
		}
	}
	// Cancellation after the payload query must erase already copied draft data.
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	wrapped := func(id stmt.ID, args ...any) ([][]any, error) {
		rows, e := query(id, args...)
		if id == uoInboxPayload {
			cancel()
		}
		return rows, e
	}
	page, err = readUOInbox(ctx, wrapped, q)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(page, uoInboxPage{}) {
		t.Fatal("late cancellation leaked draft", page, err)
	}
}

func TestUOInboxRequiredNoticeIndexes(t *testing.T) {
	f, _, _ := uoInboxFixture(t)
	// The page window, the retained/live and resume lookups, the expiry cleanup
	// and the two bounded maintenance probes are backed by named indexes.
	for _, index := range []string{"notification_records_page", "notification_records_live", "notification_records_resume", "notification_records_expiry", "notification_ack_notice", "notice_broadcast_work_governor_due"} {
		var found int
		if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&found); err != nil || found != 1 {
			t.Fatal("missing notice index", index, found, err)
		}
	}
}

func TestUOInboxSeekPlans(t *testing.T) {
	f, _, _ := uoInboxFixture(t)
	bindings := map[stmt.ID][]any{
		uoInboxHead: {"shared:admins", "server-admin"}, uoInboxBulk: {"principal:one:shared-admins"},
		uoInboxFirstOctets:     {"principal:one:shared-admins", "shared:admins", "server-admin", int64(101)},
		uoInboxTailOctets:      {"principal:one:shared-admins", "shared:admins", "server-admin", int64(3), int64(101)},
		uoInboxPayload:         {"shared:admins", "server-admin", int64(3), int64(1), int64(100)},
		uoInboxOperationOctets: {"shared:admins", "op"}, uoInboxOperationPoint: {"shared:admins", "op"},
		uoInboxCountFirst: {"principal:one:shared-admins", "shared:admins", "server-admin", int64(101)},
		uoInboxCountTail:  {"principal:one:shared-admins", "shared:admins", "server-admin", int64(3), int64(101)},
	}
	for _, d := range append(uoInboxDefinitions(), uoInboxCountDefinitions()...) {
		t.Run(string(d.ID), func(t *testing.T) {
			rows, err := f.conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, bindings[d.ID]...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plans []string
			for rows.Next() {
				var a, b, c int
				var detail string
				if err := rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				plans = append(plans, detail)
				if strings.Contains(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") {
					t.Fatal("unbounded read plan", detail)
				}
			}
			if err = rows.Err(); err != nil || len(plans) == 0 {
				t.Fatal(plans, err)
			}
			if d.ID == uoInboxFirstOctets || d.ID == uoInboxTailOctets || d.ID == uoInboxPayload || d.ID == uoInboxCountFirst || d.ID == uoInboxCountTail {
				if !strings.Contains(strings.Join(plans, " "), "notification_records_page") {
					t.Fatal("page seek not forced", plans)
				}
			}
		})
	}
}
