package db

import (
	"context"
	"os"
	"strconv"
	"time"
)

// WAL policy. SQLite's own automatic checkpoint runs inside a commit, so the person or import
// whose commit crosses the threshold pays for copying pages and a second disk sync. And while
// the server is busy that checkpoint can never start the WAL file again from the beginning,
// so the file grows for as long as the work lasts.
//
// So the automatic checkpoint is off, and two things replace it:
//
//   - A helper on its own connection copies committed pages into the database every half
//     second. This blocks nobody. When nothing has been written for a whole interval it
//     empties the file.
//   - Starting the file again needs a moment with no transaction open, and an import leaves
//     no gap for anyone but the writer to find one. So when the helper sees the file is due,
//     it says so right after a copy, and the writer starts the file again after its current
//     transaction. The copy has left almost nothing to do, so the pause is short. The writer
//     waits up to walRestartWait for reads in flight; if a slow read is in the way, the
//     helper asks again next time. The file is then cut back to walKeepBytes.
//
// The file is due at walRestartBytes while work is heavy, which keeps restarts rare, and
// at twice walKeepBytes once writing has slowed, so it is small again soon after an import.
const (
	walRestartBytes   = 256 << 20
	walKeepBytes      = 16 << 20
	walQuietGrowth    = 1 << 20 // less growth than this between two looks means writing has slowed
	walHelperInterval = 500 * time.Millisecond
	walRestartWait    = 50 * time.Millisecond
	// Past walInsistBytes the writer waits much longer for reads to finish: a pause in writing
	// is better than a file that never stops growing because some slow read is always running.
	walInsistBytes = 384 << 20
	walInsistWait  = 2 * time.Second
)

func (d *DB) walSize() (int64, time.Time) {
	info, err := os.Stat(d.path + "-wal")
	if err != nil {
		return 0, time.Time{}
	}
	return info.Size(), info.ModTime()
}

// checkpointHelper runs the WAL policy until the database closes.
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
				quiet := size-lastSize < walQuietGrowth
				lastSize, lastModified = size, modified
				d.helper.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
				if size >= walRestartBytes || quiet && size >= 2*walKeepBytes {
					d.restartDue.Store(true)
				}
			case size > 0:
				d.helper.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
			}
		}
	}
}

// restartWAL runs on the writer after every transaction, and does nothing unless the helper
// has just said the file is due.
func (d *DB) restartWAL() {
	if !d.restartDue.CompareAndSwap(true, false) {
		return
	}
	ctx := context.Background()
	wait := walRestartWait
	if size, _ := d.walSize(); size >= walInsistBytes {
		wait = walInsistWait
	}
	d.writeConn.ExecContext(ctx, `PRAGMA busy_timeout = `+strconv.FormatInt(wait.Milliseconds(), 10))
	d.writeConn.ExecContext(ctx, `PRAGMA wal_checkpoint(RESTART)`)
	d.writeConn.ExecContext(ctx, `PRAGMA busy_timeout = 5000`)
}
