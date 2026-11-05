package main

// Optional overflow for the ephemeral next-subject frontier. Main-source rows
// are closed before this private cache is written. It never takes the catalogue
// write lock. The same bundled engine uses a2MiB pager and a16MiB disk ceiling.
import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var frontierRAMHeads = 8192

func (l *lazySearch) frontierExec(ctx context.Context, q string, args ...any) error {
	c, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e := l.spill.ExecContext(c, q, args...)
	l.spillCalls++
	l.spillMaxMS = max(l.spillMaxMS, float64(time.Since(start).Microseconds())/1000)
	return e
}
func (l *lazySearch) ensureSpill(ctx context.Context) error {
	if l.spill != nil {
		return nil
	}
	f, e := os.CreateTemp(filepath.Dir(databasePath), "q-frontier-*.sqlite")
	if e != nil {
		return e
	}
	l.spillPath = f.Name()
	if e = f.Close(); e != nil {
		return e
	}
	l.spill, e = sql.Open("sqlite", l.spillPath)
	if e != nil {
		return e
	}
	l.spill.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=OFF", "PRAGMA synchronous=OFF", "PRAGMA cache_size=-2048", "PRAGMA temp_store=FILE", `CREATE TABLE frontier(owner INTEGER NOT NULL,relation INTEGER NOT NULL,id INTEGER NOT NULL,PRIMARY KEY(owner,relation)) WITHOUT ROWID`, `CREATE INDEX frontier_next ON frontier(id,owner,relation)`} {
		if e = l.frontierExec(ctx, q); e != nil {
			return e
		}
	}
	entries := l.streams
	l.streams = nil
	for i := 0; i < len(entries); i += 64 {
		if e = l.spillBatch(ctx, entries[i:min(i+64, len(entries))]); e != nil {
			return e
		}
	}
	return nil
}
func (l *lazySearch) spillBatch(ctx context.Context, entries []contextStream) error {
	rows := make([][3]int, len(entries))
	for i, s := range entries {
		rel := 0
		if s.relation {
			rel = 1
		}
		rows[i] = [3]int{s.owner, rel, s.id}
	}
	raw, e := json.Marshal(rows)
	if e != nil {
		return e
	}
	if e = l.frontierExec(ctx, `INSERT OR REPLACE INTO frontier(owner,relation,id) SELECT json_extract(value,'$[0]'),json_extract(value,'$[1]'),json_extract(value,'$[2]') FROM json_each(?)`, string(raw)); e != nil {
		return e
	}
	info, e := os.Stat(l.spillPath)
	if e != nil {
		return e
	}
	l.spillPeakBytes = max(l.spillPeakBytes, info.Size())
	if l.spillPeakBytes > 16<<20 {
		return fmt.Errorf("ephemeral frontier exceeds16MiB disk credit; explicit capacity failure")
	}
	return nil
}
func (l *lazySearch) frontierPush(ctx context.Context, s contextStream) error {
	if l.spill == nil && len(l.streams) < frontierRAMHeads {
		heap.Push(&l.streams, s)
		return nil
	}
	if e := l.ensureSpill(ctx); e != nil {
		return e
	}
	return l.spillBatch(ctx, []contextStream{s})
}
func (l *lazySearch) frontierPeek(ctx context.Context) (contextStream, bool, error) {
	if l.spill == nil {
		if len(l.streams) == 0 {
			return contextStream{}, false, nil
		}
		return l.streams[0], true, nil
	}
	c, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	var s contextStream
	var rel int
	e := l.spill.QueryRowContext(c, `SELECT owner,relation,id FROM frontier ORDER BY id,owner,relation LIMIT 1`).Scan(&s.owner, &rel, &s.id)
	l.spillCalls++
	l.spillMaxMS = max(l.spillMaxMS, float64(time.Since(start).Microseconds())/1000)
	if e == sql.ErrNoRows {
		return s, false, nil
	}
	s.relation = rel != 0
	return s, e == nil, e
}
func (l *lazySearch) frontierPop(ctx context.Context) (contextStream, error) {
	if l.spill == nil {
		return heap.Pop(&l.streams).(contextStream), nil
	}
	s, ok, e := l.frontierPeek(ctx)
	if e != nil {
		return s, e
	}
	if !ok {
		return s, fmt.Errorf("empty frontier pop")
	}
	rel := 0
	if s.relation {
		rel = 1
	}
	e = l.frontierExec(ctx, `DELETE FROM frontier WHERE owner=? AND relation=?`, s.owner, rel)
	return s, e
}
func (l *lazySearch) close() {
	l.reader.close()
	if l.spill != nil {
		l.spill.Close()
		os.Remove(l.spillPath)
		l.spill = nil
	}
}
