package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// maxSelectionDelete is the most titles one delete takes. More is refused, never split.
const maxSelectionDelete = 200

// SelectionDelete names the titles of a selection, all in one library.
type SelectionDelete struct {
	IDs []string `json:"ids"`
}

// SelectionDeletion is what deleting a selection would take, shown before it is confirmed.
// The delete is confirmed by typing Count.
type SelectionDeletion struct {
	// Count is how many titles were selected.
	Count int `json:"count"`
	// Titles is every title that would go: the selected ones, everything under them, and
	// emptied holders and other titles played from the same files.
	Titles      int   `json:"titles"`
	Files       int   `json:"files"`
	Bytes       int64 `json:"bytes"`
	ListEntries int   `json:"listEntries"`
	// OtherPeople is how many saved places and marks of other profiles go with the titles.
	OtherPeople int `json:"otherPeople"`
	// AlsoRemoves are emptied holders and titles outside the selection played from the same files.
	AlsoRemoves []TitleRef `json:"alsoRemoves"`
}

// SelectionDeleted says what deleting a selection did. A title one of whose files was kept
// stays, and is counted in neither number.
type SelectionDeleted struct {
	// Removed is how many of the selected titles are gone.
	Removed      int        `json:"removed"`
	FilesDeleted int        `json:"filesDeleted"`
	Bytes        int64      `json:"bytes"`
	Kept         []KeptFile `json:"kept"`
	AlsoRemoved  []TitleRef `json:"alsoRemoved"`
}

func (s *Server) selectionRoutes() {
	Handle(s, "POST /v1/admin/items/deletion", Owner, s.previewSelectionDeletion)
	Handle(s, "POST /v1/admin/items/delete", Owner, s.deleteSelection).with(ScopeLibrary)
}

// selection gathers what deleting each selected title would take. It refuses a selection
// that is empty, too large, repeats a title, spans libraries, or cannot be deleted at all,
// before anything is deleted.
func (s *Server) selection(r *http.Request, req SelectionDelete) ([]db.Deletion, error) {
	switch {
	case len(req.IDs) == 0:
		return nil, ErrField("ids", "Select at least one title.")
	case len(req.IDs) > maxSelectionDelete:
		return nil, ErrField("ids", "Delete at most 200 titles at once")
	}
	seen, folders := map[string]bool{}, map[string]bool{}
	var all []db.Deletion
	for n, id := range req.IDs {
		at := "ids[" + strconv.Itoa(n) + "]"
		if seen[id] {
			return nil, ErrField(at, "This title is in the selection twice.")
		}
		seen[id] = true
		del, err := s.db.ItemDeletion(r.Context(), s.viewer(r), id)
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrField(at, "This title is not in the library any more.")
		}
		if err != nil {
			return nil, err
		}
		if len(all) > 0 && del.LibraryID != all[0].LibraryID {
			return nil, ErrField(at, "A selection is deleted one library at a time.")
		}
		if !del.AllowDelete {
			return nil, refusal(http.StatusForbidden, "deleting_not_allowed", "Deleting files is turned off for this library.", false)
		}
		for _, f := range del.Files {
			if f.Remote {
				return nil, ErrConflict("remote_storage", "Portico can't delete files on remote storage")
			}
		}
		all = append(all, del)
	}
	// Every library folder the selection touches must be there before any title is deleted:
	// with one disk away, deleting the titles on the other and then refusing would leave the
	// owner told that nothing was deleted when files are gone for good.
	for _, del := range all {
		if err := foldersThere(folders, del.Files); err != nil {
			return nil, err
		}
	}
	return all, nil
}

func (s *Server) previewSelectionDeletion(r *http.Request, req SelectionDelete) (SelectionDeletion, error) {
	all, err := s.selection(r, req)
	if err != nil {
		return SelectionDeletion{}, err
	}
	impact, err := s.db.SelectionDeletionPreview(r.Context(), s.viewer(r), all)
	if err != nil {
		return SelectionDeletion{}, libraryError(err)
	}
	return SelectionDeletion{Count: len(req.IDs), Titles: impact.Titles, Files: len(impact.Files), Bytes: impact.Bytes,
		ListEntries: impact.ListEntries, OtherPeople: impact.OtherStates, AlsoRemoves: titleRefs(impact.AlsoRemoves)}, nil
}

// deleteSelection deletes the files of every selected title from disk and removes the
// titles, after the count has been typed. Each title is deleted as one title is: a file that
// changed or cannot be deleted is kept, and then so is its title.
func (s *Server) deleteSelection(r *http.Request, req SelectionDelete) (SelectionDeleted, error) {
	out := SelectionDeleted{Kept: []KeptFile{}, AlsoRemoved: []TitleRef{}}
	if _, err := s.selection(r, req); err != nil {
		return out, err
	}
	if err := confirmed(r, strconv.Itoa(len(req.IDs))); err != nil {
		return out, err
	}
	for _, id := range req.IDs {
		// Read again: an earlier title of the selection may have taken this one with it (two
		// episodes in one file), and then there is nothing left to do for it.
		del, err := s.db.ItemDeletion(r.Context(), s.viewer(r), id)
		if errors.Is(err, db.ErrNotFound) {
			out.Removed++
			continue
		}
		if err != nil {
			return out, err
		}
		one, err := s.deleteFiles(r, del)
		if err != nil {
			return out, err
		}
		out.FilesDeleted += one.FilesDeleted
		out.Bytes += one.Bytes
		out.Kept = append(out.Kept, one.Kept...)
		if one.Removed {
			out.Removed++
		}
		for _, other := range one.AlsoRemoved {
			selected := false
			for _, id := range req.IDs {
				selected = selected || id == other.ID
			}
			if !selected {
				out.AlsoRemoved = append(out.AlsoRemoved, other)
			}
		}
	}
	return out, nil
}
