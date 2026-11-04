package main

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"golang.org/x/sys/unix"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var writerDB *sql.DB
var cacheKiB = -65536
var cacheSpill = true
var passiveEnabled bool
var checkpointEvery = 50 * time.Millisecond
var cpRequest = make(chan struct{}, 1)
var cpStop = make(chan struct{})
var cpDone = make(chan struct{})
var cpRunning atomic.Bool
var cpSequence, commitCount, readerCount atomic.Uint64
var mixedEnabled bool
var mixedDuration = 600 * time.Second
var mixedStart time.Time
var fgEvents = []struct {
	at   time.Duration
	full bool
}{}
var fgNext int
var fgMutex sync.Mutex
var walSoft = int64(64 << 20)
var walHard = int64(192 << 20)
var peakLogical, peakPhysical atomic.Int64

func requestCheckpoint() {
	select {
	case cpRequest <- struct{}{}:
	default:
	}
}
func atomicMax(v *atomic.Int64, n int64) {
	for {
		old := v.Load()
		if n <= old || v.CompareAndSwap(old, n) {
			return
		}
	}
}

type walInfo struct {
	Physical, ActiveBytes, Uncheckpointed int64
	Frames, Backfill                      uint32
	Salt                                  string
	Valid                                 bool
}

func walState() walInfo {
	w := walInfo{}
	if f, e := os.Stat(databasePath + "-wal"); e == nil {
		w.Physical = f.Size()
		atomicMax(&peakPhysical, w.Physical)
	}
	f, e := os.Open(databasePath + "-shm")
	if e != nil {
		return w
	}
	defer f.Close()
	b := make([]byte, 100)
	_, e = f.ReadAt(b, 0)
	if e != nil || !bytes.Equal(b[:48], b[48:96]) || binary.LittleEndian.Uint32(b[:4]) != 3007000 {
		return w
	}
	w.Frames = binary.LittleEndian.Uint32(b[16:20])
	w.Backfill = binary.LittleEndian.Uint32(b[96:100])
	if w.Backfill > w.Frames {
		return w
	}
	size := int64(binary.LittleEndian.Uint16(b[14:16]))
	if size == 1 {
		size = 65536
	}
	if size == 0 {
		return w
	}
	w.Salt = fmt.Sprintf("%x", b[32:40])
	w.ActiveBytes = 32 + int64(w.Frames)*(size+24)
	w.Uncheckpointed = int64(w.Frames-w.Backfill) * (size + 24)
	w.Valid = true
	atomicMax(&peakLogical, w.Uncheckpointed)
	return w
}
func admitPublication() {
	if !passiveEnabled {
		return
	}
	w := walState()
	if w.ActiveBytes > walHard-(16<<20) {
		emit("wal_hard_limit", w)
		panic("WAL active prefix exhausted reserved headroom; publication stopped")
	}
	if w.Valid && w.Uncheckpointed < walSoft && w.ActiveBytes < 96<<20 {
		return
	}
	start := time.Now()
	requestCheckpoint()
	for {
		time.Sleep(time.Millisecond)
		w = walState()
		serviceForeground(writerDB)
		if w.Valid && ((w.ActiveBytes < 96<<20 && w.Uncheckpointed < walSoft/2) || (w.ActiveBytes >= 96<<20 && w.Backfill == w.Frames)) {
			break
		}
		if time.Since(start) > 30*time.Second {
			emit("wal_admission_stopped", w)
			panic("WAL did not repay within diagnostic allowance")
		}
	}
	emit("wal_publication_pause", map[string]any{"ms": float64(time.Since(start).Microseconds()) / 1000, "state": w})
}
func setupExperiment(db *sql.DB, path string) {
	databasePath = path
	writerDB = db
	for _, name := range []string{"cache_size", "cache_spill", "page_size", "synchronous", "wal_autocheckpoint"} {
		var v any
		must(db.QueryRow("PRAGMA " + name).Scan(&v))
		emit("pragma", map[string]any{"name": name, "value": v})
	}
	emit("proc_stat_start", readProcStat())
	if passiveEnabled {
		go func() {
			defer close(cpDone)
			cp := open(path)
			defer cp.Close()
			exec(cp, "PRAGMA busy_timeout=0")
			exec(cp, "PRAGMA wal_autocheckpoint=0")
			ticker := time.NewTicker(checkpointEvery)
			defer ticker.Stop()
			for {
				select {
				case <-cpStop:
					return
				case <-ticker.C:
				case <-cpRequest:
				}
				start := time.Now()
				beforeW, beforeR := commitCount.Load(), readerCount.Load()
				cpSequence.Add(1)
				cpRunning.Store(true)
				var busy, frames, backfilled int
				e := cp.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &backfilled)
				ms := float64(time.Since(start).Microseconds()) / 1000
				cpRunning.Store(false)
				w := walState()
				event := map[string]any{"ms": ms, "busy": busy, "log_frames": frames, "backfilled_frames": backfilled, "writer_commits_during": commitCount.Load() - beforeW, "reader_queries_during": readerCount.Load() - beforeR, "start_unix_ns": start.UnixNano(), "end_unix_ns": time.Now().UnixNano(), "wal": w, "cadence_ms": checkpointEvery.Milliseconds()}
				if e != nil {
					event["error"] = e.Error()
				}
				emit("passive_checkpoint", event)
			}
		}()
	}
	if mixedEnabled {
		exec(db, "CREATE TABLE q_security(id INTEGER PRIMARY KEY,value INTEGER NOT NULL)")
		exec(db, "CREATE TABLE q_progress(id INTEGER PRIMARY KEY,value INTEGER NOT NULL)")
		mixedStart = time.Now()
		rng := rand.New(rand.NewSource(41041))
		for _, class := range []struct {
			full bool
			mean float64
		}{{true, 60}, {false, 1}} {
			at := time.Duration(0)
			for at < mixedDuration+time.Hour {
				at += time.Duration(rng.ExpFloat64() * class.mean * float64(time.Second))
				fgEvents = append(fgEvents, struct {
					at   time.Duration
					full bool
				}{at, class.full})
			}
		}
		sortForeground()
	}
}
func sortForeground() {
	for i := 1; i < len(fgEvents); i++ {
		for j := i; j > 0 && fgEvents[j].at < fgEvents[j-1].at; j-- {
			fgEvents[j], fgEvents[j-1] = fgEvents[j-1], fgEvents[j]
		}
	}
}
func stopExperiment() {
	if passiveEnabled {
		close(cpStop)
		<-cpDone
	}
	emit("wal_summary", map[string]any{"peak_physical_bytes": peakPhysical.Load(), "peak_uncheckpointed_bytes": peakLogical.Load(), "soft_bytes": walSoft, "hard_bytes": walHard, "checkpointer_cadence_ms": checkpointEvery.Milliseconds()})
	emit("proc_stat_end", readProcStat())
}
func serviceForeground(db *sql.DB) {
	if !mixedEnabled {
		return
	}
	for fgNext < len(fgEvents) && time.Since(mixedStart) >= fgEvents[fgNext].at {
		event := fgEvents[fgNext]
		fgNext++
		target := mixedStart.Add(event.at)
		w := walState()
		class := "progress_NORMAL"
		syncMode := "NORMAL"
		table := "q_progress"
		if event.full {
			class = "security_FULL"
			syncMode = "FULL"
			table = "q_security"
		}
		start := time.Now()
		exec(db, "PRAGMA synchronous="+syncMode)
		begin := time.Now()
		exec(db, "BEGIN IMMEDIATE")
		exec(db, "INSERT INTO "+table+" VALUES(1,1) ON CONFLICT(id) DO UPDATE SET value=value+1")
		native(db, "foreground."+class+".commit", "COMMIT")
		hold := float64(time.Since(begin).Microseconds()) / 1000
		exec(db, "PRAGMA synchronous="+durability)
		emit("foreground_write", map[string]any{"class": class, "arrival_unix_ns": target.UnixNano(), "start_unix_ns": start.UnixNano(), "queue_ms": float64(start.Sub(target).Microseconds()) / 1000, "transaction_ms": hold, "response_ms": float64(time.Since(target).Microseconds()) / 1000, "wal_before": w, "background_commits": commitCount.Load() - uint64(fgNext), "checkpoint_running": cpRunning.Load(), "wal_bytes_scope": "committed prefix and uncheckpointed bytes; not an exact unsynced-byte oracle"})
	}
}
func finishMixed(db *sql.DB) {
	serviceForeground(db)
	if mixedEnabled {
		emit("mixed_summary", map[string]any{"elapsed_s": time.Since(mixedStart).Seconds(), "events_served": fgNext, "security_mean_s": 60, "progress_mean_s": 1, "seed": 41041})
		mixedEnabled = false
	}
}
func insertEntity(db *sql.DB, i, kind, parent int, t, p, init string) {
	id := make([]byte, 16)
	binary.LittleEndian.PutUint64(id[:8], uint64(i))
	copy(id[8:], []byte("Qfixture"))
	var par, show, season, numbering, episode, artist, album, resolution any
	if parent >= 0 {
		par = parent
	}
	r, b := i%100, i-i%100
	if kind == 3 {
		show = b + 15
		season = r - 15
	}
	if kind == 4 {
		show = b + 15
		numbering = "seasonal"
		episode = r - 18
	}
	if kind == 6 {
		artist = b + 60
	}
	if kind == 7 {
		album = b + 61 + (r-63)%2
	}
	if kind <= 4 {
		resolution = 1080
	}
	exec(db, `INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,title_fold,title_prefix_tokens,initial_prefix_tokens,show_id,season_number,numbering,episode_number,artist_id,album_id,resolution_key,overview) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, i, id, (i/100)%8, kind, par, t, facettext.SearchFold(t), string([]rune(t)[0]), facettext.SearchFold(t), string([]rune(t)[0]), i, id, id, facettext.SearchFold(t), p, init, show, season, numbering, episode, artist, album, resolution, strings.Repeat("Synthetic synopsis for physical row width. ", 16))
}
func readProcStat() string {
	b, _ := os.ReadFile("/proc/stat")
	lines := strings.Split(string(b), "\n")
	if len(lines) > 0 {
		return lines[0]
	}
	return "unavailable"
}
func threadClock() int64 {
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &ts) != nil {
		return 0
	}
	return ts.Nano()
}
func threadWait() int64 {
	b, e := os.ReadFile(fmt.Sprintf("/proc/self/task/%d/schedstat", unix.Gettid()))
	if e != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	v, _ := strconv.ParseInt(f[1], 10, 64)
	return v
}
func cpuNative(db interface {
	Exec(string, ...any) (sql.Result, error)
}, tag, q string, a ...any) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	wait := threadWait()
	cpu := threadClock()
	t := time.Now()
	active, seq := cpRunning.Load(), cpSequence.Load()
	exec(db, q, a...)
	elapsed := float64(time.Since(t).Microseconds()) / 1000
	cpuMs := float64(threadClock()-cpu) / 1e6
	waitMs := float64(threadWait()-wait) / 1e6
	overlap := active || cpRunning.Load() || seq != cpSequence.Load()
	nativeMax.Lock()
	nativeMax.values[tag] = append(nativeMax.values[tag], elapsed)
	nativeMax.values[tag+".thread_cpu"] = append(nativeMax.values[tag+".thread_cpu"], cpuMs)
	nativeMax.values[tag+".sched_wait"] = append(nativeMax.values[tag+".sched_wait"], waitMs)
	suffix := ".checkpoint_idle"
	if overlap {
		suffix = ".checkpoint_overlap"
	}
	nativeMax.values[tag+suffix] = append(nativeMax.values[tag+suffix], elapsed)
	nativeMax.Unlock()
	if elapsed > 10 {
		emit("native_cpu_sample", map[string]any{"tag": tag, "ms": elapsed, "thread_cpu_ms": cpuMs, "sched_wait_ms": waitMs, "checkpoint_overlap": overlap, "start_unix_ns": t.UnixNano(), "end_unix_ns": time.Now().UnixNano()})
	}
	if strings.Contains(tag, "commit") {
		commitCount.Add(1)
	}
}
