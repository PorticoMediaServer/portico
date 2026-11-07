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
	f.exec(t, `ALTER TABLE notification_ack_heads ADD COLUMN read_through_ms INTEGER NOT NULL DEFAULT 0 CHECK(read_through_ms>=0)`)
	f.exec(t, `INSERT INTO notification_inbox(scope,audience,revision,updated_ms) VALUES('shared:admins','server-admin',6,200)`)
	f.exec(t, `INSERT INTO notification_records(sequence,id,scope,audience,severity,source,category,title,body,dedupe_key,revision,created_ms,updated_ms,expires_ms) VALUES(1,'a','shared:admins','server-admin','warning','storage','storage.low','空き容量','Low space','a',1,10,10,10000),(2,'c','shared:admins','server-admin','warning','storage','storage.low','C','Low space','c',2,10,10,10000),(3,'b','shared:admins','server-admin','info','storage','storage.low','B','Recovered','b',5,10,10,10000)`)
	f.exec(t, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,read_ms,revision) VALUES('principal:one:shared-admins',2,50,3)`)
	f.exec(t, `INSERT INTO notification_ack_heads(principal_key,read_through_sequence,revision,read_through_ms) VALUES('principal:one:shared-admins',3,6,200)`)
	defs := uoInboxDefinitions()
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
func TestUOInboxNativeTwoBulkAndSparseStates(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	q := uoInboxTestInput()
	page, err := readUOInbox(t.Context(), query, q)
	if err != nil || len(page.items) != 3 || page.counts.read != 3 || !page.exhausted {
		t.Fatal(page, err)
	}
	want := map[string]int64{"a": 200, "b": 200, "c": 50}
	for _, item := range page.items {
		if !item.readMS.Valid || item.readMS.Int64 != want[item.id] {
			t.Fatal("bulk/individual timestamp", item)
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
	f.exec(t, `UPDATE notification_records SET revision=9 WHERE sequence=1`)
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || page.counts.unread != 1 || page.counts.archived != 0 {
		t.Fatal("restatement did not reset ACK", page, err)
	}
	q.principal = "principal:other:shared-admins"
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || page.counts.unread != 3 {
		t.Fatal("principal ACK leaked", page, err)
	}
	q.scope = "other:scope"
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || len(page.items) != 0 || !page.exhausted {
		t.Fatal("scope leak", page, err)
	}
}
func TestUOInboxNativeEmptyAndUninitializedBulkHeads(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	q := uoInboxTestInput()
	f.exec(t, `UPDATE notification_ack_heads SET read_through_sequence=0,read_through_ms=0`)
	page, err := readUOInbox(t.Context(), query, q)
	if err != nil || page.counts.unread != 2 || page.counts.read != 1 {
		t.Fatal("empty head became bulk coverage", page, err)
	}
	f.exec(t, `UPDATE notification_ack_heads SET read_through_sequence=3`)
	page, err = readUOInbox(t.Context(), query, q)
	if !errors.Is(err, errUOInboxResync) || !reflect.DeepEqual(page, uoInboxPage{}) {
		t.Fatal("uninitialized bulk became epoch readAt", page, err)
	}
	f.exec(t, `UPDATE notification_ack_heads SET read_through_ms=200,archive_through_sequence=2`)
	_, err = readUOInbox(t.Context(), query, q)
	if !errors.Is(err, errUOInboxResync) {
		t.Fatal("unsupported archive-all silently read", err)
	}
}
func TestUOInboxNativeRawKeysetAndUnread(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	q := uoInboxTestInput()
	q.limit = 2
	f.exec(t, `UPDATE notification_records SET withdrawn_ms=100 WHERE sequence=3`)
	f.exec(t, `UPDATE notification_records SET expires_ms=100 WHERE sequence=2`)
	q.state = "unread"
	page, err := readUOInbox(t.Context(), query, q)
	if err != nil || len(page.items) != 0 || page.rawRows != 2 || page.next != 2 || page.exhausted {
		t.Fatal("invisible page must advance", page, err)
	}
	probe, err := readUOInboxUnread(t.Context(), query, q)
	if err != nil || probe.found || probe.exhausted || probe.next != 2 {
		t.Fatal("partial unread probe claimed none", probe, err)
	}
	q.before = page.next
	f.exec(t, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,revision) VALUES('principal:one:shared-admins',1,7)`)
	page, err = readUOInbox(t.Context(), query, q)
	if err != nil || len(page.items) != 1 || page.items[0].id != "a" || !page.exhausted {
		t.Fatal(page, err)
	}
	probe, err = readUOInboxUnread(t.Context(), query, q)
	if err != nil || !probe.found || !probe.exhausted {
		t.Fatal(probe, err)
	}
}
func TestUOInboxNativeActualOctetsBeforePayload(t *testing.T) {
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
func TestUOInboxNativeOperationStatus(t *testing.T) {
	f, query, _ := uoInboxFixture(t)
	f.exec(t, `INSERT INTO notice_broadcast_work(scope,operation_id,digest,upper_account,upper_profile,last_account,delivered,created,updated,revision,ids,expires_ms) VALUES('shared:admins','op','digest','uo.shared.v1','server-admin','complete',1,1,0,6,'["a"]',1000)`)
	status, err := readUOInboxOperation(t.Context(), query, "shared:admins", "server-admin", "op", 500)
	if err != nil || status.id != "a" || !status.created || status.revision != 6 {
		t.Fatal(status, err)
	}
	_, err = readUOInboxOperation(t.Context(), query, "shared:admins", "server-admin", "op", 1000)
	if !errors.Is(err, ErrNotReady) {
		t.Fatal("expired receipt disclosed", err)
	}
	_, err = readUOInboxOperation(t.Context(), query, "other:scope", "server-admin", "op", 500)
	if !errors.Is(err, ErrNotReady) {
		t.Fatal("operation scope leaked", err)
	}
	f.exec(t, `UPDATE notice_broadcast_work SET upper_account='old-recipient-cursor'`)
	_, err = readUOInboxOperation(t.Context(), query, "shared:admins", "server-admin", "op", 500)
	if !errors.Is(err, errUOInboxResync) {
		t.Fatal("legacy recipient work claimed completed", err)
	}
}
func TestUOInboxNativeCancellationAndInputBounds(t *testing.T) {
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
	wrapped := func(id stmt.ID, args ...any) ([][]any, error) {
		rows, e := query(id, args...)
		if id == uoInboxPayload {
			cancel()
		}
		return rows, e
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	page, err = readUOInbox(ctx, wrapped, q)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(page, uoInboxPage{}) {
		t.Fatal("late cancellation leaked draft", page, err)
	}
}
func TestUOInboxNativeSeekPlans(t *testing.T) {
	f, _, _ := uoInboxFixture(t)
	bindings := map[stmt.ID][]any{
		uoInboxHead: {"shared:admins", "server-admin"}, uoInboxBulk: {"principal:one:shared-admins"},
		uoInboxFirstOctets:     {"principal:one:shared-admins", "shared:admins", "server-admin", int64(100)},
		uoInboxTailOctets:      {"principal:one:shared-admins", "shared:admins", "server-admin", int64(3), int64(100)},
		uoInboxPayload:         {"shared:admins", "server-admin", int64(3), int64(1), int64(100)},
		uoInboxOperationOctets: {"shared:admins", "op"}, uoInboxOperationPoint: {"shared:admins", "op"},
	}
	for _, d := range uoInboxDefinitions() {
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
		})
	}
}
