package segmentmarkers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ChapterProvenance matches the detector family whose chapter labels are suggestions.
const ChapterProvenance = "embedded_chapter_label:v1"

// ConfidencePolicy controls unapproved suggestions. Approval, measurement and
// ambiguity remain independent conditions and cannot be supplied by a number.
type ConfidencePolicy struct {
	ViewerPercent, AutomaticPercent int
	UseChapterNames                 bool
}

func DefaultConfidencePolicy() ConfidencePolicy {
	return ConfidencePolicy{ViewerPercent: 50, AutomaticPercent: 85, UseChapterNames: true}
}
func (p ConfidencePolicy) AutomaticSafe(approved bool, confidence float64, provenance string) bool {
	return approved || confidence >= float64(p.AutomaticPercent)/100 && MeasuredProvenance(provenance) && !strings.Contains(provenance, "semantic_ambiguous")
}
func (p ConfidencePolicy) ViewerVisible(approved bool, confidence float64, provenance string) bool {
	return approved || (p.UseChapterNames || provenance != ChapterProvenance) && confidence >= float64(p.ViewerPercent)/100 && !strings.Contains(provenance, "semantic_ambiguous")
}
func (p ConfidencePolicy) Valid() bool {
	return (p.ViewerPercent == 30 || p.ViewerPercent == 50 || p.ViewerPercent == 70) && (p.AutomaticPercent == 70 || p.AutomaticPercent == 85 || p.AutomaticPercent == 95)
}

// ReadConfidencePolicy reads once in the caller's existing bounded snapshot.
// An absent value uses the pre-existing policy; a broken schema fails closed.
func ReadConfidencePolicy(ctx context.Context, tx *sql.Tx) (ConfidencePolicy, error) {
	p := DefaultConfidencePolicy()
	var viewer, automatic sql.NullInt64
	var chapters sql.NullBool
	err := tx.QueryRowContext(ctx, `SELECT s."segments.useChapterNames",s."segments.viewerConfidencePercent",s."segments.automaticConfidencePercent" FROM server_settings AS s WHERE s.singleton=1`).Scan(&chapters, &viewer, &automatic)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if chapters.Valid {
		p.UseChapterNames = chapters.Bool
	}
	if viewer.Valid {
		p.ViewerPercent = int(viewer.Int64)
	}
	if automatic.Valid {
		p.AutomaticPercent = int(automatic.Int64)
	}
	if !p.Valid() {
		return p, fmt.Errorf("invalid segment confidence policy")
	}
	return p, nil
}
