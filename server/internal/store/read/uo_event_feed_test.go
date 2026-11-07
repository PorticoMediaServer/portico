package read

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestUOEventFeedGlobalWindowAndStreamAuthorityLoss(t *testing.T) {
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO api_events(id,audience,type,resource_kind,resource_id,revision,data,at_ms) VALUES(1,'p','personal.changed','item','visible','1','{}',1),(2,'foreign','personal.changed','item','secret','1',?,2),(3,'server','library.changed','library','movies','2','{}',3)`, strings.Repeat("x", 4097))
	defs := append(uoEventDefinitions(), uoEventFeedDefinitions()...)
	query := uoEventQuery(uoNativeReadQuery(t, f, defs))
	codec, err := newUOEventCursorCodec()
	if err != nil {
		t.Fatal(err)
	}
	q := uoEventFeedInput{oldest: 1, highWater: 3, nowMS: 100, binding: uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}}
	q.cursor, err = codec.encode(q.binding, 0)
	if err != nil {
		t.Fatal(err)
	}
	addressed := func(a string) bool { return a == "p" || a == "server" }
	redact := func(_ string, e uoStoredEvent) (uoStoredEvent, bool, error) { return e, true, nil }
	page, err := readUOEventFeed(t.Context(), query, codec, q, addressed, redact)
	if err != nil || len(page.events) != 2 || page.rawRows != 3 || page.more {
		t.Fatal(page, err)
	}
	if after, err := codec.decode(page.next, q.binding); err != nil || after != 3 {
		t.Fatal(after, err)
	}
	frames, err := uoEventStreamFrames(page, true, true, true, 100)
	if err != nil || len(frames) != 1 || !strings.Contains(string(frames[0]), "stream.closed") || strings.Contains(string(frames[0]), "visible") {
		t.Fatal(len(frames), err)
	}
	frames, err = uoEventStreamFrames(page, true, false, false, 100)
	if err != nil || len(frames) != 3 || !strings.Contains(string(frames[0]), "stream.opened") {
		t.Fatal(len(frames), err)
	}
	// Excessive raw lag resyncs before copying any audience or payload.
	f.exec(t, `WITH RECURSIVE n(id) AS(VALUES(4) UNION ALL SELECT id+1 FROM n WHERE id<102) INSERT INTO api_events(id,audience,type,at_ms) SELECT id,'foreign','hint',1 FROM n`)
	q.highWater = 102
	audienceRead := false
	guard := func(id stmt.ID, args ...any) ([][]any, error) {
		if id == uoEventAudiencePoint {
			audienceRead = true
		}
		return query(id, args...)
	}
	page, err = readUOEventFeed(t.Context(), guard, codec, q, addressed, redact)
	if err != nil || len(page.events) != 1 || page.events[0].Type != "stream.resync" || audienceRead {
		t.Fatal(page, audienceRead, err)
	}
	q.cursor = "forged"
	page, err = readUOEventFeed(t.Context(), query, codec, q, addressed, redact)
	if err != nil || len(page.events) != 1 || page.events[0].Type != "stream.resync" {
		t.Fatal(page, err)
	}
}
