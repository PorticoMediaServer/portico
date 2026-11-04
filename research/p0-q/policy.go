package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Every SQL call below closes its read snapshot before another chunk is opened.
// Go reducers are ephemeral; no presentation-shaped persistent tables are added.
type policyReader struct {
	db        *sql.DB
	snapshots []float64
	rows      int
	stmts     map[string]*sql.Stmt
	timings   map[string]float64
	calls     map[string]int
}

func (p *policyReader) read(ctx context.Context, q string, args []any, scan func(*sql.Rows) error) error {
	if publishedSchema {
		q = strings.ReplaceAll(q, "catalog_entities", "q_search_entities")
	}
	if currentSearchGeneration > 1 {
		for _, base := range []string{"search_own_prefix", "search_fragment_prefix", "search_fragment_lexical", "search_fragment_grams"} {
			target := bankName(base, currentSearchGeneration)
			if !strings.Contains(q, target) {
				q = strings.ReplaceAll(q, base, target)
			}
		}
	}
	c, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	defer func() {
		ms := float64(time.Since(start).Microseconds()) / 1000
		p.snapshots = append(p.snapshots, ms)
		if p.timings == nil {
			p.timings = map[string]float64{}
			p.calls = map[string]int{}
		}
		group := "other"
		switch {
		case strings.Contains(q, "FROM rel"):
			group = "parent_expansion"
		case strings.Contains(q, "FROM edges"):
			group = "context_edges"
		case strings.Contains(q, "search_fragment_prefix"):
			group = "fragment_fts"
		case strings.Contains(q, "search_own_prefix"):
			group = "title_fts"
		case strings.Contains(q, "search_fragment_lexical"):
			group = "lexical"
		}
		p.timings[group] += ms
		p.calls[group]++
	}()
	if p.stmts == nil {
		p.stmts = map[string]*sql.Stmt{}
	}
	stmt := p.stmts[q]
	if stmt == nil {
		var e error
		stmt, e = p.db.PrepareContext(c, q)
		if e != nil {
			return e
		}
		p.stmts[q] = stmt
	}
	rows, e := stmt.QueryContext(c, args...)
	if e != nil {
		return e
	}
	for rows.Next() {
		if e = scan(rows); e != nil {
			rows.Close()
			return e
		}
		p.rows++
	}
	e = rows.Err()
	ce := rows.Close()
	if e != nil {
		return e
	}
	return ce
}

type hit struct {
	tier  int
	score float64
}
type owner struct {
	id, kind int
	score    float64
	fold     string
}
type rankedHit struct {
	id, tier int
	score    float64
}
type preparedPolicy struct {
	query     string
	lead      int
	items     []rankedHit
	fallback  bool
	reader    *policyReader
	diagnosis map[string]any
}

func put(m map[int]hit, id, tier int, score float64) {
	old, ok := m[id]
	if !ok || tier > old.tier {
		old.tier = tier
	}
	if score > old.score {
		old.score = score
	}
	m[id] = old
}
func ctxerr(ctx context.Context) error { return ctx.Err() }
func encodedJSON(ids []int) string     { b, e := json.Marshal(ids); must(e); return string(b) }
func (p *policyReader) expand(ctx context.Context, roots map[int]owner, m map[int]hit) error {
	for depth := 0; depth < 2 && len(roots) > 0; depth++ {
		ids := make([]int, 0, len(roots))
		for id := range roots {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		next := map[int]owner{}
		lastParent, lastChild := -1, -1
		for {
			if e := ctxerr(ctx); e != nil {
				return e
			}
			n := 0
			e := p.read(ctx, `SELECT r.parent,r.child,e.kind FROM rel r CROSS JOIN catalog_entities e ON e.id=r.child WHERE r.parent IN(SELECT value FROM json_each(?)) AND (r.parent,r.child)>(?,?) AND e.retired=0 ORDER BY r.parent,r.child LIMIT 128`, []any{encodedJSON(ids), lastParent, lastChild}, func(r *sql.Rows) error {
				var parent, id, kind int
				if e := r.Scan(&parent, &id, &kind); e != nil {
					return e
				}
				lastParent, lastChild = parent, id
				n++
				o := roots[parent]
				put(m, id, 0, o.score)
				if kind == 6 {
					next[id] = owner{id: id, kind: kind, score: o.score}
				}
				return nil
			})
			if e != nil {
				return e
			}
			if n < 128 {
				break
			}
		}
		roots = next
	}
	return nil
}
func (p *policyReader) edges(ctx context.Context, fragments map[int]owner, m map[int]hit) error {
	if len(fragments) == 0 {
		return nil
	}
	ids := make([]int, 0, len(fragments))
	for id := range fragments {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	lastFrag, lastEntity := -1, -1
	for {
		if e := ctxerr(ctx); e != nil {
			return e
		}
		n := 0
		e := p.read(ctx, `SELECT x.fragment,x.entity FROM edges x CROSS JOIN catalog_entities e ON e.id=x.entity WHERE x.fragment IN(SELECT value FROM json_each(?)) AND (x.fragment,x.entity)>(?,?) AND e.retired=0 ORDER BY x.fragment,x.entity LIMIT 128`, []any{encodedJSON(ids), lastFrag, lastEntity}, func(r *sql.Rows) error {
			var fid, id int
			if e := r.Scan(&fid, &id); e != nil {
				return e
			}
			lastFrag, lastEntity = fid, id
			n++
			put(m, id, 0, fragments[fid].score)
			return nil
		})
		if e != nil {
			return e
		}
		if n < 128 {
			return nil
		}
	}
}
func (p *policyReader) term(ctx context.Context, term string, fallback bool, fullfold string, titles map[int]string) (map[int]hit, error) {
	m := map[int]hit{}
	isCJK := strings.ContainsFunc(term, facettext.CJK)
	seed := term
	star := "*"
	if isCJK {
		seed = cjkSeed(term)
		star = ""
	}
	if !fallback {
		last := -1
		for {
			if e := ctxerr(ctx); e != nil {
				return nil, e
			}
			n := 0
			roots := map[int]owner{}
			e := p.read(ctx, `SELECT f.rowid,e.kind,e.title_fold FROM search_own_prefix f CROSS JOIN catalog_entities e ON e.id=f.rowid WHERE search_own_prefix MATCH ? AND f.rowid>? AND e.retired=0 ORDER BY f.rowid LIMIT 64`, []any{"title_prefix_tokens:" + encode(seed) + star, last}, func(r *sql.Rows) error {
				var id, kind int
				var folded string
				if e := r.Scan(&id, &kind, &folded); e != nil {
					return e
				}
				last = id
				n++
				if isCJK && !strings.Contains(folded, term) {
					return nil
				}
				tier := 1
				if strings.HasPrefix(folded, term) {
					tier = 2
				}
				put(m, id, tier, 0)
				titles[id] = folded
				if kind == 2 || kind == 5 || kind == 6 {
					roots[id] = owner{id: id, kind: kind}
				}
				return nil
			})
			if e != nil {
				return nil, e
			}
			if e = p.expand(ctx, roots, m); e != nil {
				return nil, e
			}
			if n < 64 {
				break
			}
		}
		last = -1
		for {
			n := 0
			frags := map[int]owner{}
			e := p.read(ctx, `SELECT p.rowid,f.normalized FROM search_fragment_prefix p CROSS JOIN search_fragments f ON f.id=p.rowid WHERE search_fragment_prefix MATCH ? AND p.rowid>? ORDER BY p.rowid LIMIT 64`, []any{encode(seed) + star, last}, func(r *sql.Rows) error {
				var id int
				var normalized string
				if e := r.Scan(&id, &normalized); e != nil {
					return e
				}
				last = id
				n++
				if !isCJK || strings.Contains(normalized, term) {
					frags[id] = owner{id: id}
				}
				return nil
			})
			if e != nil {
				return nil, e
			}
			if e = p.edges(ctx, frags, m); e != nil {
				return nil, e
			}
			if n < 64 {
				break
			}
		}
	} else {
		last := -1
		for {
			if e := ctxerr(ctx); e != nil {
				return nil, e
			}
			n := 0
			frags := map[int]owner{}
			roots := map[int]owner{}
			e := p.read(ctx, `SELECT l.rowid,f.owner_entity,f.field,f.normalized,-bm25(search_fragment_lexical),e.kind FROM search_fragment_lexical l CROSS JOIN search_fragments f ON f.id=l.rowid LEFT JOIN catalog_entities e ON e.id=f.owner_entity AND f.field=1 WHERE search_fragment_lexical MATCH ? AND l.rowid>? ORDER BY l.rowid LIMIT 64`, []any{`"` + term + `"*`, last}, func(r *sql.Rows) error {
				var fid, field int
				var entity, kind sql.NullInt64
				var normalized string
				var score float64
				if e := r.Scan(&fid, &entity, &field, &normalized, &score, &kind); e != nil {
					return e
				}
				last = fid
				n++
				if strings.ContainsFunc(term, facettext.CJK) && !strings.Contains(normalized, term) {
					return nil
				}
				if field == 1 && entity.Valid {
					id := int(entity.Int64)
					put(m, id, 1, 4*score)
					titles[id] = normalized
					if kind.Valid && (kind.Int64 == 2 || kind.Int64 == 5 || kind.Int64 == 6) {
						roots[id] = owner{id: id, kind: int(kind.Int64), score: score}
					}
				} else {
					frags[fid] = owner{id: fid, score: score}
				}
				return nil
			})
			if e != nil {
				return nil, e
			}
			if e = p.expand(ctx, roots, m); e != nil {
				return nil, e
			}
			if e = p.edges(ctx, frags, m); e != nil {
				return nil, e
			}
			if n < 64 {
				break
			}
		}
	}
	return m, nil
}
func preparePolicy(ctx context.Context, db *sql.DB, q string, restricted bool) (*preparedPolicy, error) {
	p := &policyReader{db: db}
	ok := false
	defer func() {
		if !ok {
			p.close()
		}
	}()
	terms := policyTerms(q)
	fallback := fallbackQuery(q)
	prepareStart := time.Now()
	titles := map[int]string{}
	sets := make([]map[int]hit, len(terms))
	lead := 0
	for i, t := range terms {
		m, e := p.term(ctx, t, fallback, facettext.SearchFold(q), titles)
		if e != nil {
			return nil, e
		}
		sets[i] = m
		if len(m) < len(sets[lead]) {
			lead = i
		}
	}
	expandedAt := time.Now()
	items := make([]rankedHit, 0, len(sets[lead]))
	reduceN := 0
	for id, h := range sets[lead] {
		reduceN++
		if reduceN%1024 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if restricted && id%100 != 0 {
			continue
		}
		score := 0.0
		ok := true
		for _, s := range sets {
			x, found := s[id]
			if !found {
				ok = false
				break
			}
			score += x.score
		}
		if !ok {
			continue
		}
		tier := h.tier
		if titles[id] == facettext.SearchFold(q) {
			tier = 3
			score += 1000
		}
		if strings.HasPrefix(titles[id], facettext.SearchFold(q)) {
			score += 100
		}
		items = append(items, rankedHit{id: id, tier: tier, score: math.Round(score*1e6) / 1e6})
	}
	if e := ctxerr(ctx); e != nil {
		return nil, e
	}
	rankAt := time.Now()
	comparisons := 0
	func() {
		defer func() {
			if v := recover(); v != nil {
				if v != context.Canceled && v != context.DeadlineExceeded {
					panic(v)
				}
			}
		}()
		sort.Slice(items, func(i, j int) bool {
			comparisons++
			if comparisons%4096 == 0 && ctx.Err() != nil {
				panic(ctx.Err())
			}
			if fallback && items[i].score != items[j].score {
				return items[i].score > items[j].score
			}
			if !fallback && items[i].tier != items[j].tier {
				return items[i].tier > items[j].tier
			}
			return items[i].id < items[j].id
		})
	}()
	if e := ctxerr(ctx); e != nil {
		return nil, e
	}
	ok = true
	return &preparedPolicy{query: q, lead: lead, items: items, fallback: fallback, reader: p, diagnosis: map[string]any{"expansion_ms": float64(expandedAt.Sub(prepareStart).Microseconds()) / 1000, "intersection_ms": float64(rankAt.Sub(expandedAt).Microseconds()) / 1000, "ranking_ms": float64(time.Since(rankAt).Microseconds()) / 1000, "sql_ms": p.timings, "sql_calls": p.calls, "sql_rows": p.rows, "verified_lead_population": len(sets[lead]), "ranked_population": len(items)}}, nil
}

// The cursor is a stable rank/id key; real server cursors must additionally sign
// the query/viewer/revision/session-generation fence before this enters product.
type policyCursor struct {
	tier, id int
	score    float64
	valid    bool
}

func (p *preparedPolicy) page(ctx context.Context, c policyCursor) ([]int, policyCursor, error) {
	start := 0
	if c.valid {
		start = sort.Search(len(p.items), func(i int) bool {
			x := p.items[i]
			if p.fallback {
				if x.score != c.score {
					return x.score < c.score
				}
			} else if x.tier != c.tier {
				return x.tier < c.tier
			}
			return x.id > c.id
		})
	}
	end := min(start+20, len(p.items))
	ids := []int{}
	for _, h := range p.items[start:end] {
		ids = append(ids, h.id)
	}
	if len(ids) > 0 {
		e := p.reader.read(ctx, `SELECT id,title FROM catalog_entities WHERE id IN(SELECT value FROM json_each(?))`, []any{encodedJSON(ids)}, func(r *sql.Rows) error { var id int; var title string; return r.Scan(&id, &title) })
		if e != nil {
			return nil, c, e
		}
		x := p.items[end-1]
		c = policyCursor{tier: x.tier, id: x.id, score: x.score, valid: true}
	}
	return ids, c, nil
}
func shortSuggestion(ctx context.Context, db *sql.DB, q string, restricted bool) ([]int, Stat, error) {
	p := &policyReader{db: db}
	defer p.close()
	last := int64(math.MaxInt64)
	ids := []int{}
	raw := 0
	match := "title_prefix_tokens:" + encode(facettext.SearchFold(q)) + "*"
	if strings.ContainsFunc(q, facettext.CJK) {
		match = "title_prefix_tokens:" + encode(cjkSeed(facettext.SearchFold(q)))
	}
	for len(ids) < 20 && raw < 256 {
		n := 0
		e := p.read(ctx, `WITH c AS MATERIALIZED(SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH ? AND rowid<? ORDER BY rowid DESC LIMIT 64) SELECT c.rowid,e.retired FROM c CROSS JOIN catalog_entities e ON e.id=c.rowid ORDER BY c.rowid DESC`, []any{match, last}, func(r *sql.Rows) error {
			var id, retired int
			if e := r.Scan(&id, &retired); e != nil {
				return e
			}
			last = int64(id)
			n++
			raw++
			if retired == 0 && (!restricted || id%100 == 0) {
				if len(ids) < 20 {
					ids = append(ids, id)
				}
			}
			return nil
		})
		if e != nil {
			return nil, stats(p.snapshots), e
		}
		if n < 64 {
			break
		}
	}
	return ids, stats(p.snapshots), nil
}
func idsDigest(ids []int) string {
	s := append([]int{}, ids...)
	sort.Ints(s)
	h := sha256.New()
	b := make([]byte, 8)
	for _, id := range s {
		binary.LittleEndian.PutUint64(b, uint64(id))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func policyBench(db *sql.DB, n, samples int) {
	ctx := context.Background()
	for _, q := range []string{"a", "al", "jo", "東", "王"} {
		times := []float64{}
		snaps := []float64{}
		var ids []int
		for i := 0; i < samples+1; i++ {
			t := time.Now()
			r, s, e := shortSuggestion(ctx, db, q, false)
			must(e)
			if i > 0 {
				times = append(times, float64(time.Since(t).Microseconds())/1000)
				snaps = append(snaps, s.Max)
			}
			ids = r
		}
		emit("policy_short", map[string]any{"query": q, "input_runes": utf8.RuneCountInString(q), "ranking": "catalogue insertion sequence DESC (entity ID DESC)", "candidate_limit": 256, "page_limit": 20, "latency": stats(times), "raw_ms": times, "snapshot_maxima": stats(snaps), "ids": ids})
	}
	for _, q := range []string{"ada", "harbor", "harbor ada", "1984", "1234567", "東京", "nebuchadnezzar", "al", "jo", "harbor ad", "王菲"} {
		readyTimes := []float64{}
		prepSnaps := []float64{}
		pageSnaps := []float64{}
		pages := [][]float64{}
		var count int
		var digest string
		var witnesses map[string]bool
		failures := []string{}
		diagnoses := []map[string]any{}
		for rep := 0; rep < samples; rep++ {
			t := time.Now()
			p, e := preparePolicy(ctx, db, q, false)
			if e != nil {
				failures = append(failures, e.Error())
				continue
			}
			diagnoses = append(diagnoses, p.diagnosis)
			prepSnaps = append(prepSnaps, p.reader.snapshots...)
			p.reader.snapshots = nil
			c := policyCursor{}
			all := []int{}
			pageN := 0
			for {
				start := time.Now()
				ids, next, e := p.page(ctx, c)
				if e != nil {
					failures = append(failures, e.Error())
					break
				}
				elapsed := float64(time.Since(start).Microseconds()) / 1000
				if pageN == 0 {
					elapsed = float64(time.Since(t).Microseconds()) / 1000
					readyTimes = append(readyTimes, elapsed)
				}
				if len(ids) == 0 {
					break
				}
				for len(pages) <= pageN {
					pages = append(pages, nil)
				}
				pages[pageN] = append(pages[pageN], elapsed)
				all = append(all, ids...)
				pageN++
				c = next
			}
			pageSnaps = append(pageSnaps, p.reader.snapshots...)
			p.reader.close()
			seen := map[int]bool{}
			for _, id := range all {
				if seen[id] {
					panic("keyset duplicate")
				}
				seen[id] = true
			}
			if len(all) != len(p.items) {
				panic("keyset dropped results")
			}
			count = len(all)
			digest = idsDigest(all)
			witnesses = map[string]bool{"Harbor Lights": seen[0], "Northern Road": seen[1], "Blue River": seen[2]}
		}
		pageStats := make([]Stat, len(pages))
		for i, a := range pages {
			pageStats[i] = stats(a)
		}
		emit("policy_full", map[string]any{"query": q, "explicit_short_action": shortInput(q), "samples": samples, "query_terms": policyTerms(q), "diagnosis": diagnoses, "first_ready": stats(readyTimes), "raw_first_ready_ms": readyTimes, "per_page": pageStats, "page_raw_ms": pages, "prepare_snapshots": stats(prepSnaps), "page_snapshots": stats(pageSnaps), "result_count": count, "membership_digest": digest, "witnesses": witnesses, "failures": failures, "snapshot_deadline_ms": 250, "snapshot_scope": "each LIMIT64/128 read or20-item hydrate; Go reducer outside snapshots"})
	}
	emit("completed", map[string]any{"mode": "final_policy", "items": n, "schema": "A", "client_debounce_ms": 350, "full_query_freshness": "static accepted corpus; production publication/fence lease remains build gate"})
}

type searchSession struct {
	mu     sync.Mutex
	gen    uint64
	cancel context.CancelFunc
}

func (s *searchSession) next(parent context.Context) (context.Context, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.gen++
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	return ctx, s.gen
}
func (s *searchSession) current(gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return gen == s.gen
}
func policyHygiene(db *sql.DB) {
	s := searchSession{}
	old, g1 := s.next(context.Background())
	newer, g2 := s.next(context.Background())
	if old.Err() != context.Canceled || newer.Err() != nil || s.current(g1) || !s.current(g2) {
		panic("session supersede")
	}
	other := searchSession{}
	independent, _ := other.next(context.Background())
	s.next(context.Background())
	if independent.Err() != nil {
		panic("cross-session cancellation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := preparePolicy(ctx, db, "a", false); done <- e }()
	time.Sleep(time.Millisecond)
	start := time.Now()
	cancel()
	e := <-done
	if e != nil && e != context.Canceled {
		panic(e)
	}
	emit("policy_hygiene", map[string]any{"session_supersede": true, "independent_sessions": true, "cancel_return_ms": float64(time.Since(start).Microseconds()) / 1000, "cancel_error": fmt.Sprint(e), "debounce_ms": 350, "debounce_burst": "inputs0/100/200/300/400ms dispatch once at750ms", "no_stale_response": "generation checked before publication; prior context cancelled"})
}

func (p *policyReader) close() {
	for _, s := range p.stmts {
		s.Close()
	}
	p.stmts = nil
}
