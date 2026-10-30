package catalog

import (
	"context"
	"database/sql"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// FacetBaseItemPolicyCursor is durable BASE reducer progress for ONE candidate
// item, not a persisted class membership graph. The producer must bind it to a
// coherent source witness and reset it when that witness changes. Unrestricted
// shows and restricted-decade classes have different oracles; this is only the
// exact ItemRestrictionSQL subpredicate used by ordinary/member-visible facts.
// It deliberately cannot bless a whole class or make readiness succeed.
type FacetBaseItemPolicyCursor struct {
	Item        int64 `json:"item"`
	AfterTerm   int64 `json:"afterTerm"`
	Started     bool  `json:"started"`
	KnownRating bool  `json:"knownRating"`
	Denied      bool  `json:"denied"`
}

// StepFacetBaseItemPolicy evaluates at most eight RAW attribute edges, under the
// caller's writer snapshot. Filtering happens after the indexed raw LIMIT. It
// reproduces itemRestrictionClause exactly, without ambiguous bucket decoding,
// an unbounded EXISTS over all a holder's labels, or a new class-holder graph.
// An oversized legal scalar is one transient O(B) exception. The caller must
// roll back/reset if its source/authority stamp changes before publication.
// Done=false never means visible. Once Done=true, visible is an exact verdict
// for this witness. Policy revisions/admission remain the caller's live check.
func StepFacetBaseItemPolicy(ctx context.Context, tx *sql.Tx, r identity.ContentRestrictions, c *FacetBaseItemPolicyCursor) (done, visible bool, err error) {
	if tx == nil || c == nil || c.Item <= 0 {
		return false, false, ErrFacetBaseInput
	}
	if !r.Active() {
		return true, true, nil
	}
	if !c.Started {
		var exists bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities ci JOIN catalog_libraries l ON l.id=ci.library_id WHERE ci.id=? AND l.retired=0)`, c.Item).Scan(&exists)
		if err != nil {
			return false, false, err
		}
		c.Started = true
		c.Denied = !exists
	}
	if c.Denied {
		return true, false, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT ae.term_id,t.field_id,t.value_key,lower(ae.source_value),ra.minimum_age
 FROM catalog_item_attribute_edges ae CROSS JOIN catalog_attribute_terms t ON t.id=ae.term_id
 LEFT JOIN content_rating_ages ra ON t.field_id=1 AND ra.value_key=t.value_key
 WHERE ae.item_id=? AND ae.term_id>? ORDER BY ae.term_id LIMIT 8`, c.Item, c.AfterTerm)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	n, bytes := 0, 0
	for rows.Next() {
		var term int64
		var field int
		var key, source string
		var age sql.NullInt64
		if err = rows.Scan(&term, &field, &key, &source, &age); err != nil {
			return false, false, err
		}
		if !chargeFacetColdSource(ctx, len(key)+len(source)) {
			return false, false, nil
		}
		if n > 0 && bytes+len(key)+len(source) > 256<<10 {
			return false, false, nil
		}
		n++
		bytes += len(key) + len(source)
		c.AfterTerm = term
		if field == 1 && age.Valid {
			c.KnownRating = c.KnownRating || age.Int64 >= 0
			c.Denied = c.Denied || (r.MaximumAge != nil && age.Int64 > int64(*r.MaximumAge))
		}
		if field == 2 {
			for _, label := range r.BlockedLabels {
				if key == strings.ToLower(label) {
					c.Denied = true
					break
				}
			}
		}
		if field == 2 || field == 3 {
			for _, label := range r.MemberDeniedLabels {
				if source == strings.ToLower(label) {
					c.Denied = true
					break
				}
			}
		}
		if c.Denied {
			return true, false, nil
		}
		if bytes >= 256<<10 {
			return false, false, nil
		}
	}
	if err = rows.Err(); err != nil {
		return false, false, err
	}
	if n == 8 {
		return false, false, nil
	}
	return true, !r.BlockUnrated || c.KnownRating, nil
}
