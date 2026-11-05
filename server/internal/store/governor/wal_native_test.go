package governor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestNativeWALCopyIsNotReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.sqlite")
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, settings, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if settings.Engine != "3.53.4" || settings.TempStore != 1 {
		t.Fatal(settings)
	}
	writer, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = sqliteconn.Assert(t.Context(), writer, cfg); err != nil {
		t.Fatal(err)
	}
	// Tiny observation fixture, not INT's canonical schema or command recipe.
	if _, err = writer.ExecContext(t.Context(), "CREATE TABLE fixture(id INTEGER PRIMARY KEY, v TEXT); INSERT INTO fixture VALUES(1,'first')"); err != nil {
		t.Fatal(err)
	}
	before, err := ReadWAL(path)
	if err != nil || before.Frames == 0 || before.UncheckpointedBytes == 0 {
		t.Fatal(before, err)
	}
	w := gate(t)
	publish(t, w, before, time.Now())
	cfg.Role = sqliteconn.Checkpointer
	cpDB, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cpDB.Close()
	cp, err := cpDB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if _, err = sqliteconn.Assert(t.Context(), cp, cfg); err != nil {
		t.Fatal(err)
	}
	var busy, frames, copied int
	if err = cp.QueryRowContext(t.Context(), "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil {
		t.Fatal(err)
	}
	after, err := ReadWAL(path)
	if err != nil || busy != 0 || uint32(frames) != before.Frames || uint32(copied) != after.Backfill || after.Backfill != after.Frames || after.UncheckpointedBytes != 0 || after.ActiveBytes != before.ActiveBytes || after.PhysicalBytes != before.PhysicalBytes {
		t.Fatal(before, after, busy, frames, copied, err)
	}
	publish(t, w, after, time.Now())
	if w.ResetCount() != 0 {
		t.Fatal("completed copy counted as reset")
	}
	if _, err = writer.ExecContext(t.Context(), "UPDATE fixture SET v='second' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	reused, err := ReadWAL(path)
	if err != nil || reused.Salt == after.Salt || reused.Frames >= after.Frames || reused.PhysicalBytes != after.PhysicalBytes {
		t.Fatal(after, reused, err)
	}
	publish(t, w, reused, time.Now())
	if w.ResetCount() != 1 {
		t.Fatal("native reuse not observed")
	}
}
