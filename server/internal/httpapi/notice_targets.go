package httpapi

import (
	"database/sql"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// A notice is written once and read for months, and what its reader may see
// can narrow in between: a profile is given an age limit, a library is taken
// away. The owner can also send every profile one message with a link in it.
// So a link is checked against the reader, on every read: an action that would
// open a title or a library this viewer may not see is left out of the notice,
// and the notice's own words stay. Without this an inbox was a place a hidden
// title's identifier could still be read from.
func (d Dependencies) visibleNoticeActions(r *http.Request, p identity.Principal, items []operations.Notice) error {
	var libraries []string
	loaded := false
	for i := range items {
		kept := make([]operations.NoticeAction, 0, len(items[i].Actions))
		for _, action := range items[i].Actions {
			if action.Kind != "navigate" || action.Target == nil || action.Target.EntityID == "" && action.Target.LibraryID == "" {
				kept = append(kept, action)
				continue
			}
			if !loaded {
				visible, err := d.visibleLibraryIDs(r, p)
				if err != nil {
					return err
				}
				libraries, loaded = visible, true
			}
			ok, err := d.noticeTargetVisible(r, p, libraries, *action.Target)
			if err != nil {
				return err
			}
			if ok {
				kept = append(kept, action)
			}
		}
		items[i].Actions = kept
	}
	return nil
}

func (d Dependencies) visibleLibraryIDs(r *http.Request, p identity.Principal) ([]string, error) {
	if d.Catalog == nil {
		return []string{}, nil
	}
	visible, err := d.visibleLibraries(r, p)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(visible))
	for _, library := range visible {
		ids = append(ids, library.ID)
	}
	return ids, nil
}

// noticeTargetVisible answers with the catalogue's own rule, the one every
// list applies. Views that name no catalogue title (a recording, a report, a
// settings page) carry identifiers of other kinds and are not judged here.
func (d Dependencies) noticeTargetVisible(r *http.Request, p identity.Principal, libraries []string, target operations.NoticeTarget) (bool, error) {
	allowed := func(library string) bool {
		for _, id := range libraries {
			if id == library {
				return true
			}
		}
		return false
	}
	if target.LibraryID != "" && !allowed(target.LibraryID) {
		return false, nil
	}
	kind := ""
	switch target.View {
	case "library":
		return target.EntityID == "" || allowed(target.EntityID), nil
	case "item", "episode":
		kind = "item"
	case "show", "season", "collection":
		kind = target.View
	default:
		return true, nil
	}
	if target.EntityID == "" {
		return true, nil
	}
	if d.Catalog == nil {
		return false, nil
	}
	viewer, err := d.catalogViewer(r, p, libraries, "")
	if err != nil {
		return false, err
	}
	switch err = d.Catalog.WithContext(r.Context()).VisibleEntity(r.Context(), viewer, kind, target.EntityID); {
	case err == nil:
		return true, nil
	case err == sql.ErrNoRows:
		return false, nil
	default:
		return false, err
	}
}
