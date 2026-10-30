package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestRecordingOwnerDirectoryCurrentMembershipAndPagination(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','Owner',x'00','primary',1);
INSERT INTO direct_profiles(id,account_id,name) VALUES('child','owner','Child'),('deleted','owner','Deleted');
UPDATE direct_profiles SET deleted=1 WHERE id='deleted';
INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('disabled','Disabled',x'00','disabled-profile',1);
UPDATE direct_memberships SET disabled=1 WHERE account_id='disabled';
INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('a-revoked','Revoked',x'00','revoked-profile',1);
UPDATE direct_memberships SET disabled=1 WHERE account_id='a-revoked';
INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('hosted-account','Hosted account',x'00','hosted-profile',1);
UPDATE direct_profiles SET name='Hosted child' WHERE id='hosted-profile';
INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('hosted-account','portico-account');`)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	policy := hosted.Policy{ServerID: id.ID(), Revision: 1, ExpiresAt: expires, Members: []hosted.Member{
		{AccountID: "a-revoked", ProfileID: "revoked-profile", Role: "member", Username: "Revoked", ProfileName: "Revoked"},
		{AccountID: "hosted-account", ProfileID: "hosted-profile", Role: "member", Username: "Hosted account", ProfileName: "Hosted child"},
	}}
	raw, _ := json.Marshal(policy)
	if _, err = db.Exec(`INSERT INTO policy VALUES(?,?,?,?)`, id.ID(), 1, string(raw), expires); err != nil {
		t.Fatal(err)
	}
	control, err := hosted.New(db, id, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), zeroHostedRootID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := id.Issue("owner", "primary", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{DB: db, Identity: id, Hosted: control})
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token.AccessToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	const route = "/v1/admin/dvr/recording-owners"
	seen := map[string]recordingOwnerChoice{}
	cursor := ""
	pages := 0
	for {
		w := get(route + "?limit=1&cursor=" + url.QueryEscape(cursor))
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var envelope struct {
			Result struct {
				Items      []recordingOwnerChoice `json:"items"`
				NextCursor string                 `json:"nextCursor"`
			} `json:"result"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		pages++
		if pages > 10 {
			t.Fatal("nonterminating cursor")
		}
		for _, v := range envelope.Result.Items {
			if _, exists := seen[v.Owner.ProfileID]; exists {
				t.Fatal("duplicate page entry")
			}
			seen[v.Owner.ProfileID] = v
		}
		if envelope.Result.NextCursor == "" {
			break
		}
		if envelope.Result.NextCursor == cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = envelope.Result.NextCursor
	}
	if len(seen) != 3 || seen["primary"].AccountName != "Owner" || seen["child"].ProfileName != "Child" || seen["hosted-profile"].ProfileName != "Hosted child" {
		t.Fatalf("directory %#v", seen)
	}
	if pages != 3 {
		t.Fatalf("directory did not advance: %d pages", pages)
	}
	var grants int
	if err = db.QueryRow(`SELECT count(*) FROM profile_restrictions`).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("directory manufactured restrictions: %d %v", grants, err)
	}
	// Who can record: the owner's main profile records by default; nobody else does.
	if !seen["primary"].CanRecord || seen["child"].CanRecord || seen["hosted-profile"].CanRecord || seen["primary"].Revision != 1 {
		t.Fatalf("recording defaults %#v", seen)
	}
	// A disabled Portico Account membership stops offering its profiles;
	// the owner and child on the other account remain selectable.
	if _, err = db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='hosted-account'`); err != nil {
		t.Fatal(err)
	}
	w := get(route)
	var envelope struct {
		Result struct {
			Items []recordingOwnerChoice `json:"items"`
		} `json:"result"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(envelope.Result.Items) != 2 {
		t.Fatalf("stale policy directory %d %s", w.Code, w.Body.String())
	}
	for _, suffix := range []string{"?limit=0", "?limit=201", "?limit=1&limit=2", "?cursor=bad", "?cursor=W10", "?unknown=1", "?%zz=1"} {
		if w = get(route + suffix); w.Code != 400 {
			t.Fatalf("accepted %s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
}
