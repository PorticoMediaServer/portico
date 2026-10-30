package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestQualityDefaultsR25DownloadOmissionAndReplay(t *testing.T) {
	h := requestHarness(t, 1)
	ctx := context.Background()
	var omitted Request
	if err := json.Unmarshal([]byte(`{"operationId":"omitted","mediaId":"`+h.item+`"}`), &omitted); err != nil {
		t.Fatal(err)
	}
	batch, err := h.service.Submit(ctx, h.viewer, omitted, nil)
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Quality != "original" {
		t.Fatal(batch, err)
	}
	explicit := omitted
	explicit.Quality = "original"
	replay, err := h.service.Submit(ctx, h.viewer, explicit, nil)
	if err != nil || !replay.Duplicate || replay.BatchID != batch.BatchID {
		t.Fatal(replay, err)
	}
	container := newContainerRequest("container-default", h.show)
	container.Quality = ""
	first, err := h.service.SubmitContainer(ctx, h.viewer, container)
	if err != nil {
		t.Fatal(err)
	}
	container.Quality = "original"
	repeated, err := h.service.SubmitContainer(ctx, h.viewer, container)
	if err != nil || repeated.RequestID != first.RequestID {
		t.Fatal(repeated, err)
	}
	var stored string
	if err = h.db.QueryRow(`SELECT quality FROM download_requests WHERE id=?`, first.RequestID).Scan(&stored); err != nil || stored != "original" {
		t.Fatal(stored, err)
	}
	container.Quality = "automatic"
	if _, err = h.service.SubmitContainer(ctx, h.viewer, container); !errors.Is(err, ErrRequestConflict) {
		t.Fatal("explicit choice was overwritten", err)
	}
	for _, raw := range []string{`""`, `"auto"`, `"1080p"`, `"mobile"`, `"bogus"`, `null`, `42`} {
		if err = json.Unmarshal([]byte(`{"quality":`+raw+`}`), new(Request)); err == nil {
			t.Fatal("item accepted explicit invalid", raw)
		}
		if err = json.Unmarshal([]byte(`{"quality":`+raw+`}`), new(ContainerRequest)); err == nil {
			t.Fatal("container accepted explicit invalid", raw)
		}
	}
	for _, q := range []string{"original", "automatic", "2160p", "1080p-high", "1080p-low", "720p-high", "720p-low", "480p", "240p"} {
		var request ContainerRequest
		if err = json.Unmarshal([]byte(`{"quality":"`+q+`"}`), &request); err != nil || string(request.Quality) != q {
			t.Fatal(q, request, err)
		}
	}
	// Different operation IDs retain explicitly selected Automatic in durable requests.
	container.OperationID = "explicit-auto"
	if _, err = h.service.SubmitContainer(ctx, h.viewer, container); err != nil {
		t.Fatal(err)
	}
	if err = h.db.QueryRow(`SELECT quality FROM download_requests WHERE operation_id='explicit-auto'`).Scan(&stored); err != nil || stored != "automatic" {
		t.Fatal(stored, err)
	}
}

func TestQualityDefaultsR25OptionalRequestSchema(t *testing.T) {
	schema := ContainerRequest{}.JSONSchema()
	for _, name := range schema["required"].([]string) {
		if name == "quality" {
			t.Fatal("quality is still mandatory")
		}
	}
	quality := schema["properties"].(map[string]any)["quality"].(map[string]any)
	if quality["default"] != nil || len(quality["enum"].([]string)) != 12 {
		t.Fatal(quality)
	}
}
