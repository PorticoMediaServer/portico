package intchange013

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"testing"
)

// Same versioned encoding as schema.nativeLayout: SQL bytes and engine are
// included, NULL declarations have a distinct tag, and every string is LP64BE.
// This emits metadata for a local embed draft, not an adopted layout claim.
func TestChange013EmbedMetadata(t *testing.T) {
	writeText := func(h hash.Hash, s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		io.WriteString(h, s)
	}
	out := map[string]any{}
	for _, v := range []struct {
		name, ddl string
		objects   int
	}{{"adopted012", before, 2135}, {"draft012plus013", candidate, 2136}} {
		f := native(t, v.ddl)
		digest := sha256.New()
		io.WriteString(digest, "portico-native-layout-sha256-v1\x00")
		writeText(digest, fmt.Sprintf("%x", sha256.Sum256([]byte(v.ddl))))
		writeText(digest, "3.53.4")
		rows, err := f.c.QueryContext(t.Context(), `SELECT type,name,tbl_name,sql FROM main.sqlite_schema ORDER BY type COLLATE BINARY,name COLLATE BINARY,tbl_name COLLATE BINARY`)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var kind, name, table string
			var declaration sql.NullString
			if err = rows.Scan(&kind, &name, &table, &declaration); err != nil {
				t.Fatal(err)
			}
			writeText(digest, kind)
			writeText(digest, name)
			writeText(digest, table)
			if declaration.Valid {
				digest.Write([]byte{1})
				writeText(digest, declaration.String)
			} else {
				digest.Write([]byte{0})
			}
			count++
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if count != v.objects {
			t.Fatal("object count", v.name, count, v.objects)
		}
		tables := f.scalar(t, `SELECT count(*) FROM sqlite_schema WHERE type='table'`)
		if tables != 708 {
			t.Fatal("table count", tables)
		}
		out[v.name] = map[string]any{"sql_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(v.ddl))), "layout_sha256": fmt.Sprintf("%x", digest.Sum(nil)), "objects": count, "tables": tables, "engine": "3.53.4", "format": "portico-native-layout-sha256-v1"}
	}
	evidence(t, "013-embed-metadata.json", out)
}
