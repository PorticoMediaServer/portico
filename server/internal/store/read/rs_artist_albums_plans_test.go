package read

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func audioRelationsFixture(t *testing.T) *browseFixture {
	t.Helper()
	f := rsChildrenSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(13,'author',0,1,1,1,1)`)
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(13001,randomblob(16),1,13)`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,'  ÉCOLE  ','e','e','e','e',0,public_id,public_id FROM catalog_subjects WHERE id=13001`)
	f.exec(t, `INSERT INTO catalog_song_artists(song_id,artist_id) VALUES(4001,2006)`)
	return f
}

func TestRSAudioRelationPlans(t *testing.T) {
	f := audioRelationsFixture(t)
	cfg := browseReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	raw, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	defs := RSAudioRelationDefinitions()
	cat, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	var captures []plan.Golden
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g, err := plan.Capture(t.Context(), conn, cat, browsePlanFixture(cfg, rsChildrenFixtureHash(t), stats), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.New(defs, g.Receipt()); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, g)
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		b, e := json.MarshalIndent(captures, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "rs-audio-relations-plans.json"), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
