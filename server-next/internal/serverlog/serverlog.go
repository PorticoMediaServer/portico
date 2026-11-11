// Package serverlog is the server's log as the owner reads it in Messages: every line goes to
// standard error as before and to a file for each day in the data folder's logs folder, kept
// as long as the owner chooses (7, 14 or 30 days). The last 500 lines are also held in
// memory, so Messages can follow the log live. "Record more detail" raises the detail for a
// while and lowers it again by itself; a restart ends it.
//
// A value whose name says it is a password, token, secret or key is never written to a file.
package serverlog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// Settings is the owner's `logs` settings document.
type Settings struct {
	// KeepDays is how long messages are kept: 7, 14 or 30 days.
	KeepDays int `json:"keepDays"`
}

// Document is the logs settings document.
var Document = settings.Define("logs", Settings{KeepDays: 30}, func(s *Settings) error {
	if s.KeepDays != 7 && s.KeepDays != 14 && s.KeepDays != 30 {
		return &settings.Invalid{Field: "keepDays", Message: "Keep messages for 7, 14 or 30 days."}
	}
	return nil
})

// Entry is one line of the log.
type Entry struct {
	Seq     int64             `json:"-"`
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// recent is how many lines are held in memory to follow the log live.
const recent = 500

// Log is the server's log.
type Log struct {
	dir   string
	level slog.LevelVar
	inner slog.Handler
	// Keep says how many days of files to keep; nil keeps 30.
	Keep func() int

	mu   sync.Mutex
	file *os.File
	day  string
	// written is how much today's file holds.
	written int64
	ring    []Entry
	seq     int64
	more    chan struct{} // closed when a line is added
	until   time.Time     // when "Record more detail" ends
	timer   *time.Timer
}

// Open starts the log, its files in dir. Lines also go to stderr as text.
func Open(dir string, stderr io.Writer) (*Log, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	l := &Log{dir: dir, more: make(chan struct{})}
	l.level.Set(slog.LevelInfo)
	l.inner = slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: &l.level})
	return l, nil
}

// Handler is the handler every logger of the server writes through.
func (l *Log) Handler() slog.Handler { return &handler{log: l, inner: l.inner} }

// Close closes today's file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

type handler struct {
	log    *Log
	inner  slog.Handler
	attrs  []slog.Attr
	prefix string
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.log.level.Level()
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := *h
	out.inner = h.inner.WithAttrs(attrs)
	for _, a := range attrs {
		a.Key = h.prefix + a.Key
		out.attrs = append(slices.Clone(out.attrs), a)
	}
	return &out
}

func (h *handler) WithGroup(name string) slog.Handler {
	out := *h
	out.inner = h.inner.WithGroup(name)
	out.prefix = h.prefix + name + "."
	return &out
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	err := h.inner.Handle(ctx, r)
	e := Entry{Time: r.Time, Level: strings.ToLower(r.Level.String()), Message: Clean(r.Message)}
	add := func(a slog.Attr) {
		if e.Attrs == nil {
			e.Attrs = map[string]string{}
		}
		value := Clean(a.Value.Resolve().String())
		if secretName(a.Key) {
			value = "[hidden]"
		}
		e.Attrs[a.Key] = value
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		a.Key = h.prefix + a.Key
		add(a)
		return true
	})
	return errors.Join(err, h.log.add(e))
}

// secretName says a value is one never to keep, by its name: a password, token, secret, key,
// PIN, code, cookie, credential or signature. Names are compared without "_", "-" and ".".
func secretName(name string) bool {
	name = strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(name))
	for _, word := range []string{"password", "passwd", "token", "secret", "authorization", "passphrase", "apikey", "cookie", "credential",
		"signature"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	for _, end := range []string{"key", "pin", "code"} {
		if strings.HasSuffix(name, end) {
			return true
		}
	}
	return false
}

// Secrets inside text: an error that carries the address it called holds its key in the query,
// a tool's own output holds "token = …", a JSON answer holds "access_token": "…". Every
// message and value is cleaned of them before it is kept, shown or exported.
var secretPatterns = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)\b(api_?key|access_token|refresh_token|id_token|client_secret|token|secret|password|passwd|pass|pwd|sig|signature|key|code|pin)(\s*[=:]\s*)[^&\s"',;]+`), "$1$2[hidden]"},
	{regexp.MustCompile(`(?i)"(api_?key|access_token|refresh_token|id_token|client_secret|private_key|token|secret|password|passwd|pin)"\s*:\s*"[^"]*"`), `"$1":"[hidden]"`},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "$1 [hidden]"},
	{regexp.MustCompile(`\bpk_[A-Za-z0-9_-]+`), "pk_[hidden]"},
}

var (
	hiddenMu     sync.RWMutex
	hiddenValues []string
)

// Hide registers a secret's value as it is loaded (a source's password, a provider's key), so
// that wherever it appears in a log line it is replaced. Short values are not registered:
// they would hide ordinary words.
func Hide(value string) {
	if len(value) < 6 {
		return
	}
	hiddenMu.Lock()
	defer hiddenMu.Unlock()
	if !slices.Contains(hiddenValues, value) {
		hiddenValues = append(hiddenValues, value)
	}
}

// Clean hides every secret a piece of text holds, as it would be kept in the log.
func Clean(text string) string {
	hiddenMu.RLock()
	for _, v := range hiddenValues {
		text = strings.ReplaceAll(text, v, "[hidden]")
	}
	hiddenMu.RUnlock()
	for _, p := range secretPatterns {
		text = p.re.ReplaceAllString(text, p.with)
	}
	return text
}

// dayFileLimit is the most one day's file holds. Past it the day's file says so once and
// keeps nothing more; standard error still has every line.
var dayFileLimit int64 = 256 << 20

func (l *Log) add(e Entry) error {
	line, _ := json.Marshal(e)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e.Seq = l.seq
	if len(l.ring) == recent {
		l.ring = l.ring[1:]
	}
	l.ring = append(l.ring, e)
	close(l.more)
	l.more = make(chan struct{})
	day := e.Time.Format(time.DateOnly)
	if l.file == nil || day != l.day {
		if l.file != nil {
			l.file.Close()
		}
		f, err := os.OpenFile(filepath.Join(l.dir, fileName(day)), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			l.file = nil
			return err
		}
		l.file, l.day = f, day
		l.written = 0
		if info, err := f.Stat(); err == nil {
			l.written = info.Size()
		}
		l.removeOld(e.Time)
	}
	switch {
	case l.written >= dayFileLimit+1:
		return nil
	case l.written+int64(len(line))+1 > dayFileLimit:
		// One last line, and nothing more today.
		full, _ := json.Marshal(Entry{Time: e.Time, Level: "warn", Message: "today's message file is full; more messages go to standard error only"})
		_, err := l.file.Write(append(full, '\n'))
		l.written = dayFileLimit + 1
		return err
	}
	n, err := l.file.Write(append(line, '\n'))
	l.written += int64(n)
	return err
}

func fileName(day string) string { return "server-" + day + ".log" }

// removeOld removes the files of days older than the owner keeps, when a new day starts.
func (l *Log) removeOld(now time.Time) {
	keep := 30
	if l.Keep != nil {
		keep = l.Keep()
	}
	oldest := now.AddDate(0, 0, -keep).Format(time.DateOnly)
	for _, day := range l.days() {
		if day < oldest {
			os.Remove(filepath.Join(l.dir, fileName(day)))
		}
	}
}

// days lists the days there are files for, newest first.
func (l *Log) days() []string {
	names, _ := filepath.Glob(filepath.Join(l.dir, "server-*.log"))
	var days []string
	for _, name := range names {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(name), "server-"), ".log")
		if _, err := time.Parse(time.DateOnly, day); err == nil {
			days = append(days, day)
		}
	}
	slices.Sort(days)
	slices.Reverse(days)
	return days
}

// Since returns the lines in memory after seq (0 for all of them, at most the last 500) and a
// channel closed when another is added.
func (l *Log) Since(seq int64) ([]Entry, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Entry
	for _, e := range l.ring {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out, l.more
}

// Detail raises the detail of the log until the time, or lowers it again with a zero time.
func (l *Log) Detail(until time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.until = until
	if until.IsZero() || !time.Now().Before(until) {
		l.until = time.Time{}
		l.level.Set(slog.LevelInfo)
		return
	}
	l.level.Set(slog.LevelDebug)
	l.timer = time.AfterFunc(time.Until(until), func() { l.Detail(time.Time{}) })
}

// DetailUntil is when "Record more detail" ends, zero while it is off.
func (l *Log) DetailUntil() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.until
}

// Query picks lines from the files, newest first.
type Query struct {
	// Level is the least level to include: debug, info, warn or error.
	Level string
	// Text keeps lines whose message or values contain it, ignoring case.
	Text string
	// From and To bound the time; zero is no bound.
	From, To time.Time
	// Cursor is where the page before stopped, "" for the newest line.
	Cursor string
	Limit  int
}

// scanBudget bounds how much of the files one read goes through, so a filter that matches
// little answers quickly and gives a cursor to read on.
const scanBudget = 8 << 20

var levels = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}

// Read returns the lines that match, newest first, and the cursor of the next page ("" at the
// end of the oldest file).
func (l *Log) Read(q Query) ([]Entry, string, error) {
	day, offset, err := parseCursor(q.Cursor)
	if err != nil {
		return nil, "", err
	}
	least := levels[q.Level]
	text := strings.ToLower(q.Text)
	var out []Entry
	scanned := 0
	for _, d := range l.days() {
		if day != "" && d > day {
			continue
		}
		if !q.From.IsZero() && d < q.From.Format(time.DateOnly) {
			break
		}
		start := int64(-1)
		if d == day {
			start = offset
		}
		lines, err := backwards(filepath.Join(l.dir, fileName(d)), start)
		if err != nil {
			return nil, "", err
		}
		for lines.next() {
			scanned += len(lines.line) + 1
			var e Entry
			if json.Unmarshal(lines.line, &e) == nil && levels[e.Level] >= least && (q.To.IsZero() || !e.Time.After(q.To)) &&
				(q.From.IsZero() || !e.Time.Before(q.From)) && (text == "" || matches(e, text)) {
				out = append(out, e)
			}
			if len(out) == q.Limit || scanned >= scanBudget {
				lines.close()
				return out, d + ":" + strconv.FormatInt(lines.at, 10), nil
			}
		}
		if lines.err != nil {
			return nil, "", lines.err
		}
	}
	return out, "", nil
}

func matches(e Entry, text string) bool {
	if strings.Contains(strings.ToLower(e.Message), text) {
		return true
	}
	for _, v := range e.Attrs {
		if strings.Contains(strings.ToLower(v), text) {
			return true
		}
	}
	return false
}

// ErrCursor: the cursor was not one this log gave.
var ErrCursor = errors.New("not a cursor of the log")

func parseCursor(cursor string) (string, int64, error) {
	if cursor == "" {
		return "", -1, nil
	}
	day, at, ok := strings.Cut(cursor, ":")
	n, err := strconv.ParseInt(at, 10, 64)
	if _, dayErr := time.Parse(time.DateOnly, day); !ok || err != nil || dayErr != nil || n < 0 {
		return "", 0, ErrCursor
	}
	return day, n, nil
}

// reverseLines reads a file's lines from the end towards the start. buf holds the file's
// bytes from pos up to the end of what is still unread.
type reverseLines struct {
	f    *os.File
	pos  int64
	buf  []byte
	line []byte
	at   int64 // where the current line starts: the cursor of the lines before it
	err  error
}

// backwards reads the lines of a file that start before offset, last first; -1 is the end.
func backwards(name string, offset int64) (*reverseLines, error) {
	f, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return &reverseLines{}, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if offset < 0 || offset > info.Size() {
		offset = info.Size()
	}
	return &reverseLines{f: f, pos: offset}, nil
}

func (r *reverseLines) close() {
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
}

func (r *reverseLines) next() bool {
	for r.f != nil {
		r.buf = bytes.TrimRight(r.buf, "\n")
		i := bytes.LastIndexByte(r.buf, '\n')
		switch {
		case i >= 0:
			r.line, r.at, r.buf = r.buf[i+1:], r.pos+int64(i+1), r.buf[:i+1]
			return true
		case r.pos == 0:
			r.line, r.at, r.buf = r.buf, 0, nil
			r.f.Close()
			r.f = nil
			return len(r.line) > 0
		}
		n := min(int64(64<<10), r.pos)
		chunk := make([]byte, n)
		if _, err := r.f.ReadAt(chunk, r.pos-n); err != nil {
			r.err = err
			r.f.Close()
			r.f = nil
			return false
		}
		r.buf, r.pos = append(chunk, r.buf...), r.pos-n
	}
	return false
}

// Files lists the log files with lines between from and now, oldest first, for a support
// export. Each is read with Copy.
func (l *Log) Files(from time.Time) []string {
	var out []string
	for _, day := range l.days() {
		if day >= from.Format(time.DateOnly) {
			out = append(out, filepath.Join(l.dir, fileName(day)))
		}
	}
	slices.Reverse(out)
	return out
}

// Lines reads a log file's entries in order, for a support export.
func Lines(name string, each func(Entry)) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scan.Scan() {
		var e Entry
		if json.Unmarshal(scan.Bytes(), &e) == nil {
			each(e)
		}
	}
	return scan.Err()
}
