package dbwork

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The write gate is only a discipline if nothing writes around it. The static
// scan in discipline_test.go catches a raw write transaction where it is
// written; this is the runtime half, in the driver, where every statement
// passes whatever helper issued it.
//
// An OpenHandle database has three pools. The single writer connection may
// change the database only while the gate is held. The foreground and
// background read pools may never change it: a write there takes SQLite's
// write lock without a place in the priority ladder, and once the writer's
// busy timeout is short it is also what makes a gated writer fail.
//
// An ungated write is a defect, but refusing it in production would drop a
// write that would otherwise succeed. So production counts and logs it and
// lets it run; a test binary refuses it with ErrUngatedWrite, unless the write
// comes straight from a test file (fixture seeding is not server code).

// ErrUngatedWrite is a database change attempted outside the write gate, in a
// test binary.
var ErrUngatedWrite = errors.New("dbwork: database change outside the write gate")

type connRole uint8

const (
	roleUnmanaged connRole = iota
	roleReader
	roleWriter
)

// roleParameter marks which OpenHandle pool a DSN opens. observedDriver.Open
// strips it before the SQLite driver sees the name.
const roleParameter = "_portico_role"

func connRoleOf(name string) (string, connRole) {
	// The filename is escaped, so the first '?' starts the parameters.
	filename, query, ok := strings.Cut(name, "?")
	if !ok {
		return name, roleUnmanaged
	}
	role := roleUnmanaged
	kept := make([]string, 0, 12)
	for _, parameter := range strings.Split(query, "&") {
		switch parameter {
		case roleParameter + "=writer":
			role = roleWriter
		case roleParameter + "=reader":
			role = roleReader
		default:
			kept = append(kept, parameter)
		}
	}
	if role == roleUnmanaged {
		return name, role
	}
	if len(kept) == 0 {
		return filename, role
	}
	return filename + "?" + strings.Join(kept, "&"), role
}

// withRole appends the pool marker to a DSN built by Policy.DSN.
func withRole(dsn string, role string) string {
	if strings.Contains(dsn, "?") {
		return dsn + "&" + roleParameter + "=" + role
	}
	return dsn + "?" + roleParameter + "=" + role
}

// writeHead matches a data-changing statement anywhere in a CTE or a script.
var writeHead = regexp.MustCompile(`(?is)\b(insert\s+(or\s+\w+\s+)?into|replace\s+into|update\s+(or\s+\w+\s+)?[\w."\x60\[\]]+\s+set|delete\s+from)\b`)

// mutatesDatabase reports whether a statement changes the database file. A
// temporary table lives outside it, and a PRAGMA is a connection setting or a
// checkpoint, never ordinary data, so neither counts.
func mutatesDatabase(query string) bool {
	text := strings.TrimLeft(stripLeadingComments(query), " \t\r\n(")
	word := text
	if cut := strings.IndexAny(word, " \t\r\n("); cut >= 0 {
		word = word[:cut]
	}
	switch strings.ToLower(word) {
	case "insert", "update", "delete", "replace", "drop", "alter", "analyze", "vacuum", "reindex":
		return !temporaryTarget(text)
	case "create":
		return !temporaryTarget(text)
	case "with":
		return writeHead.MatchString(text)
	case "begin":
		// A transaction that may write: BEGIN IMMEDIATE takes the write lock.
		return true
	}
	// A script whose later statements write.
	if strings.Contains(strings.TrimRight(text, "; \t\r\n"), ";") {
		return writeHead.MatchString(text)
	}
	return false
}

func temporaryTarget(text string) bool {
	fields := strings.Fields(strings.ToLower(text))
	for index := 1; index < len(fields) && index < 4; index++ {
		switch fields[index] {
		case "temp", "temporary":
			return true
		}
		if strings.HasPrefix(fields[index], "temp.") {
			return true
		}
	}
	return false
}

func stripLeadingComments(query string) string {
	for {
		trimmed := strings.TrimLeft(query, " \t\r\n\f")
		switch {
		case strings.HasPrefix(trimmed, "--"):
			cut := strings.IndexByte(trimmed, '\n')
			if cut < 0 {
				return ""
			}
			query = trimmed[cut+1:]
		case strings.HasPrefix(trimmed, "/*"):
			cut := strings.Index(trimmed, "*/")
			if cut < 0 {
				return ""
			}
			query = trimmed[cut+2:]
		default:
			return trimmed
		}
	}
}

// mutationMemo caches mutatesDatabase per statement text, which is a bounded
// set of constants in practice; past the limit a text is classified each time.
var mutationMemo struct {
	sync.Map
	count atomic.Int64
}

func mutates(query string) bool {
	if cached, ok := mutationMemo.Load(query); ok {
		return cached.(bool)
	}
	out := mutatesDatabase(query)
	if mutationMemo.count.Load() < statementHeadLimit {
		if _, loaded := mutationMemo.LoadOrStore(strings.Clone(query), out); !loaded {
			mutationMemo.count.Add(1)
		}
	}
	return out
}

// checkWriter is the driver's check that a change goes through the gate. A
// transaction a test opened itself on a read pool is the test's: helpers it
// passes that transaction to are not flagged statement by statement.
func (c *observedConn) checkWriter(_ context.Context, query string) error {
	switch c.role {
	case roleWriter:
		if gate.Held() || !mutates(query) {
			return nil
		}
	case roleReader:
		if c.testTx || !mutates(query) {
			return nil
		}
	default:
		return nil
	}
	testOwned, err := noteUngatedWrite(c.role, query)
	if query == "BEGIN" {
		c.testTx = testOwned
	}
	return err
}

var ungated struct {
	writer, reader atomic.Uint64
	logged         atomic.Int64
}

// UngatedWrites reports changes made outside the gate: on the writer
// connection without a hold, and through a read pool.
func UngatedWrites() (writer, reader uint64) {
	return ungated.writer.Load(), ungated.reader.Load()
}

// UngatedStats is UngatedWrites for diagnostics. Both stay zero on a healthy
// server; anything else names a write that bypassed the gate.
type UngatedStats struct {
	Writer uint64 `json:"writer"`
	Reader uint64 `json:"reader"`
}

// Ungated snapshots UngatedWrites.
func Ungated() UngatedStats {
	writer, reader := UngatedWrites()
	return UngatedStats{Writer: writer, Reader: reader}
}

func noteUngatedWrite(role connRole, query string) (testOwned bool, err error) {
	where := "a read pool"
	if role == roleWriter {
		ungated.writer.Add(1)
		where = "the writer connection without the write gate"
	} else {
		ungated.reader.Add(1)
	}
	statement := strings.Join(strings.Fields(query), " ")
	if len(statement) > 96 {
		statement = statement[:96]
	}
	if !testing.Testing() {
		now := time.Now().UnixNano()
		if last := ungated.logged.Load(); now-last >= int64(time.Minute) && ungated.logged.CompareAndSwap(last, now) {
			function, _, _, _ := ungatedCaller()
			log.Printf("dbwork: a database change ran on %s in %s: %s", where, function, statement)
		}
		return false, nil
	}
	function, file, line, inTest := ungatedCaller()
	if inTest {
		return true, nil
	}
	message := fmt.Sprintf("dbwork: a database change ran on %s in %s: %s", where, function, statement)
	if path := os.Getenv(PlanReportEnv); path != "" {
		planReport.Lock()
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%s\t%s:%d\n", message, file, line)
			f.Close()
		}
		planReport.Unlock()
		return false, nil
	}
	return false, fmt.Errorf("%w: %s at %s:%d", ErrUngatedWrite, message, file, line)
}

// ungatedCaller is driverCaller; a test substitutes it to see the refusal a
// server call site gets.
var ungatedCaller = driverCaller

// driverCaller finds the code that issued a statement: the first frame outside
// dbwork, database/sql and the SQLite driver.
func driverCaller() (function, file string, line int, inTest bool) {
	var pcs [40]uintptr
	n := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		infrastructure := strings.Contains(frame.Function, "/internal/dbwork.") && !strings.HasSuffix(frame.File, "_test.go") ||
			strings.HasPrefix(frame.Function, "database/sql.") ||
			strings.Contains(frame.Function, "/internal/thirdparty/sqlite.")
		if !infrastructure || !more {
			return frame.Function, frame.File, frame.Line, testCode(frame.File)
		}
	}
}

// testSupport are packages only tests import: fixture builders and the scale
// benchmark's seeding. Their writes are a test's own, like a _test.go file's.
var testSupport = map[string]bool{"catalogtest": true, "decodertest": true, "readingtest": true, "testauth": true, "testtier": true, "scalebench": true, "waketest": true, "fixture": true}

// testCode reports whether a source file is test code rather than server code.
func testCode(file string) bool {
	if strings.HasSuffix(file, "_test.go") {
		return true
	}
	slash := strings.LastIndexByte(file, '/')
	if slash < 0 {
		return false
	}
	directory := file[:slash]
	return testSupport[directory[strings.LastIndexByte(directory, '/')+1:]]
}
