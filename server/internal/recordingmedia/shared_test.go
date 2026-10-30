package recordingmedia

import (
	"context"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"strings"
	"testing"
)

func TestSharedObjectUsesOneInodeAndLastOwnerReleasesBytes(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	driver, e := New(root, "", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer driver.Close()
	request := func(profile, id string) dvr.CaptureRequest {
		return dvr.CaptureRequest{Owner: livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: profile}, Recording: dvr.Recording{ID: strings.Repeat(id, 64)}, ArtifactKey: strings.Repeat(id, 64) + ".1"}
	}
	first, second := request("first", "a"), request("second", "b")
	store, _, e := driver.store(first.Owner, first.ArtifactKey)
	if e != nil {
		t.Fatal(e)
	}
	writer, e := store.Begin(4096)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = writer.Write([]byte(strings.Repeat("closed bytes", 100)))
	object, e := writer.Seal(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	original, e := store.ObjectPath(object)
	if e != nil {
		t.Fatal(e)
	}
	reader, _, path, e := driver.shareObject(context.Background(), second, first, object)
	if e != nil {
		t.Fatal(e)
	}
	a, e := os.Stat(original)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.Stat(path)
	if e != nil || !os.SameFile(a, b) {
		t.Fatal("media bytes were copied")
	}
	reader.Close()
	before, e := driver.Measure(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if before.UsedBytes != object.Size {
		t.Fatalf("shared bytes counted twice: %+v", before)
	}
	if e = driver.Remove(context.Background(), first.Owner, first.ArtifactKey, object); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("first owner removal deleted the second owner's media")
	}
	after, e := driver.Measure(context.Background())
	if e != nil || after.UsedBytes != object.Size {
		t.Fatalf("remaining bytes %+v %v", after, e)
	}
	if e = driver.Remove(context.Background(), second.Owner, second.ArtifactKey, object); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatalf("final owner retained physical bytes: %v", e)
	}
}
