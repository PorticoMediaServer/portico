package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// The owner's defaults reach the running tuner/recorder without restarting, persist, and
// changing the recordings folder retains one Recorded TV library and its previous folders.
func TestLiveAndRecordingDefaultsApplyWithoutRestart(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	save := func(name string, value any) {
		t.Helper()
		if w := call(t, s, "PUT", "/v1/admin/settings/"+name, owner, value, nil); w.Code != 200 {
			t.Fatalf("%s settings: %d %s", name, w.Code, w.Body)
		}
	}
	save("live", json.RawMessage(`{"rewindMinutes":60,"retrySeconds":90,"guideDays":3}`))
	tuning := live.Document.Get(s.sources.Settings).Tuning()
	if tuning.Stretch != time.Hour || tuning.Retry != 90*time.Second || tuning.MaxBytes != live.DefaultTuning.MaxBytes {
		t.Fatalf("tuner defaults: %+v", tuning)
	}
	first, second := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	save("dvr", map[string]any{"folder": first, "beforeMs": 1000, "afterMs": 2000, "floorBytes": 1 << 20, "capBytes": 100 << 30, "keepNewest": 3, "stopViewers": true})
	options := s.dvrOptions()
	if options.Folder != first || options.PadBefore != time.Second || options.PadAfter != 2*time.Second || options.KeepNewest != 3 || !options.StopViewers {
		t.Fatalf("recorder defaults: %+v", options)
	}
	library, err := s.recorder.Library(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	save("dvr", map[string]any{"folder": second})
	changed, err := s.recorder.Library(t.Context())
	if err != nil || changed.PublicID != library.PublicID || len(changed.Paths) != 2 {
		t.Fatalf("folder switch replaced the library or lost a path: %+v %v", changed, err)
	}
	reopened, _, err := settings.Open(t.Context(), s.db)
	if err != nil || live.Document.Get(reopened).GuideDays != 3 {
		t.Fatalf("saved live defaults after reopen: %v", err)
	}
	for _, body := range []string{`{"rewindMinutes":9}`, `{"guideDays":5}`} {
		if code := status(t, s, "PUT", "/v1/admin/settings/live", owner, json.RawMessage(body)); code != 422 {
			t.Errorf("invalid live defaults answered %d", code)
		}
	}
	if code := status(t, s, "PUT", "/v1/admin/settings/dvr", owner, map[string]string{"folder": "  " + filepath.Join(s.dataDir, "dvr") + "  "}); code != 400 {
		t.Errorf("recording inside data folder answered %d", code)
	}
	alias := filepath.Join(t.TempDir(), "data-link")
	if err := os.Symlink(s.dataDir, alias); err == nil {
		if code := status(t, s, "PUT", "/v1/admin/settings/dvr", owner, map[string]string{"folder": filepath.Join(alias, "not-created", "dvr")}); code != 400 {
			t.Errorf("recording through a link into data folder answered %d", code)
		}
	}
	if s.dvrOptions().Folder != second {
		t.Fatal("a refused folder changed where the recorder writes")
	}
}

// A saved destination applies to new recordings. The files of an older recording still
// supply its size and Delete, rather than being looked for under the new destination.
func TestChangingRecordingFolderKeepsRecordedFile(t *testing.T) {
	s, owner, folder := recordable(t)
	recorded := recordUntilTitled(t, s, owner, "Morning News")
	original := get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+recorded.ID)
	if original.Bytes == 0 {
		t.Fatal("the original recording has no file size")
	}
	files, err := filepath.Glob(filepath.Join(folder, "Morning News", "Morning News - *.ts"))
	if err != nil || len(files) != 1 {
		t.Fatalf("recorded file: %v %v", files, err)
	}
	destination := filepath.Join(t.TempDir(), "new-recordings")
	if w := call(t, s, "PUT", "/v1/admin/settings/dvr", owner, map[string]string{"folder": destination}, nil); w.Code != 200 {
		t.Fatalf("new folder: %d %s", w.Code, w.Body)
	}
	after := get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+recorded.ID)
	if after.Bytes != original.Bytes {
		t.Fatalf("changing folder lost the old file's size: %d, was %d", after.Bytes, original.Bytes)
	}
	if code := status(t, s, "DELETE", "/v1/dvr/recordings/"+recorded.ID, owner, nil); code != 204 {
		t.Fatalf("delete answered %d", code)
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatalf("the old file was not deleted: %v", err)
	}
}
