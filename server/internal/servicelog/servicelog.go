// Package servicelog is the server's message log: a bounded in-memory ring that
// answers admin reads and live tails, and a rotating file set under the state
// directory that survives a restart for post-mortem work.
//
// It is deliberately not a logging framework. The standard library's log package
// stays the writing interface for the rest of the server; a Recorder installed
// with log.SetOutput observes those lines, so no call site has to change.
package servicelog

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Levels, most severe first. A filter names the least severe level to include,
// so "warn" returns warnings and errors.
var Levels = []string{"error", "warn", "info", "debug"}

// Categories are the retention buckets a per-category retention setting keys on.
// A line whose category is not recognised is recorded as "server".
var Categories = []string{"server", "playback", "scan", "network", "client"}

func rank(level string) int {
	for i, l := range Levels {
		if l == level {
			return i + 1
		}
	}
	return 0
}

// ValidLevel reports whether a string names a level.
func ValidLevel(v string) bool { return rank(v) > 0 }

// ValidCategory reports whether a string names a retention category.
func ValidCategory(v string) bool {
	for _, c := range Categories {
		if c == v {
			return true
		}
	}
	return false
}

// Record is one message. Sequence is monotonic for the life of the process and
// is what a cursor points at.
type Record struct {
	Sequence int64  `json:"sequence,string"`
	At       string `json:"at"`
	Level    string `json:"level"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

// Options configures a Recorder.
type Options struct {
	// Directory is where rotating files are written. Empty disables files and
	// keeps the ring only, which is what tests want.
	Directory string
	// Capacity is the number of records the ring holds. Zero uses 5000.
	Capacity int
	// FileBytes is the size at which the active file rotates. Zero uses 4 MiB.
	FileBytes int64
	// FileCount is how many rotated files to keep besides the active one.
	// Zero uses 4.
	FileCount int
	Now       func() time.Time
}

type subscriber struct {
	level string
	ch    chan Record
}

type Recorder struct {
	mu      sync.Mutex
	pruneMu sync.Mutex
	// ring is a fixed-size circular buffer: records occupy
	// ((start+i) % len(ring)) for i in [0, count), oldest first. Appending is
	// O(1); the previous layout copied every retained record header per line.
	ring        []Record
	start       int
	count       int
	next        int64
	instance    string
	options     Options
	file        *os.File
	written     int64
	subscribers map[int64]*subscriber
	nextSub     int64
	// level is the configured threshold; debugUntil raises it to debug while a
	// debug window is open. Both are read under mu.
	level      string
	debugUntil time.Time
	retention  map[string]int
	partial    []byte
}

// New creates a Recorder. It opens (but does not truncate) the active file when
// a directory is configured; a directory that cannot be opened degrades to a
// ring-only recorder rather than failing startup, because losing the log file
// must never cost the server its ability to run.
func New(o Options) *Recorder {
	if o.Capacity <= 0 {
		o.Capacity = 5000
	}
	if o.FileBytes <= 0 {
		o.FileBytes = 4 << 20
	}
	if o.FileCount <= 0 {
		o.FileCount = 4
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		panic(err)
	}
	r := &Recorder{instance: hex.EncodeToString(nonce[:]), options: o, subscribers: map[int64]*subscriber{}, level: "info", retention: map[string]int{}}
	r.ring = make([]Record, o.Capacity)
	if o.Directory != "" {
		if err := os.MkdirAll(o.Directory, 0700); err == nil {
			if f, err := os.OpenFile(r.path(0), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
				r.file, r.written = f, fileSize(f)
			}
		}
	}
	r.loadHistory()
	return r
}

func fileSize(f *os.File) int64 {
	info, err := f.Stat()
	if err != nil {
		return 0
	}
	return info.Size()
}

func (r *Recorder) path(index int) string {
	if index == 0 {
		return filepath.Join(r.options.Directory, "messages.log")
	}
	return filepath.Join(r.options.Directory, "messages."+strconv.Itoa(index)+".log")
}

// SetLevel installs the configured threshold, which the settings registry owns.
func (r *Recorder) SetLevel(level string) {
	if !ValidLevel(level) {
		return
	}
	r.mu.Lock()
	r.level = level
	r.mu.Unlock()
}

// SetRetention installs per-category retention in days. A category with no entry
// keeps whatever the ring holds.
func (r *Recorder) SetRetention(days map[string]int) {
	r.mu.Lock()
	r.retention = map[string]int{}
	for category, n := range days {
		if ValidCategory(category) && n > 0 {
			r.retention[category] = n
		}
	}
	r.mu.Unlock()
	r.Prune()
}

// OpenDebugWindow raises the effective level to debug until the given instant.
// Passing a past instant closes the window immediately.
func (r *Recorder) OpenDebugWindow(until time.Time) {
	r.mu.Lock()
	r.debugUntil = until
	r.mu.Unlock()
}

// Effective reports the level in force and, when a debug window is open, the
// instant it reverts.
func (r *Recorder) Effective() (string, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.options.Now().Before(r.debugUntil) {
		return "debug", r.debugUntil
	}
	return r.level, time.Time{}
}

// Write makes the Recorder an io.Writer for the standard log package. A line is
// classified by an optional "level:" or "[category]" prefix; anything else is an
// info-level server message. Partial writes are buffered until a newline, so a
// multi-line panic trace is not split into unrelated records.
func (r *Recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.partial = append(r.partial, p...)
	var lines []string
	for {
		index := strings.IndexByte(string(r.partial), '\n')
		if index < 0 {
			break
		}
		lines = append(lines, string(r.partial[:index]))
		r.partial = r.partial[index+1:]
	}
	if len(r.partial) > 64<<10 {
		lines, r.partial = append(lines, string(r.partial)), nil
	}
	r.mu.Unlock()
	for _, line := range lines {
		level, category, message := classify(line)
		r.Record(level, category, message)
	}
	return len(p), nil
}

// Tee returns a writer that records and also forwards to another writer, so
// installing the recorder as the standard log output does not take stderr away
// from whoever is watching the process. The forwarded copy is scrubbed too: a
// container or service manager may persist stderr independently of the recorder.
func (r *Recorder) Tee(also io.Writer) io.Writer {
	if also == nil {
		return r
	}
	return io.MultiWriter(redactedWriter{to: also}, r)
}

// Standard log records arrive as one Write. Report the caller's byte count even
// when redaction changes the forwarded length.
type redactedWriter struct{ to io.Writer }

func (w redactedWriter) Write(p []byte) (int, error) {
	clean := []byte(redactClientLog(string(p)))
	n, err := w.to.Write(clean)
	if err != nil {
		return 0, err
	}
	if n != len(clean) {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}

// classify reads the optional prefixes a call site may use. "warn: text" sets
// the level; "[playback] text" sets the category; both may appear in either
// order. Standard log output carries a date and time prefix, which is dropped in
// favour of the recorder's own timestamp.
func classify(line string) (string, string, string) {
	level, category := "info", "server"
	message := strings.TrimSpace(stripStandardPrefix(line))
	for range 2 {
		switch {
		case strings.HasPrefix(message, "["):
			if end := strings.IndexByte(message, ']'); end > 1 {
				name := strings.ToLower(message[1:end])
				if ValidCategory(name) {
					category, message = name, strings.TrimSpace(message[end+1:])
					continue
				}
				// A tag that is not a category ("[http]") stays in the message, but the
				// level written after it still counts: "[http] warn: …" is a warning.
				if name, rest, ok := strings.Cut(strings.TrimSpace(message[end+1:]), ":"); ok && ValidLevel(strings.ToLower(strings.TrimSpace(name))) {
					level, message = strings.ToLower(strings.TrimSpace(name)), message[:end+1]+" "+strings.TrimSpace(rest)
				}
			}
		default:
			if name, rest, ok := strings.Cut(message, ":"); ok && ValidLevel(strings.ToLower(strings.TrimSpace(name))) {
				level, message = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(rest)
				continue
			}
		}
		break
	}
	return level, category, message
}

// stripStandardPrefix removes "2006/01/02 15:04:05 " if the line starts with it.
func stripStandardPrefix(line string) string {
	if len(line) > 20 && line[4] == '/' && line[7] == '/' && line[10] == ' ' && line[13] == ':' && line[16] == ':' {
		return line[20:]
	}
	return line
}

// Record appends one message. It is safe for concurrent use and never blocks on
// a slow live-tail subscriber: a subscriber that cannot keep up drops records
// rather than stalling the server.
func (r *Recorder) Record(level, category, message string) {
	message = redactClientLog(message)
	if !ValidLevel(level) {
		level = "info"
	}
	if !ValidCategory(category) {
		category = "server"
	}
	if len(message) > 8192 {
		message = message[:8192]
	}
	r.mu.Lock()
	effective := r.level
	if r.options.Now().Before(r.debugUntil) {
		effective = "debug"
	}
	if rank(level) > rank(effective) {
		r.mu.Unlock()
		return
	}
	r.next++
	record := Record{Sequence: r.next, At: r.options.Now().UTC().Format(time.RFC3339Nano), Level: level, Category: category, Message: message}
	r.appendRing(record)
	r.appendFileLocked(record)
	// Publish under the same lock that orders sequence IDs and retirement.
	// A slow subscriber reconnects and replays; silently dropping lines would
	// make a live tail look complete when it is not.
	for id, sub := range r.subscribers {
		if rank(record.Level) > rank(sub.level) {
			continue
		}
		select {
		case sub.ch <- record:
		default:
			delete(r.subscribers, id)
			close(sub.ch)
		}
	}
	r.mu.Unlock()
}

// appendRing adds a record to the circular buffer, dropping the oldest when it
// is full. The caller holds mu (or is loading history before publication).
func (r *Recorder) appendRing(record Record) {
	if r.count < len(r.ring) {
		r.ring[(r.start+r.count)%len(r.ring)] = record
		r.count++
		return
	}
	r.ring[r.start] = record
	r.start = (r.start + 1) % len(r.ring)
}

func (r *Recorder) appendFileLocked(record Record) {
	if r.file == nil {
		return
	}
	line := fmt.Sprintf("%s\t%s\t%s\t%s\n", record.At, record.Level, record.Category, strings.ReplaceAll(record.Message, "\n", " "))
	n, err := r.file.WriteString(line)
	r.written += int64(n)
	if err != nil || r.written < r.options.FileBytes {
		return
	}
	r.rotateLocked()
}

// rotateLocked closes the active file before moving it. Windows refuses to
// rename or remove a file that is still open, and refuses a rename onto an
// existing path, so the destination is removed first on every platform: doing it
// unconditionally keeps one code path rather than a per-OS branch that only one
// target ever exercises.
func (r *Recorder) rotateLocked() {
	if r.file == nil {
		return
	}
	r.file.Close()
	r.file, r.written = nil, 0
	for index := r.options.FileCount; index >= 1; index-- {
		source, destination := r.path(index-1), r.path(index)
		if index == r.options.FileCount {
			os.Remove(destination)
		}
		if _, err := os.Stat(source); err != nil {
			continue
		}
		os.Remove(destination)
		if err := os.Rename(source, destination); err != nil {
			return
		}
	}
	if f, err := os.OpenFile(r.path(0), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
		r.file, r.written = f, fileSize(f)
	}
}

// Close releases the active file. A Recorder stays usable afterwards as a
// ring-only recorder.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// Prune drops records older than their category's retention.
func (r *Recorder) Prune() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.retention) == 0 {
		return
	}
	now := r.options.Now()
	// Compact in place, oldest first. Prune stays O(retained records) and is
	// rare, while Record is the hot path. Writes never pass the read offset.
	kept := 0
	for i := 0; i < r.count; i++ {
		record := r.ring[(r.start+i)%len(r.ring)]
		days, ok := r.retention[record.Category]
		if ok {
			at, err := time.Parse(time.RFC3339Nano, record.At)
			if err == nil && now.Sub(at) > time.Duration(days)*24*time.Hour {
				continue
			}
		}
		r.ring[(r.start+kept)%len(r.ring)] = record
		kept++
	}
	r.count = kept
	if r.count == 0 {
		r.start = 0
	}
}

// Query is a message log read.
type Query struct {
	// Level is the least severe level to include. Empty means debug (all).
	Level string
	// Category narrows to one retention category. Empty means all.
	Category string
	// Cursor is the sequence to read before, for paging backwards through
	// history. Empty starts at the newest record.
	Cursor string
	Limit  int
}

// Result is the message log page: newest first, with a cursor for the next page.
type Result struct {
	Level      string   `json:"level"`
	Effective  string   `json:"effectiveLevel"`
	Limit      int      `json:"limit"`
	NextCursor string   `json:"nextCursor,omitempty"`
	Items      []Record `json:"items"`
}

// Read answers a message log page from the ring, newest first.
func (r *Recorder) Read(q Query) (Result, error) {
	if q.Level == "" {
		q.Level = "debug"
	}
	if !ValidLevel(q.Level) {
		return Result{}, fmt.Errorf("level")
	}
	if q.Category != "" && !ValidCategory(q.Category) {
		return Result{}, fmt.Errorf("category")
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > 500 {
		return Result{}, fmt.Errorf("limit")
	}
	before := int64(0)
	if q.Cursor != "" {
		n, err := strconv.ParseInt(q.Cursor, 10, 64)
		if err != nil || n < 1 {
			return Result{}, fmt.Errorf("cursor")
		}
		before = n
	}
	effective, _ := r.Effective()
	out := Result{Level: q.Level, Effective: effective, Limit: q.Limit, Items: []Record{}}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := r.count - 1; i >= 0; i-- {
		record := r.ring[(r.start+i)%len(r.ring)]
		if before > 0 && record.Sequence >= before {
			continue
		}
		if rank(record.Level) > rank(q.Level) || q.Category != "" && record.Category != q.Category {
			continue
		}
		if len(out.Items) == q.Limit {
			out.NextCursor = strconv.FormatInt(out.Items[len(out.Items)-1].Sequence, 10)
			break
		}
		out.Items = append(out.Items, record)
	}
	return out, nil
}

// Subscribe opens a live tail at or above the given level. The returned cancel
// must be called; the channel is closed by it.
func (r *Recorder) Subscribe(level string) (<-chan Record, func()) {
	_, _, records, cancel := r.SubscribeAfter(level, nil)
	return records, cancel
}

// EventID includes a process nonce: an ID from before restart must reset even
// if the new ring happens to have reached the same sequence number.
func (r *Recorder) EventID(sequence int64) string {
	return r.instance + ":" + strconv.FormatInt(sequence, 10)
}
func (r *Recorder) EventSequence(id string) (int64, error) {
	instance, raw, ok := strings.Cut(id, ":")
	if !ok {
		return 0, fmt.Errorf("invalid log event ID")
	}
	sequence, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || sequence < 0 {
		return 0, fmt.Errorf("invalid log event ID")
	}
	if instance != r.instance {
		return -1, nil
	}
	return sequence, nil
}

// SubscribeAfter atomically snapshots missed records and joins the live tail,
// so writes cannot be lost or delivered twice between replay and subscription.
func (r *Recorder) SubscribeAfter(level string, after *int64) ([]Record, bool, <-chan Record, func()) {
	if !ValidLevel(level) {
		level = "info"
	}
	s := &subscriber{level: level, ch: make(chan Record, 256)}
	r.mu.Lock()
	var replay []Record
	reset := false
	if after != nil {
		reset = *after < 0 || *after > r.next || (r.count > 0 && *after < r.ring[r.start].Sequence-1)
		for i := 0; i < r.count; i++ {
			record := r.ring[(r.start+i)%len(r.ring)]
			if (reset || record.Sequence > *after) && rank(record.Level) <= rank(level) {
				replay = append(replay, record)
			}
		}
	}
	r.nextSub++
	id := r.nextSub
	r.subscribers[id] = s
	r.mu.Unlock()
	return replay, reset, s.ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if _, ok := r.subscribers[id]; ok {
			delete(r.subscribers, id)
			close(s.ch)
		}
	}
}

// Files lists the rotated log files, newest first, for the diagnostics bundle.
func (r *Recorder) Files() []string {
	if r.options.Directory == "" {
		return nil
	}
	entries, err := os.ReadDir(r.options.Directory)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, "messages") && strings.HasSuffix(name, ".log") {
			out = append(out, filepath.Join(r.options.Directory, name))
		}
	}
	sort.Strings(out)
	return out
}

// Tail returns the last n lines of the active file, for the diagnostics bundle
// when the ring has already rolled past them.
func (r *Recorder) Tail(n int) []string {
	if r.options.Directory == "" {
		return nil
	}
	f, err := os.Open(r.path(0))
	if err != nil {
		return nil
	}
	defer f.Close()
	lines := []string{}
	scanner := bufio.NewScanner(io.Reader(f))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}

// loadHistory restores a bounded tail, assigning new sequence IDs for this run.
func (r *Recorder) loadHistory() {
	if r.options.Directory == "" {
		return
	}
	for index := r.options.FileCount; index >= 0; index-- {
		file, err := os.Open(r.path(index))
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			continue
		}
		if info.Size() > r.options.FileBytes {
			file.Seek(info.Size()-r.options.FileBytes, io.SeekStart)
		}
		scanner := bufio.NewScanner(io.LimitReader(file, r.options.FileBytes))
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			fields := strings.SplitN(scanner.Text(), "\t", 4)
			if len(fields) != 4 || !ValidLevel(fields[1]) || !ValidCategory(fields[2]) {
				continue
			}
			if _, err := time.Parse(time.RFC3339Nano, fields[0]); err != nil {
				continue
			}
			r.next++
			record := Record{Sequence: r.next, At: fields[0], Level: fields[1], Category: fields[2], Message: redactClientLog(fields[3])}
			// Insertion preserves file order, so the ring keeps the newest
			// bounded tail exactly as before: oldest first from start.
			r.appendRing(record)
		}
		file.Close()
	}
}
