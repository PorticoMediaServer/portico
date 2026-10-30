package sqlite

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// SearchMatchVersion belongs in the parent's complete-query preparation key.
const SearchMatchVersion = "unicode61-rd2-sqlite35304-local-v1"

// These bound query state, not maintained document size. Document scalars keep
// SQLite's own length limit and the caller's explicit exceptional O(B) policy.
const SearchMatchMaxQueryBytes = 65536
const SearchMatchMaxPhrases = 4096

// SearchMatchResult is a fixed-size JSON object; it never contains input text,
// tokens, positions or result IDs. Counters include both columns even after a
// match is found. TokenBytes counts complete tokenizer output BEFORE SQLite's
// byte clamp (which can split a UTF-8 sequence), not just compared bytes.
type SearchMatchResult struct {
	Match          bool  `json:"match"`
	SourceBytes    int64 `json:"sourceBytes"`
	QueryBytes     int64 `json:"queryBytes"`
	DocumentTokens int64 `json:"documentTokens"`
	QueryTokens    int64 `json:"queryTokens"`
	TokenBytes     int64 `json:"tokenBytes"`
	TransientBytes int64 `json:"transientBytes"`
}

// SearchMatchTransientBound is a conservative reservation for the bridge plus
// the driver's Go argument copies, before allocation. B is the sum of the two
// source byte lengths; Q is the sum of RAW phrase byte lengths, P their count.
// The bound is 16*B + 256*Q + 512*P + 64KiB. All arithmetic is in bytes.
//
// Native input copies total B+Q over a call and only one is live at a time.
// unicode61's fold buffer doubles, with <=3 live buffer capacities during a
// resize; UTF-8 output uses <=4 bytes per consumed input byte, so its peak is
// <=12*(max input length+6)+constant. It is retained only until xDelete here.
// Query strings, token copies, slice growth and per-phrase positional booleans
// fit the Q/P terms. Document callbacks borrow native token bytes synchronously
// and retain none. Fixed overhead covers tokenizer/API/VM/state bookkeeping.
//
// This excludes caller-held row/binding/page buffers, phrasesJSON encoding and
// decoding, delivery serialization and the parent's old+clone state. The caller
// charges those separately, reserves before issuing SQL and releases all scalar
// buffers before another unit. B is actual expanded source size, not the 64KiB
// pre-expansion metadata limit. This is O(B) space/work, not a time guarantee;
// one long token or separator run cannot be interrupted inside unicode61.
func SearchMatchTransientBound(sourceBytes, queryBytes int64, phrases int) int64 {
	if sourceBytes < 0 || queryBytes < 0 || phrases < 0 || sourceBytes > (math.MaxInt64-65536)/16 || queryBytes > SearchMatchMaxQueryBytes || phrases > SearchMatchMaxPhrases {
		return math.MaxInt64
	}
	q := 256*queryBytes + 512*int64(phrases) + 65536
	if sourceBytes > (math.MaxInt64-q)/16 {
		return math.MaxInt64
	}
	return 16*sourceBytes + q
}

// ErrSearchMatchCapacity is returned before copying native inputs for lengths
// the pinned tokenizer's signed-int API cannot represent, or excessive query
// state. SQL preserves SQLITE_TOOBIG so callers can classify capacity without
// parsing text. No document truncation or approximate match is substituted.
var ErrSearchMatchCapacity = &Error{msg: "search_match_capacity", code: sqlite3.SQLITE_TOOBIG}

type searchPhrase struct {
	tokens []string
	active []bool
	found  bool
}

type searchTokenState struct {
	db      uintptr
	done    <-chan struct{}
	result  *SearchMatchResult
	phrases []searchPhrase
	query   int // -1 while streaming a document column
}

// Only opaque monotonically assigned integer handles cross the native callback
// boundary. Entries are scoped to one synchronous call and always removed.
var searchTokenStates sync.Map
var searchTokenSequence atomic.Uint64

func searchTokenCallback(tls *libc.TLS, handle uintptr, flags int32, token uintptr, size, _, _ int32) int32 {
	v, ok := searchTokenStates.Load(handle)
	if !ok {
		return sqlite3.SQLITE_MISUSE
	}
	s := v.(*searchTokenState)
	if searchCanceled(tls, s.db, s.done) {
		return sqlite3.SQLITE_INTERRUPT
	}
	// unicode61 has no colocated synonyms. Fail closed if that ever changes.
	if size < 0 || flags&sqlite3.FTS5_TOKEN_COLOCATED != 0 {
		return sqlite3.SQLITE_ERROR
	}
	s.result.TokenBytes += int64(size)
	if size > sqlite3.FTS5_MAX_TOKEN_SIZE {
		size = sqlite3.FTS5_MAX_TOKEN_SIZE
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(token)), int(size))
	if s.query >= 0 {
		s.result.QueryTokens++
		p := &s.phrases[s.query]
		p.tokens = append(p.tokens, string(b))
		return sqlite3.SQLITE_OK
	}
	s.result.DocumentTokens++
	for i := range s.phrases {
		p := &s.phrases[i]
		if p.found || len(p.tokens) == 0 {
			continue
		}
		// Descending updates preserve every overlapping phrase start without
		// retaining document tokens. Only the final term is a byte prefix.
		for j := len(p.tokens) - 1; j >= 0; j-- {
			matches := false
			if p.active[j] {
				q := p.tokens[j]
				if j == len(p.tokens)-1 {
					matches = len(b) >= len(q) && bytes.Equal(b[:len(q)], []byte(q))
				} else {
					matches = len(b) == len(q) && bytes.Equal(b, []byte(q))
				}
			}
			p.active[j+1] = matches
		}
		p.found = p.active[len(p.tokens)]
	}
	return sqlite3.SQLITE_OK
}

type searchTokenizer struct {
	tls      *libc.TLS
	db       uintptr
	done     <-chan struct{}
	instance uintptr
	methods  sqlite3.Tfts5_tokenizer
}

func searchCanceled(tls *libc.TLS, db uintptr, done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return sqlite3.Xsqlite3_is_interrupted(tls, db) != 0
	}
}

func searchRC(rc int32) error {
	if rc != sqlite3.SQLITE_OK {
		return &Error{msg: fmt.Sprintf("local FTS5 tokenizer: %s (%d)", ErrorCodeString[int(rc)], rc), code: int(rc)}
	}
	return nil
}

// openSearchTokenizer obtains only the public FTS5 API. The SELECT calls fts5's
// pointer getter; it reads no table and creates no schema, work or result rows.
func openSearchTokenizer(tls *libc.TLS, db uintptr) (_ *searchTokenizer, err error) {
	// Native slots: API*, statement*, tokenizer context*, tokenizer instance*,
	// and two argument pointers. They never contain a Go pointer.
	slots := sqlite3.Xsqlite3_malloc64(tls, 6*uint64(ptrSize))
	if slots == 0 {
		return nil, searchRC(sqlite3.SQLITE_NOMEM)
	}
	defer sqlite3.Xsqlite3_free(tls, slots)
	libc.Xmemset(tls, slots, 0, 6*uint64(ptrSize))
	stringsNative := make([]uintptr, 0, 5)
	defer func() {
		for _, p := range stringsNative {
			sqlite3.Xsqlite3_free(tls, p)
		}
	}()
	cstring := func(s string) uintptr {
		p := sqlite3AllocCString(tls, s)
		stringsNative = append(stringsNative, p)
		return p
	}
	sql, pointerType, name, option, value := cstring("SELECT fts5(?1)"), cstring("fts5_api_ptr"), cstring("unicode61"), cstring("remove_diacritics"), cstring("2")
	if sql == 0 || pointerType == 0 || name == 0 || option == 0 || value == 0 {
		return nil, searchRC(sqlite3.SQLITE_NOMEM)
	}
	if err := searchRC(sqlite3.Xsqlite3_prepare_v2(tls, db, sql, -1, slots+ptrSize, 0)); err != nil {
		return nil, err
	}
	stmt := *(*uintptr)(unsafe.Pointer(slots + ptrSize))
	defer func() {
		if stmt != 0 {
			if e := searchRC(sqlite3.Xsqlite3_finalize(tls, stmt)); err == nil {
				err = e
			}
		}
	}()
	if err := searchRC(sqlite3.Xsqlite3_bind_pointer(tls, stmt, 1, slots, pointerType, 0)); err != nil {
		return nil, err
	}
	if rc := sqlite3.Xsqlite3_step(tls, stmt); rc != sqlite3.SQLITE_ROW {
		return nil, searchRC(rc)
	}
	apiPointer := *(*uintptr)(unsafe.Pointer(slots))
	if apiPointer == 0 {
		return nil, errors.New("local FTS5 API unavailable")
	}
	// Finalize before creating the tokenizer, so even a late SELECT failure
	// cannot strand an instance behind an error return.
	rc := sqlite3.Xsqlite3_finalize(tls, stmt)
	stmt = 0
	if err := searchRC(rc); err != nil {
		return nil, err
	}
	api := *(*sqlite3.Tfts5_api)(unsafe.Pointer(apiPointer))
	methods := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(sqlite3.Tfts5_tokenizer{})))
	if methods == 0 {
		return nil, searchRC(sqlite3.SQLITE_NOMEM)
	}
	defer sqlite3.Xsqlite3_free(tls, methods)
	find := api.FxFindTokenizer
	rc = (*(*func(*libc.TLS, uintptr, uintptr, uintptr, uintptr) int32)(unsafe.Pointer(&find)))(tls, apiPointer, name, slots+2*ptrSize, methods)
	if err := searchRC(rc); err != nil {
		return nil, err
	}
	t := &searchTokenizer{tls: tls, db: db, methods: *(*sqlite3.Tfts5_tokenizer)(unsafe.Pointer(methods))}
	*(*uintptr)(unsafe.Pointer(slots + 4*ptrSize)) = option
	*(*uintptr)(unsafe.Pointer(slots + 5*ptrSize)) = value
	create := t.methods.FxCreate
	rc = (*(*func(*libc.TLS, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&create)))(tls, *(*uintptr)(unsafe.Pointer(slots + 2*ptrSize)), slots+4*ptrSize, 2, slots+3*ptrSize)
	if err := searchRC(rc); err != nil {
		return nil, err
	}
	t.instance = *(*uintptr)(unsafe.Pointer(slots + 3*ptrSize))
	return t, nil
}

func (t *searchTokenizer) close() {
	f := t.methods.FxDelete
	(*(*func(*libc.TLS, uintptr))(unsafe.Pointer(&f)))(t.tls, t.instance)
}

func (t *searchTokenizer) tokenize(text string, flags int32, handle uintptr) error {
	if searchCanceled(t.tls, t.db, t.done) {
		return searchRC(sqlite3.SQLITE_INTERRUPT)
	}
	p := sqlite3AllocCString(t.tls, text)
	if p == 0 {
		return searchRC(sqlite3.SQLITE_NOMEM)
	}
	defer sqlite3.Xsqlite3_free(t.tls, p)
	f := t.methods.FxTokenize
	rc := (*(*func(*libc.TLS, uintptr, uintptr, int32, uintptr, int32, uintptr) int32)(unsafe.Pointer(&f)))(t.tls, t.instance, handle, flags, p, int32(len(text)), cFuncPointer(searchTokenCallback))
	if rc == sqlite3.SQLITE_OK && searchCanceled(t.tls, t.db, t.done) {
		rc = sqlite3.SQLITE_INTERRUPT
	}
	return searchRC(rc)
}

// MatchSearchDocument evaluates AND across raw phrases, each equivalent to the
// FTS5 expression "escaped phrase"*. A phrase must occur consecutively inside
// search OR kind; separate phrases may occur in different columns. No syntax
// operators are accepted or interpreted. Empty tokenized phrases are false,
// exactly as the pinned MATCH engine. An empty phrase list is also false (the
// caller handles an absent search leaf). CJK instr checks remain the caller's.
//
// The context is usable only synchronously inside a scalar callback. No native
// pointer, tokenizer instance, input buffer or query state survives this call.
func (ctx *FunctionContext) MatchSearchDocument(search, kind string, phrases []string) (SearchMatchResult, error) {
	r := SearchMatchResult{SourceBytes: int64(len(search)) + int64(len(kind))}
	for _, p := range phrases {
		r.QueryBytes += int64(len(p))
	}
	r.TransientBytes = SearchMatchTransientBound(r.SourceBytes, r.QueryBytes, len(phrases))
	if len(search) >= math.MaxInt32 || len(kind) >= math.MaxInt32 || r.QueryBytes > SearchMatchMaxQueryBytes || len(phrases) > SearchMatchMaxPhrases {
		return r, ErrSearchMatchCapacity
	}
	if ctx == nil || ctx.tls == nil || ctx.ctx == 0 {
		return r, errors.New("local FTS5 matcher requires a live scalar context")
	}
	if len(phrases) == 0 {
		return r, nil
	}
	db := sqlite3.Xsqlite3_context_db_handle(ctx.tls, ctx.ctx)
	var done <-chan struct{}
	if p, ok := callbackProgressStates.Load(db); ok {
		done = p.(*progressState).done
	}
	if searchCanceled(ctx.tls, db, done) {
		return r, searchRC(sqlite3.SQLITE_INTERRUPT)
	}
	t, err := openSearchTokenizer(ctx.tls, db)
	if err != nil {
		return r, err
	}
	defer t.close()
	t.done = done
	s := &searchTokenState{db: db, done: done, result: &r, phrases: make([]searchPhrase, len(phrases))}
	var handle uintptr
	for {
		handle = uintptr(searchTokenSequence.Add(1))
		if handle != 0 {
			if _, exists := searchTokenStates.LoadOrStore(handle, s); !exists {
				break
			}
		}
	}
	defer searchTokenStates.Delete(handle)
	for i, p := range phrases {
		s.query = i
		if err := t.tokenize(p, sqlite3.FTS5_TOKENIZE_QUERY|sqlite3.FTS5_TOKENIZE_PREFIX, handle); err != nil {
			return r, err
		}
		s.phrases[i].active = make([]bool, len(s.phrases[i].tokens)+1)
	}
	s.query = -1
	for _, column := range [...]string{search, kind} {
		for i := range s.phrases {
			clear(s.phrases[i].active)
			s.phrases[i].active[0] = true
		}
		if err := t.tokenize(column, sqlite3.FTS5_TOKENIZE_DOCUMENT, handle); err != nil {
			return r, err
		}
	}
	r.Match = true
	for _, p := range s.phrases {
		r.Match = r.Match && p.found
	}
	return r, nil
}
