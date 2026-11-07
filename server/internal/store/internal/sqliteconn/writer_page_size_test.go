package sqliteconn

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestWriterPageSizeIsExplicitAndWriterOnly(t *testing.T) {
	base := Config{Role: Writer, Connections: 1, CacheKiB: 1024, Synchronous: Normal}
	for _, size := range []int32{0, 512, 1024, 4096, 65536} {
		cfg := base
		cfg.WriterPageSize = size
		if err := cfg.validate(); err != nil {
			t.Fatal(size, err)
		}
	}
	for _, size := range []int32{-1, 1, 511, 1000, 65537} {
		cfg := base
		cfg.WriterPageSize = size
		if err := cfg.validate(); !errors.Is(err, ErrPolicy) {
			t.Fatal("invalid page assertion", size, err)
		}
	}
	for _, role := range []Role{Reader, Checkpointer} {
		cfg := base
		cfg.Role, cfg.WriterPageSize = role, 4096
		if err := cfg.validate(); !errors.Is(err, ErrPolicy) {
			t.Fatal("non-writer page assertion", role, err)
		}
	}
	for _, value := range []any{Config{WriterPageSize: 4096}, Settings{WriterPageSize: 4096}} {
		b, err := json.Marshal(value)
		if err != nil || !bytes.Contains(b, []byte(`"WriterPageSize":4096`)) {
			t.Fatal("missing explicit evidence", string(b), err)
		}
	}
}

func TestWriterPageSizeAssertionNeverSetsOrRepairs(t *testing.T) {
	_, db, cfg := fixture(t)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var actual int32
	if err := conn.QueryRowContext(t.Context(), "PRAGMA main.page_size").Scan(&actual); err != nil {
		t.Fatal(err)
	}
	got, err := Assert(t.Context(), conn, cfg)
	if err != nil || got.WriterPageSize != 0 {
		t.Fatal("zero policy fabricated a geometry sample", got, err)
	}
	cfg.WriterPageSize = actual
	got, err = Assert(t.Context(), conn, cfg)
	if err != nil || got.WriterPageSize != actual {
		t.Fatal("actual page assertion", got, err)
	}
	wrong := actual / 2
	if actual == 512 {
		wrong = 1024
	}
	cfg.WriterPageSize = wrong
	got, err = Assert(t.Context(), conn, cfg)
	if !errors.Is(err, ErrPolicy) || got.WriterPageSize != actual {
		t.Fatal("mismatched geometry accepted or hidden", got, err)
	}
	var after int32
	if err := conn.QueryRowContext(t.Context(), "PRAGMA main.page_size").Scan(&after); err != nil || after != actual {
		t.Fatal("assertion changed physical page size", actual, after, err)
	}
}
