package seed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// Tree writes the synthetic library as real folders of empty files under root, one folder for
// each of the four libraries, named the way people name them, so that a real scan can be
// measured. It returns how many files it made. The same seed and size give the same tree.
func Tree(cfg Config, root string) (int, error) {
	t := &tree{cfg: cfg, root: root}
	for _, spec := range Libraries {
		t.lib = spec.Kind
		left := cfg.Leaves(spec.Kind)
		for n := 0; left > 0 && t.err == nil; n++ {
			r := (&builder{cfg: cfg}).rng(spec.Kind, n)
			switch spec.Kind {
			case db.LibraryMovies:
				name := fmt.Sprintf("%s (%d)", title(r), 1950+r.IntN(76))
				t.file(name, name+".mkv")
				if r.IntN(3) == 0 {
					t.file(name, name+".en.srt")
				}
				if r.IntN(20) == 0 {
					t.file(name, name+" - 4K.mkv")
				}
				left--
			case db.LibraryShows:
				show := fmt.Sprintf("%s %d", title(r), n)
				for season, seasons := 1, 1+r.IntN(8); season <= seasons && left > 0; season++ {
					for episode, episodes := 1, min(6+r.IntN(18), left); episode <= episodes; episode++ {
						t.file(filepath.Join(show, fmt.Sprintf("Season %d", season)), fmt.Sprintf("%s - S%02dE%02d.mkv", show, season, episode))
						left--
					}
				}
			case db.LibraryMusic:
				artist := fmt.Sprintf("%s %s %d", word(r), word(r), n)
				for album, albums := 0, 1+r.IntN(12); album < albums && left > 0; album++ {
					folder := filepath.Join(artist, fmt.Sprintf("%s (%d)", title(r), 1960+r.IntN(66)))
					for track, tracks := 1, min(8+r.IntN(9), left); track <= tracks; track++ {
						t.file(folder, fmt.Sprintf("%02d - %s.flac", track, title(r)))
						left--
					}
				}
			case db.LibraryBooks:
				author := fmt.Sprintf("%s %s %d", word(r), word(r), n)
				for book, books := 0, 1+r.IntN(8); book < books && left > 0; book++ {
					t.file(author, fmt.Sprintf("%s %d - %s.epub", title(r), book, author))
					left--
				}
			}
		}
	}
	return t.files, t.err
}

type tree struct {
	cfg   Config
	root  string
	lib   string
	made  string // the folder made last
	files int
	err   error
}

func (t *tree) file(folder, name string) {
	if t.err != nil {
		return
	}
	dir := filepath.Join(t.root, t.lib, folder)
	if dir != t.made {
		if t.err = os.MkdirAll(dir, 0o755); t.err != nil {
			return
		}
		t.made = dir
	}
	// A few bytes, differing in size, so that no two files look like one that moved.
	if t.err = os.WriteFile(filepath.Join(dir, name), make([]byte, 1+t.files%61), 0o644); t.err == nil {
		t.files++
	}
}

// ScanTree makes the four libraries over a tree written by Tree, if they are not there, and
// scans them with the real scanner. Run again over an unchanged tree it is an unchanged
// rescan. progress, if not nil, is called as the scan goes.
func ScanTree(ctx context.Context, d *db.DB, scanner *scan.Scanner, root string, progress func(Totals)) (Totals, error) {
	var totals Totals
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		return totals, err
	}
	var mine []db.Library
	for _, lib := range libraries {
		if len(lib.Paths) == 1 && filepath.Dir(lib.Paths[0].Path) == root {
			mine = append(mine, lib)
		}
	}
	if len(mine) == 0 {
		for _, spec := range Libraries {
			lib, err := d.NewLibrary(ctx, db.NewLibrary{Name: spec.Name + " in " + filepath.Base(root), Kind: spec.Kind, Language: "en",
				Folders: []string{filepath.Join(root, spec.Kind)}, Options: db.LibraryOptions{Trash: db.TrashRule{Mode: db.TrashManual}}})
			if err != nil {
				return totals, err
			}
			mine = append(mine, lib)
		}
	}
	start := time.Now()
	var before scan.Stats
	add := func(stats scan.Stats) {
		totals.Files = before.Files + stats.Files
		totals.FilesNew = before.FilesNew + stats.FilesNew
		totals.FilesChanged = before.FilesChanged + stats.FilesChanged
		totals.ItemsNew = before.ItemsNew + stats.ItemsNew
		totals.RowsWritten = before.RowsWritten + stats.RowsWritten
		totals.Probed = before.Probed + stats.Probed
		totals.Elapsed = time.Since(start)
		totals.FilesPerSec = float64(totals.Files) / totals.Elapsed.Seconds()
	}
	run := *scanner
	run.Progress = func(stats scan.Stats) {
		add(stats)
		if progress != nil {
			progress(totals)
		}
	}
	for _, lib := range mine {
		stats, err := run.Scan(ctx, lib)
		add(stats)
		if err != nil {
			return totals, fmt.Errorf("library %s: %w", lib.Name, err)
		}
		before.Files, before.FilesNew, before.FilesChanged = totals.Files, totals.FilesNew, totals.FilesChanged
		before.ItemsNew, before.RowsWritten, before.Probed = totals.ItemsNew, totals.RowsWritten, totals.Probed
	}
	return totals, nil
}
