// Package evidence is linked only by the scanner measurement build overlay.
package evidence

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var names = []string{"quantum", "analysis", "probe", "accept", "finish", "scheduler_wait", "pacing", "title_wait", "writer_begin", "sql"}

type Metric struct {
	Count int64 `json:"count"`
	Nanos int64 `json:"nanos"`
}

var state = struct {
	sync.Mutex
	on          bool
	began, last time.Time
	active      [10]int
	spans       map[string]Metric
	masks       map[uint64]int64
	sql         map[string]Metric
}{spans: map[string]Metric{}, masks: map[uint64]int64{}, sql: map[string]Metric{}}

func advance(now time.Time) {
	if state.on {
		var mask uint64
		for i, n := range state.active {
			if n > 0 {
				mask |= 1 << i
			}
		}
		state.masks[mask] += now.Sub(state.last).Nanoseconds()
	}
	state.last = now
}
func Begin(name string) func() {
	idx := -1
	for i, n := range names {
		if name == n {
			idx = i
			break
		}
	}
	if idx < 0 {
		panic("unknown evidence span")
	}
	state.Lock()
	now := time.Now()
	advance(now)
	state.active[idx]++
	state.Unlock()
	return func() {
		state.Lock()
		defer state.Unlock()
		end := time.Now()
		advance(end)
		state.active[idx]--
		if state.on {
			start := now
			if start.Before(state.began) {
				start = state.began
			}
			m := state.spans[name]
			m.Count++
			m.Nanos += end.Sub(start).Nanoseconds()
			state.spans[name] = m
		}
	}
}

var quoted = regexp.MustCompile(`'([^']|'')*'`)
var table = regexp.MustCompile(`(?i)\b(?:from|join|into|update)\s+([a-z_][a-z_0-9]*)`)
var families sync.Map
var familyCacheCount atomic.Int64

// Fixed categories only: neither SQL text nor bound data leaves the process.
func Family(query string) string {
	if cached, ok := families.Load(query); ok {
		return cached.(string)
	}
	result := classify(query)
	if familyCacheCount.Load() < 4096 {
		if _, loaded := families.LoadOrStore(query, result); !loaded {
			familyCacheCount.Add(1)
		}
	}
	return result
}
func classify(query string) string {
	q := strings.ToLower(quoted.ReplaceAllString(query, "?"))
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return "other"
	}
	verb := fields[0]
	switch verb {
	case "select":
		verb = "read"
	case "with":
		verb = "with"
	case "insert", "update", "delete", "replace":
		verb = "write"
	case "begin", "commit", "rollback", "savepoint", "release":
		return "transaction"
	case "pragma":
		return "pragma"
	default:
		verb = "other"
	}
	category := "other"
	for _, m := range table.FindAllStringSubmatch(q, -1) {
		t := m[1]
		switch {
		case strings.HasPrefix(t, "catalog_info"), strings.HasPrefix(t, "catalog_stream"), strings.HasPrefix(t, "catalog_asset"):
			category = "info_assets"
		case strings.HasPrefix(t, "catalog_dirty"), strings.HasPrefix(t, "reading_path"), strings.HasPrefix(t, "catalog_work"), strings.HasPrefix(t, "catalog_browse"):
			category = "derived_work"
		case strings.HasPrefix(t, "inventory_analysis"), strings.HasPrefix(t, "analysis_stage"):
			category = "analysis_queue"
		case strings.HasPrefix(t, "inventory_"):
			category = "inventory"
		case t == "jobs", strings.HasPrefix(t, "library_source"), strings.HasPrefix(t, "library_scan"), t == "libraries":
			category = "scan_control"
		case strings.HasPrefix(t, "session"), strings.HasPrefix(t, "access_"), strings.HasPrefix(t, "account"), strings.HasPrefix(t, "profile"), t == "policy":
			category = "authority"
		case strings.HasPrefix(t, "catalog_"):
			category = "catalog_other"
		default:
			continue
		}
		break // first recognized physical table; deterministic family rule
	}
	return verb + ":" + category
}
func SQL(query string, step func() (int, error)) (int, error) {
	family := Family(query)
	done := Begin("sql")
	start := time.Now()
	var rc int
	var err error
	pprof.Do(context.Background(), pprof.Labels("sql_family", family), func(context.Context) { rc, err = step() })
	elapsed := time.Since(start)
	done()
	state.Lock()
	if state.on {
		m := state.sql[family]
		m.Count++
		m.Nanos += elapsed.Nanoseconds()
		state.sql[family] = m
	}
	state.Unlock()
	return rc, err
}
func Serve() {
	root := os.Getenv("SCANNER_EVIDENCE_DIR")
	if root == "" || len(os.Args) != 1 || os.Getenv("PORTICO_SUPERVISED_WORKER") != "1" {
		return
	}
	path := filepath.Join(root, "control.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		panic(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		state.Lock()
		defer state.Unlock()
		now := time.Now()
		advance(now)
		switch r.URL.Path {
		case "/start":
			state.on = true
			resetReceipts()
			state.began = now
			state.last = now
			state.spans = map[string]Metric{}
			state.masks = map[uint64]int64{}
			state.sql = map[string]Metric{}
		case "/stop":
			state.on = false
		case "/identity":
		default:
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"pid": os.Getpid(), "names": names, "windowNanos": now.Sub(state.began).Nanoseconds(), "spans": state.spans, "masks": state.masks, "sqlFamilies": state.sql, "receiptReadyResume": receiptReport()})
	})
	go func() { _ = http.Serve(listener, mux) }()
}
