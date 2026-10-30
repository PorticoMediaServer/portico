package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
)

// playbackAuthorityTx is the playback control service's authority callback.
// Authentication, item membership and cached hosted restrictions are observed
// in the transaction that accepts or rejects a command. No nested connection,
// external hosted request, media probe or filesystem operation is permitted here.
func (d Dependencies) playbackAuthorityTx(ctx context.Context, tx *sql.Tx, expected identity.Principal, itemID string) (identity.Principal, error) {
	if d.Identity == nil || tx == nil {
		return identity.Principal{}, identity.ErrUnauthorized
	}
	current, err := d.Identity.ReauthorizeTx(ctx, tx, expected)
	if err != nil {
		return identity.Principal{}, err
	}
	library := ""
	if itemID != "" {
		if err = tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, itemID).Scan(&library); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return identity.Principal{}, identity.ErrNotVisible
			}
			return identity.Principal{}, err
		}
	}
	if err = d.allowedLibraryTx(ctx, current, library, tx); err != nil {
		return identity.Principal{}, err
	}
	if itemID != "" {
		if err = d.itemRestrictionsTx(ctx, tx, current, itemID); err != nil {
			return identity.Principal{}, err
		}
	}
	if current.Role == "owner" {
		if err = d.ownerAuthorityTx(ctx, tx, current); err != nil {
			return identity.Principal{}, err
		}
	}
	return current, nil
}

// configurePlaybackEvidence connects the adopted queue/VOD session transport to
// personal state. Authentication, canonical-writer selection, history and resume
// mutation all execute in the transaction accepting the observation.
func (d Dependencies) configurePlaybackEvidence() {
	if d.Playback == nil {
		return
	}
	d.Playback.AuthorityTx = d.playbackAuthorityTx
	d.Playback.ContinueAuthorityTx = d.playbackFamilyAuthorityTx
	if d.Identity != nil && d.DiscCaptures != nil {
		// The optional host composition refuses unsupported tool/storage closures.
		// Only an original remote video Play can enter its private factory.
		_ = d.Playback.ConfigureDiscInputs(d.Identity.ID(), d.DiscCaptures)
		if d.TelemetryContext != nil {
			// Explicit process/key and active-copy budgets. Active work borrows
			// the existing two playback probe slots; terminal keys are retained
			// and saturation refuses rather than forgetting/renewing authority.
			_ = d.Playback.ConfigureDiscPlay(d.TelemetryContext, d.Identity, d.DiscCaptures, d.DiscConditional, playbackdisc.PreparationLimits{Retained: 4096, Active: 2, PerDevice: 1})
		}
	}
	d.Playback.MarkersTx = func(ctx context.Context, tx *sql.Tx, p identity.Principal, scope playback.OffersScope, source string) (segmentmarkers.Set, error) {
		return mediaanalysis.ViewerMarkersTx(ctx, tx, d.markerAccess(p, scope.LibraryID, scope.ItemID, scope.ViewerFence), source)
	}
	if d.Catalog != nil {
		d.Playback.PersonalProgress = func(tx *sql.Tx, v identity.Viewer, playbackID, item string, sequence int64, position, duration float64, state string, canonical bool, rate float64, seeked *bool, generation int, playing bool, received time.Time, proved *catalog.PlayedTime) error {
			return d.Catalog.AcceptPlaybackProgressEvidence(tx, v, playbackID, item, sequence, position, duration, state, canonical, rate, seeked, generation, playing, received, proved)
		}
	}
}

// playbackFamilyAuthorityTx is for already accepted work bound to a source
// family, never HTTP authentication. A rotated token is not a new device. The
// request handler must authenticate a current bearer before entering a domain;
// this callback permits only that same durable family's asynchronous work.
func (d Dependencies) playbackFamilyAuthorityTx(ctx context.Context, tx *sql.Tx, expected identity.Principal, itemID string) (identity.Principal, error) {
	if d.Identity == nil || tx == nil {
		return identity.Principal{}, identity.ErrUnauthorized
	}
	if expected.ServerID == "" {
		expected.ServerID = d.Identity.ID()
	} // Media grant principals are loaded from this server's DB.
	var family string
	err := tx.QueryRowContext(ctx, `SELECT family_id FROM authorization_family_tokens WHERE token_hash=?`, expected.Hash).Scan(&family)
	if errors.Is(err, sql.ErrNoRows) {
		return knownItemAuthority(ctx, tx, itemID)(d.playbackAuthorityTx(ctx, tx, expected, itemID))
	}
	if err != nil {
		return identity.Principal{}, err
	}
	verifiedFamily, err := d.Identity.FamilyAuthorityTx(ctx, tx, family, expected)
	if err != nil {
		return identity.Principal{}, err
	}
	// Grant principals can start with no device metadata. The existing family
	// authority lookup proves the binding before it is used for accounting.
	expected.DeviceID = verifiedFamily.DeviceID
	if err = tx.QueryRowContext(ctx, `SELECT t.token_hash FROM authorization_session_families f JOIN authorization_family_tokens t ON t.family_id=f.id AND t.generation=f.current_generation WHERE f.id=? AND f.revoked=0 AND t.retired=0`, family).Scan(&expected.Hash); err != nil {
		return identity.Principal{}, err
	}
	return knownItemAuthority(ctx, tx, itemID)(d.playbackAuthorityTx(ctx, tx, expected, itemID))
}

// knownItemAuthority turns a refusal of already accepted work into "retry"
// while the item has an unpublished catalogue change: a title or artwork edit
// mid-playback must not end the stream. A real restriction, once published,
// still refuses. The caller already had the item, so this reveals nothing.
//
// Catalogue facts publish synchronously now, so there is no unpublished change
// left to wait for (the deleted ItemPendingTx fenced fact publication, domains
// 1 and 28, both synchronous): a refusal stands.
func knownItemAuthority(ctx context.Context, tx *sql.Tx, itemID string) func(identity.Principal, error) (identity.Principal, error) {
	return func(p identity.Principal, err error) (identity.Principal, error) {
		return p, err
	}
}
