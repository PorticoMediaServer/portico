package scan

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/naming"
)

// Scanner scans libraries. One Scanner serves every scan of a server.
type Scanner struct {
	DB *db.DB
	// Prober reads the inside of media files. With none, files are listed and never read.
	Prober Prober
	// Source opens the tree a library folder points at. With none, every folder is a local one.
	Source func(root string) Source
	Log    *slog.Logger
	// BatchFiles is about how many files go into one transaction. 0 means 200.
	BatchFiles int
	// Progress, if set, is told how far a scan has got after every folder with changes and
	// every so many unchanged ones.
	Progress func(Stats)
}

// Stats is what a scan did.
type Stats struct {
	Folders      int
	Files        int // files looked at
	FilesNew     int
	FilesChanged int // replaced, or back after being missing
	FilesMoved   int
	FilesGone    int
	ItemsNew     int
	Probed       int
	Removed      int   // missing titles the library's trash rule removed
	RowsWritten  int64 // rows the folder walk inserted, updated or deleted
}

// errEmptied is the state of a library folder that had files and now lists as empty: most
// likely a disk that is not mounted. Nothing in it is marked missing.
var errEmptied = errors.New("the folder is empty where it had files before; nothing was marked missing")

// Scan scans one library: every folder is listed, the differences are stored, the files not
// yet read are read (unless the library's depth is the file list only), and the library's
// trash rule is applied. It is safe to run again after any interruption: what was stored is
// found unchanged, and the rest is done.
func (s *Scanner) Scan(ctx context.Context, lib db.Library) (Stats, error) {
	scan, err := s.DB.BeginScan(ctx, lib.ID)
	if err != nil {
		return Stats{}, err
	}
	r := &run{s: s, ctx: ctx, lib: lib, names: namesKind(lib.Kind), scan: scan, claimed: map[int64]bool{}, sources: map[string]Source{},
		nfos: map[string][]localmetadata.VideoNFO{}}
	if r.batch = s.BatchFiles; r.batch <= 0 {
		r.batch = 200
	}
	paths := map[int64]error{}
	for _, p := range lib.Paths {
		r.path, r.src = p, r.source(p.Path)
		unavailable, err := r.walkPath()
		if err != nil {
			return r.stats, err
		}
		if unavailable != nil {
			s.log().Warn("library folder not scanned", "library", lib.Name, "folder", p.Path, "error", unavailable)
		}
		paths[p.ID] = unavailable
	}
	// Books and comics are not audio or video: there is nothing in them for the prober.
	if lib.Options.Depth != db.DepthFiles && s.Prober != nil && lib.Kind != db.LibraryBooks && lib.Kind != db.LibraryComics {
		if err := r.probe(); err != nil {
			return r.stats, err
		}
	}
	if cutoff, ok := lib.Options.Trash.Cutoff(time.Now()); ok {
		if r.stats.Removed, err = s.DB.EmptyTrash(ctx, lib.ID, cutoff); err != nil {
			return r.stats, err
		}
	}
	return r.stats, s.DB.EndScan(ctx, lib.ID, paths)
}

func (s *Scanner) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// run is one scan of one library.
type run struct {
	s       *Scanner
	ctx     context.Context
	lib     db.Library
	names   string // the library's kind as the naming rules call it
	scan    int64
	batch   int
	stats   Stats
	path    db.LibraryPath
	src     Source
	rules   []naming.IgnoreRules
	pending db.ImportBatch // what has been gathered for the next transaction
	claimed map[int64]bool // stored files already recognised at a new place by this scan
	sources map[string]Source
	nfos    map[string][]localmetadata.VideoNFO // NFO files read by this scan, by path
}

func (r *run) source(root string) Source {
	if src, ok := r.sources[root]; ok {
		return src
	}
	var src Source = Local{Root: root}
	if r.s.Source != nil {
		src = r.s.Source(root)
	}
	r.sources[root] = src
	return src
}

// walkPath scans one folder of the library. The first error is why the folder could not be
// scanned at all, which marks nothing missing; the second stops the scan.
func (r *run) walkPath() (unavailable, err error) {
	entries, err := r.src.List(r.ctx, "")
	if err != nil {
		return err, r.ctx.Err()
	}
	if len(entries) == 0 {
		had, err := r.s.DB.FilesUnder(r.ctx, r.path.ID)
		if err != nil {
			return nil, err
		}
		if had > 0 {
			return errEmptied, nil
		}
	}
	if err := r.walkFolder("", entries, 0); err != nil {
		return nil, err
	}
	return nil, r.flush()
}

// state is how a file compares with what the last scan stored.
type state int

const (
	same    state = iota
	added         // not stored
	changed       // stored with another size or modified time, or with another problem
	back          // stored as missing
	moved         // stored at another place, which no longer has it
)

// found is one file a folder holds now.
type found struct {
	entry   Entry
	rel     string // path under the library folder
	role    db.FileRole
	state   state
	problem db.FileProblem
	from    *db.FilePlace // where a moved file was
	stream  *db.Stream    // what a subtitle's name says about it
	reading *naming.Reading
	written bool
}

func (f *found) ref() db.FileRef {
	dir := path.Dir(f.rel)
	if dir == "." {
		dir = ""
	}
	return db.FileRef{Dir: dir, Name: path.Base(f.rel)}
}

// touched reports whether the titles this file plays need looking at: it is new here.
func (f *found) touched() bool { return f.state == added || f.state == moved }

// folder is one folder being scanned.
type folder struct {
	rel     string
	files   []*found // every file the scan keeps, by name
	media   []*found
	differs bool
}

func (r *run) walkFolder(rel string, entries []Entry, depth int) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	pushed := 0
	for _, e := range entries {
		if !e.Dir && (e.Name == ".ignore" || e.Name == ".plexignore") && e.Size <= naming.MaxIgnoreBytes {
			if raw, err := readSmall(r.ctx, r.src, path.Join(rel, e.Name), naming.MaxIgnoreBytes); err == nil {
				scope := rel
				if scope == "" {
					scope = "."
				}
				rules, _ := naming.ParseIgnore(scope, string(raw), rel == "")
				r.rules = append(r.rules, rules)
				pushed++
			}
		}
	}
	defer func() { r.rules = r.rules[:len(r.rules)-pushed] }()

	stored, err := r.s.DB.Folder(r.ctx, r.path.ID, rel)
	if err != nil {
		return err
	}
	f := &folder{rel: rel, differs: !stored.Known}
	var folders []string
	inDisc := discFolder(path.Base(rel))
	listed := map[string]bool{}
	for _, e := range entries {
		child := path.Join(rel, e.Name)
		file := &found{entry: e, rel: child}
		switch {
		case !utf8.ValidString(e.Name):
			// Never guessed at: the entry is listed for the owner under a readable stand-in.
			file.entry.Name = strings.ToValidUTF8(e.Name, "�")
			file.rel = path.Join(rel, file.entry.Name)
			file.role, file.problem = db.FileOther, db.ProblemUnreadableName
		case naming.IgnoreNameBeforeStat(e.Name):
			continue
		case len(child) > naming.MaxPathBytes || depth+1 >= naming.MaxDepth:
			file.role, file.problem = db.FileOther, db.ProblemUnsupported
		case e.Dir:
			if !inDisc && !naming.IgnorePathWithSize(child, true, 0) && !r.ignored(child, true) {
				folders = append(folders, e.Name)
			}
			continue
		default:
			file.role = role(r.lib.Kind, e.Name)
			if inDisc {
				file.role = 0
				if discTitle(path.Base(rel), e.Name) {
					file.role = db.FileMedia
				}
			}
			if file.role == 0 || naming.IgnorePathWithSize(child, false, e.Size) || r.ignored(child, false) {
				continue
			}
		}
		if file.problem != db.ProblemNone {
			file.entry.Dir, file.entry.Size, file.entry.ModifiedNS = false, 0, 0
		}
		if listed[file.entry.Name] {
			continue
		}
		listed[file.entry.Name] = true
		was, known := stored.Files[file.entry.Name]
		switch {
		case !known:
			file.state = added
		case was.Size != file.entry.Size || was.ModifiedNS != file.entry.ModifiedNS || was.Problem != file.problem && file.problem != db.ProblemNone:
			file.state = changed
		case was.Missing:
			file.state = back
		}
		f.differs = f.differs || file.state != same
		f.files = append(f.files, file)
		if file.role == db.FileMedia {
			f.media = append(f.media, file)
		}
	}
	r.stats.Folders++
	r.stats.Files += len(f.files)
	var gone []db.FileRef
	for name, was := range stored.Files {
		if !listed[name] && !was.Missing {
			gone = append(gone, db.FileRef{Dir: rel, Name: name})
		}
	}
	if f.differs || len(gone) > 0 {
		sort.Slice(f.files, func(a, b int) bool { return f.files[a].entry.Name < f.files[b].entry.Name })
		sort.Slice(f.media, func(a, b int) bool { return f.media[a].entry.Name < f.media[b].entry.Name })
		if err := r.store(f, gone); err != nil {
			return err
		}
	}
	if r.s.Progress != nil && r.stats.Folders%500 == 0 {
		r.s.Progress(r.stats)
	}

	sort.Strings(folders)
	here := map[string]bool{}
	for _, name := range folders {
		child := path.Join(rel, name)
		here[child] = true
		entries, err := r.src.List(r.ctx, child)
		if err != nil {
			if r.ctx.Err() != nil {
				return r.ctx.Err()
			}
			// One folder that cannot be read is passed over: nothing in it is marked missing.
			r.s.log().Warn("folder not read", "library", r.lib.Name, "folder", r.src.LocalPath(child), "error", err)
			continue
		}
		if err := r.walkFolder(child, entries, depth+1); err != nil {
			return err
		}
	}
	// A folder the last scan stored and this complete listing does not have is gone, with
	// everything under it.
	for _, child := range stored.Folders {
		if !here[child] {
			if err := r.flush(); err != nil {
				return err
			}
			if err := r.s.DB.MarkFolderGone(r.ctx, r.lib.ID, r.path.ID, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *run) ignored(rel string, dir bool) bool {
	for _, rules := range r.rules {
		if rules.Match(rel, dir) {
			return true
		}
	}
	return false
}

// unit is the rows of one title: the files that make it up and the items they are. A unit is
// always written in one transaction, so a title is never stored half made.
type unit struct {
	files []*found
	items []db.ImportItem
}

// store writes what differs in one folder, in transactions of about batch files.
func (r *run) store(f *folder, gone []db.FileRef) error {
	if err := r.findMoves(f, &gone); err != nil {
		return err
	}
	units := r.units(f)
	// Files no title claimed are stored too: artwork, sidecars of nothing, files with problems.
	rest := unit{}
	claimed := map[*found]bool{}
	for _, u := range units {
		for _, file := range u.files {
			claimed[file] = true
		}
	}
	for _, file := range f.files {
		if !claimed[file] && file.state != same {
			rest.files = append(rest.files, file)
		}
	}
	units = append(units, rest)

	// Folders share transactions: a library of one film to a folder would otherwise sync the
	// disk once for every film.
	batch := &r.pending
	if batch.PathID != r.path.ID {
		*batch = db.ImportBatch{LibraryID: r.lib.ID, PathID: r.path.ID, Scan: r.scan}
	}
	batch.Dirs = append(batch.Dirs, db.ImportDir{RelPath: f.rel})
	batch.Gone = append(batch.Gone, gone...)
	r.stats.FilesGone += len(gone)
	for _, u := range units {
		base := len(batch.Items)
		for _, file := range u.files {
			if file.written || file.state == same {
				continue
			}
			file.written = true
			if file.state == moved {
				batch.Moves = append(batch.Moves, db.ImportMove{FileID: file.from.ID, To: file.ref()})
				r.stats.FilesMoved++
				continue
			}
			in := db.ImportFile{FileRef: file.ref(), Role: file.role, Size: file.entry.Size, ModifiedNS: file.entry.ModifiedNS,
				Unprobed: file.role == db.FileMedia, Problem: file.problem}
			if file.stream != nil {
				in.Streams = []db.Stream{*file.stream}
			}
			batch.Files = append(batch.Files, in)
		}
		for _, item := range u.items {
			if item.ParentRow > 0 {
				item.ParentRow += base
			}
			batch.Items = append(batch.Items, item)
		}
		if len(batch.Files)+len(batch.Moves) >= r.batch {
			if err := r.flush(); err != nil {
				return err
			}
			// The rest of this folder goes into the next transaction, which names the folder again.
			batch.Dirs = append(batch.Dirs, db.ImportDir{RelPath: f.rel})
		}
	}
	return nil
}

// flush writes what the scan has gathered, in one transaction.
func (r *run) flush() error {
	batch := r.pending
	r.pending = db.ImportBatch{LibraryID: batch.LibraryID, PathID: batch.PathID, Scan: batch.Scan}
	if len(batch.Dirs) == 0 {
		return nil
	}
	stats, err := r.s.DB.Import(r.ctx, batch)
	r.stats.FilesNew += stats.FilesNew
	r.stats.FilesChanged += stats.FilesChanged
	r.stats.ItemsNew += stats.ItemsNew
	r.stats.RowsWritten += stats.RowsWritten
	if r.s.Progress != nil {
		r.s.Progress(r.stats)
	}
	return err
}

// findMoves recognises new media files that are stored files at a new place: the same size
// and modified time, where the stored place no longer has the file. Exactly one stored file
// must fit; with two there is no telling which moved.
func (r *run) findMoves(f *folder, gone *[]db.FileRef) error {
	for _, file := range f.media {
		if file.state != added {
			continue
		}
		places, err := r.s.DB.SameFiles(r.ctx, r.lib.ID, file.entry.Size, file.entry.ModifiedNS)
		if err != nil {
			return err
		}
		var from *db.FilePlace
		fits := 0
		for i := range places {
			place := &places[i]
			if r.claimed[place.ID] || path.Ext(place.Rel) != path.Ext(file.rel) {
				continue
			}
			if !place.Missing {
				if there, err := r.source(place.Root).Exists(r.ctx, place.Rel); err != nil || there {
					continue
				}
			}
			from = place
			fits++
		}
		if fits != 1 {
			continue
		}
		r.claimed[from.ID] = true
		file.state, file.from = moved, from
		// A file renamed inside this folder is not also gone from it.
		for i, ref := range *gone {
			if from.PathID == r.path.ID && path.Join(ref.Dir, ref.Name) == from.Rel {
				*gone = append((*gone)[:i], (*gone)[i+1:]...)
				break
			}
		}
	}
	return nil
}

// units groups a folder's files into titles, for the kind of library this is.
func (r *run) units(f *folder) []unit {
	for _, file := range f.media {
		reading := naming.Read(r.readPath(file), r.names)
		file.reading = &reading
	}
	switch r.lib.Kind {
	case db.LibraryMovies:
		return r.filmUnits(f)
	case db.LibraryShows, db.LibraryAnime:
		return r.showUnits(f)
	}
	return nil
}

// readPath is the path the naming rules read for a file. A disc is read as its folder.
func (r *run) readPath(file *found) string {
	if dir := path.Dir(file.rel); discFolder(path.Base(dir)) {
		return dir
	}
	return file.rel
}
