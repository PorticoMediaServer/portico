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
// The file can start again only at a moment when every page in it has been copied into the
// database and no read is still using it. While an import commits every few milliseconds
// there is no such moment: some read always began before the latest commit. Someone has to
// stop writing for about as long as a read takes, and it should be the import, never a person.
// So the automatic checkpoint is off, and:
//
//   - A helper on its own connection copies committed pages every half second. This blocks
//     nobody. When nothing has been written for a whole interval it empties the file.
//   - Once the file passes walDrainBytes the writer holds background work back and takes only
//     people's writes. The helper then copies every few milliseconds, and whenever the writer
//     has nothing to do it tries to empty the file, without waiting for anyone. With the
//     import quiet that works as soon as the reads in flight have finished. Background work
//     then goes on. A person's write never waits for any of this.
//   - People writing without a gap, or a read that does not end, can keep that from working.
//     After walDrainPatience background work goes on anyway, and is held back again when the
//     file has grown by half of walDrainBytes more.
//   - If the file reaches walInsistBytes all the same, the writer waits up to walInsistWait
//     for reads, once a second, and empties it. A pause in writing is better than a file with
//     no limit.
const (
	walKeepBytes      = 16 << 20
	walInsistWait     = 2 * time.Second
	walHelperInterval = 500 * time.Millisecond
	walDrainInterval  = 10 * time.Millisecond
	walDrainPatience  = time.Second
)

// Variables so a test can reach them with a small database.
var (
	walDrainBytes  int64 = 128 << 20
	walInsistBytes int64 = 384 << 20
)

func (d *DB) walSize() (int64, time.Time) {
	info, err := os.Stat(d.path + "-wal")
	if err != nil {
		return 0, time.Time{}
	}
	return info.Size(), info.ModTime()
}

// checkpointHelper runs the helper's part of the WAL policy until the database closes.
func (d *DB) checkpointHelper() {
	defer close(d.helperDone)
	tick := time.NewTicker(walDrainInterval)
	defer tick.Stop()
	var lastSize int64
	var lastModified time.Time
	for n := 0; ; n++ {
		select {
		case <-d.closed:
			return
		case <-tick.C:
			if d.checkpointsOff.Load() {
				continue
			}
			if d.draining.Load() {
				d.helper.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
				continue
			}
			if n%int(walHelperInterval/walDrainInterval) != 0 {
				continue
			}
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

// afterWrite runs on the writer after every transaction. It costs one file stat, and decides
// whether background work is held back.
func (d *DB) afterWrite() {
	if d.checkpointsOff.Load() {
		return
	}
	size, _ := d.walSize()
	now := time.Now()
	switch {
	case size < walDrainBytes:
		d.draining.Store(false)
		d.drainFrom = walDrainBytes
	case d.draining.Load():
		if now.After(d.drainUntil) {
			d.draining.Store(false)
			d.drainFrom = size + walDrainBytes/2
		}
	case size >= d.drainFrom:
		d.draining.Store(true)
		d.drainUntil = now.Add(walDrainPatience)
	}
	if size >= walInsistBytes && now.After(d.nextInsist) {
		d.nextInsist = now.Add(time.Second)
		d.emptyWAL(walInsistWait)
	}
}

// emptyWAL runs on the writer: every walDrainInterval while background work is held back and
// no person is writing, with no wait, and from afterWrite with a wait when the file is far
// too large. It copies what is left to copy and, if no read still uses the file, empties it.
func (d *DB) emptyWAL(wait time.Duration) {
	ctx := context.Background()
	d.writeConn.ExecContext(ctx, `PRAGMA busy_timeout = `+strconv.FormatInt(wait.Milliseconds(), 10))
	d.writeConn.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	d.writeConn.ExecContext(ctx, `PRAGMA busy_timeout = 5000`)
}

// Diagnose is for the load tool, to tell the disk from this package's own work: it turns the
// WAL policy off or on, and turns the disk sync of every commit off or on.
func (d *DB) Diagnose(checkpoints, diskSync bool) {
	d.checkpointsOff.Store(!checkpoints)
	mode := "FULL"
	if !diskSync {
		mode = "OFF"
	}
	d.writeConn.Exec(`PRAGMA synchronous = ` + mode)
	d.helper.Exec(`PRAGMA synchronous = ` + mode)
}
