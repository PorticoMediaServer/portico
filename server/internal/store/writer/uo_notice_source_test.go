package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

func TestUONoticePublishSourceAdmission(t *testing.T) {
	view := uoNoticeView{scope: "shared:admins", audience: uoNoticeAudienceServerAdm}
	if !view.valid() {
		t.Fatal("canonical shared view refused")
	}
	for _, edit := range []func(*uoNoticeView){
		func(v *uoNoticeView) { v.audience = "" },
		func(v *uoNoticeView) { v.audience = "owner" },
		func(v *uoNoticeView) { v.scope = "" },
		func(v *uoNoticeView) { v.scope = "   " },
		func(v *uoNoticeView) { v.scope = strings.Repeat("s", uoNoticeMaxScopeBytes+1) },
	} {
		copy := view
		edit(&copy)
		if copy.valid() {
			t.Fatal("accepted invalid view", copy)
		}
	}
	for _, audience := range []string{uoNoticeAudienceProfile, uoNoticeAudienceAccount, uoNoticeAudienceServerAdm} {
		if !(uoNoticeView{scope: "s", audience: audience}).valid() {
			t.Fatal("refused allowlisted audience", audience)
		}
	}
	if !uoNoticeText("storage low", uoNoticeMaxTitleBytes, true) || !uoNoticeText("", 10, false) {
		t.Fatal("valid text refused")
	}
	for _, text := range []string{"", " ", "\t\n", "bell\x07", "bidi\u202e", "isolate\u2066", strings.Repeat("x", uoNoticeMaxTitleBytes+1), "\xff"} {
		if uoNoticeText(text, uoNoticeMaxTitleBytes, true) {
			t.Fatal("accepted invalid text", text)
		}
	}
	if uoNoticeText(strings.Repeat("x", uoNoticeMaxBodyBytes+1), uoNoticeMaxBodyBytes, false) {
		t.Fatal("accepted oversized optional text")
	}
	if uoNoticeText(" ", 10, false) {
		t.Fatal("accepted whitespace-only optional text")
	}
	for _, identifier := range []string{"", "storage low", "storage/low", "storagé", strings.Repeat("s", uoNoticeMaxSourceBytes+1)} {
		if uoNoticeIdentifier(identifier, uoNoticeMaxSourceBytes) {
			t.Fatal("accepted invalid identifier", identifier)
		}
	}
	for _, identifier := range []string{"storage", "storage.low_v2:one-two", "A0"} {
		if !uoNoticeIdentifier(identifier, uoNoticeMaxSourceBytes) {
			t.Fatal("refused valid identifier", identifier)
		}
	}
}

func TestUONoticePublishSourceEncoding(t *testing.T) {
	if got, err := uoNoticeEncodedArguments(nil); err != nil || got != "{}" {
		t.Fatal("empty arguments", got, err)
	}
	if got, err := uoNoticeEncodedActions(nil); err != nil || got != "[]" {
		t.Fatal("empty actions", got, err)
	}
	// Canonical encoding sorts keys, so an identical producer retry is equal
	// regardless of map iteration order.
	first, err := uoNoticeEncodedArguments(map[string]string{"b": "2", "a": "1", "c": "3"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := uoNoticeEncodedArguments(map[string]string{"c": "3", "a": "1", "b": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != `{"a":"1","b":"2","c":"3"}` {
		t.Fatal("non-canonical arguments", first, second)
	}
	actions, err := uoNoticeEncodedActions([]notify.Action{{Kind: "navigate", Label: "Home", Target: &notify.NavigateTarget{View: "home"}}})
	if err != nil || !strings.Contains(actions, `"kind":"navigate"`) {
		t.Fatal("actions encoding", actions, err)
	}
	view := uoNoticeView{scope: "s", audience: uoNoticeAudienceServerAdm}
	if got, want := uoNoticeRecordBytes(view, "id", "info", "storage", "storage.low", "title", "body", "dedupe", "{}", "[]"), int64(2+1+12+4+7+11+5+4+6+2+2); got != want {
		t.Fatal("record byte accounting", got, want)
	}
}

func TestUONoticePublishSourceLookupIsScoped(t *testing.T) {
	w := uoNoticePublishFixture(t)
	q := uoNoticeDraft("a")
	q.view = uoNoticeView{scope: `["authority","account-one","profile-one"]`, audience: uoNoticeAudienceProfile}
	published := uoNoticePublishOne(t, w, q)
	turn := uoNoticePublishTurn(t, w)
	stored, err := loadUONoticeSourceRecord(t.Context(), turn, q.view, q.source, q.dedupe)
	if err != nil || !stored.exists || stored.id != published.id || stored.revision != published.revision || stored.authorityRevision != published.authorityRevision {
		t.Fatal("own source record", stored, err)
	}
	// The identical produced fact in another principal's scope is absent, not
	// disclosed; nothing here can see or restate the first record.
	for _, other := range []uoNoticeView{
		{scope: `["authority","account-one","profile-two"]`, audience: uoNoticeAudienceProfile},
		{scope: `["authority","account-two","profile-one"]`, audience: uoNoticeAudienceProfile},
		{scope: "shared:admins", audience: uoNoticeAudienceServerAdm},
	} {
		absent, err := loadUONoticeSourceRecord(t.Context(), turn, other, q.source, q.dedupe)
		if err != nil || absent.exists {
			t.Fatal("source record leaked across inboxes", other, absent, err)
		}
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	missing, err := loadUONoticeWithdrawTarget(t.Context(), uoNoticePublishTurn(t, w), q.view, "missing")
	if !errors.Is(err, ErrConflict) || missing.exists {
		t.Fatal("missing withdrawal target", missing, err)
	}
}
