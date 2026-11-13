package api

import (
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server-next/internal/prepare"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStoragePresetsUseTheOnePreparedDownloadsValue(t *testing.T) {
	s, owner, members := seededWith(t, 0, 1)
	read := func() StorageKeeping { return get[StorageKeeping](t, s, owner, "/v1/admin/storage/keeping") }
	if got := read(); got.Preset != "balanced" || got.PreparedDownloadsDays != 30 {
		t.Fatal(got)
	}
	for _, c := range []struct {
		preset string
		days   int
	}{{"short", 14}, {"long", 90}, {"balanced", 30}} {
		var got StorageKeeping
		if w := call(t, s, "PUT", "/v1/admin/storage/keeping", owner, map[string]string{"preset": c.preset}, &got); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if got.Preset != c.preset || got.PreparedDownloadsDays != c.days || prepare.Document.Get(s.settings).KeepDays != c.days {
			t.Fatal(got, c)
		}
	}
	for _, days := range prepare.KeepChoices {
		if w := call(t, s, "PUT", "/v1/admin/settings/downloads", owner, map[string]int{"keepDays": days}, nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		got := read()
		if got.PreparedDownloadsDays != days || (days == 7 || days == 365) && got.Preset != "custom" {
			t.Fatal(got, days)
		}
	}
	stale := read()
	if w := call(t, s, "PUT", "/v1/admin/storage/keeping", owner, map[string]int{"preparedDownloadsDays": 7}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if prepare.Document.Get(s.settings).KeepDays != 7 || read().Preset != "custom" {
		t.Fatal(read())
	}
	req := httptest.NewRequest("PUT", "/v1/admin/storage/keeping", strings.NewReader(`{"preset":"long"}`))
	req.Header.Set("Authorization", "Bearer "+owner)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", fmt.Sprint(int64(stale.revision)))
	reply := httptest.NewRecorder()
	s.ServeHTTP(reply, req)
	if reply.Code != 412 {
		t.Fatal("a child edit did not invalidate the preset revision", reply.Code, reply.Body)
	}
	doc, err := s.settings.Read("storage")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(doc.Values, &fields); err != nil {
		t.Fatal(err)
	}
	if _, stored := fields["keepGeneratedFiles"]; stored {
		t.Fatal("the preset was a second stored source", fields)
	}
	if _, err := s.settings.Read(prepare.Document.Name()); err != nil || prepare.Document.Get(s.settings).KeepDays != 7 {
		t.Fatal(err)
	}
	if w := call(t, s, "PUT", "/v1/admin/storage/keeping", members[0], map[string]string{"preset": "long"}, nil); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	if w := call(t, s, "PUT", "/v1/admin/storage/keeping", owner, map[string]int{"preparedDownloadsDays": 8}, nil); w.Code != 422 {
		t.Fatal(w.Code, w.Body)
	}
}
