package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func main() {
	dir := os.Args[1]
	must(os.MkdirAll(dir, 0755))
	for _, persistent := range []bool{false, true} {
		path := filepath.Join(dir, fmt.Sprintf("control-%v.sqlite", persistent))
		dsn := path
		if persistent {
			p := url.Values{}
			for _, q := range []string{"foreign_keys(1)", "synchronous(NORMAL)", "wal_autocheckpoint(0)", "cache_size(-65536)"} {
				p.Add("_pragma", q)
			}
			dsn += "?" + p.Encode()
		}
		db, e := sql.Open("sqlite", dsn)
		must(e)
		db.SetMaxOpenConns(1)
		for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA synchronous=NORMAL", "PRAGMA wal_autocheckpoint=0", "PRAGMA cache_size=-65536", "CREATE TABLE parent(id INTEGER PRIMARY KEY)", "CREATE TABLE child(id INTEGER REFERENCES parent(id))", "INSERT INTO parent VALUES(1)", "INSERT INTO child VALUES(1)"} {
			_, e = db.Exec(q)
			must(e)
		}
		snap := func() map[string]any {
			m := map[string]any{}
			for _, p := range []string{"foreign_keys", "synchronous", "wal_autocheckpoint", "cache_size"} {
				var v int
				must(db.QueryRow("PRAGMA " + p).Scan(&v))
				m[p] = v
			}
			c, e := db.Conn(context.Background())
			must(e)
			must(c.Raw(func(v any) error { m["connection"] = fmt.Sprintf("%p", v); return nil }))
			must(c.Close())
			return m
		}
		before := snap()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
		rows, e := db.QueryContext(ctx, `WITH RECURSIVE c(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM c WHERE x<1000000) SELECT x FROM c ORDER BY random()`)
		if e == nil {
			for rows.Next() {
				var n int
				must(rows.Scan(&n))
			}
			e = rows.Err()
			must(rows.Close())
		}
		cancel()
		after := snap()
		_, de := db.Exec("DELETE FROM parent WHERE id=1")
		err := ""
		if de != nil {
			err = de.Error()
		}
		out := map[string]any{"persistent_dsn": persistent, "before": before, "after_timeout": after, "read_error": fmt.Sprint(e), "delete_error": err}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		must(db.Close())
	}
}
