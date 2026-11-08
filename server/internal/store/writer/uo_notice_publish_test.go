package writer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

func uoNoticePublishFixture(t *testing.T) *executor {
	t.Helper()
	return wc008NativeExecutor(t)
}

// Synthetic component custody only. This does not register/authorize a UO
// command, run receipt classification or supply production forecasts.
func uoNoticePublishTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	defs := append(uoNoticePublishDefinitions(), wcAPIEventDefinitions()...)
	return wc008NativeTurn(t, w, PublishNotice, defs)
}

func uoNoticePublishCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func uoNoticeDraft(id string) uoNoticePublish {
	return uoNoticePublish{
		view: uoNoticeView{scope: "shared:admins", audience: uoNoticeAudienceServerAdm},
		id:   id, severity: notify.SeverityWarning, source: "storage", category: "storage.low",
		title: "Storage low", body: "Free space is low", dedupe: id,
		nowMS: 10, expiresMS: 10000, authorityRevision: 1,
	}
}

func uoNoticePublishOne(t *testing.T, w *executor, q uoNoticePublish) uoNoticeEffect {
	t.Helper()
	turn := uoNoticePublishTurn(t, w)
	out, err := applyUONoticePublish(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	uoNoticePublishCommit(t, turn)
	return out
}

func uoNoticeWithdrawOne(t *testing.T, w *executor, q uoNoticeWithdraw) uoNoticeEffect {
	t.Helper()
	turn := uoNoticePublishTurn(t, w)
	out, err := applyUONoticeWithdraw(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	uoNoticePublishCommit(t, turn)
	return out
}

// Plain SQL over the disposable fixture: the independent oracle for record,
// inbox-head and event effects.
func uoNoticeSQLCounts(t *testing.T, w *executor) (records, inbox, revision, events, operations int64) {
	t.Helper()
	err := w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT count(*) FROM notification_records),
 (SELECT count(*) FROM notification_inbox),
 COALESCE((SELECT value FROM notification_revision WHERE singleton=1),0),
 (SELECT count(*) FROM api_events),
 (SELECT count(*) FROM notice_broadcast_work)`).Scan(&records, &inbox, &revision, &events, &operations)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestUONoticePublishCreateDedupeAndRestateAuthority(t *testing.T) {
	w := uoNoticePublishFixture(t)
	q := uoNoticeDraft("a")
	first := uoNoticePublishOne(t, w, q)
	if !first.created || !first.changed || first.id != "a" || first.sequence <= 0 || first.revision != 1 || first.authorityRevision != 1 || first.inboxRevision != 1 || first.withdrawn {
		t.Fatal("create effect", first)
	}
	if records, inbox, revision, events, _ := uoNoticeSQLCounts(t, w); records != 1 || inbox != 1 || revision != 1 || events != 1 {
		t.Fatal("create facts", records, inbox, revision, events)
	}
	var storedID, storedDedupe, storedBody string
	var storedRevision, storedAuthority int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT id,dedupe_key,body,revision,authority_revision FROM notification_records`).Scan(&storedID, &storedDedupe, &storedBody, &storedRevision, &storedAuthority); err != nil || storedID != "a" || storedDedupe != "a" || storedBody != q.body || storedRevision != 1 || storedAuthority != 1 {
		t.Fatal("stored record", storedID, storedDedupe, storedBody, storedRevision, storedAuthority, err)
	}
	// A restatement under a changed source authority revision is the explicit
	// way a shared record moves forward; a stale witness cannot overwrite it.
	restated := q
	restated.expectedRevision, restated.expectedAuthorityRevision = first.revision, first.authorityRevision
	restated.authorityRevision = 2
	restated.body = "Free space recovered"
	restated.nowMS = 20
	out := uoNoticePublishOne(t, w, restated)
	if out.created || !out.changed || out.id != "a" || out.sequence != first.sequence || out.revision != 2 || out.authorityRevision != 2 || out.inboxRevision != 2 {
		t.Fatal("restatement effect", out)
	}
	if records, inbox, revision, events, _ := uoNoticeSQLCounts(t, w); records != 1 || inbox != 1 || revision != 2 || events != 2 {
		t.Fatal("restatement kept one shared record and head", records, inbox, revision, events)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT body,revision,authority_revision FROM notification_records`).Scan(&storedBody, &storedRevision, &storedAuthority); err != nil || storedBody != restated.body || storedRevision != 2 || storedAuthority != 2 {
		t.Fatal("restated record", storedBody, storedRevision, storedAuthority, err)
	}
	var headRevision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM notification_inbox WHERE scope=? AND audience=?`, q.view.scope, q.view.audience).Scan(&headRevision); err != nil || headRevision != 2 {
		t.Fatal("durable inbox head", headRevision, err)
	}
	// A stale authority/record witness is refused and rolls back the turn.
	stale := restated
	stale.expectedAuthorityRevision = 1
	stale.body = "stale"
	turn := uoNoticePublishTurn(t, w)
	if out, err := applyUONoticePublish(t.Context(), turn, stale); !errors.Is(err, ErrConflict) || out != (uoNoticeEffect{}) {
		t.Fatal("stale restatement", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT body FROM notification_records`).Scan(&storedBody); err != nil || storedBody != "Free space recovered" {
		t.Fatal("stale restatement leaked", storedBody, err)
	}
}

func TestUONoticePublishExactRetryWritesNoSecondHint(t *testing.T) {
	w := uoNoticePublishFixture(t)
	q := uoNoticeDraft("a")
	first := uoNoticePublishOne(t, w, q)
	records, inbox, revision, events, _ := uoNoticeSQLCounts(t, w)
	elsewhere := uoNoticeDraft("a")
	turn := uoNoticePublishTurn(t, w)
	retry, err := applyUONoticePublish(t.Context(), turn, elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if retry.changed || retry.created || retry.id != first.id || retry.sequence != first.sequence || retry.revision != first.revision || retry.authorityRevision != first.authorityRevision || retry.event != (wcAPIEventEffect{}) {
		t.Fatal("exact retry was a new hint", retry)
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if again, inboxAgain, revisionAgain, eventsAgain, _ := uoNoticeSQLCounts(t, w); again != records || inboxAgain != inbox || revisionAgain != revision || eventsAgain != events {
		t.Fatal("exact retry wrote native state", again, inboxAgain, revisionAgain, eventsAgain)
	}
}

func TestUONoticePublishWithdrawalIdempotentAndScoped(t *testing.T) {
	w := uoNoticePublishFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	withdraw := uoNoticeWithdraw{view: uoNoticeDraft("a").view, id: "a", expectedRevision: a.revision, authorityRevision: a.authorityRevision, nowMS: 100}
	out := uoNoticeWithdrawOne(t, w, withdraw)
	if !out.changed || !out.withdrawn || out.revision != 2 || out.authorityRevision != 1 || out.inboxRevision != 2 {
		t.Fatal("withdraw effect", out)
	}
	_, _, revision, events, _ := uoNoticeSQLCounts(t, w)
	again := uoNoticeWithdrawOne(t, w, uoNoticeWithdraw{view: withdraw.view, id: "a", expectedRevision: out.revision, authorityRevision: 1, nowMS: 200})
	if again.changed || !again.withdrawn || again.revision != out.revision || again.event != (wcAPIEventEffect{}) {
		t.Fatal("withdrawal was not idempotent", again)
	}
	if _, _, revisionAfter, eventsAfter, _ := uoNoticeSQLCounts(t, w); revisionAfter != revision || eventsAfter != events {
		t.Fatal("idempotent withdrawal wrote state", revisionAfter, eventsAfter)
	}
	var withdrawn int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM notification_records WHERE withdrawn_ms IS NOT NULL`).Scan(&withdrawn); err != nil || withdrawn != 1 {
		t.Fatal("record not withdrawn", withdrawn, err)
	}
	// A stale witness or another inbox can never withdraw this record.
	for _, wrong := range []uoNoticeWithdraw{
		{view: withdraw.view, id: "a", expectedRevision: 1, authorityRevision: 1, nowMS: 300},
		{view: uoNoticeView{scope: "other:scope", audience: uoNoticeAudienceServerAdm}, id: "a", expectedRevision: out.revision, authorityRevision: 1, nowMS: 300},
		{view: withdraw.view, id: "missing", expectedRevision: out.revision, authorityRevision: 1, nowMS: 300},
	} {
		turn := uoNoticePublishTurn(t, w)
		if got, err := applyUONoticeWithdraw(t.Context(), turn, wrong); !errors.Is(err, ErrConflict) || got != (uoNoticeEffect{}) {
			t.Fatal("unscoped/stale withdrawal", wrong, got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM notification_records WHERE withdrawn_ms IS NULL`).Scan(&withdrawn); err != nil || withdrawn != 0 {
		t.Fatal("bad withdrawal resurrected a record", withdrawn, err)
	}
	// Only an explicit restatement reinstates, as a new revision and hint.
	revive := uoNoticeDraft("a")
	revive.expectedRevision, revive.expectedAuthorityRevision = out.revision, 1
	revive.nowMS = 400
	restated := uoNoticePublishOne(t, w, revive)
	if restated.withdrawn || !restated.changed || restated.revision != 3 {
		t.Fatal("explicit restatement", restated)
	}
	var live int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM notification_records WHERE withdrawn_ms IS NULL`).Scan(&live); err != nil || live != 1 {
		t.Fatal("restatement did not clear only its own withdrawal", live, err)
	}
}

func uoNoticeWideArguments(count, width int) map[string]string {
	out := make(map[string]string, count)
	for i := 0; i < count; i++ {
		out[fmt.Sprintf("arg%02d", i)] = strings.Repeat("v", width)
	}
	return out
}

func uoNoticeWideActions(count int) []notify.Action {
	out := make([]notify.Action, 0, count)
	for i := 0; i < count; i++ {
		arguments := make(map[string]string, 8)
		for j := 0; j < 8; j++ {
			arguments[fmt.Sprintf("key%d", j)] = strings.Repeat("a", 200)
		}
		out = append(out, notify.Action{Kind: "navigate", Label: "Open", Target: &notify.NavigateTarget{View: "home"}, Arguments: arguments})
	}
	return out
}

func TestUONoticePublishRecordByteCap(t *testing.T) {
	w := uoNoticePublishFixture(t)
	near := uoNoticeDraft("wide")
	near.severity = notify.SeverityInfo
	near.title = strings.Repeat("t", uoNoticeMaxTitleBytes)
	near.body = strings.Repeat("b", uoNoticeMaxBodyBytes)
	near.arguments = uoNoticeWideArguments(11, uoNoticeMaxArgumentBytes)
	near.actions = uoNoticeWideActions(1)
	if uoNoticeRecordBytes(near.view, near.id, near.severity, near.source, near.category, near.title, near.body, near.dedupe, mustUONoticeJSON(t, near.arguments, near.actions)) > uoNoticeMaxRecordBytes {
		t.Fatal("near-cap fixture exceeds the cap before admission")
	}
	accepted := uoNoticePublishOne(t, w, near)
	if !accepted.changed {
		t.Fatal("near-cap record refused", accepted)
	}
	var stored int64
	// Independent oracle: the raw TEXT bytes SQLite actually stored.
	if err := w.db.QueryRowContext(t.Context(), `SELECT length(id)+length(scope)+length(audience)+length(severity)+length(source)+length(category)+length(title)+length(body)+length(dedupe_key)+length(arguments)+length(actions) FROM notification_records`).Scan(&stored); err != nil || stored > uoNoticeMaxRecordBytes || stored <= 0 {
		t.Fatal("stored record bytes", stored, uoNoticeMaxRecordBytes, err)
	}
	// Every field is individually legal, but the joint record budget refuses.
	wide := uoNoticeDraft("too-wide")
	wide.severity = notify.SeverityInfo
	wide.title = strings.Repeat("t", uoNoticeMaxTitleBytes)
	wide.body = strings.Repeat("b", uoNoticeMaxBodyBytes)
	wide.arguments = uoNoticeWideArguments(12, uoNoticeMaxArgumentBytes)
	wide.actions = uoNoticeWideActions(3)
	if uoNoticeRecordBytes(wide.view, wide.id, wide.severity, wide.source, wide.category, wide.title, wide.body, wide.dedupe, mustUONoticeJSON(t, wide.arguments, wide.actions)) <= uoNoticeMaxRecordBytes {
		t.Fatal("over-cap fixture does not exceed the cap")
	}
	turn := uoNoticePublishTurn(t, w)
	if out, err := applyUONoticePublish(t.Context(), turn, wide); !errors.Is(err, ErrInvalid) || out != (uoNoticeEffect{}) {
		t.Fatal("over-cap record accepted", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if records, _, _, _, _ := uoNoticeSQLCounts(t, w); records != 1 {
		t.Fatal("refused record left a prefix", records)
	}
}

func mustUONoticeJSON(t *testing.T, arguments map[string]string, actions []notify.Action) string {
	t.Helper()
	args, err := uoNoticeEncodedArguments(arguments)
	if err != nil {
		t.Fatal(err)
	}
	acts, err := uoNoticeEncodedActions(actions)
	if err != nil {
		t.Fatal(err)
	}
	return args + acts
}

func TestUONoticePublishRootSeamRefuses(t *testing.T) {
	w := uoNoticePublishFixture(t)
	out := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	binding, err := bindUONoticeRoot(out)
	if !errors.Is(err, ErrNoticeRootUnavailable) || binding != (uoNoticeRootBinding{}) {
		t.Fatal("root seam must refuse until UO-01/UO-03 land", binding, err)
	}
	if records, _, _, _, _ := uoNoticeSQLCounts(t, w); records != 1 {
		t.Fatal("root refusal wrote native state", records)
	}
}

func TestUONoticePublishSharedOperationRecord(t *testing.T) {
	w := uoNoticePublishFixture(t)
	q := uoNoticeDraft("a")
	q.operation = uoNoticeOperation{id: "broadcast-one", digest: [32]byte{1}, expiresMS: 5000}
	out := uoNoticePublishOne(t, w, q)
	if !out.created {
		t.Fatal("create", out)
	}
	if _, _, _, _, operations := uoNoticeSQLCounts(t, w); operations != 1 {
		t.Fatal("not one shared operation record", operations)
	}
	var ids, upper string
	if err := w.db.QueryRowContext(t.Context(), `SELECT ids,upper_account FROM notice_broadcast_work WHERE scope=? AND operation_id=?`, q.view.scope, q.operation.id).Scan(&ids, &upper); err != nil || ids != `["a"]` || upper != "uo.shared.v1" {
		t.Fatal("shared operation row", ids, upper, err)
	}
	restated := q
	restated.expectedRevision, restated.expectedAuthorityRevision = out.revision, out.authorityRevision
	restated.authorityRevision = 2
	restated.nowMS = 20
	if got := uoNoticePublishOne(t, w, restated); !got.changed || got.created {
		t.Fatal("restatement", got)
	}
	if _, _, _, _, operations := uoNoticeSQLCounts(t, w); operations != 1 {
		t.Fatal("restatement duplicated the shared operation", operations)
	}
}

func TestUONoticePublishInboxIsolation(t *testing.T) {
	w := uoNoticePublishFixture(t)
	first := uoNoticeDraft("a")
	first.view = uoNoticeView{scope: `["authority","account-one","profile-one"]`, audience: uoNoticeAudienceProfile}
	uoNoticePublishOne(t, w, first)
	// The same producer source and dedupe key in another principal's inbox is
	// a separate record; nothing from the first inbox is visible.
	second := uoNoticeDraft("b")
	second.view = uoNoticeView{scope: `["authority","account-one","profile-two"]`, audience: uoNoticeAudienceProfile}
	second.dedupe = "a"
	second.source = first.source
	second.category = first.category
	got := uoNoticePublishOne(t, w, second)
	if got.id != "b" || got.sequence == 0 || got.revision != 2 || got.inboxRevision != 2 {
		t.Fatal("second principal did not get an independent record", got)
	}
	if records, inbox, revision, events, _ := uoNoticeSQLCounts(t, w); records != 2 || inbox != 2 || revision != 2 || events != 2 {
		t.Fatal("inbox isolation facts", records, inbox, revision, events)
	}
	turn := uoNoticePublishTurn(t, w)
	if out, err := applyUONoticeWithdraw(t.Context(), turn, uoNoticeWithdraw{view: second.view, id: "a", expectedRevision: 1, authorityRevision: 1, nowMS: 100}); !errors.Is(err, ErrConflict) || out != (uoNoticeEffect{}) {
		t.Fatal("withdrawal crossed inboxes", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var withdrawn int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM notification_records WHERE withdrawn_ms IS NOT NULL`).Scan(&withdrawn); err != nil || withdrawn != 0 {
		t.Fatal("cross-inbox withdrawal changed a record", withdrawn, err)
	}
}

func TestUONoticePublishRollbackOnFailure(t *testing.T) {
	for _, failure := range []string{"event", "revision-overflow", "conflict", "cancel", "missing-turn"} {
		t.Run(failure, func(t *testing.T) {
			w := uoNoticePublishFixture(t)
			q := uoNoticeDraft("a")
			var turn *commandTurn
			if failure == "event" {
				turn = wc008NativeTurn(t, w, PublishNotice, uoNoticePublishDefinitions())
			} else {
				turn = uoNoticePublishTurn(t, w)
			}
			if failure == "revision-overflow" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO notification_revision(singleton,value) VALUES(1,?)`, int64(math.MaxInt64)); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "conflict" {
				q.expectedRevision, q.expectedAuthorityRevision = 1, 1
			}
			ctx := t.Context()
			if failure == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			target := turn
			if failure == "missing-turn" {
				target = nil
			}
			out, err := applyUONoticePublish(ctx, target, q)
			if err == nil || out != (uoNoticeEffect{}) {
				t.Fatal("failure leaked a result", out, err)
			}
			if turn != nil {
				if _, err = turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
			}
			if records, inbox, revision, events, operations := uoNoticeSQLCounts(t, w); records != 0 || inbox != 0 || revision != 0 || events != 0 || operations != 0 {
				t.Fatal("failed turn left a prefix", records, inbox, revision, events, operations)
			}
		})
	}
}

func TestUONoticePublishInputBounds(t *testing.T) {
	base := uoNoticeDraft("a")
	for _, edit := range []func(*uoNoticePublish){
		func(q *uoNoticePublish) { q.view.audience = "everyone" },
		func(q *uoNoticePublish) { q.view.scope = "" },
		func(q *uoNoticePublish) { q.id = "" },
		func(q *uoNoticePublish) { q.severity = "notice" },
		func(q *uoNoticePublish) { q.source = "bad source" },
		func(q *uoNoticePublish) { q.category = "" },
		func(q *uoNoticePublish) { q.dedupe = "" },
		func(q *uoNoticePublish) { q.title = " " },
		func(q *uoNoticePublish) { q.body = strings.Repeat("b", uoNoticeMaxBodyBytes+1) },
		func(q *uoNoticePublish) { q.nowMS = 0 },
		func(q *uoNoticePublish) { q.expiresMS = q.nowMS },
		func(q *uoNoticePublish) { q.authorityRevision = 0 },
		func(q *uoNoticePublish) { q.expectedRevision = 1 },
		func(q *uoNoticePublish) { q.arguments = map[string]string{"bad key": "v"} },
		func(q *uoNoticePublish) {
			q.arguments = map[string]string{}
			for i := 0; i <= uoNoticeMaxArguments; i++ {
				q.arguments[fmt.Sprintf("key%02d", i)] = "v"
			}
		},
		func(q *uoNoticePublish) {
			q.actions = []notify.Action{{Kind: "command", Label: "Go", Command: "unknown"}}
		},
		func(q *uoNoticePublish) {
			q.operation = uoNoticeOperation{id: "", digest: [32]byte{1}, expiresMS: 10000}
		},
		func(q *uoNoticePublish) {
			q.operation = uoNoticeOperation{id: "op", digest: [32]byte{1}, expiresMS: 10}
		},
	} {
		copy := base
		edit(&copy)
		if err := copy.validate(); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted invalid publish input", copy)
		}
	}
	out, err := applyUONoticePublish(t.Context(), nil, base)
	if err == nil || out != (uoNoticeEffect{}) {
		t.Fatal("missing turn published", out, err)
	}
	withdraw := uoNoticeWithdraw{view: base.view, id: "a", expectedRevision: 1, authorityRevision: 1, nowMS: 1}
	for _, edit := range []func(*uoNoticeWithdraw){
		func(q *uoNoticeWithdraw) { q.id = "" },
		func(q *uoNoticeWithdraw) { q.expectedRevision = 0 },
		func(q *uoNoticeWithdraw) { q.authorityRevision = 0 },
		func(q *uoNoticeWithdraw) { q.nowMS = 0 },
		func(q *uoNoticeWithdraw) { q.view.audience = "" },
	} {
		copy := withdraw
		edit(&copy)
		if err := copy.validate(); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted invalid withdraw input", copy)
		}
	}
}

func TestUONoticePublishDefinitionsPrepare(t *testing.T) {
	w := uoNoticePublishFixture(t)
	wc008NativePrepare(t, w, uoNoticePublishDefinitions())
	wc008NativePrepare(t, w, wcAPIEventDefinitions())
}
