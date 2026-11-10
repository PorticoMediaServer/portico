package db

import (
	"context"
	"os"
	"strconv"
	"time"
)

// WAL policy. SQLite's own automatic checkpoint runs inside a commit, so the person or import
// whose commit crosses the threshold pays for copying pages and a second disk sync, and under
// constant work the file is never started again from the beginning, so it only grows.
//
// SQLite starts the file again by itself when a write begins at a moment when every page in
// it has been copied into the database and no read is still using it. Nothing has to wait
// for that; it only has to be made likely. So the automatic checkpoint is off, and:
//
//   - A helper on its own connection copies committed pages every half second. This blocks
//     nobody. When nothing has been written for a whole interval it empties the file.
//   - Once the file passes walCatchUpBytes, the writer copies what its own transaction just
//     wrote, straight after committing it. That is a small copy and a disk sync, it never
//     waits for a read, and it means the next write begins with everything copied, so the
//     file starts again as soon as the reads in flight are all recent. It is then cut back to
//     walKeepBytes and the writer stops copying.
//   - If slow reads keep that from happening until the file reaches walInsistBytes, the
//     writer waits up to walInsistWait for them, once a second, and starts the file again.
//     A pause in writing is better than a file with no limit.
const (
	walKeepBytes      = 16 << 20
	walCatchUpBytes   = 64 << 20
	walInsistBytes    = 384 << 20
	walInsistWait     = 2 * time.Second
	walHelperInterval = 500 * time.Millisecond
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
				lastSize, lastModified = size, modified
				d.helper.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
			case size > 0:
				d.helper.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
			}
		}
	}
}

// afterWrite runs on the writer after every transaction. It costs one file stat while the
// WAL is small.
func (d *DB) afterWrite() {
	size, _ := d.walSize()
	if size < walCatchUpBytes {
		return
	}
	ctx := context.Background()
	if size < walInsistBytes || time.Now().Before(d.nextInsist) {
		d.writeConn.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`)
		return
	}
	d.nextInsist = time.Now().Add(time.Second)
	d.writeConn.ExecContext(ctx, `PRAGMA busy_timeout = `+strconv.FormatInt(walInsistWait.Milliseconds(), 10))
	d.writeConn.ExecContext(ctx, `PRAGMA wal_checkpoint(RESTART)`)
	d.writeConn.ExecContext(ctx, `PRAGMA busy_timeout = 5000`)
}
