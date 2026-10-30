package dvr

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// Read policy once per bounded response/guide batch, under the same authority
// transaction. A recording grant never widens profile or account content limits.
func programmePredicateTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner) (func(string) bool, error) {
	policy, err := identity.RestrictionsForViewerTx(ctx, tx, identity.Viewer{Authority: o.Authority, AccountID: o.AccountID, ProfileID: o.ProfileID})
	if err != nil {
		return nil, err
	}
	if !policy.AllowDVR {
		return func(string) bool { return false }, nil
	}
	content, err := access.ContentLimitsTx(ctx, tx, o.AccountID, policy.Content())
	if err != nil {
		return nil, err
	}
	return func(rating string) bool { return catalog.RatingAllowed(content, rating) }, nil
}
func programmeRating(p livechannels.Programme) string {
	if p.Rating != nil {
		return p.Rating.Value
	}
	return ""
}
func programmeVisibleTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, p livechannels.Programme) error {
	allowed, err := programmePredicateTx(ctx, tx, o)
	if err != nil {
		return err
	}
	if !allowed(programmeRating(p)) {
		return ErrDenied
	}
	return nil
}
func restrictRecording(r *Recording, allowed func(string) bool) {
	if allowed(programmeRating(r.Programme)) {
		return
	}
	r.Programme.Restricted()
	r.SeriesID, r.SeriesTitle, r.ItemID, r.LibraryID = "", "", "", ""
	r.Watched = nil
	r.PositionMs = 0
	r.DurationMs = 0
	r.Overlaps = []Overlap{}
}
func restrictRecordingTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, r *Recording) error {
	allowed, err := programmePredicateTx(ctx, tx, o)
	if err != nil {
		return err
	}
	restrictRecording(r, allowed)
	return nil
}
