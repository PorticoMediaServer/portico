package scan

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/localmetadata"
)

var ErrMetadataUnreadable = errors.New("this title's metadata could not be read from its files")

func (s *Scanner) metadataRun(ctx context.Context, lib db.Library) *run {
	r := &run{s: s, ctx: ctx, lib: lib, names: namesKind(lib.Kind), claimed: map[int64]bool{}, sources: map[int64]Source{}, paths: map[int64]db.LibraryPath{}, walked: map[string]bool{},
		nfos: map[string][]localmetadata.VideoNFO{}, shows: map[string]showLocal{}, albums: map[string]albumLocal{}, artists: map[db.ItemKey][]db.ImportImage{},
		bookPictures: map[string]bool{}}
	if r.batch = s.BatchFiles; r.batch <= 0 {
		r.batch = 200
	}
	for _, p := range lib.Paths {
		r.paths[p.ID] = p
	}
	return r
}

// RefreshMetadata reuses the scan's title readers without a scan, file import,
// missing-file pass, version rewrite or directory marker update.
func (s *Scanner) RefreshMetadata(ctx context.Context, id string) error {
	lib, files, err := s.DB.LocalMetadataFiles(ctx, id)
	if err != nil {
		return err
	}
	if lib.Kind != db.LibraryBooks && lib.Kind != db.LibraryComics && lib.Kind != db.LibraryAudiobooks {
		return nil
	}
	// An explicit metadata read asks for Basic facts even in a names-only library.
	// Remote whole-archive formats still retain the existing Complete-depth gate.
	if lib.Options.Depth == db.DepthFiles {
		lib.Options.Depth = db.DepthBasic
	}
	// Prefer a format that carries book metadata over a PDF rendition of the same book.
	if lib.Kind == db.LibraryBooks {
		sort.SliceStable(files, func(i, j int) bool {
			return strings.ToLower(path.Ext(files[i].Rel)) != ".pdf" && strings.ToLower(path.Ext(files[j].Rel)) == ".pdf"
		})
	}
	r := s.metadataRun(ctx, lib)
	for _, target := range files {
		r.path, r.src = r.paths[target.PathID], r.source(target.PathID)
		f, err := r.metadataFolder(path.Dir(target.Rel), target.Rel)
		if err != nil {
			return err
		}
		for _, u := range r.units(f) {
			targetItem := -1
			for n, it := range u.items {
				for _, v := range it.Versions {
					for _, part := range v.Parts {
						if path.Join(part.File.Dir, part.File.Name) == target.Rel && it.Kind != db.KindSeries {
							targetItem = n
						}
					}
				}
			}
			if targetItem < 0 {
				continue
			}
			// The other rows only identify/create the series that holds this title.
			// Its neighbours' artwork is not part of this refresh.
			for n := range u.items {
				it := &u.items[n]
				if n != targetItem {
					it.Images = nil
					continue
				}
				it.Versions, it.Reread, it.RereadLocal = nil, true, true
				// Refreshed pictures are already in the artwork store. Do not manufacture
				// file rows for new sidecars, or touch an active scan's seen_scan values.
				for i := range it.Images {
					it.Images[i].File = nil
				}
			}
			_, err := s.DB.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: target.PathID, Items: u.items})
			return err
		}
	}
	return ErrMetadataUnreadable
}

func (r *run) metadataFolder(dir, target string) (*folder, error) {
	if dir == "." {
		dir = ""
	}
	entries, err := r.src.List(r.ctx, dir)
	if err != nil {
		return nil, ErrMetadataUnreadable
	}
	f := &folder{rel: dir}
	for _, e := range entries {
		if e.Dir {
			continue
		}
		file := &found{entry: e, rel: path.Join(dir, e.Name), role: role(r.lib.Kind, e.Name)}
		if file.rel == target {
			file.state = changed
		}
		f.files = append(f.files, file)
		if file.role == db.FileMedia {
			f.media = append(f.media, file)
		}
	}
	sort.Slice(f.media, func(i, j int) bool { return f.media[i].rel < f.media[j].rel })
	return f, nil
}

// RefreshMetadata is the concrete scanner entry point used by metadata.Service.
func (m *Manager) RefreshMetadata(ctx context.Context, id string) error {
	return m.scanner.RefreshMetadata(ctx, id)
}

func localRereads(units []unit) []unit {
	for i := range units {
		for j := range units[i].items {
			it := &units[i].items[j]
			if it.Reread && it.Kind != db.KindSeries {
				it.RereadLocal = true
			}
		}
	}
	return units
}
