package db

import (
	"context"
	"os"
	"strconv"
	"time"
)

// WAL policy. SQLite's own automatic checkpoint runs inside a commit, so the person or import
// whose commit crosses the threshold pays for copying pages and a second disk sync. And while
// reads overlap, as they do on a busy server, that checkpoint can never start the WAL file
// again from the beginning, so the file grows for as long as the server stays busy.
//
// So the automatic checkpoint is off, and two things replace it:
//
//   - A helper on its own connection copies committed pages into the database every half
//     second. It never blocks a writer or a reader.
//   - When the file passes walRestartBytes, the writer, between two transactions, waits a
//     moment for the reads in flight to finish and starts the file again. The helper has
//     already copied nearly everything, so this is short. The file then stays near that
//     size however long an import runs.
//   - When writing stops, the helper empties the file.
const (
	walRestartBytes    = 32 << 20
	walHelperInterval  = 500 * time.Millisecond
	walRestartInterval = time.Second           // at most one attempt a second
	walRestartWait     = 50 * time.Millisecond // how long a restart may wait for reads to finish
)

func (d *DB) walSize() (int64, time.Time) {
	info, err := os.Stat(d.path + "-wal")
	if err != nil {
		return 0, time.Time{}
	}
	return info.Size(), info.ModTime()
}

// checkpointHelper copies committed pages into the database whenever the WAL has changed.
func (d *DB) checkpointHelper() {
	defer close(d.helperDone)
	tick := time.NewTicker(walHelperInterval)
	defer tick.Stop()
	var lastSize int64
	var lastModified time.Time
	for {
		select {
		case <-d.closed:
			return
		case <-tick.C:
			size, modified := d.walSize()
			switch {
			case size != lastSize || !modified.Equal(lastModified):
				lastSize, lastModified = size, modified
				d.helper.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
			case size > 0:
				// Nothing was written for a whole interval. If no read is in the way this
				// empties the file; otherwise it tries again next time.
				d.helper.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
			}
		}
	}
}

// restartWAL runs on the writer between transactions. It is cheap unless the file is large.
func (d *DB) restartWAL() {
	if time.Since(d.lastRestart) < walRestartInterval {
		return
	}
	if size, _ := d.walSize(); size < walRestartBytes {
		return
	}
	d.lastRestart = time.Now()
	ctx := context.Background()
	// A restart waits for reads in flight. Bound that wait: if a slow read is running, the
	// next attempt will do.
	d.writeConn.ExecContext(ctx, `PRAGMA busy_timeout = `+strconv.FormatInt(walRestartWait.Milliseconds(), 10))
	d.writeConn.ExecContext(ctx, `PRAGMA wal_checkpoint(RESTART)`)
	d.writeConn.ExecContext(ctx, `PRAGMA busy_timeout = 5000`)
}
