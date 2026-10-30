package sqlite

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
)

// The oracle is the real maintained driver/FTS5 engine, not a second tokenizer.
// Only fixture documents are indexed. Production matching creates no table.
func searchMatchTestConn(t *testing.T, before func(*conn), after func(SearchMatchResult)) *conn {
	t.Helper()
	d := &Driver{}
	var c *conn
	err := d.RegisterDeterministicScalarFunction("local_match_test", 3, func(ctx *FunctionContext, args []driver.Value) (driver.Value, error) {
		if before != nil {
			before(c)
		}
		var phrases []string
		if err := json.Unmarshal([]byte(args[2].(string)), &phrases); err != nil {
			return nil, err
		}
		r, err := ctx.MatchSearchDocument(args[0].(string), args[1].(string), phrases)
		if after != nil {
			after(r)
		}
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(r)
		return string(out), err
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := d.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	c = raw.(*conn)
	t.Cleanup(func() { c.Close() })
	return c
}

func searchTestArgs(values ...any) []driver.NamedValue {
	args := make([]driver.NamedValue, len(values))
	for i, v := range values {
		args[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return args
}

func searchTestExec(t *testing.T, c *conn, sql string, values ...any) {
	t.Helper()
	if _, err := c.ExecContext(context.Background(), sql, searchTestArgs(values...)); err != nil {
		t.Fatal(err)
	}
}

func searchTestValue(t *testing.T, c *conn, sql string, values ...any) driver.Value {
	t.Helper()
	rows, err := c.QueryContext(context.Background(), sql, searchTestArgs(values...))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var v [1]driver.Value
	if err := rows.Next(v[:]); err != nil {
		t.Fatal(err)
	}
	return v[0]
}

func searchTestMatch(t *testing.T, c *conn, search, kind string, phrases []string) SearchMatchResult {
	t.Helper()
	encoded, err := json.Marshal(phrases)
	if err != nil {
		t.Fatal(err)
	}
	v := searchTestValue(t, c, "SELECT local_match_test(?1,?2,?3)", search, kind, string(encoded))
	var r SearchMatchResult
	if err := json.Unmarshal([]byte(v.(string)), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func searchOracleExpression(phrases []string) string {
	parts := make([]string, len(phrases))
	for i, p := range phrases {
		parts[i] = `"` + strings.ReplaceAll(p, `"`, `""`) + `"*`
	}
	return strings.Join(parts, " AND ")
}

func TestSearchMatchFTS5Oracle(t *testing.T) {
	c := searchMatchTestConn(t, nil, nil)
	if v := searchTestValue(t, c, "SELECT sqlite_version()"); v != "3.53.4" {
		t.Fatalf("unqualified SQLite version: %v", v)
	}
	searchTestExec(t, c, "CREATE VIRTUAL TABLE oracle USING fts5(search,kind, tokenize='unicode61 remove_diacritics 2')")
	tests := []struct {
		name, search, kind string
		phrases            []string
		want               bool
	}{
		{"bare-prefix", "alphabet soup", "", []string{"a"}, true},
		{"bare-interior", "beta", "", []string{"a"}, false},
		{"text-13", "abcdefghijklmno", "", []string{"abcdefghijklm"}, true},
		{"number-7", "1234567890", "", []string{"1234567"}, true},
		{"numeric-scripts", "١٢٣٤٥ １２３４５", "", []string{"١٢", "１２"}, true},
		{"and-columns", "foo soup", "barley", []string{"foo", "bar"}, true},
		{"kind", "beta", "k2 l1", []string{"k2"}, true},
		{"library", "beta", "k2 l1", []string{"l1"}, true},
		{"phrase", "foo barista", "", []string{"foo bar"}, true},
		{"phrase-first-not-prefix", "foobar barista", "", []string{"foo bar"}, false},
		{"phrase-column-boundary", "foo", "bar", []string{"foo bar"}, false},
		{"phrase-overlap", "a a a bbbbb", "", []string{"a a b"}, true},
		{"phrase-restart", "a x a a bbbbb", "", []string{"a a b"}, true},
		{"unicode-modern-retained", "foo barista", "", []string{"foo𞤀bar"}, false},
		// Go considers U+1CF2 a letter; SQLite's unicode61 considers it a
		// separator. A single SearchTerms term can therefore be a phrase.
		{"unicode-letter-split", "foo barista", "", []string{"foo\u1cf2bar"}, true},
		{"unicode-letter-split-cross-column", "foo", "barista", []string{"foo\u1cf2bar"}, false},
		{"unicode-space-split", "foo barista", "", []string{"foo\u3000bar"}, true},
		{"unicode-modern-no-gap", "foobar", "", []string{"foo𞤀bar"}, false},
		{"accents", "ÉCOLE côte ṩḊ", "", []string{"ecole", "cote", "sd"}, true},
		{"punctuation", "foo---barista", "", []string{"foo.bar"}, true},
		{"quoted-punctuation", `foo "barista"`, "", []string{`foo "bar`}, true},
		{"han", "東京都 東京 京", "", []string{"東京", "京"}, true},
		{"kana", "カタカナ カタ タ", "", []string{"カタ", "タ"}, true},
		{"hangul", "한국어 한국 국", "", []string{"한국", "국"}, true},
		{"cjk-latin", "東京A", "", []string{"東京a"}, true},
		{"empty-token", "foo", "", []string{"---"}, false},
		{"empty-string", "foo", "", []string{""}, false},
		{"empty-conjunct", "foo", "", []string{"foo", "---"}, false},
		{"empty-source", "", "", []string{"foo"}, false},
		{"invalid-utf8", "foo\xffbar \xe3\x81 beta", "", []string{"foo", "bar", "beta"}, true},
		{"truncated-utf8", "foo 東京\xe6\x9d", "", []string{"東京"}, true},
		{"nul-source", "foo\x00bar", "", []string{"bar"}, true},
		{"long-token-prefix", strings.Repeat("a", 40000) + "x", "", []string{strings.Repeat("a", 33000) + "y"}, true},
		{"long-phrase-exact-clamp", strings.Repeat("a", 40000) + "z foo", "", []string{strings.Repeat("a", 33000) + "y foo"}, true},
		{"long-utf8-split", strings.Repeat("界", 11000) + "終", "", []string{strings.Repeat("界", 11000) + "別"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			searchTestExec(t, c, "DELETE FROM oracle")
			searchTestExec(t, c, "INSERT INTO oracle(search,kind) VALUES(?1,?2)", tc.search, tc.kind)
			oracle := searchTestValue(t, c, "SELECT count(*) FROM oracle WHERE oracle MATCH ?1", searchOracleExpression(tc.phrases)).(int64) != 0
			local := searchTestMatch(t, c, tc.search, tc.kind, tc.phrases)
			if local.Match != oracle || oracle != tc.want {
				t.Fatalf("local=%v MATCH=%v expected=%v work=%+v", local.Match, oracle, tc.want, local)
			}
			if local.SourceBytes != int64(len(tc.search)+len(tc.kind)) || local.TransientBytes < local.SourceBytes {
				t.Fatalf("invalid accounting: %+v", local)
			}
		})
	}
}

func TestSearchMatchWorkAndCleanup(t *testing.T) {
	c := searchMatchTestConn(t, nil, nil)
	text := strings.Repeat("alpha beta ", 20000)
	for i := 0; i < 5; i++ {
		started := time.Now()
		r := searchTestMatch(t, c, text, "k2 l1", []string{"alpha", "l1"})
		if !r.Match || r.DocumentTokens != 40002 || r.QueryTokens != 2 || r.QueryBytes != 7 || r.TokenBytes != 180011 || r.SourceBytes != int64(len(text)+5) {
			t.Fatalf("work receipt: %+v", r)
		}
		if i == 0 {
			t.Logf("cold singleton: sourceBytes=%d documentTokens=%d queryTokens=%d tokenBytes=%d transientBytes=%d wall=%s", r.SourceBytes, r.DocumentTokens, r.QueryTokens, r.TokenBytes, r.TransientBytes, time.Since(started))
		}
	}
	// Other dictionary terms cannot influence this call's tokenizer work.
	before := searchTestMatch(t, c, "alpha beta", "k2", []string{"a"})
	searchTestExec(t, c, "CREATE VIRTUAL TABLE unrelated USING fts5(text)")
	for i := 0; i < 100; i++ {
		searchTestExec(t, c, "INSERT INTO unrelated VALUES(?)", fmt.Sprintf("alpha%04d", i))
	}
	after := searchTestMatch(t, c, "alpha beta", "k2", []string{"a"})
	if before != after {
		t.Fatalf("global vocabulary influenced local work: %+v %+v", before, after)
	}
	assertNoSearchTokenStates(t)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoSearchTokenStates(t)
}

func assertNoSearchTokenStates(t *testing.T) {
	t.Helper()
	searchTokenStates.Range(func(_, _ any) bool { t.Error("retained native token callback state"); return false })
}

func TestSearchMatchCancelAndReuse(t *testing.T) {
	// Interrupt from within the active scalar, after VM activation. An idle
	// interrupt alone is cleared by SQLite; this verifies the native callback.
	var interrupt bool
	c := searchMatchTestConn(t, func(c *conn) {
		if interrupt {
			c.interrupt(c.db)
		}
	}, nil)
	interrupt = true
	_, err := c.QueryContext(context.Background(), "SELECT local_match_test('alpha beta','k2','[\"a\"]')", nil)
	var native *Error
	if !errors.As(err, &native) || native.Code()&255 != sqlite3.SQLITE_INTERRUPT {
		t.Fatalf("interruption lost: %v", err)
	}
	assertNoSearchTokenStates(t)
	interrupt = false
	if r := searchTestMatch(t, c, "alpha beta", "k2", []string{"a"}); !r.Match {
		t.Fatal(r)
	}
	assertNoSearchTokenStates(t)
}

func TestSearchMatchIdleCancellationAndClose(t *testing.T) {
	c := searchMatchTestConn(t, nil, nil)
	s, err := newStmt(c, "SELECT local_match_test('alpha beta','k2','[\"a\"]')")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	scope := c.watchProgress(ctx)
	cancel()
	c.interrupt(c.db)
	_, err = c.step(s.pstmt)
	scope.restore()
	var native *Error
	if !errors.As(err, &native) || native.Code()&255 != sqlite3.SQLITE_INTERRUPT {
		t.Fatalf("short scalar lost idle cancellation: %v", err)
	}
	c.reset(s.pstmt)
	if r := searchTestMatch(t, c, "alpha", "k2", []string{"a"}); !r.Match {
		t.Fatal(r)
	}
	if _, ok := callbackProgressStates.Load(c.db); !ok {
		t.Fatal("test did not exercise callback cancellation registry")
	}
	db := c.db
	s.Close()
	c.Close()
	if _, ok := callbackProgressStates.Load(db); ok {
		t.Fatal("connection close retained callback progress state")
	}
	assertNoSearchTokenStates(t)
}

func TestSearchMatchActiveCancellation(t *testing.T) {
	var receipt SearchMatchResult
	started, finished := make(chan struct{}), make(chan struct{})
	c := searchMatchTestConn(t, func(_ *conn) { close(started) }, func(r SearchMatchResult) { receipt = r })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer close(finished)
		<-started
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			active := false
			searchTokenStates.Range(func(_, _ any) bool { active = true; return false })
			if active {
				cancel()
				return
			}
			runtime.Gosched()
		}
	}()
	text := strings.Repeat("alpha beta ", 100000)
	rows, err := c.QueryContext(ctx, "SELECT local_match_test(?1,'k2','[\"a\"]')", searchTestArgs(text))
	if rows != nil {
		rows.Close()
	}
	<-finished
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stream cancellation lost: %v", err)
	}
	if receipt.DocumentTokens >= 200001 {
		t.Fatalf("canceled scalar exhausted document: %+v", receipt)
	}
	assertNoSearchTokenStates(t)
}

func TestSearchMatchConcurrentConnections(t *testing.T) {
	const workers = 4
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		c := searchMatchTestConn(t, nil, nil)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if r := searchTestMatch(t, c, "foo a a barista", "k2 l1", []string{"a a bar", "l1"}); !r.Match {
					t.Error(r)
				}
			}
		}()
	}
	wg.Wait()
	assertNoSearchTokenStates(t)
}

func TestSearchMatchCapacityAndContext(t *testing.T) {
	var ctx FunctionContext
	if _, err := ctx.MatchSearchDocument("", "", []string{strings.Repeat("a", SearchMatchMaxQueryBytes+1)}); !errors.Is(err, ErrSearchMatchCapacity) {
		t.Fatal(err)
	}
	if _, err := ctx.MatchSearchDocument("", "", []string{"a"}); err == nil {
		t.Fatal("accepted a detached callback context")
	}
	c := searchMatchTestConn(t, nil, nil)
	if r := searchTestMatch(t, c, "alpha", "k2", []string{}); r.Match || r.DocumentTokens != 0 {
		t.Fatal(r)
	}
	// Rows can be exhausted and closed repeatedly without owning native state.
	for i := 0; i < 10; i++ {
		rows, err := c.QueryContext(context.Background(), "SELECT local_match_test('alpha','k2','[\"a\"]')", nil)
		if err != nil {
			t.Fatal(err)
		}
		var v [1]driver.Value
		if err := rows.Next(v[:]); err != nil {
			t.Fatal(err)
		}
		if err := rows.Next(v[:]); err != io.EOF {
			t.Fatal(err)
		}
		rows.Close()
	}
	assertNoSearchTokenStates(t)
}

func TestSearchMatchContextLifetime(t *testing.T) {
	d := &Driver{}
	var held *FunctionContext
	if err := d.RegisterScalarFunction("hold_match_context", 0, func(ctx *FunctionContext, _ []driver.Value) (driver.Value, error) {
		held = ctx
		r, err := ctx.MatchSearchDocument("alpha", "k2", []string{"a"})
		if !r.Match {
			t.Error("live callback context did not match")
		}
		return int64(1), err
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := d.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	c := raw.(*conn)
	defer c.Close()
	if v := searchTestValue(t, c, "SELECT hold_match_context()"); v != int64(1) {
		t.Fatal(v)
	}
	if held == nil || held.ctx != 0 || held.tls != nil {
		t.Fatal("Go context retained native callback state")
	}
	if _, err := held.MatchSearchDocument("alpha", "k2", []string{"a"}); err == nil {
		t.Fatal("accepted an expired callback context")
	}
}
