package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"testing"
	"time"
)

func TestIdleEventHeartbeatsDoNotReadTheDatabase(t *testing.T) {
	old := eventHeartbeat
	eventHeartbeat = 50 * time.Millisecond
	defer func() { eventHeartbeat = old }()
	d, _, _, member := notificationFixture(t)
	d.Events = eventfeed.New(d.DB)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+member.AccessToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, ": heartbeat") {
			break
		}
	}
	before := dbwork.Reads().Statements
	for i := 0; i < 3; {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, ": heartbeat") {
			i++
		}
	}
	if after := dbwork.Reads().Statements; after != before {
		t.Fatalf("idle heartbeats issued %d statements", after-before)
	}
}

func TestGridValidatorSurvivesTokenRotationAndUnrelatedWrites(t *testing.T) {
	d, _, _, member := notificationFixture(t)
	p := identity.Principal{Viewer: member.Viewer, Hash: "old", Epoch: 1}
	req := httptest.NewRequest("GET", "/v1/libraries/films/content", nil)
	req.SetPathValue("id", "films")
	if _, err := d.DB.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('films','Films','movie','/films'),('other','Other','movie','/other')`); err != nil {
		t.Fatal(err)
	}
	before := d.gridTag(req, p, 24*time.Hour)
	if before == "" {
		t.Fatal("validator was disabled")
	}
	p.Hash = "renewed"
	if _, err := d.DB.Exec(`UPDATE library_revisions SET revision=revision+1 WHERE library_id='other'`); err != nil {
		t.Fatal(err)
	}
	if after := d.gridTag(req, p, 24*time.Hour); before != after {
		t.Fatal("token renewal or another library changed validator")
	}
	if _, err := d.DB.Exec(`UPDATE library_revisions SET revision=revision+1 WHERE library_id='films'`); err != nil {
		t.Fatal(err)
	}
	if after := d.gridTag(req, p, 24*time.Hour); before == after {
		t.Fatal("affected resource kept validator")
	}
	before = d.gridTag(req, p, 24*time.Hour)
	if _, err := d.DB.Exec(`UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if after := d.gridTag(req, p, 24*time.Hour); before == after {
		t.Fatal("committed owner settings kept a stale grid validator")
	}
	before = d.gridTag(req, p, 24*time.Hour)
	if _, err := d.DB.Exec(`INSERT INTO console_documents VALUES('server',900,'{}',0)`); err != nil {
		t.Fatal(err)
	}
	if after := d.gridTag(req, p, 24*time.Hour); before != after {
		t.Fatal("retired server document still changed the grid validator")
	}
	before = d.gridTag(req, p, 24*time.Hour)
	if _, err := d.DB.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age) VALUES(?,1) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=1`, p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if after := d.gridTag(req, p, 24*time.Hour); before == after {
		t.Fatal("changed restrictions kept validator")
	}
}

func TestCatalogueFailuresRequireOwner(t *testing.T) {
	d, _, owner, member := notificationFixture(t)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	if _, err := d.DB.Exec(`INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(18,999,'A title could not be read.',1)`); err != nil {
		t.Fatal(err)
	}
	if w := notificationRequest(mux, "GET", member.AccessToken, "/v1/admin/catalogue/failures", ""); w.Code != 403 {
		t.Fatalf("member: %d %s", w.Code, w.Body.String())
	}
	if w := notificationRequest(mux, "GET", owner.AccessToken, "/v1/admin/catalogue/failures", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "A title could not be read.") {
		t.Fatalf("owner: %d %s", w.Code, w.Body.String())
	}
}
