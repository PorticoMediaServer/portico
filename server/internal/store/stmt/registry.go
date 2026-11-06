// Package stmt freezes the new store's static SQL and validates bindings before
// execution. It neither opens databases nor owns writable transactions. Owners
// compose registries at construction from compiled, closed statement variants;
// request data is supplied only through Bind.
package stmt

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/internal/qualification"
)

type ID string
type Kind string

const (
	Read        Kind = "read"
	Write       Kind = "write"
	Maintenance Kind = "maintenance"
	DDL         Kind = "ddl"
)

type Type string

const (
	Integer Type = "integer"
	Real    Type = "real"
	Text    Type = "text"
	Blob    Type = "blob"
	Boolean Type = "boolean"
)

// Param uses native driver values: int64, float64, string, []byte and bool.
// NULL is nil, distinct from an empty string or a non-nil empty blob.
type Param struct {
	Name     string `json:"name"`
	Type     Type   `json:"type"`
	Nullable bool   `json:"nullable"`
}

// Cohort identifies a reviewed binding population, not a representative literal
// substituted into SQL. Owners supply first/continuation, empty and fanout cases.
type Cohort struct {
	Name     string `json:"name"`
	Bindings []any  `json:"bindings"`
}

type Waiver struct {
	StatementID ID     `json:"statementId"`
	Stage       string `json:"stage"` // only "eqp"; grammar errors are never waivable
	Form        string `json:"form"`
	Match       string `json:"match"` // entire exact EQP detail
	Reason      string `json:"reason"`
}

// IndexColumn pins a declared key's ordered native columns, not auxiliary
// rowid/primary-key payload fields reported by index_xinfo.
type IndexColumn struct {
	Name      string `json:"name"`
	Collation string `json:"collation"`
	Desc      bool   `json:"desc"`
}

// AutoIndexBinding is a declaration for Capture to verify against live MAIN.
// It grants no capability by itself. Origin is exactly "pk" or "u". LIMIT
// parameters in this subset must be non-null integers and Bind rejects <= 0.
type AutoIndexBinding struct {
	Table   string        `json:"table"`
	Name    string        `json:"name"`
	Origin  string        `json:"origin"`
	Columns []IndexColumn `json:"columns"`
}

// Plan is consumed by the plan harness; a SQL hash alone is not qualification.
type Plan struct {
	NativeRead *FTSRead `json:"nativeRead,omitempty"`
	Hot        bool     `json:"hot"`
	Indexes    []string `json:"indexes"`
	// PrimaryKeys names EQP table/alias targets for native PK paths that have no named INDEXED BY path.
	PrimaryKeys []string `json:"primaryKeys"`
	// Seeks pins the actual prefix/range constraints, including a separate tie seek.
	Seeks []string `json:"seeks"`
	// AllowedScans is retained as legacy manifest metadata; it grants no waiver.
	// An exceptional scan now requires a statement-specific EQP waiver.
	AllowedScans []string           `json:"allowedScans"`
	Waivers      []Waiver           `json:"waivers,omitempty"`
	AutoIndexes  []AutoIndexBinding `json:"autoIndexes,omitempty"`
	// NativeInsert selects one of three fixed external-content FTS5 INSERTs.
	// It is audit intent only; Capture must mint the ordinary opaque receipt.
	NativeInsert FTSInsertSlot `json:"nativeInsert,omitempty"`
	// NativeRetained selects exactly five separately closed own-head/retained
	// operations. It grants no generic virtual-table or hidden-control access.
	NativeRetained FTSRetainedSlot `json:"nativeRetained,omitempty"`
}
type Definition struct {
	ID        ID       `json:"id"`
	SQL       string   `json:"sql"`
	Owner     string   `json:"owner"`
	CallSites []string `json:"callSites"`
	Kind      Kind     `json:"kind"`
	Params    []Param  `json:"params"`
	Cohorts   []Cohort `json:"cohorts"`
	Plan      Plan     `json:"plan"`
}
type Entry struct {
	Definition
	SQLSHA256      string `json:"sqlSha256"`
	qualified      bool
	positiveLimits []int // derived from exact frozen SQL; never caller metadata
}

// Receipt is an opaque Capture-minted capability. Its implementation and mint
// are internal; nil/zero values and JSON data cannot authorize registration.
type Receipt = qualification.Receipt

var ErrUnqualified = errors.New("stmt: executable registration requires a matching Capture receipt")

// Registry is immutable after New; there is no global mutable registration hook.
type Registry struct{ entries map[ID]*Entry }
type Statement struct{ entry *Entry }
type Bound struct {
	entry *Entry
	args  []any
}

// Describe validates and freezes an INERT catalogue for Capture/manifest review.
// Lookup, binding and execution remain unavailable until New verifies receipts.
func Describe(definitions []Definition) (*Registry, error) {
	r := &Registry{entries: make(map[ID]*Entry, len(definitions))}
	for _, input := range definitions {
		d := cloneDefinition(input)
		if !utf8.ValidString(d.SQL) {
			return nil, fmt.Errorf("stmt %q: SQL must be valid UTF-8", d.ID)
		}
		if d.ID == "" || d.Owner == "" || len(d.CallSites) == 0 {
			return nil, fmt.Errorf("stmt %q: ID, owner and call sites required", d.ID)
		}
		if _, ok := r.entries[d.ID]; ok {
			return nil, fmt.Errorf("stmt %q: duplicate ID", d.ID)
		}
		if err := validateFTSReadDefinition(d); err != nil {
			return nil, fmt.Errorf("stmt %q: %w", d.ID, err)
		}
		switch d.Kind {
		case Read, Write, Maintenance, DDL:
		default:
			return nil, fmt.Errorf("stmt %q: invalid kind", d.ID)
		}
		seenWaivers := map[string]bool{}
		seenBindings := map[string]bool{}
		for _, binding := range d.Plan.AutoIndexes {
			key := binding.Table + "\x00" + binding.Name
			if binding.Table == "" || !strings.HasPrefix(binding.Name, "sqlite_autoindex_") ||
				(binding.Origin != "pk" && binding.Origin != "u") || len(binding.Columns) == 0 ||
				!slices.Contains(d.Plan.Indexes, binding.Name) || seenBindings[key] {
				return nil, fmt.Errorf("stmt %q: invalid/duplicate autoindex binding", d.ID)
			}
			seenBindings[key] = true
			for _, col := range binding.Columns {
				if col.Name == "" || col.Collation == "" {
					return nil, fmt.Errorf("stmt %q: autoindex columns require names and collations", d.ID)
				}
			}
		}
		for _, w := range d.Plan.Waivers {
			key := w.Stage + "\x00" + w.Form + "\x00" + w.Match
			if w.StatementID != d.ID || strings.TrimSpace(w.Form) == "" || strings.TrimSpace(w.Match) == "" || strings.TrimSpace(w.Reason) == "" || seenWaivers[key] {
				return nil, fmt.Errorf("stmt %q: invalid/duplicate statement waiver", d.ID)
			}
			if w.Stage != "eqp" {
				return nil, fmt.Errorf("stmt %q: only exact EQP waivers are permitted", d.ID)
			}
			seenWaivers[key] = true
		}
		count, limits, err := parameterLayout(d.SQL)
		if err != nil {
			return nil, fmt.Errorf("stmt %q: %w", d.ID, err)
		}
		if count != len(d.Params) {
			return nil, fmt.Errorf("stmt %q: %d placeholders, %d parameters", d.ID, count, len(d.Params))
		}
		if err := validateFTSInsertDefinition(d); err != nil {
			return nil, fmt.Errorf("stmt %q: %w", d.ID, err)
		}
		if err := validateFTSRetainedDefinition(d); err != nil {
			return nil, fmt.Errorf("stmt %q: %w", d.ID, err)
		}
		if len(d.Plan.AutoIndexes) == 0 {
			limits = nil
		}
		for _, position := range limits {
			if d.Params[position].Type != Integer || d.Params[position].Nullable {
				return nil, fmt.Errorf("stmt %q: native-key LIMIT requires a non-null integer parameter", d.ID)
			}
		}
		names := map[string]bool{}
		for _, p := range d.Params {
			if p.Name == "" || names[p.Name] {
				return nil, fmt.Errorf("stmt %q: empty or duplicate parameter name", d.ID)
			}
			names[p.Name] = true
			switch p.Type {
			case Integer, Real, Text, Blob, Boolean:
			default:
				return nil, fmt.Errorf("stmt %q: invalid parameter type", d.ID)
			}
		}
		for _, site := range d.CallSites {
			if site == "" {
				return nil, fmt.Errorf("stmt %q: empty call site", d.ID)
			}
		}
		if len(d.Cohorts) == 0 {
			return nil, fmt.Errorf("stmt %q: at least one named cohort required", d.ID)
		}
		cohorts := map[string]bool{}
		for _, c := range d.Cohorts {
			if c.Name == "" || cohorts[c.Name] {
				return nil, fmt.Errorf("stmt %q: empty or duplicate cohort", d.ID)
			}
			cohorts[c.Name] = true
			if err := validateFTSReadBindings(d.Plan.NativeRead, c.Bindings); err != nil {
				return nil, fmt.Errorf("stmt %q cohort %q: %w", d.ID, c.Name, err)
			}
			if err := validate(d.Params, c.Bindings); err != nil {
				return nil, fmt.Errorf("stmt %q cohort %q: %w", d.ID, c.Name, err)
			}
			if err := validatePositiveLimits(limits, c.Bindings); err != nil {
				return nil, fmt.Errorf("stmt %q cohort %q: %w", d.ID, c.Name, err)
			}
			if err := validateFTSInsertBindings(d.Plan.NativeInsert, c.Bindings); err != nil {
				return nil, fmt.Errorf("stmt %q cohort %q: %w", d.ID, c.Name, err)
			}
			if err := validateFTSRetainedBindings(d.Plan.NativeRetained, c.Bindings); err != nil {
				return nil, fmt.Errorf("stmt %q cohort %q: %w", d.ID, c.Name, err)
			}
		}
		hash := sha256.Sum256([]byte(d.SQL))
		r.entries[d.ID] = &Entry{Definition: d, SQLSHA256: hex.EncodeToString(hash[:]), positiveLimits: limits}
	}
	return r, nil
}

// New creates an executable registry only from exact Capture-qualified
// definitions. Hot is a declared contract, never a caller-issued capability.
// Multiple receipts permit finite composition of separately captured variants.
func New(definitions []Definition, receipts ...Receipt) (*Registry, error) {
	for _, d := range definitions {
		if !utf8.ValidString(d.SQL) {
			return nil, fmt.Errorf("stmt %q: SQL must be valid UTF-8", d.ID)
		}
	}
	r, err := Describe(definitions)
	if err != nil {
		return nil, err
	}
	if len(r.entries) == 0 {
		return nil, ErrUnqualified
	}
	for id, e := range r.entries {
		data, err := qualification.Encode(e.Definition)
		if err != nil {
			return nil, err
		}
		allowed := false
		for _, proof := range receipts {
			if qualification.Allows(proof, string(id), data) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("stmt %q: %w", id, ErrUnqualified)
		}
		e.qualified = true
	}
	return r, nil
}
func (r *Registry) Lookup(id ID) (Statement, error) {
	if r != nil {
		if e, ok := r.entries[id]; ok {
			if !e.qualified {
				return Statement{}, fmt.Errorf("stmt %q: %w", id, ErrUnqualified)
			}
			return Statement{entry: e}, nil
		}
	}
	return Statement{}, fmt.Errorf("stmt %q: unregistered ID", id)
}

// Manifest returns independent copies in stable ID order for review and hashing.
func (r *Registry) Manifest() []Entry {
	result := []Entry{}
	if r == nil {
		return result
	}
	for _, e := range r.entries {
		result = append(result, Entry{Definition: cloneDefinition(e.Definition), SQLSHA256: e.SQLSHA256})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (s Statement) Definition() (Definition, error) {
	if s.entry == nil || !s.entry.qualified {
		return Definition{}, fmt.Errorf("stmt: unregistered statement")
	}
	return cloneDefinition(s.entry.Definition), nil
}
func (s Statement) Bind(args ...any) (Bound, error) {
	if s.entry == nil || !s.entry.qualified {
		return Bound{}, fmt.Errorf("stmt: unregistered statement")
	}
	if err := validateFTSReadBindings(s.entry.Plan.NativeRead, args); err != nil {
		return Bound{}, fmt.Errorf("stmt %q: %w", s.entry.ID, err)
	}
	if err := validate(s.entry.Params, args); err != nil {
		return Bound{}, fmt.Errorf("stmt %q: %w", s.entry.ID, err)
	}
	if err := validatePositiveLimits(s.entry.positiveLimits, args); err != nil {
		return Bound{}, fmt.Errorf("stmt %q: %w", s.entry.ID, err)
	}
	if err := validateFTSInsertBindings(s.entry.Plan.NativeInsert, args); err != nil {
		return Bound{}, fmt.Errorf("stmt %q: %w", s.entry.ID, err)
	}
	if err := validateFTSRetainedBindings(s.entry.Plan.NativeRetained, args); err != nil {
		return Bound{}, fmt.Errorf("stmt %q: %w", s.entry.ID, err)
	}
	frozen := cloneArgs(args)
	if s.entry.Plan.NativeRead != nil {
		// The bounded MATCH query owns only its charged bytes, even when the
		// caller supplied a small substring of a larger backing allocation.
		frozen[0] = strings.Clone(frozen[0].(string))
	}
	if s.entry.Plan.NativeInsert != "" {
		// A bounded slice of a large caller backing string must not retain the
		// larger allocation for this new native capability's bound lifetime.
		frozen[1] = strings.Clone(frozen[1].(string))
	}
	if s.entry.Plan.NativeRetained != "" {
		for n, value := range frozen {
			if text, ok := value.(string); ok {
				frozen[n] = strings.Clone(text)
			}
		}
	}
	return Bound{entry: s.entry, args: frozen}, nil
}

func validatePositiveLimits(positions []int, args []any) error {
	for _, position := range positions {
		if position < 0 || position >= len(args) {
			return fmt.Errorf("LIMIT parameter position is invalid")
		}
		value, ok := args[position].(int64)
		if !ok || value <= 0 {
			return fmt.Errorf("LIMIT parameter must be a strictly positive int64")
		}
	}
	return nil
}

// Execer and Queryer are implemented by sql.Conn and sql.Tx. Possession of a
// Bound does not confer access to either: writer/governor retain that custody.
type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (b Bound) ExecContext(ctx context.Context, executor Execer) (sql.Result, error) {
	if b.entry == nil || !b.entry.qualified {
		return nil, fmt.Errorf("stmt: unbound statement")
	}
	if b.entry.Kind == Read {
		return nil, fmt.Errorf("stmt %q: read requires QueryContext", b.entry.ID)
	}
	return executor.ExecContext(ctx, b.entry.SQL, cloneArgs(b.args)...)
}

// QueryContext also supports accepting writes with RETURNING; Kind is manifest
// metadata, not a read authorization boundary. The owner supplies the connection.
func (b Bound) QueryContext(ctx context.Context, executor Queryer) (*sql.Rows, error) {
	if b.entry == nil || !b.entry.qualified {
		return nil, fmt.Errorf("stmt: unbound statement")
	}
	return executor.QueryContext(ctx, b.entry.SQL, cloneArgs(b.args)...)
}
func validate(params []Param, args []any) error {
	if len(params) != len(args) {
		return fmt.Errorf("expected %d bindings, got %d", len(params), len(args))
	}
	for i, p := range params {
		v := args[i]
		if v == nil {
			if p.Nullable {
				continue
			}
			return fmt.Errorf("parameter %q: NULL forbidden", p.Name)
		}
		valid := false
		switch p.Type {
		case Integer:
			_, valid = v.(int64)
		case Text:
			_, valid = v.(string)
		case Blob:
			var bytes []byte
			bytes, valid = v.([]byte)
			valid = valid && bytes != nil
		case Boolean:
			_, valid = v.(bool)
		case Real:
			var number float64
			number, valid = v.(float64)
			valid = valid && !math.IsNaN(number) && !math.IsInf(number, 0)
		}
		if !valid {
			return fmt.Errorf("parameter %q: expected %s (nullable=%t)", p.Name, p.Type, p.Nullable)
		}
	}
	return nil
}
func cloneArgs(args []any) []any {
	result := slices.Clone(args)
	if result == nil {
		result = []any{}
	}
	for i, v := range result {
		if bytes, ok := v.([]byte); ok {
			result[i] = slices.Clone(bytes)
		}
	}
	return result
}
func cloneDefinition(d Definition) Definition {
	if d.Plan.NativeRead != nil {
		copy := *d.Plan.NativeRead
		d.Plan.NativeRead = &copy
	}
	d.CallSites = slices.Clone(d.CallSites)
	d.Params = slices.Clone(d.Params)
	if d.Params == nil {
		d.Params = []Param{}
	}
	d.Plan.Indexes = slices.Clone(d.Plan.Indexes)
	d.Plan.PrimaryKeys = slices.Clone(d.Plan.PrimaryKeys)
	d.Plan.Seeks = slices.Clone(d.Plan.Seeks)
	d.Plan.AllowedScans = slices.Clone(d.Plan.AllowedScans)
	d.Plan.Waivers = slices.Clone(d.Plan.Waivers)
	d.Plan.AutoIndexes = slices.Clone(d.Plan.AutoIndexes)
	for i := range d.Plan.AutoIndexes {
		d.Plan.AutoIndexes[i].Columns = slices.Clone(d.Plan.AutoIndexes[i].Columns)
	}
	d.Cohorts = slices.Clone(d.Cohorts)
	for i := range d.Cohorts {
		d.Cohorts[i].Bindings = cloneArgs(d.Cohorts[i].Bindings)
	}
	return d
}

// RowQueryer lets the private owner use sql.Conn/sql.Tx single-row statements.
type RowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Row defers an invalid Bound error to Scan, matching database/sql's row contract.
// Scan closes the underlying result even when conversion or context fails.
type Row struct {
	row *sql.Row
	err error
}

func (b Bound) QueryRowContext(ctx context.Context, executor RowQueryer) Row {
	if b.entry == nil || !b.entry.qualified {
		return Row{err: fmt.Errorf("stmt: unbound statement")}
	}
	return Row{row: executor.QueryRowContext(ctx, b.entry.SQL, cloneArgs(b.args)...)}
}
func (r Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.row == nil {
		return fmt.Errorf("stmt: unbound row")
	}
	return r.row.Scan(dest...)
}
