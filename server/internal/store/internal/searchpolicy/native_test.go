package searchpolicy_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	_ "modernc.org/sqlite"
)

func TestBundledNativeProfile(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "profile.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var actual searchpolicy.Engine
	if err := conn.QueryRowContext(ctx, "SELECT sqlite_version(),sqlite_source_id()").Scan(&actual.Version, &actual.SourceID); err != nil {
		t.Fatal(err)
	}
	if got := searchpolicy.Compare(searchpolicy.Compiled(), actual); got.State != searchpolicy.Current {
		t.Fatalf("bundled engine/profile = %+v: %+v", actual, got)
	}
	if err := schema.InitializeEmpty(ctx, conn); err != nil {
		t.Fatal(err)
	}
	// The literal list is the reviewed v1 native/content contract, not every
	// table in the database. An unrelated DDL change must not rebuild search.
	rows, err := conn.QueryContext(ctx, `SELECT type,name,sql FROM main.sqlite_schema WHERE name IN(
 'catalog_people_names','reading_text_fts','search_own_prefix',
 'search_fragment_prefix','search_fragment_lexical','search_fragment_grams',
 'search_own_content','search_person_content','search_fragment_content')
 ORDER BY name COLLATE BINARY`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	h := sha256.New()
	h.Write([]byte("portico-search-native-format-v1\x00"))
	count := 0
	for rows.Next() {
		var kind, name, declaration string
		if err := rows.Scan(&kind, &name, &declaration); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{kind, name, declaration} {
			var n [8]byte
			binary.BigEndian.PutUint64(n[:], uint64(len(value)))
			h.Write(n[:])
			h.Write([]byte(value))
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 9 {
		t.Fatalf("native format declarations = %d, want 9", count)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != searchpolicy.NativeFormatSHA256 {
		t.Fatalf("native format = %s, want %s; review the bank format before changing this pin", got, searchpolicy.NativeFormatSHA256)
	}
}
