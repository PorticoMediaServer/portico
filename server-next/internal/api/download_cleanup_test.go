package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnerDownloadCleanupKeepsDevicesAndSharedCopies(t *testing.T) {
	rig := newDownloadRig(t)
	ctx := context.Background()
	_, first := rig.ask(rig.owner, DownloadAsk{ItemID: rig.film, Quality: "240p"})
	rig.waitReady(rig.owner, first.ID)
	sam := addMember(t, rig.s, rig.owner, "sam", "member")
	browser := signInAs(t, rig.s, "sam", testPassword, 3).AccessToken
	_, second := rig.ask(browser, DownloadAsk{ItemID: rig.film, Quality: "240p"})
	rig.waitReady(browser, second.ID)
	copies, err := rig.s.db.PersonDownloadCopies(ctx, sam.ID)
	if err != nil || len(copies) != 1 {
		t.Fatal(copies, err)
	}
	copy := copies[0]
	files, err := filepath.Glob(filepath.Join(rig.s.prepare.Folder, copy.PublicID+"*"))
	if err != nil || len(files) == 0 {
		t.Fatal("no prepared files", err)
	}
	usage := get[DownloadsByPerson](t, rig.s, rig.owner, "/v1/admin/downloads")
	var ownerID string
	for _, person := range usage.Items {
		if person.UserID != sam.ID {
			ownerID = person.UserID
		}
	}
	if ownerID == "" {
		t.Fatal(usage)
	}
	addMember(t, rig.s, rig.owner, "member", "member")
	member := signInAs(t, rig.s, "member", testPassword, 4).AccessToken
	route := func(id string) string { return "/v1/admin/members/" + id + "/downloads" }
	if code := status(t, rig.s, "DELETE", route("unknown-member"), rig.owner, nil); code != 404 {
		t.Fatal(code)
	}
	if code := status(t, rig.s, "DELETE", route(ownerID), member, nil); code != 403 {
		t.Fatal(code)
	}
	if code := status(t, rig.s, "DELETE", route(ownerID), rig.owner, nil); code != 204 {
		t.Fatal(code)
	}
	if got := get[Download](t, rig.s, rig.owner, "/v1/downloads/"+first.ID); got.Items[0].State != "expired" {
		t.Fatal(got)
	}
	if got := get[Download](t, rig.s, browser, "/v1/downloads/"+second.ID); got.Items[0].State != "ready" {
		t.Fatal("another device lost its shared copy", got)
	}
	release, err := rig.s.prepare.ReadCopy(ctx, copy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if code := status(t, rig.s, "DELETE", route(sam.ID), rig.owner, nil); code != 409 {
		release()
		t.Fatal(code)
	}
	release()
	addMember(t, rig.s, rig.owner, "admin", "admin")
	admin := signInAs(t, rig.s, "admin", testPassword, 5).AccessToken
	if code := status(t, rig.s, "DELETE", route(sam.ID), admin, nil); code != 403 {
		t.Fatal(code)
	}
	if code := status(t, rig.s, "DELETE", route(sam.ID), rig.owner, nil); code != 204 {
		t.Fatal(code)
	}
	for _, file := range files {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("prepared file survived", file, err)
		}
	}
	if _, err := os.Stat(filepath.Join(rig.folder, "film.mkv")); err != nil {
		t.Fatal("cleanup touched the original", err)
	}
	var synced DownloadSyncReply
	w := call(t, rig.s, "POST", "/v1/downloads/sync", rig.owner, DownloadSync{Holds: []HeldTitle{{DownloadID: first.ID, ItemID: rig.film}}}, &synced)
	if w.Code != 200 || len(synced.Remove) != 0 || len(synced.Fetch) != 0 {
		t.Fatal("cleanup revoked a device-held download", w.Code, synced)
	}
	if code := status(t, rig.s, "PATCH", "/v1/downloads/"+first.ID, rig.owner, DownloadChange{Retry: true}); code != 200 {
		t.Fatal(code)
	}
	rig.waitReady(rig.owner, first.ID)
}
