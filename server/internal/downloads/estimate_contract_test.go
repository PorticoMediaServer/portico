package downloads

import (
	"context"
	"testing"
)

func TestEstimatedProgressCannotOutgrowPublishedTotal(t *testing.T) {
	h := newHarness(t)
	batch, err := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "estimate", MediaID: h.item, Quality: QualityOriginal}, nil)
	if err != nil || len(batch.Items) != 1 {
		t.Fatalf("%+v %v", batch, err)
	}
	id := batch.Items[0].ID
	if _, err = h.db.Exec(`UPDATE download_preparations SET state='running',bytes_total=10,bytes_done=20,estimated=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	result, err := h.service.Get(context.Background(), h.viewer, id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Progress.BytesDone != 20 || result.Progress.BytesTotal != 20 || result.Artifact.Bytes != 20 || !result.Progress.Estimated || result.Progress.Percent != 100 {
		t.Fatalf("%+v", result)
	}
	// The projection does not rewrite quota/worker state or turn an estimate exact.
	var total int64
	if err = h.db.QueryRow(`SELECT bytes_total FROM download_preparations WHERE id=?`, id).Scan(&total); err != nil || total != 10 {
		t.Fatalf("stored %d %v", total, err)
	}
	if _, err = h.db.Exec(`UPDATE download_preparations SET state='ready',bytes_total=15,bytes_done=20,estimated=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	result, err = h.service.Get(context.Background(), h.viewer, id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact.Bytes != 15 || result.Progress.BytesTotal != 15 || result.Progress.BytesDone != 15 || result.Progress.Estimated {
		t.Fatalf("exact size changed %+v", result)
	}
}
