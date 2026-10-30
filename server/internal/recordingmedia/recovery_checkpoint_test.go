package recordingmedia

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
)

func TestRecoveryPersistsFinalCheckpointBeforeValidation(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	driver, e := New(root, "", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer driver.Close()
	id := strings.Repeat("a", 64)
	request := dvr.CaptureRequest{Owner: livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}, Recording: dvr.Recording{ID: id, End: time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}, ArtifactKey: id + ".1"}
	store, _, e := driver.store(request.Owner, request.ArtifactKey)
	if e != nil {
		t.Fatal(e)
	}
	writer, e := store.BeginRetained(request.ArtifactKey, maxCaptureBytes)
	if e != nil {
		t.Fatal(e)
	}
	bytes := make([]byte, 188*3)
	for i := 0; i < len(bytes); i += 188 {
		bytes[i] = 0x47
		bytes[i+3] = 0x10
	}
	if _, e = writer.Write(bytes); e != nil {
		t.Fatal(e)
	}
	object, e := writer.Checkpoint()
	if e != nil {
		t.Fatal(e)
	}
	if e = writer.Retain(); e != nil {
		t.Fatal(e)
	}
	stopped := errors.New("checkpoint refused before sealing")
	request.Checkpoint = func(cp dvr.CaptureCheckpoint) error {
		if !cp.WriterRetired || cp.Object != object {
			t.Fatalf("wrong recovery checkpoint: %+v", cp)
		}
		return stopped
	}
	if _, e = driver.Recover(context.Background(), request, dvr.CaptureCheckpoint{Object: object}); !errors.Is(e, stopped) {
		t.Fatalf("recovery bypassed checkpoint before validation: %v", e)
	}
	// A refused checkpoint must leave the recoverable stage in place.
	next, e := store.ResumeRetained(context.Background(), request.ArtifactKey, object.Size, maxCaptureBytes)
	if e != nil {
		t.Fatal(e)
	}
	next.Retain()
}
