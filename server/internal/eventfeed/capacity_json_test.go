package eventfeed

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
)

func TestCapacityJSONPublishesOnlySequencing(t *testing.T) {
	e := Event{ID: "123", Type: apievents.CapacityChangedType, At: "2026-10-25T20:00:00Z", Resource: Resource{Kind: "session", ID: "private-session"}, Revision: "private-revision", Data: json.RawMessage(`{"accountId":"private-account","playing":2}`)}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || string(fields["id"]) != `"123"` || string(fields["type"]) != `"capacity.changed"` || string(fields["at"]) != `"2026-10-25T20:00:00Z"` {
		t.Fatalf("capacity envelope includes non-sequencing fields: %s", raw)
	}
}

func TestOrdinaryEventJSONKeepsExistingFields(t *testing.T) {
	e := Event{ID: "123", Type: "session.updated", At: "2026-10-25T20:00:00Z", Resource: Resource{Kind: "session", ID: "session"}, Revision: "3", Data: json.RawMessage(`{"state":"ended"}`)}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"123","type":"session.updated","at":"2026-10-25T20:00:00Z","resource":{"kind":"session","id":"session"},"revision":"3","data":{"state":"ended"}}`
	if string(raw) != want {
		t.Fatalf("ordinary event serialization changed: %s", raw)
	}
}
