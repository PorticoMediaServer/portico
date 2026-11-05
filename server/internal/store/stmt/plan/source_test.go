package plan_test

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These primitive semantic oracles are ported from V's product contract. They
// do not import old-layer order/identity helpers or assert legacy lexical bugs.
// Writer acceptance, full API responses and crash journals remain later gates.
func checkCanonicalSourceFacts(t *testing.T, conn *sql.Conn, registry *stmt.Registry) {
	t.Helper()
	ctx := context.Background()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	must("INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)")
	must("INSERT INTO catalog_libraries(id,library_id,root) VALUES(1,'library1','/same'),(2,'library2','/same')")
	raw := []string{"2026-11-04T12:00:00Z", "2026-11-04T12:00:00.5Z", "2026-11-04T12:00:00.1Z", "2026-11-04T12:00:00.11Z", "2026-11-04T12:00:00.0000002Z", "2026-11-04T12:00:00.0000001Z", "2026-11-04T12:00:00.000000000Z", "2026-11-04T12:00:00Z"}
	for i, source := range raw {
		id := int64(i + 1)
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		must("INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,1)", id, public)
		instant, err := time.Parse(time.RFC3339Nano, source)
		if err != nil {
			t.Fatal(err)
		}
		// Fixture loading normalizes source time to the canonical stored format.
		// The literal expected order below is independent of this normalization.
		fixed := instant.UTC().Format("2006-01-02T15:04:05.000000000Z")
		title := fmt.Sprintf("title-%d", id)
		must("INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,added_at,metadata_incarnation,artwork_incarnation) VALUES(?,?,1,1,?,?,?,?,?,0,?,?,?)", id, public, title, title, title, title, title, fixed, public, public)
	}
	for _, direction := range []string{"asc", "desc"} {
		read := func(name string, args ...any) []struct {
			id  int64
			key string
		} {
			s, err := registry.Lookup(stmt.ID("chronology." + direction + "." + name))
			if err != nil {
				t.Fatal(err)
			}
			bindings := append([]any{int64(1), int64(1)}, args...)
			bound, err := s.Bind(bindings...)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := bound.QueryContext(ctx, conn)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			result := []struct {
				id  int64
				key string
			}{}
			for rows.Next() {
				var row struct {
					id  int64
					key string
				}
				if err := rows.Scan(&row.id, &row.key); err != nil {
					t.Fatal(err)
				}
				result = append(result, row)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return result
		}
		page := read("first", int64(1))
		got := []int64{}
		for len(page) > 0 {
			last := page[0]
			got = append(got, last.id)
			if len(got) > len(raw) {
				t.Fatal("chronology cursor loop")
			}
			page = read("tail", last.key, last.id, int64(1))
			if len(page) == 0 {
				page = read("next", last.key, int64(1))
			}
		}
		want := []int64{2, 4, 3, 5, 6, 8, 7, 1}
		if direction == "asc" {
			slices.Reverse(want)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("CHRONO-LEAF-INSTANT-ORDER %s: got %v, want %v", direction, got, want)
		}
	}
	// Same physical root/path still has different retained identities and assets
	// in independent libraries. One library's retained subject need not be live.
	public := make([]byte, 16)
	binary.LittleEndian.PutUint64(public, 9)
	must("INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(9,?,2,1)", public)
	must("INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(1,'same/path',1),(2,'same/path',9)")
	must("INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(1,'token1','same/path',0,0,'','','',0,0,0,1),(2,'token2','same/path',0,0,'','','',0,0,0,2)")
	for _, library := range []int64{1, 2} {
		s, _ := registry.Lookup("identity.by_source")
		bound, err := s.Bind(library, "same/path")
		if err != nil {
			t.Fatal(err)
		}
		var got int64
		if err := bound.QueryRowContext(ctx, conn).Scan(&got); err != nil {
			t.Fatal(err)
		}
		want := int64(1)
		if library == 2 {
			want = 9
		}
		if got != want {
			t.Fatalf("retained identity leaked across library %d: %d", library, got)
		}
		s, _ = registry.Lookup("asset.by_path")
		bound, err = s.Bind(library, "same/path")
		if err != nil {
			t.Fatal(err)
		}
		var token string
		if err := bound.QueryRowContext(ctx, conn).Scan(&got, &token); err != nil {
			t.Fatal(err)
		}
		if got != library || token != fmt.Sprintf("token%d", library) {
			t.Fatalf("asset leaked across library %d: %d/%s", library, got, token)
		}
	}
	must("DELETE FROM catalog_entities WHERE id=1")
	s, _ := registry.Lookup("identity.by_source")
	bound, _ := s.Bind(int64(1), "same/path")
	var retained int64
	if err := bound.QueryRowContext(ctx, conn).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("retained identity lost with live item: %d %v", retained, err)
	}
}
