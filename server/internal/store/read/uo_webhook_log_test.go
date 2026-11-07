package read

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestUOWebhookLogNativeKeysetAndPreflight(t *testing.T) {
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO webhooks(id,name,url,secret,events,created_ms,updated_ms) VALUES('hook','Fixture','https://example.invalid','never-copy-secret','[]',1,1),('other','Other','https://example.invalid','other-secret','[]',1,1)`)
	f.exec(t, `INSERT INTO webhook_deliveries(id,webhook_id,type,data,created_ms) VALUES('z','hook','server.started','{"private":"never-copy-data"}',100),('a','hook','server.started','{}',100),('foreign','other','server.started','{}',200)`)
	base := uoNativeReadQuery(t, f, uoWebhookLogDefinitions())
	q := uoWebhookLogInput{webhook: "hook", authority: 1, revision: 1, byteBudget: 64 << 10, limit: 1}
	var ids []string
	for turn := 0; turn < 3; turn++ {
		page, err := readUOWebhookLog(t.Context(), base, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.items {
			ids = append(ids, item.id)
		}
		if page.next == nil {
			break
		}
		q.after = page.next
	}
	if !reflect.DeepEqual(ids, []string{"z", "a"}) {
		t.Fatal(ids)
	}
	q.after = nil
	q.authority = 2
	if _, err := readUOWebhookLog(t.Context(), base, q); !errors.Is(err, ErrDenied) {
		t.Fatal("stale webhook log disclosed", err)
	}
	q.authority = 1
	f.exec(t, `UPDATE webhook_deliveries SET error=? WHERE id='z'`, strings.Repeat("x", 65))
	payload := false
	guard := func(id stmt.ID, args ...any) ([][]any, error) {
		if id == uoWebhookLogPoint {
			payload = true
		}
		return base(id, args...)
	}
	if page, err := readUOWebhookLog(t.Context(), guard, q); !errors.Is(err, ErrUnqualified) || payload || len(page.items) != 0 {
		t.Fatal(page, payload, err)
	}
	for _, d := range uoWebhookLogDefinitions() {
		if strings.Contains(d.SQL, "secret") || strings.Contains(d.SQL, "payload") || strings.Contains(d.SQL, "data") {
			t.Fatal("private delivery facts in log recipe", d.ID)
		}
	}
}
