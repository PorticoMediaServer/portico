package read

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func uoEventFeedFixture(t *testing.T) (*browseFixture, uoEventQuery) {
	t.Helper()
	f := newBrowseSource(t)
	// The foreign audience owns 4097 payload bytes. It must never be read while
	// another principal's feed is composed.
	f.exec(t, `INSERT INTO api_events(id,audience,type,resource_kind,resource_id,revision,data,at_ms) VALUES(1,'p','personal.changed','item','visible','1','{}',1),(2,'foreign','personal.changed','item','secret','1',?,2),(3,'server','library.changed','library','movies','2','{}',3)`, strings.Repeat("x", 4097))
	return f, uoEventNativeQuery(t, f, append(uoEventDefinitions(), uoEventFeedDefinitions()...))
}

func uoEventFeedTestInput(t *testing.T, codec *uoEventCursorCodec, binding uoEventBinding, after int64) uoEventFeedInput {
	t.Helper()
	cursor, err := codec.encode(binding, after)
	if err != nil {
		t.Fatal(err)
	}
	return uoEventFeedInput{cursor: cursor, nowMS: 100, binding: binding}
}

func TestUOEventPageFeedRetentionGapAndExcessLagResync(t *testing.T) {
	f, query := uoEventFeedFixture(t)
	codec, err := newUOEventCursorCodec()
	if err != nil {
		t.Fatal(err)
	}
	binding := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
	q := uoEventFeedTestInput(t, codec, binding, 0)
	addressed := func(a string) bool { return a == "p" || a == "server" }
	redact := func(_ string, e uoStoredEvent) (uoStoredEvent, bool, error) { return e, true, nil }
	foreignPayload := false
	guard := func(id stmt.ID, args ...any) ([][]any, error) {
		if (id == uoEventPoint || id == uoEventOctets) && len(args) > 0 && args[0] == int64(2) {
			foreignPayload = true
		}
		return query(id, args...)
	}
	page, err := readUOEventFeed(t.Context(), guard, codec, q, addressed, redact)
	if err != nil || len(page.events) != 2 || page.rawRows != 3 || page.more || foreignPayload {
		t.Fatal(page, foreignPayload, err)
	}
	if after, err := codec.decode(page.next, binding); err != nil || after != 3 {
		t.Fatal(after, err)
	}

	// A retained floor above the cursor is a gap, never an empty complete page.
	f.exec(t, `DELETE FROM api_events WHERE id=1`)
	page, err = readUOEventFeed(t.Context(), query, codec, q, addressed, redact)
	if err != nil || len(page.events) != 1 || page.events[0].Type != "stream.resync" {
		t.Fatal(page, err)
	}
	// More than 100 raw rows of lag resyncs before copying any payload. The
	// cursor sits at the legal gap left by the deleted floor row.
	f.exec(t, `WITH RECURSIVE n(id) AS(VALUES(4) UNION ALL SELECT id+1 FROM n WHERE id<102) INSERT INTO api_events(id,audience,type,at_ms) SELECT id,'foreign','hint',1 FROM n`)
	q = uoEventFeedTestInput(t, codec, binding, 1)
	page, err = readUOEventFeed(t.Context(), query, codec, q, addressed, redact)
	if err != nil || len(page.events) != 1 || page.events[0].Type != "stream.resync" {
		t.Fatal(page, err)
	}
	q.cursor = "forged"
	page, err = readUOEventFeed(t.Context(), query, codec, q, addressed, redact)
	if err != nil || len(page.events) != 1 || page.events[0].Type != "stream.resync" {
		t.Fatal(page, err)
	}
}

func TestUOEventPageStreamAuthorityLossClosesWithoutPages(t *testing.T) {
	page := uoEventFeedPage{events: []uoWireEvent{{ID: "cursor", Type: "personal.changed", Data: []byte(`{}`)}}, next: "cursor"}
	frames, err := uoEventStreamFrames(page, true, true, true, 100)
	if err != nil || len(frames) != 1 || !strings.Contains(string(frames[0]), "stream.closed") || strings.Contains(string(frames[0]), "personal.changed") {
		t.Fatal(len(frames), err)
	}
	frames, err = uoEventStreamFrames(page, true, false, false, 100)
	if err != nil || len(frames) != 2 || !strings.Contains(string(frames[0]), "stream.opened") {
		t.Fatal(len(frames), err)
	}
}

func TestUOEventPageBoundsNewestRetentionAndEmptyRestoreResync(t *testing.T) {
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO api_events(id,audience,type,at_ms) VALUES(5,'p','hint',1),(900,'p','hint',2)`)
	query := uoEventNativeQuery(t, f, append(uoEventDefinitions(), uoEventFeedDefinitions()...))
	low, high, err := readUOEventBounds(t.Context(), query)
	if err != nil || low != 5 || high != 900 {
		t.Fatal(low, high, err)
	}
	// WC retention can remove an old row but must never delete this newest row.
	f.exec(t, `DELETE FROM api_events WHERE id=5`)
	low, high, err = readUOEventBounds(t.Context(), query)
	if err != nil || low != 900 || high != 900 {
		t.Fatal(low, high, err)
	}
	codec, err := newUOEventCursorCodec()
	if err != nil {
		t.Fatal(err)
	}
	oldBinding := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
	cursor, err := codec.encode(oldBinding, 900)
	if err != nil {
		t.Fatal(err)
	}
	// Corruption/restore-only control: canonical live retention cannot empty it.
	f.exec(t, `DELETE FROM api_events`)
	restored := oldBinding
	restored.database = [16]byte{4}
	q := uoEventFeedInput{cursor: cursor, nowMS: 100, binding: restored}
	page, err := readUOEventFeed(t.Context(), query, codec, q, func(string) bool { return true }, func(_ string, e uoStoredEvent) (uoStoredEvent, bool, error) { return e, true, nil })
	if err != nil || len(page.events) != 1 || page.events[0].Type != "stream.resync" {
		t.Fatal(page, err)
	}
	if _, err := codec.decode(page.next, oldBinding); err == nil {
		t.Fatal("restore reused old binding")
	}
	if after, err := codec.decode(page.next, restored); err != nil || after != 0 {
		t.Fatal(after, err)
	}
}
