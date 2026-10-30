package livechannels

import (
	"context"
	"database/sql"
	"os"
	"time"
)

// Large guides use a private, disposable SQLite spool. It is separate from the
// catalogue writer and uses a small page cache; publication still checkpoints
// through the ordinary 128-row transactions and exposes only a complete guide.
//
// Everything below runs on the spool's own handle. The catalogue's write gate
// arbitrates writers to the one persistent database file, so a spool write must
// not take it: a guide refresh would otherwise queue the whole import behind
// sign-ins and progress writes, and hold that gate for whole-spool statements,
// for a file no reader of the catalogue can ever see. The spool commits also
// stay out of `dbwork`'s publication counter for the same reason: nothing the
// spool does is a catalogue change.
const programmeMemoryRows = 10000
const spoolColumns = `key,channel,title,lineage,series,episode,new_evidence,description,icon,facts,start_ms,end_ms`

type programmeSpool struct {
	db      *sql.DB
	path    string
	tx      *sql.Tx
	pending int
	count   int
}

// exec runs one autocommit statement on the private spool file: no gate, no
// commit notification, no publication. The file has one connection and one
// user, so there is no lock for another attempt to wait out.
func (s *programmeSpool) exec(query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(context.Background(), query, args...)
}

func newProgrammeSpool() (*programmeSpool, error) {
	file, e := os.CreateTemp(os.TempDir(), "portico-programmes-*.sqlite")
	if e != nil {
		return nil, e
	}
	path := file.Name()
	file.Close()
	db, e := sql.Open("sqlite", path)
	if e != nil {
		os.Remove(path)
		return nil, e
	}
	db.SetMaxOpenConns(1)
	spool := &programmeSpool{db: db, path: path}
	for _, query := range []string{`PRAGMA cache_size=-4096`, `PRAGMA journal_mode=DELETE`, `CREATE TABLE programmes(key TEXT PRIMARY KEY,channel TEXT NOT NULL,title TEXT NOT NULL,lineage TEXT NOT NULL,series TEXT NOT NULL,episode TEXT NOT NULL,new_evidence TEXT NOT NULL,description TEXT NOT NULL,icon TEXT NOT NULL,facts TEXT NOT NULL,start_ms INTEGER NOT NULL,end_ms INTEGER,UNIQUE(channel,start_ms)) STRICT`, `CREATE INDEX programmes_time ON programmes(channel,start_ms)`} {
		if _, e = spool.exec(query); e != nil {
			spool.close()
			return nil, e
		}
	}
	return spool, nil
}
func (s *programmeSpool) close() {
	if s.tx != nil {
		s.tx.Rollback()
	}
	s.db.Close()
	os.Remove(s.path)
	os.Remove(s.path + "-journal")
}
func (s *programmeSpool) flush() error {
	if s.tx == nil {
		return nil
	}
	e := s.tx.Commit()
	s.tx = nil
	s.pending = 0
	return e
}
func (s *programmeSpool) add(p parsedProgramme) (bool, error) {
	var e error
	if s.tx == nil {
		s.tx, e = s.db.BeginTx(context.Background(), nil)
		if e != nil {
			return false, e
		}
	}
	var end any
	if !p.end.IsZero() {
		end = p.end.UnixMilli()
	}
	result, e := s.tx.Exec(`INSERT INTO programmes VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, p.key, p.channel, p.title, p.lineage, p.series, p.episode, p.newEvidence, p.description, p.icon, p.facts, p.start.UnixMilli(), end)
	if e != nil {
		return false, e
	}
	n, _ := result.RowsAffected()
	if n == 1 {
		s.count++
	}
	s.pending++
	if s.pending >= 128 {
		e = s.flush()
	}
	return n == 1, e
}
func (d *parsedSource) close() {
	if d.spool != nil {
		d.spool.close()
	}
}
func (d *parsedSource) programmeCount() int {
	if d.spool != nil {
		return d.spool.count
	}
	return len(d.programmes)
}
func (d *parsedSource) spill() error {
	spool, e := newProgrammeSpool()
	if e != nil {
		return e
	}
	d.spool = spool
	for _, p := range d.programmes {
		kept, e := spool.add(p)
		if e != nil {
			return e
		}
		if !kept {
			d.diagnostics.SkippedProgrammes++
		}
	}
	d.programmes = nil
	return nil
}
func (d *parsedSource) normaliseSpool(days int) error {
	s := d.spool
	if e := s.flush(); e != nil {
		return e
	}
	next := `(SELECT min(b.start_ms) FROM programmes b WHERE b.channel=programmes.channel AND b.start_ms>programmes.start_ms)`
	result, e := s.exec(`UPDATE programmes SET end_ms=` + next + ` WHERE end_ms IS NULL AND ` + next + ` IS NOT NULL AND ` + next + `-start_ms<=604800000`)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	d.diagnostics.AdjustedProgrammes += int(n)
	result, e = s.exec(`DELETE FROM programmes WHERE end_ms IS NULL`)
	if e != nil {
		return e
	}
	n, _ = result.RowsAffected()
	if n > 0 {
		d.diagnostics.SkippedProgrammes += int(n)
		d.diagnostics.note("live.import.programmeEndMissing")
	}
	result, e = s.exec(`UPDATE programmes SET end_ms=` + next + ` WHERE end_ms>` + next)
	if e != nil {
		return e
	}
	n, _ = result.RowsAffected()
	d.diagnostics.AdjustedProgrammes += int(n)
	if days > 0 {
		now := time.Now()
		if _, e = s.exec(`DELETE FROM programmes WHERE end_ms<=? OR start_ms>=?`, now.Add(-24*time.Hour).UnixMilli(), now.Add(time.Duration(days)*24*time.Hour).UnixMilli()); e != nil {
			return e
		}
	}
	return s.db.QueryRow(`SELECT count(*) FROM programmes`).Scan(&s.count)
}
func scanSpool(rows *sql.Rows) (parsedProgramme, error) {
	var p parsedProgramme
	var start, end int64
	e := rows.Scan(&p.key, &p.channel, &p.title, &p.lineage, &p.series, &p.episode, &p.newEvidence, &p.description, &p.icon, &p.facts, &start, &end)
	p.start = time.UnixMilli(start).UTC()
	p.end = time.UnixMilli(end).UTC()
	return p, e
}
func (d *parsedSource) batches(ctx context.Context, size int, work func([]parsedProgramme) error) error {
	if d.spool == nil {
		for begin := 0; begin < len(d.programmes); begin += size {
			if e := ctx.Err(); e != nil {
				return e
			}
			if e := work(d.programmes[begin:min(begin+size, len(d.programmes))]); e != nil {
				return e
			}
		}
		return nil
	}
	rows, e := d.spool.db.QueryContext(ctx, `SELECT `+spoolColumns+` FROM programmes ORDER BY channel,start_ms,key`)
	if e != nil {
		return e
	}
	defer rows.Close()
	batch := make([]parsedProgramme, 0, size)
	for rows.Next() {
		p, e := scanSpool(rows)
		if e != nil {
			return e
		}
		batch = append(batch, p)
		if len(batch) == size {
			if e = work(batch); e != nil {
				return e
			}
			batch = batch[:0]
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if len(batch) > 0 {
		return work(batch)
	}
	return nil
}
func (d *parsedSource) ranges() (map[string][2]string, error) {
	out := map[string][2]string{}
	if d.spool != nil {
		rows, e := d.spool.db.Query(`SELECT channel,min(start_ms),max(end_ms) FROM programmes GROUP BY channel`)
		if e != nil {
			return out, e
		}
		defer rows.Close()
		for rows.Next() {
			var channel string
			var start, end int64
			if e = rows.Scan(&channel, &start, &end); e != nil {
				return out, e
			}
			out[channel] = [2]string{time.UnixMilli(start).UTC().Format(time.RFC3339), time.UnixMilli(end).UTC().Format(time.RFC3339)}
		}
		return out, rows.Err()
	}
	for _, p := range d.programmes {
		v := out[p.channel]
		a, b := p.start.Format(time.RFC3339), p.end.Format(time.RFC3339)
		if v[0] == "" || a < v[0] {
			v[0] = a
		}
		if b > v[1] {
			v[1] = b
		}
		out[p.channel] = v
	}
	return out, nil
}
