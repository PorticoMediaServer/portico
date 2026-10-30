package httpapi

import (
	"encoding/json"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"testing"
)

func TestStoredStillWatchingSettingsControlQueueCompletion(t *testing.T) {
	f := newV1Fixture(t, 4)
	for _, item := range f.records {
		if _, err := f.db.Exec(`UPDATE catalog_entities SET kind=? WHERE id=?`, compactcatalog.Episode, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "still-watching-queue"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items...)}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	if _, err := f.db.Exec(`UPDATE queues_v1 SET automatic_advances=2 WHERE id=?`, q.Queue.ID); err != nil {
		t.Fatal(err)
	}
	read := f.raw("GET", "/v1/queues/"+q.Queue.ID, f.owner.AccessToken, nil, nil)
	if read.Code != 200 {
		t.Fatal(read.Code, read.Body.String())
	}
	var header playbackv1.QueueView
	if err := json.Unmarshal(read.Body.Bytes(), &header); err != nil {
		t.Fatal(err)
	}
	if header.PostPlay == nil || !header.PostPlay.PassoutCheckDue || header.PostPlay.AutomaticAdvances != 2 {
		t.Fatal("stored still-watching setting did not apply", header.PostPlay)
	}
	denied := f.raw("POST", "/v1/queues/"+q.Queue.ID+":advance", f.owner.AccessToken, map[string]string{"If-Match": header.Revision, "Idempotency-Key": "still-watching-auto"}, map[string]any{"reason": "completion"})
	if denied.Code != http.StatusConflict {
		t.Fatal("automatic advance ignored still-watching check", denied.Code, denied.Body.String())
	}
	// A deliberate Continue uses next and starts a new uninterrupted run.
	manual := f.raw("POST", "/v1/queues/"+q.Queue.ID+":advance", f.owner.AccessToken, map[string]string{"If-Match": header.Revision, "Idempotency-Key": "still-watching-manual"}, map[string]any{"reason": "next"})
	if manual.Code != 200 {
		t.Fatal(manual.Code, manual.Body.String())
	}
	var reply playbackv1.QueueReply
	if err := json.Unmarshal(manual.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Queue.PostPlay == nil || reply.Queue.PostPlay.PassoutCheckDue || reply.Queue.PostPlay.AutomaticAdvances != 0 {
		t.Fatal("manual action did not reset still-watching count", reply.Queue.PostPlay)
	}
}
