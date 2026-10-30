package administration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"strings"
	"testing"
)

func TestPersistedLiveConfigurationCredentialsAndRevision(t *testing.T) {
	s, db, _ := newService(t)
	ctx := context.Background()
	id := channelLookupSource(t, db)
	secret := "private-password-017"
	username := "private-user-017"
	raw := `{"draft":{"id":"` + id + `","name":"Source","kind":"xtream","locator":"https://provider.invalid","guideUrl":"https://guide.invalid/` + secret + `/guide.xml?token=private-token-017","username":"` + username + `","password":"` + secret + `","refreshMs":3600000,"ownerLimit":2,"viewerAccess":"server-members","useDiscoveredCapacity":true,"futureSecret":"opaque-secret-017"},"approvals":[],"physicalCount":4,"futureSecret":"root-secret-017"}`
	if _, err := db.Exec(`INSERT INTO live_remote_configs(source_id,sealed,next_due_ms) VALUES(?,?,0)`, id, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO live_source_settings(source_id,physical_count,owner_limit,viewer_access) VALUES(?,4,2,'server-members')`, id); err != nil {
		t.Fatal(err)
	}
	doc, err := s.LiveSourceConfiguration(ctx, allow, id)
	if err != nil {
		t.Fatal(err)
	}
	assertPublic := func(doc LiveSourceDocument) {
		t.Helper()
		body, _ := json.Marshal(doc)
		for _, v := range []string{secret, username, "private-token-017", "opaque-secret-017", "root-secret-017", "username", "password"} {
			if strings.Contains(string(body), v) {
				t.Fatalf("private field escaped: %s", v)
			}
		}
	}
	assertPublic(doc)
	if doc.Settings.Remote.GuideURL != "" || !doc.Settings.Remote.GuideURLRedacted {
		t.Fatal("guide tokens not redacted")
	}
	doc.Settings.Remote.OwnerLimit = 3
	doc.Settings.Remote.RefreshMs = 43200000
	access := "owner-only"
	doc.Settings.ViewerAccess = &access
	saved, err := s.SaveLiveSourceConfiguration(ctx, allow, id, Change[LiveSourceSettings]{ExpectedRevision: doc.Revision, OperationID: "persisted-settings-1", Settings: doc.Settings})
	if err != nil {
		t.Fatal(err)
	}
	assertPublic(saved)
	read := func() map[string]any {
		t.Helper()
		var raw []byte
		if err := db.QueryRow(`SELECT sealed FROM live_remote_configs WHERE source_id=?`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if json.Unmarshal(raw, &v) != nil {
			t.Fatal("bad config")
		}
		if v["futureSecret"] != "root-secret-017" {
			t.Fatal("unknown root changed")
		}
		return v["draft"].(map[string]any)
	}
	draft := read()
	if draft["password"] != secret || draft["username"] != username || draft["futureSecret"] != "opaque-secret-017" || draft["guideUrl"] != "https://guide.invalid/"+secret+"/guide.xml?token=private-token-017" {
		t.Fatal("omitted secret changed")
	}
	if _, err = s.SaveLiveSourceConfiguration(ctx, allow, id, Change[LiveSourceSettings]{ExpectedRevision: doc.Revision, OperationID: "persisted-stale", Settings: doc.Settings}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale accepted", err)
	}
	replacement := "replacement-secret-017"
	empty := ""
	saved.Settings.Remote.Password = &replacement
	saved.Settings.Remote.Username = &empty
	saved, err = s.SaveLiveSourceConfiguration(ctx, allow, id, Change[LiveSourceSettings]{ExpectedRevision: saved.Revision, OperationID: "persisted-replace", Settings: saved.Settings})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(saved)
	if strings.Contains(string(body), replacement) || strings.Contains(string(body), secret) || saved.Settings.Remote.GuideURL != "" {
		t.Fatal("replacement echoed")
	}
	if draft = read(); draft["password"] != replacement {
		t.Fatal("replacement missing")
	}
	if _, ok := draft["username"]; ok {
		t.Fatal("cleared username retained")
	}
	saved.Settings.Remote.Password = &empty
	saved, err = s.SaveLiveSourceConfiguration(ctx, allow, id, Change[LiveSourceSettings]{ExpectedRevision: saved.Revision, OperationID: "persisted-clear", Settings: saved.Settings})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := read()["password"]; ok {
		t.Fatal("cleared password retained")
	}
	var adminBody string
	if err = db.QueryRow(`SELECT body FROM admin_documents WHERE scope=?`, liveSourceScope(id)).Scan(&adminBody); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(adminBody, "remote") || strings.Contains(adminBody, "password") {
		t.Fatal("duplicate private state")
	}
	if *saved.Settings.ViewerAccess != "owner-only" || saved.Settings.Remote.OwnerLimit != 3 || saved.TunerCount != 3 {
		t.Fatal("persisted projection mismatch")
	}
	// A source revision (including another enable/publish) is part of this same fence.
	if _, err = db.Exec(`UPDATE live_sources SET revision=revision+1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveLiveSourceConfiguration(ctx, allow, id, Change[LiveSourceSettings]{ExpectedRevision: saved.Revision, OperationID: "persisted-source-stale", Settings: saved.Settings}); !errors.Is(err, ErrConflict) {
		t.Fatal("source race accepted", err)
	}
}

func TestPersistedLiveConfigurationGuidePolicy(t *testing.T) {
	s, db, _ := newService(t)
	ctx := context.Background()
	id := channelLookupSource(t, db)
	raw := `{"draft":{"id":"` + id + `","kind":"m3u","guideUrl":"","refreshMs":3600000},"approvals":[]}`
	if _, err := db.Exec(`INSERT INTO live_remote_configs(source_id,sealed,next_due_ms) VALUES(?,?,0)`, id, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	doc, err := s.LiveSourceConfiguration(ctx, allow, id)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := func() {
		t.Helper()
		current, err := s.LiveSourceConfiguration(ctx, allow, id)
		if err != nil || current.Revision != doc.Revision || current.Digest != doc.Digest {
			t.Fatal("policy refusal changed the document", err)
		}
		var sealed string
		if err = db.QueryRow(`SELECT sealed FROM live_remote_configs WHERE source_id=?`, id).Scan(&sealed); err != nil || sealed != raw {
			t.Fatal("policy refusal changed private configuration", err)
		}
		var writes int
		if err = db.QueryRow(`SELECT (SELECT COUNT(*) FROM admin_documents WHERE scope=?) + (SELECT COUNT(*) FROM admin_receipts WHERE scope=?)`, liveSourceScope(id), liveSourceScope(id)).Scan(&writes); err != nil || writes != 0 {
			t.Fatal("policy refusal wrote a document or receipt", err)
		}
	}
	for i, address := range []string{"http://169.254.169.254/latest/meta-data", "http://192.168.10.23/guide.xml"} {
		doc.Settings.Remote.GuideURL = address
		before := dbwork.WriteGate().Stats().Acquired
		_, err = s.SaveLiveSourceConfiguration(ctx, allow, id, Change[LiveSourceSettings]{ExpectedRevision: doc.Revision, OperationID: []string{"guide-blocked", "guide-lan"}[i], Settings: doc.Settings})
		if !errors.Is(err, livechannels.ErrNetworkPolicy) {
			t.Fatalf("policy bypass: %v", err)
		}
		if dbwork.WriteGate().Stats().Acquired != before {
			t.Fatal("policy refusal acquired the writer")
		}
		unchanged()
		if i == 1 {
			var lan *livechannels.LANConfirmationRequired
			if !errors.As(err, &lan) || len(lan.Roots) != 1 {
				t.Fatal("missing LAN confirmation", err)
			}
			doc.Settings.Remote.ConfirmedLANRoots = lan.Roots
		}
	}
	// Approve must also refuse blocked confirmation roots before taking the writer.
	roots := doc.Settings.Remote.ConfirmedLANRoots
	doc.Settings.Remote.ConfirmedLANRoots = []string{"http://169.254.169.254/"}
	before := dbwork.WriteGate().Stats().Acquired
	_, err = s.SaveLiveSourceConfiguration(ctx, allow, id, Change[LiveSourceSettings]{ExpectedRevision: doc.Revision, OperationID: "guide-confirmation-blocked", Settings: doc.Settings})
	if !errors.Is(err, livechannels.ErrNetworkPolicy) || dbwork.WriteGate().Stats().Acquired != before {
		t.Fatal("confirmation policy refusal took the writer or was bypassed", err)
	}
	unchanged()
	doc.Settings.Remote.ConfirmedLANRoots = roots
	saved, err := s.SaveLiveSourceConfiguration(ctx, allow, id, Change[LiveSourceSettings]{ExpectedRevision: doc.Revision, OperationID: "guide-confirmed", Settings: doc.Settings})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Settings.Remote.GuideURL != "http://192.168.10.23/guide.xml" {
		t.Fatal("address not persisted")
	}
}
