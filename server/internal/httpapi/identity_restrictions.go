package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// viewerRestrictions resolves the content restriction a viewer session carries and
// the fence fragment that makes an open cursor or a cached page expire when the
// restriction changes.
//
// Every catalog surface that can show a title calls this once, folds the fragment
// into its viewer fence and passes the restriction into the catalog request. The
// catalog then applies exactly one predicate (internal/catalog/restrictions.go);
// no surface writes a visibility rule of its own.
func (d Dependencies) viewerRestrictions(r *http.Request, p identity.Principal) (identity.ContentRestrictions, string, error) {
	if tx := dbwork.Snapshot(r.Context()); tx != nil {
		// Currentness/acceptance reads use their actual profile and account
		// rows, including uncommitted changes, never the outside viewer cache.
		restrictions, err := contentaccess.RestrictionsTx(r.Context(), tx, p.Viewer)
		return restrictions, catalog.RestrictionFence(restrictions), err
	}
	// Several surfaces on one page each ask for this, and it is one row that
	// changes only through a restriction publication. The cache is fenced on the
	// authority generation, so a publication invalidates it before the next read
	// rather than within a TTL, and the security-fence lanes bypass it entirely.
	strict := strictPrincipal(dbwork.ClassFrom(r.Context(), dbwork.ClassInteractive))
	profileKey := identity.PersonalKey(p.Viewer)
	var restrictions identity.ContentRestrictions
	var found bool
	if !strict {
		restrictions, found = d.restrictions.lookup(profileKey, time.Now())
	}
	if !found {
		generation := dbwork.AuthorityGeneration()
		var e error
		restrictions, e = d.Identity.ViewerRestrictions(r.Context(), p)
		if e != nil {
			return identity.ContentRestrictions{}, "", e
		}
		if !strict {
			d.restrictions.store(profileKey, restrictions, time.Now(), generation)
		}
	}
	if d.Access.Access != nil && p.AccountID != "" {
		// The account's limits narrow the profile's restrictions on the same
		// rating scale (access.Limits.Narrow): one ceiling, one unrated rule.
		var revision int64
		var body string
		e := d.DB.QueryRowContext(r.Context(), `SELECT revision,body FROM access_limits WHERE account_id=?`, p.AccountID).Scan(&revision, &body)
		if e != nil && e != sql.ErrNoRows {
			return identity.ContentRestrictions{}, "", e
		}
		if e == nil {
			limits := access.DefaultLimits()
			if e = json.Unmarshal([]byte(body), &limits); e != nil {
				return identity.ContentRestrictions{}, "", e
			}
			restrictions = limits.Narrow(restrictions, revision)
		}
	}
	return restrictions, catalog.RestrictionFence(restrictions), nil
}

func (d Dependencies) catalogViewer(r *http.Request, p identity.Principal, libraries []string, fence string) (catalog.Viewer, error) {
	restrictions, _, err := d.viewerRestrictions(r, p)
	if err != nil {
		return catalog.Viewer{}, err
	}
	return catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: fence, Libraries: libraries, Restrictions: restrictions}, nil
}

// restrictedItem refuses a single-item read whose title the profile may not see, so
// knowing an id is not a way around a list that filtered it out.
func (d Dependencies) restrictedItem(r *http.Request, p identity.Principal, item string) error {
	// One check: the catalogue predicate over the viewer's whole policy (the
	// profile's restrictions narrowed by its account's limits). Without a
	// catalogue service the same predicate runs through contentaccess.
	if d.Catalog == nil {
		if tx := dbwork.Snapshot(r.Context()); tx != nil {
			return d.itemRestrictionsTx(r.Context(), tx, p, item)
		}
		gate, err := dbwork.BeginSnapshot(r.Context(), d.DB)
		if err != nil {
			return err
		}
		defer gate.Rollback()
		return d.itemRestrictionsTx(r.Context(), gate.Tx(), p, item)
	}
	// The catalogue answers a restricted title exactly as an absent one
	// (sql.ErrNoRows); admission callers expect ErrContentRestricted, which
	// every writer maps to the same 404.
	library, e := d.Catalog.WithContext(r.Context()).LibraryForItem(item)
	if errors.Is(e, sql.ErrNoRows) {
		return identity.ErrContentRestricted
	}
	if e != nil {
		return e
	}
	viewer, e := d.catalogViewer(r, p, []string{library}, "")
	if e != nil {
		return e
	}
	if e = d.Catalog.WithContext(r.Context()).VisibleItem(r.Context(), viewer, item); errors.Is(e, sql.ErrNoRows) {
		return identity.ErrContentRestricted
	}
	return e
}
