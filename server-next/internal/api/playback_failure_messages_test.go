package api

import (
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/serverlog"
)

// A way of playing that failed on a device is remembered for that device and file: the
// session moves to the next way, the next start begins there, and Messages say what failed
// and when it is tried again. The former owner list and Forget routes are gone.
func TestRememberedFailures(t *testing.T) {
	th := newTheatre(t)
	messages, err := serverlog.Open(filepath.Join(t.TempDir(), "logs"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { messages.Close() })
	th.s.UseLog(messages)
	// Use the real service with Messages; this test drives its starts explicitly.
	th.s.playback = playback.New(th.s.db, th.engine, th.engine, slog.New(messages.Handler()), th.clock.Now)
	th.s.playback.UseSettings(th.s.settings)
	token := th.owner.AccessToken
	session := th.play(token, th.film)
	path := "/v1/playback/sessions/" + session.ID
	var moved PlaybackSession
	failure := PlaybackChange{Failure: &playback.Failure{Code: "decode_error", Detail: "https://private.example/media?token=do-not-log"}, PositionMs: ptr(60_000)}
	if w := call(t, th.s, "PATCH", path, token, failure, &moved); w.Code != 200 || moved.Presentation.Route != "repackaged" ||
		moved.Presentation.StartPositionMs != 60_000 {
		t.Fatalf("after Original failed: %d %+v", w.Code, moved.Presentation)
	}
	status(t, th.s, "DELETE", path, token, nil)
	// Signed out and in again on the same device, it still starts with the next way.
	status(t, th.s, "DELETE", "/v1/auth/session", token, nil)
	again := onMain(t, th.s, signIn(t, th.s, 0)).AccessToken
	if next := th.play(again, th.film); next.Presentation.Route != "repackaged" {
		t.Fatalf("the next start on the device took the %s way", next.Presentation.Route)
	}
	// Another device is not affected.
	other := onMain(t, th.s, signIn(t, th.s, 9)).AccessToken
	if next := th.play(other, th.film); next.Presentation.Route != "original" {
		t.Fatalf("another device took the %s way", next.Presentation.Route)
	}
	var lines []serverlog.Entry
	readMessages := func() { lines, _ = messages.Since(0) }
	readMessages()
	until := th.clock.Now().Add(7 * 24 * time.Hour).Format("Jan 2, 2006 at 15:04 MST")
	if len(lines) != 1 || lines[0].Kind != "playback" || lines[0].Message != "Original failed for \"Heat\" on Device 0. Repackaged until "+until+"." ||
		strings.Contains(lines[0].Message, "private.example") || strings.Contains(lines[0].Message, "do-not-log") {
		t.Fatalf("the remembered failure's Messages: %+v", lines)
	}
	for _, route := range []struct{ method, path string }{{"GET", "/v1/admin/playback/failures"}, {"DELETE", "/v1/admin/playback/failures/prf_old"}} {
		if code := status(t, th.s, route.method, route.path, again, nil); code != http.StatusNotFound {
			t.Fatalf("the removed route %s answered %d", route.path, code)
		}
	}
	th.clock.Add(7*24*time.Hour + time.Minute)
	again = onMain(t, th.s, signIn(t, th.s, 0)).AccessToken
	if next := th.play(again, th.film); next.Presentation.Route != "original" {
		t.Fatalf("after seven days the start took the %s way", next.Presentation.Route)
	}
	th.play(again, th.film)
	readMessages()
	if len(lines) != 2 || lines[1].Kind != "playback" || lines[1].Message != "Trying Original again for \"Heat\" on Device 0." {
		t.Fatalf("retry should be said once: %+v", lines)
	}
	// When every way has failed the change is refused and nothing more is tried.
	session = th.play(again, th.film)
	path = "/v1/playback/sessions/" + session.ID
	uncertain := PlaybackChange{Failure: &playback.Failure{Code: "stalled_without_data"}}
	for _, want := range []int{200, 200, http.StatusUnprocessableEntity} {
		if code := status(t, th.s, "PATCH", path, again, uncertain); code != want {
			t.Fatalf("a failure report answered %d, want %d", code, want)
		}
	}
	// An uncertain failure is forgotten after six hours, and Original is tried again.
	th.clock.Add(6*time.Hour + time.Minute)
	again = onMain(t, th.s, signIn(t, th.s, 0)).AccessToken
	if next := th.play(again, th.film); next.Presentation.Route != "original" {
		t.Fatalf("after six hours the start took the %s way", next.Presentation.Route)
	}
	readMessages()
	if len(lines) != 6 || !strings.Contains(lines[3].Message, "Converted until ") ||
		!strings.Contains(lines[4].Message, "This way is avoided until ") ||
		lines[5].Message != "Trying Original again for \"Heat\" on Device 0." {
		t.Fatalf("uncertain failures and automatic retry: %+v", lines)
	}
}
