package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

// Pins carry order. The boolean pin remains the record of intent; this file owns
// the viewer's arrangement of what they pinned, both for saved resources and for
// the libraries in the navigation rail.

var ErrPinOrderConflict = errors.New("the pin order changed on another device; reload and try again")
var ErrLibraryNavigationConflict = errors.New("library pins changed on another device; reload and try again")

const maximumPinOrder = 200
const maximumPinnedLibraries = 100

type PinOrderEntry struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type PinOrder struct {
	Revision int64           `json:"revision"`
	Order    []PinOrderEntry `json:"order"`
}

// PinnedResource is one pinned collection, saved view or playlist as the
// viewer's navigation shows it: what it is and its name.
type PinnedResource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
	// PinRevision is what an unpin sends as its expected revision.
	PinRevision int64 `json:"pinRevision"`
}

// PinnedResources is the viewer's pins in their arrangement.
type PinnedResources struct {
	Revision int64            `json:"revision"`
	Order    []PinnedResource `json:"order"`
}
type LibraryNavigation struct {
	PinnedLibraryIDs []string `json:"pinnedLibraryIds"`
	Revision         int64    `json:"revision"`
}

func pinOrderRevision(q personalReader, owner string) (int64, error) {
	var revision int64
	e := q.QueryRow(`SELECT COALESCE((SELECT revision FROM saved_pin_order_revisions WHERE owner_key=?),0)`, owner).Scan(&revision)
	return revision, e
}

// PinOrder reads the viewer's pinned collections, saved views and playlists
// in their arrangement: every one they pinned and may still open, the arranged
// ones first in their order, then the rest by name; at most maximumPinOrder.
// A pin on something deleted or no longer shared is simply not listed.
func (s *Service) PinOrder(a ResourceActor) (PinnedResources, error) {
	out := PinnedResources{Order: []PinnedResource{}}
	owner := actorKey(a)
	revision, e := pinOrderRevision(s.read(), owner)
	if e != nil {
		return out, e
	}
	out.Revision = revision
	rows, e := s.read().Query(`SELECT p.kind,p.resource_id,COALESCE(r.name,l.name,'') AS pin_name,p.revision FROM saved_pins p
 LEFT JOIN saved_pin_order o ON o.owner_key=p.owner_key AND o.kind=p.kind AND o.resource_id=p.resource_id
 LEFT JOIN saved_resources r ON p.kind IN('collection','view') AND r.id=p.resource_id AND r.kind=p.kind AND r.deleted=0
  AND (r.owner_key=p.owner_key OR EXISTS(SELECT 1 FROM saved_resource_shares share WHERE share.resource_id=r.id AND share.authority=? AND share.account_id=? AND share.profile_id=?) OR (r.kind='collection' AND r.visibility='server'))
 LEFT JOIN catalog_playlists l ON p.kind='playlist' AND l.token=p.resource_id AND l.deleted=0
  AND (l.owner_authority=? AND l.owner_account=? AND l.owner_profile=? OR EXISTS(SELECT 1 FROM playlist_shares share WHERE share.playlist_id=l.token AND share.authority=? AND share.account_id=? AND share.profile_id=?))
 WHERE p.owner_key=? AND p.pinned=1 AND (r.id IS NOT NULL OR l.id IS NOT NULL)
 ORDER BY o.position IS NULL,o.position,lower(pin_name),p.resource_id LIMIT ?`, a.Authority, a.AccountID, a.ProfileID, a.Authority, a.AccountID, a.ProfileID, a.Authority, a.AccountID, a.ProfileID, owner, maximumPinOrder)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var entry PinnedResource
		if e = rows.Scan(&entry.Kind, &entry.ID, &entry.Name, &entry.PinRevision); e != nil {
			return out, e
		}
		out.Order = append(out.Order, entry)
	}
	return out, rows.Err()
}

// SetPinOrder replaces the arrangement in one transaction. Only resources the
// viewer has actually pinned may appear, so an order can never leak the
// existence of something they cannot see.
func (s *Service) SetPinOrder(a ResourceActor, expected int64, order []PinOrderEntry, authorize func(*sql.Tx) error) (PinOrder, error) {
	out := PinOrder{Order: []PinOrderEntry{}}
	if a.Authority == "" || a.AccountID == "" || a.ProfileID == "" {
		return out, requestinput.New("a complete viewer is required")
	}
	if len(order) > maximumPinOrder {
		return out, requestinput.New("pin order exceeds 200 entries")
	}
	owner := actorKey(a)
	gated, e := dbwork.Begin(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive))
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	prior, e := usermutation.CheckContext(s.Context(), tx, time.Now().UnixMilli())
	if e != nil {
		return out, e
	}
	if prior != nil {
		current, err := s.WithContext(gated.Context()).PinOrder(a)
		out.Revision = current.Revision
		for _, entry := range current.Order {
			out.Order = append(out.Order, PinOrderEntry{Kind: entry.Kind, ID: entry.ID})
		}
		return out, err
	}
	if e = authorize(tx); e != nil {
		return out, e
	}
	revision, e := pinOrderRevision(tx, owner)
	if e != nil {
		return out, e
	}
	if revision != expected {
		return out, ErrPinOrderConflict
	}
	seen := map[PinOrderEntry]bool{}
	for _, entry := range order {
		if entry.Kind != "collection" && entry.Kind != "view" && entry.Kind != "playlist" {
			return out, requestinput.New("pin kind must be collection, view or playlist")
		}
		if seen[entry] {
			return out, requestinput.New("a pinned resource may appear once in the order")
		}
		seen[entry] = true
		var pinned int
		if e = tx.QueryRow(`SELECT COALESCE((SELECT pinned FROM saved_pins WHERE owner_key=? AND kind=? AND resource_id=?),0)`, owner, entry.Kind, entry.ID).Scan(&pinned); e != nil {
			return out, e
		}
		if pinned != 1 {
			return out, sql.ErrNoRows
		}
	}
	if _, e = tx.Exec(`DELETE FROM saved_pin_order WHERE owner_key=?`, owner); e != nil {
		return out, e
	}
	for index, entry := range order {
		if _, e = tx.Exec(`INSERT INTO saved_pin_order(owner_key,kind,resource_id,position) VALUES(?,?,?,?)`, owner, entry.Kind, entry.ID, index); e != nil {
			return out, e
		}
	}
	revision++
	if _, e = tx.Exec(`INSERT INTO saved_pin_order_revisions(owner_key,revision) VALUES(?,?) ON CONFLICT(owner_key) DO UPDATE SET revision=excluded.revision`, owner, revision); e != nil {
		return out, e
	}
	if e = hintPinsChanged(tx, owner, "pins"); e != nil {
		return out, e
	}
	if e = usermutation.SaveContext(s.Context(), tx, usermutation.Result{Status: "applied", Revision: revision}, time.Now().UnixMilli()); e != nil {
		return out, e
	}
	if e = gated.Commit(); e != nil {
		return out, e
	}
	out.Revision = revision
	out.Order = append(out.Order, order...)
	return out, nil
}

// LibraryNavigation reads the viewer's pinned libraries in their arrangement.
// Libraries that disappeared are dropped rather than reported as holes.
func (s *Service) LibraryNavigation(a ResourceActor, allowed func(string) bool) (LibraryNavigation, error) {
	out := LibraryNavigation{PinnedLibraryIDs: []string{}}
	owner := actorKey(a)
	if e := s.read().QueryRow(`SELECT COALESCE((SELECT revision FROM library_navigation_revisions WHERE owner_key=?),0)`, owner).Scan(&out.Revision); e != nil {
		return out, e
	}
	rows, e := s.read().Query(`SELECT n.library_id FROM library_navigation n JOIN libraries l ON l.id=n.library_id WHERE n.owner_key=? ORDER BY n.position,n.library_id`, owner)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return out, e
		}
		if allowed == nil || allowed(id) {
			out.PinnedLibraryIDs = append(out.PinnedLibraryIDs, id)
		}
	}
	return out, rows.Err()
}

// SetLibraryNavigation replaces the pinned library order. The route's authorize
// callback proves the viewer may see each library inside the same transaction.
func (s *Service) SetLibraryNavigation(a ResourceActor, expected int64, libraries []string, authorize func(*sql.Tx, string) error) (LibraryNavigation, error) {
	out := LibraryNavigation{PinnedLibraryIDs: []string{}}
	if a.Authority == "" || a.AccountID == "" || a.ProfileID == "" {
		return out, requestinput.New("a complete viewer is required")
	}
	if len(libraries) > maximumPinnedLibraries {
		return out, requestinput.New("pinned libraries exceed 100 entries")
	}
	owner := actorKey(a)
	gated2, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassFrom(context.Background(), dbwork.ClassInteractive))
	if e != nil {
		return out, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if e = authorize(tx, ""); e != nil {
		return out, e
	}
	var revision int64
	if e = tx.QueryRow(`SELECT COALESCE((SELECT revision FROM library_navigation_revisions WHERE owner_key=?),0)`, owner).Scan(&revision); e != nil {
		return out, e
	}
	if revision != expected {
		return out, ErrLibraryNavigationConflict
	}
	seen := map[string]bool{}
	for _, library := range libraries {
		if seen[library] {
			return out, requestinput.New("a library may appear once in the pin order")
		}
		seen[library] = true
		var exists int
		if e = tx.QueryRow(`SELECT 1 FROM libraries WHERE id=?`, library).Scan(&exists); e != nil {
			return out, e
		}
		if e = authorize(tx, library); e != nil {
			return out, e
		}
	}
	if _, e = tx.Exec(`DELETE FROM library_navigation WHERE owner_key=?`, owner); e != nil {
		return out, e
	}
	for index, library := range libraries {
		if _, e = tx.Exec(`INSERT INTO library_navigation(owner_key,library_id,position) VALUES(?,?,?)`, owner, library, index); e != nil {
			return out, e
		}
	}
	revision++
	if _, e = tx.Exec(`INSERT INTO library_navigation_revisions(owner_key,revision) VALUES(?,?) ON CONFLICT(owner_key) DO UPDATE SET revision=excluded.revision`, owner, revision); e != nil {
		return out, e
	}
	if e = hintPinsChanged(tx, owner, "library-navigation"); e != nil {
		return out, e
	}
	if e = gated2.Commit(); e != nil {
		return out, e
	}
	out.Revision = revision
	out.PinnedLibraryIDs = append(out.PinnedLibraryIDs, libraries...)
	return out, nil
}

// LibrariesForViewer returns pinned libraries first, in the viewer's order, each
// flagged so a client never has to re-derive the arrangement.
func (s *Service) LibrariesForViewer(a ResourceActor) ([]Library, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out []Library
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).LibrariesForViewer(a)
			return err
		})
		return out, err
	}
	rows, err := s.read().QueryContext(s.Context(), `SELECT l.id,l.name,l.kind,n.library_id IS NOT NULL
 FROM libraries l LEFT JOIN library_navigation n ON n.owner_key=? AND n.library_id=l.id
 WHERE NOT EXISTS(SELECT 1 FROM catalog_libraries gone WHERE gone.library_id=l.id AND gone.retired=1)
 ORDER BY n.library_id IS NULL,n.position,CASE WHEN n.library_id IS NOT NULL THEN l.id END,l.position,l.name,l.id`, actorKey(a))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		var lib Library
		if err = rows.Scan(&lib.ID, &lib.Name, &lib.Kind, &lib.Pinned); err != nil {
			return nil, err
		}
		lib.DefaultView = DefaultLibraryView(lib.Kind)
		out = append(out, lib)
	}
	return out, rows.Err()
}

// hintPinsChanged tells the viewer's other devices that their pins moved, so
// the rail and Saved there follow within the feed's usual delay.
func hintPinsChanged(tx *sql.Tx, owner, what string) error {
	_, e := tx.Exec(`INSERT INTO api_change_pending(audience,type,resource_kind,resource_id,revision)
 VALUES(?,'personal.changed','personal',?,?)
 ON CONFLICT(audience,type,resource_kind,resource_id) DO UPDATE SET revision=excluded.revision`, "personal:"+owner, what, strconv.FormatInt(time.Now().UnixNano(), 10))
	return e
}
