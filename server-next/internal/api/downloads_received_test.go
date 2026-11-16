package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A copy the server makes for a download is temporary (area 9 rule 24). A device says it has a
// title's whole file and the copy goes, unless another device's download still waits for it;
// saying it twice changes nothing, and the title reads as received. A browser cannot say it:
// one that was sent the whole file counts as having it.
func TestADownloadCopyGoesWhenEveryDeviceThatAskedHasIt(t *testing.T) {
	b := newBookShelf(t)
	page := pngPicture(t, 8, 12)
	for _, name := range []string{"1.png", "2.png"} {
		if err := os.MkdirAll(filepath.Join(b.comics, "Nemo", "Nemo 001"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(b.comics, "Nemo", "Nemo 001", name), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b.scan()
	// A comic made of a folder of pictures downloads as a CBZ the server makes (#3623).
	id := b.find(db.KindIssue, "Nemo 001")
	on := func(n int, platform string) string {
		var in SignInResponse
		info := device(n)
		info.Platform = platform
		call(t, b.s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "justin", Password: testPassword, Device: info}, &in)
		return onMain(t, b.s, in).AccessToken
	}
	phone, tablet, browser := b.owner.AccessToken, on(31, "ios"), on(32, "web")
	ask := func(token string) (Download, DownloadedItem) {
		t.Helper()
		var dl Download
		var item DownloadedItem
		if w := call(t, b.s, "POST", "/v1/downloads", token, DownloadAsk{ItemID: id, Quality: "original"}, &dl); w.Code != 201 {
			t.Fatalf("asking answered %d: %s", w.Code, w.Body)
		}
		call(t, b.s, "GET", "/v1/downloads/"+dl.ID+"/items/"+id, token, nil, &item)
		return dl, item
	}
	state := func(token string, dl Download) string {
		t.Helper()
		var now Download
		call(t, b.s, "GET", "/v1/downloads/"+dl.ID, token, nil, &now)
		return now.Items[0].State
	}
	copies := func() int {
		names, _ := filepath.Glob(filepath.Join(b.s.reading.Copies, "*.cbz"))
		return len(names)
	}
	received := func(token string, dl Download) {
		t.Helper()
		if w := call(t, b.s, "POST", "/v1/downloads/"+dl.ID+"/items/"+id+"/received", token, nil, nil); w.Code != 204 {
			t.Fatalf("saying it was received answered %d: %s", w.Code, w.Body)
		}
	}
	onPhone, _ := ask(phone)
	onTablet, _ := ask(tablet)
	if copies() != 1 {
		t.Fatalf("%d copies for one book", copies())
	}
	received(phone, onPhone)
	received(phone, onPhone)
	if state(phone, onPhone) != "received" || state(tablet, onTablet) != "ready" || copies() != 1 {
		t.Fatalf("after the phone: %s, %s, %d copies", state(phone, onPhone), state(tablet, onTablet), copies())
	}
	if w := call(t, b.s, "POST", "/v1/downloads/"+onTablet.ID+"/items/"+id+"/received", phone, nil, nil); w.Code != 404 {
		t.Fatalf("another device's download answered %d", w.Code)
	}
	received(tablet, onTablet)
	if copies() != 0 {
		t.Fatal("the copy stayed after the last device had it")
	}

	inBrowser, item := ask(browser)
	res := b.fetch(item.Files[0].URL)
	if res.StatusCode != 200 || state(browser, inBrowser) != "received" {
		t.Fatalf("a browser sent the whole file: %d, %s", res.StatusCode, state(browser, inBrowser))
	}
}
