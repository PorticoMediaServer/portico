package catalog

// This foundation is explicitly composed. Its source adapters capture actual
// family fences; legacy readiness names and mutable visibility classes alone
// do not prove an exact SIMPLE image. Installing DDL cannot enable a reader:
// the composition root must own background production and live authority.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

var (
	ErrFacetBaseUnavailable = errors.New("exact SIMPLE facet base is not available")
	ErrFacetBaseInput       = errors.New("invalid SIMPLE facet base input")
	ErrFacetBaseCapacity    = errors.New("maintained SIMPLE facet base capacity exceeded")
)

// FacetBasePolicy is a typed, unambiguous content equivalence key. Revisions,
// profile IDs and admission belong to live authority, not shared membership.
// Empty labels and whitespace are significant: itemRestrictionClause lowercases
// its parameters but does NOT trim them. Keep nil-age distinct from age zero.
// BlockRecordings is included conservatively even though library admission is
// responsible for recording lists. Never use RestrictionFence/bucket strings as
// an equality substitute for this document.
func FacetBasePolicy(r identity.ContentRestrictions) string {
	labels := func(in []string) []string {
		out := make([]string, len(in))
		for i, v := range in {
			out[i] = strings.ToLower(v)
		}
		sort.Strings(out)
		return uniqueFacetLabels(out)
	}
	p := struct {
		MaximumAge         *int     `json:"maximumAge"`
		BlockUnrated       bool     `json:"blockUnrated"`
		BlockedLabels      []string `json:"blockedLabels"`
		MemberDeniedLabels []string `json:"memberDeniedLabels"`
		BlockRecordings    bool     `json:"blockRecordings"`
	}{r.MaximumAge, r.BlockUnrated, labels(r.BlockedLabels), labels(r.MemberDeniedLabels), r.BlockRecordings}
	b, _ := json.Marshal(p)
	return string(b)
}

func uniqueFacetLabels(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}

// FacetBaseScope includes the internal library lifetime, not only its public
// spelling. Source records the exact branch oracle (including show/member and
// restricted-decade rules). Policy must come from FacetBasePolicy.
type FacetBaseScope struct {
	LibraryID int64
	Library   string
	Field     string
	Policy    string
	Source    string
}

func (s FacetBaseScope) valid() bool {
	if s.LibraryID <= 0 || s.Library == "" || s.Policy == "" || s.Source == "" {
		return false
	}
	switch s.Field {
	case "genre", "year", "decade", "studio", "collection", "contentRating", "audioLanguage", "network", "tag", "label", "series", "resolution":
		return true
	}
	return false
}

// Witness must describe ALL accepted inputs: source/applied and serving
// generations, classification, source ownership/tombstone frontier, algorithm
// and policy semantics. It is an opaque exact equality document, not a hash or
// raw library revision. Authority is a separate live revocation epoch.
type FacetBaseSnapshot struct {
	ID        int64          `json:"id"`
	Scope     FacetBaseScope `json:"scope"`
	Witness   string         `json:"witness"`
	Authority string         `json:"authority"`
	Total     int64          `json:"total"`
}

// FacetBaseFact is an absolute accepted count, not a query result. Zero removes
// a value in an incremental staged build. Public Value bytes are never folded.
type FacetBaseFact struct {
	Value, Label string
	Count        int64
}
type FacetBaseBatch struct {
	Facts  []FacetBaseFact
	Cursor string
	Done   bool
}

// FacetBaseProducer is the exact source contract. Next must consume
// an immutable accepted image or a lossless durable BASE delta journal, using
// existing members/counts/show witnesses. Cursor is durable base work only.
// It must preserve the census's distinct unrestricted-show, restricted-show,
// member-source-label/tag and published restricted-decade semantics. In
// particular ambiguous rating/label buckets and trimmed visibility classes
// cannot certify counts. No foreground aggregate or accepting-write fanout.
//
// Next is one small writer quantum (<=8 facts, ordinary <=256KiB); one legal
// oversized scalar may take O(B) work. It must return complete absolute facts
// for bootstrap, or explicitly copy the prior immutable base in bounded chunks
// before applying deltas. It may not omit unchanged values in an empty build.
// ValidatePublication runs in the SAME writer transaction as pointer activation
// and must prove the whole image complete and coherent at snapshot.Witness,
// including all in-flight producer work/failures/serving/policy fences. It must
// be constant work; CheckReadiness returning nil is insufficient.
type FacetBaseProducer interface {
	Next(context.Context, *sql.Tx, FacetBaseSnapshot, string) (FacetBaseBatch, error)
	ValidatePublication(context.Context, *sql.Tx, FacetBaseSnapshot) error
}

// FacetBases owns no goroutines and is never constructed lazily by a request.
// Every mutation uses the caller's bounded background writer transaction.
// MaxBytes bounds conservatively charged base data, not SQLite physical pages;
// the owner must also charge/measure shared indexes and reclaim unused texts.
type FacetBases struct {
	DB       *sql.DB
	Producer FacetBaseProducer
	MaxBytes int64
}

// Begin creates durable BASE maintenance, never a query job. The caller already
// captured a producer-supported coherent witness. A nil producer fails closed.
func (b *FacetBases) Begin(ctx context.Context, tx *sql.Tx, scope FacetBaseScope, witness, authority string) (int64, error) {
	if b == nil || b.Producer == nil {
		return 0, ErrFacetBaseUnavailable
	}
	if tx == nil || !scope.valid() || witness == "" || authority == "" || b.MaxBytes <= 0 {
		return 0, ErrFacetBaseInput
	}
	var class int64
	err := tx.QueryRowContext(ctx, `INSERT INTO catalog_facet_base_classes(library_id,library,field,policy,source) VALUES(?,?,?,?,?)
	 ON CONFLICT(library_id,field,policy,source) DO UPDATE SET library=library RETURNING id`, scope.LibraryID, scope.Library, scope.Field, scope.Policy, scope.Source).Scan(&class)
	if err != nil {
		return 0, err
	}
	var storedLibrary string
	if err = tx.QueryRowContext(ctx, `SELECT library FROM catalog_facet_base_classes WHERE id=?`, class).Scan(&storedLibrary); err != nil {
		return 0, err
	}
	if storedLibrary != scope.Library {
		return 0, ErrFacetBaseInput
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT b.id FROM catalog_facet_bases b WHERE b.class_id=? AND b.witness=? AND b.authority=? AND (b.complete=1 OR EXISTS(SELECT 1 FROM catalog_facet_base_work w WHERE w.base_id=b.id)) ORDER BY b.id DESC LIMIT 1`, class, witness, authority).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO catalog_facet_bases(class_id,witness,authority) VALUES(?,?,?) RETURNING id`, class, witness, authority).Scan(&id)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_facet_base_work(base_id,cursor) VALUES(?,'')`, id)
	if err != nil {
		return 0, err
	}
	var used int64
	if err = tx.QueryRowContext(ctx, `SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`).Scan(&used); err != nil {
		return 0, err
	}
	if used > b.MaxBytes {
		return 0, ErrFacetBaseCapacity
	}
	return id, nil
}

// Step applies at most one producer quantum and activates in constant work.
// On error the caller MUST roll back its transaction (including producer work).
// There is no independently committed checkpoint or partially visible output.
func (b *FacetBases) Step(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	if b == nil || b.Producer == nil {
		return false, ErrFacetBaseUnavailable
	}
	if tx == nil || b.MaxBytes <= 0 {
		return false, ErrFacetBaseInput
	}
	snap, err := facetBaseSnapshot(ctx, tx, id, false)
	if err != nil {
		if _, completeErr := facetBaseSnapshot(ctx, tx, id, true); completeErr == nil {
			return true, nil
		}
		return false, err
	}
	var cursor string
	if err = tx.QueryRowContext(ctx, `SELECT cursor FROM catalog_facet_base_work WHERE base_id=?`, id).Scan(&cursor); err != nil {
		return false, err
	}
	batch, err := b.Producer.Next(ctx, tx, snap, cursor)
	if err != nil {
		return false, err
	}
	if len(batch.Facts) > 8 || (!batch.Done && batch.Cursor == cursor) {
		return false, ErrFacetBaseInput
	}
	var bytes int64
	for _, f := range batch.Facts {
		bytes += int64(len(f.Value)) + int64(len(f.Label))
	}
	if len(batch.Facts) > 1 && bytes > 256<<10 {
		return false, ErrFacetBaseInput
	}
	for _, f := range batch.Facts {
		if err = b.put(ctx, tx, snap, f); err != nil {
			return false, err
		}
	}
	if !batch.Done {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_facet_base_work SET cursor=? WHERE base_id=?`, batch.Cursor, id)
		if err != nil {
			return false, err
		}
		var used int64
		if err = tx.QueryRowContext(ctx, `SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`).Scan(&used); err != nil {
			return false, err
		}
		if used > b.MaxBytes {
			return false, ErrFacetBaseCapacity
		}
		return false, nil
	}
	snap, err = facetBaseSnapshot(ctx, tx, id, false)
	if err != nil {
		return false, err
	}
	if err = b.Producer.ValidatePublication(ctx, tx, snap); err != nil {
		return false, err
	}
	// Additional maintained source facts (for example bounded genre artwork)
	// share this ledger even when the final producer quantum emits no values.
	var used int64
	if err = tx.QueryRowContext(ctx, `SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`).Scan(&used); err != nil {
		return false, err
	}
	if used > b.MaxBytes {
		return false, ErrFacetBaseCapacity
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_facet_bases SET complete=1 WHERE id=? AND complete=0`, id); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_facet_base_classes SET active_base=? WHERE id=(SELECT class_id FROM catalog_facet_bases WHERE id=?)`, id, id); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_base_work WHERE base_id=?`, id)
	return err == nil, err
}

func (b *FacetBases) put(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot, f FacetBaseFact) error {
	if f.Value == "" || f.Count < 0 {
		return ErrFacetBaseInput
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_facet_base_rows WHERE base_id=? AND value_id=(SELECT id FROM catalog_facet_base_values WHERE library_id=? AND field=? AND value=?)`, s.ID, s.Scope.LibraryID, s.Scope.Field, f.Value); err != nil {
		return err
	}
	if f.Count == 0 {
		return nil
	}
	value, err := ensureFacetBaseValueID(ctx, tx, s.Scope, f.Value)
	if err != nil {
		return err
	}
	var text int64
	// Immutable text variants preserve old collection names/bases across renames.
	if err := tx.QueryRowContext(ctx, `INSERT INTO catalog_facet_base_texts(value_id,label,folded) VALUES(?,?,?) ON CONFLICT(value_id,label) DO UPDATE SET label=excluded.label RETURNING id`, value, f.Label, sorttext.Fold(f.Label)).Scan(&text); err != nil {
		return err
	}
	// Count-order indexes necessarily repeat label/value order bytes. Account
	// those copies explicitly; this is a conservative logical admission charge.
	charge := int64(160) + 2*int64(len(f.Value)) + 2*int64(len(f.Label))
	var used int64
	if err := tx.QueryRowContext(ctx, `SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`).Scan(&used); err != nil {
		return err
	}
	if charge > b.MaxBytes || used > b.MaxBytes-charge {
		return ErrFacetBaseCapacity
	}
	order := asciiLower(f.Label)
	if strings.HasPrefix(s.Scope.Source, SearchVideoGenreSource+":") {
		order = sorttext.Key(f.Label, "", "")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_facet_base_rows(base_id,value_id,text_id,negative_count,label_order,value_order,charged_bytes) VALUES(?,?,?,?,?,?,?)`, s.ID, value, text, -f.Count, order, f.Value, charge)
	return err
}

// ensureFacetBaseValueID preserves the exact shared (library,field,value)
// identity. It is also used by current-show staging; text remains shared by put.
func ensureFacetBaseValueID(ctx context.Context, tx *sql.Tx, scope FacetBaseScope, value string) (int64, error) {
	if value == "" {
		return 0, ErrFacetBaseInput
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_facet_base_values WHERE library_id=? AND field=? AND value=?`, scope.LibraryID, scope.Field, value).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO catalog_facet_base_values(library_id,field,value) VALUES(?,?,?) ON CONFLICT(library_id,field,value) DO UPDATE SET value=excluded.value RETURNING id`, scope.LibraryID, scope.Field, value).Scan(&id)
	return id, err
}

// RetireStep reclaims one non-active base in bounded quanta. Dropping COMPLETE
// inputs makes its in-memory preparations stale; it must never leave a partial
// COMPLETE basis readable. Clear complete first in the SAME transaction.
func (b *FacetBases) RetireStep(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_facet_base_classes WHERE active_base=?)`, id).Scan(&active); err != nil {
		return false, err
	}
	if active {
		return false, ErrFacetBaseInput
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_facet_bases SET complete=0 WHERE id=?`, id); err != nil {
		return false, err
	}
	// Invalidate staged work at retirement's first quantum. A repeated demand
	// must not resume a partially reclaimed base from its former source cursor.
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_facet_base_work WHERE base_id=?`, id); err != nil {
		return false, err
	}
	var source string
	if err := tx.QueryRowContext(ctx, `SELECT c.source FROM catalog_facet_bases b JOIN catalog_facet_base_classes c ON c.id=b.class_id WHERE b.id=?`, id).Scan(&source); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if strings.HasPrefix(source, SearchVideoGenreSource+":") {
		done, err := retireSearchGenreBaseStep(ctx, tx, id)
		if err != nil || !done {
			return false, err
		}
	}
	if source == RestrictedShowFacetBaseSource {
		done, err := retireRestrictedShowSeenStep(ctx, tx, id)
		if err != nil || !done {
			return false, err
		}
	}
	r, err := tx.ExecContext(ctx, `DELETE FROM catalog_facet_base_rows WHERE base_id=? AND value_id IN(SELECT value_id FROM catalog_facet_base_rows WHERE base_id=? LIMIT 8)`, id, id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_base_work WHERE base_id=?`, id); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_bases WHERE id=?`, id)
	return err == nil, err
}

type facetBaseQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func facetBaseSnapshot(ctx context.Context, q facetBaseQuerier, id int64, complete bool) (FacetBaseSnapshot, error) {
	var s FacetBaseSnapshot
	condition := "b.complete=0"
	if complete {
		condition = "b.complete=1"
	}
	err := q.QueryRowContext(ctx, `SELECT b.id,c.library_id,c.library,c.field,c.policy,c.source,b.witness,b.authority,b.total
 FROM catalog_facet_bases b JOIN catalog_facet_base_classes c ON c.id=b.class_id WHERE b.id=? AND `+condition, id).Scan(&s.ID, &s.Scope.LibraryID, &s.Scope.Library, &s.Scope.Field, &s.Scope.Policy, &s.Scope.Source, &s.Witness, &s.Authority, &s.Total)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrFacetBaseUnavailable
	}
	return s, err
}

// CollectTextStep examines at most eight raw identities. Eligibility follows
// LIMIT in Go, so referenced texts cannot make the cleanup query scan a whole
// directory looking for eight orphans. The owner separately walks value IDs.
func (b *FacetBases) CollectTextStep(ctx context.Context, tx *sql.Tx, after int64) (next int64, done bool, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM catalog_facet_base_texts WHERE id>? ORDER BY id LIMIT 8`, after)
	if err != nil {
		return after, false, err
	}
	ids := make([]int64, 0, 8)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return after, false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return after, false, err
	}
	next = after
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_base_texts WHERE id=? AND NOT EXISTS(SELECT 1 FROM catalog_facet_base_rows WHERE text_id=?)`, id, id); err != nil {
			return next, false, err
		}
		next = id
	}
	return next, len(ids) < 8, nil
}
func (b *FacetBases) CollectValueStep(ctx context.Context, tx *sql.Tx, after int64) (next int64, done bool, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM catalog_facet_base_values WHERE id>? ORDER BY id LIMIT 8`, after)
	if err != nil {
		return after, false, err
	}
	ids := make([]int64, 0, 8)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return after, false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return after, false, err
	}
	next = after
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_facet_base_values WHERE id=? AND NOT EXISTS(SELECT 1 FROM catalog_facet_base_texts WHERE value_id=?) AND NOT EXISTS(SELECT 1 FROM catalog_facet_base_rows WHERE value_id=?)`, id, id, id); err != nil {
			return next, false, err
		}
		next = id
	}
	return next, len(ids) < 8, nil
}
