package read

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Native query component controls, not a GV/current-authority publication or
// complete feed qualifier. The production query closure belongs to one scope.
func uoEventFixture(t *testing.T) (*browseFixture, uoEventQuery) {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO api_events(id,audience,type,resource_kind,resource_id,revision,data,at_ms) VALUES(1,'p','personal.changed','item','hidden-title','1','{}',1),(2,'p','library.changed','library','movies','2','{"label":"音"}',2),(3,'other','library.changed','library','secret','3','{}',3)`)
	defs := uoEventDefinitions()
	for n := range defs {
		var args []any
		for _, p := range defs[n].Params {
			if p.Type == stmt.Text {
				args = append(args, "p")
			} else {
				args = append(args, int64(1))
			}
		}
		defs[n].Cohorts = []stmt.Cohort{{Name: "native-component", Bindings: args}}
	}
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	query := func(id stmt.ID, args ...any) (out [][]any, err error) {
		d, err := registry.Lookup(id)
		if err != nil {
			return nil, err
		}
		b, err := d.Bind(args...)
		if err != nil {
			return nil, err
		}
		rows, err := b.QueryContext(t.Context(), f.conn)
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, rows.Close()) }()
		columns, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			v := make([]any, len(columns))
			dest := make([]any, len(v))
			for n := range v {
				dest[n] = &v[n]
			}
			if err := rows.Scan(dest...); err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
	return f, query
}
func TestUOEventPageNativePreflightRedactionAndPosition(t *testing.T) {
	_, query := uoEventFixture(t)
	codec, err := newUOEventCursorCodec()
	if err != nil {
		t.Fatal(err)
	}
	q := uoEventPageInput{audience: "p", upper: 3, oldest: 1, limit: 100, wireBytes: 64 << 10, binding: uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}}
	page, err := readUOEventPage(t.Context(), query, codec, q, func(e uoStoredEvent) (uoStoredEvent, bool, error) { return e, e.resourceKind != "item", nil })
	if err != nil || len(page.events) != 1 || page.rawRows != 2 || !page.exhausted || page.after != 3 {
		t.Fatal(page, err)
	}
	encoded, err := json.Marshal(page.events)
	if err != nil || strings.Contains(string(encoded), "hidden-title") || strings.Contains(string(encoded), "secret") {
		t.Fatal("redaction leak", string(encoded), err)
	}
	if after, err := codec.decode(page.next, q.binding); err != nil || after != 3 {
		t.Fatal("opaque raw high-water", after, err)
	}
}
func TestUOEventPageOversizePreflightDoesNotCopyPayload(t *testing.T) {
	f, base := uoEventFixture(t)
	f.exec(t, `UPDATE api_events SET data=? WHERE id=1`, strings.Repeat("x", 4097))
	codec, err := newUOEventCursorCodec()
	if err != nil {
		t.Fatal(err)
	}
	q := uoEventPageInput{audience: "p", upper: 3, oldest: 1, limit: 100, wireBytes: 64 << 10, binding: uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}}
	payload := false
	query := func(id stmt.ID, args ...any) ([][]any, error) {
		if id == uoEventPoint {
			payload = true
		}
		return base(id, args...)
	}
	page, err := readUOEventPage(t.Context(), query, codec, q, func(e uoStoredEvent) (uoStoredEvent, bool, error) { return e, true, nil })
	if !errors.Is(err, errUOEventResync) || payload || len(page.events) != 0 || page.next != "" {
		t.Fatal("oversize copied/disclosed", page, payload, err)
	}
}

func TestUOEventTitlePathIsAllOrGeneric(t *testing.T) {
	e := uoStoredEvent{kind: "progress.changed", resourceKind: "library", resourceID: "movies", data: `{"titles":["visible","hidden"],"observation":"4"}`}
	redacted, keep, err := redactUOEventTitles(e, func(id string) bool { return id == "visible" })
	if err != nil || !keep || strings.Contains(redacted.data, "titles") || !strings.Contains(redacted.data, "observation") {
		t.Fatal(redacted, keep, err)
	}
	e.resourceKind, e.resourceID = "item", "hidden"
	if _, keep, err := redactUOEventTitles(e, func(string) bool { return false }); err != nil || keep {
		t.Fatal("hidden resource escaped", keep, err)
	}
}

func TestUOSSEFrameRejectsLineInjection(t *testing.T) {
	if _, err := encodeUOSSEFrame(uoWireEvent{ID: "opaque\nevent: forged", Type: "stream.position"}); err == nil {
		t.Fatal("cursor injection accepted")
	}
	frame, err := encodeUOSSEFrame(uoWireEvent{ID: "opaque", Type: "stream.position"})
	if err != nil || !strings.HasPrefix(string(frame), "id: opaque\nevent: stream.position\ndata: ") {
		t.Fatal(string(frame), err)
	}
}
