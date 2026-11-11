package api

import (
	"database/sql"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A new route that is not a read, or a new data-layer write that changes a title, its place,
// its rating, its tags, its files or a profile's state on it, must have a row in titleChanges
// or a reason in noRowNeeded. The writes are found by reading internal/db: every exported
// method of DB that reaches the writer and writes one of the tables below.
func TestEveryTitleChangeIsChecked(t *testing.T) {
	known := map[string]bool{}
	for _, change := range titleChanges {
		for _, name := range change.covers {
			known[name] = true
		}
	}
	found := map[string]bool{}
	for _, r := range newTestServer(t).Routes() {
		if !strings.HasPrefix(r.Pattern, "GET ") {
			found[r.Pattern] = true
		}
	}
	for _, name := range titleWrites(t) {
		found["db."+name] = true
	}
	for name := range found {
		if !known[name] && noRowNeeded[name] == "" {
			t.Errorf("%s may change what a profile sees: add a row to titleChanges that goes through it, "+
				"or say in noRowNeeded why it needs none", name)
		}
	}
	for name := range known {
		if !found[name] && !strings.HasPrefix(name, "GET ") {
			t.Errorf("a row of titleChanges names %s, which is not a route or a data-layer write that changes titles", name)
		}
	}
	for name := range noRowNeeded {
		if !found[name] {
			t.Errorf("noRowNeeded names %s, which is not a route or a data-layer write that changes titles", name)
		}
	}
}

// titleTables hold titles, their places, ratings, tags and files, and each profile's state on them.
var titleTables = []string{"items", "item_tags", "tags", "item_relations", "credits", "media", "media_parts", "files", "user_item_state"}

var writeStatement = regexp.MustCompile(`(?is)\b(?:INSERT(?:\s+OR\s+\w+)?\s+INTO|REPLACE\s+INTO|UPDATE|DELETE\s+FROM)\s+(\w+)`)

// titleWrites type-checks internal/db and returns its exported DB methods that reach the writer
// and, through anything they call in the package, write a title table.
func titleWrites(t *testing.T) []string {
	fset := token.NewFileSet()
	paths, err := filepath.Glob("../db/*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	// Only calls inside the package matter, so other modules' packages are left empty.
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: stdlibOnly{importer.Default()}, Error: func(error) {}}
	pkg, _ := conf.Check("db", fset, files, info)
	type body struct {
		calls  []*types.Func
		tables []string
		writer bool
	}
	bodies := map[*types.Func]*body{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			b := &body{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.Ident:
					if called, ok := info.Uses[n].(*types.Func); ok && called.Pkg() == pkg {
						b.calls = append(b.calls, called.Origin())
						b.writer = b.writer || called.Name() == "writeThen"
					}
				case *ast.BasicLit:
					for _, m := range writeStatement.FindAllStringSubmatch(n.Value, -1) {
						b.tables = append(b.tables, strings.ToLower(m[1]))
					}
				}
				return true
			})
			if obj, ok := info.Defs[fn.Name].(*types.Func); ok {
				bodies[obj] = b
			}
		}
	}
	var out []string
	for fn := range bodies {
		recv := fn.Type().(*types.Signature).Recv()
		if !fn.Exported() || recv == nil || !strings.HasSuffix(recv.Type().String(), ".DB") {
			continue
		}
		seen, writer, title := map[*types.Func]bool{}, false, false
		var walk func(*types.Func)
		walk = func(f *types.Func) {
			if seen[f] || bodies[f] == nil {
				return
			}
			seen[f] = true
			writer = writer || bodies[f].writer
			for _, table := range bodies[f].tables {
				title = title || slices.Contains(titleTables, table)
			}
			for _, called := range bodies[f].calls {
				walk(called)
			}
		}
		walk(fn)
		if writer && title {
			out = append(out, fn.Name())
		}
	}
	if !slices.Contains(out, "Import") {
		t.Fatalf("reading internal/db found %v and not Import, so it is not reading it right", out)
	}
	return out
}

type stdlibOnly struct{ std types.Importer }

func (s stdlibOnly) Import(path string) (*types.Package, error) {
	if !strings.Contains(strings.Split(path, "/")[0], ".") {
		return s.std.Import(path)
	}
	p := types.NewPackage(path, filepath.Base(path))
	p.MarkComplete()
	return p, nil
}

// What the rows do. Each builds an import batch shaped the way the scanner shapes one.

type library struct{ id, path int64 }

func (w *world) library(kind string) library {
	all, err := w.s.db.AllLibraries(background)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, l := range all {
		if l.Kind == kind {
			return library{l.ID, l.Paths[0].ID}
		}
	}
	w.t.Fatalf("no %s library", kind)
	return library{}
}

func (w *world) importBatch(b db.ImportBatch) {
	w.t.Helper()
	if b.Scan == 0 {
		b.Scan = 2
	}
	if _, err := w.s.db.Import(background, b); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) name(what string) string {
	w.n++
	return fmt.Sprintf("%s %d", what, w.n)
}

func mediaFile(dir, name string, width int) db.ImportFile {
	return db.ImportFile{FileRef: db.FileRef{Dir: dir, Name: name}, Role: db.FileMedia, Size: 1e6 + int64(len(dir+name)), ModifiedNS: 1,
		Container: "matroska", DurationMS: 3600e3, Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264", Width: width, Height: width * 9 / 16},
			{Kind: db.StreamAudio, Codec: "aac", Channels: 2}}}
}

// label is one of the seeded labels, the ones the restricted profiles block.
func (w *world) label(n int) db.TagName {
	return db.TagName{Kind: db.TagLabel, Name: w.labels[n%len(w.labels)].Name}
}

// seedPerson is a person the seeder credits, so a title added here changes what they list.
var seedPerson = db.ImportCredit{Name: "Seeded person", Person: db.ExternalID{Provider: "seed", ID: "p0"}, Role: db.RoleActor}

// film adds a film in its own folder with one version per file name, and returns its id.
func (w *world) film(rating *int, tags []db.TagName, files ...string) string {
	w.t.Helper()
	lib, dir := w.library(db.LibraryMovies), w.name("Added film")
	b := db.ImportBatch{LibraryID: lib.id, PathID: lib.path, Dirs: []db.ImportDir{{RelPath: dir}}}
	item := db.ImportItem{Kind: db.KindMovie, Keys: []db.ItemKey{db.FolderKey(lib.path, dir)}, Title: dir, Year: 2001,
		RatingAge: rating, Tags: tags, Credits: []db.ImportCredit{seedPerson}}
	for n, file := range files {
		f := mediaFile(dir, file, 1920)
		f.Unprobed = strings.HasPrefix(file, "unread")
		b.Files = append(b.Files, f)
		item.Versions = append(item.Versions, db.ImportVersion{Name: fmt.Sprint("v", n), Parts: []db.ImportPart{{File: f.FileRef}}})
	}
	b.Items = []db.ImportItem{item}
	w.importBatch(b)
	return w.idOf(`SELECT i.public_id FROM item_keys k JOIN items i ON i.id = k.item_id WHERE k.key = ?`, db.FolderKey(lib.path, dir).Key)
}

func (w *world) idOf(query string, args ...any) string {
	w.t.Helper()
	var id string
	if err := w.raw.QueryRow(query, args...).Scan(&id); err != nil {
		w.t.Fatalf("%s: %v", query, err)
	}
	return id
}

// show is a show the scanner would make from a folder with one season of three episodes. The
// season is the ninth, which no seeded show reaches, so merging one into a seeded show moves
// its episodes across.
type show struct {
	library
	dir, item string
	rating    int
}

const addedSeason = 9

func (w *world) showItems(s show) []db.ImportItem {
	rating := s.rating
	return []db.ImportItem{
		{Kind: db.KindShow, Keys: []db.ItemKey{db.FolderKey(s.path, s.dir)}, Title: s.dir, RatingAge: &rating, Tags: []db.TagName{{Kind: db.TagGenre, Name: "Drama"}}},
		{Kind: db.KindSeason, Title: fmt.Sprint("Season ", addedSeason), IndexNumber: intp(addedSeason), ParentRow: 1, ChildKey: fmt.Sprint("s", addedSeason)},
	}
}

func intp(n int) *int { return &n }

func (w *world) addShow(name string, rating int) show {
	w.t.Helper()
	lib := w.library(db.LibraryShows)
	s := show{library: lib, dir: w.name(name), rating: rating}
	season := fmt.Sprintf("%s/Season %02d", s.dir, addedSeason)
	b := db.ImportBatch{LibraryID: lib.id, PathID: lib.path, Dirs: []db.ImportDir{{RelPath: s.dir}, {RelPath: season}}, Items: w.showItems(s)}
	for e := 1; e <= 3; e++ {
		f := mediaFile(season, fmt.Sprintf("%s S%02dE%02d.mkv", s.dir, addedSeason, e), 1280)
		b.Files = append(b.Files, f)
		b.Items = append(b.Items, db.ImportItem{Kind: db.KindEpisode, Title: fmt.Sprint("Episode ", e), IndexNumber: intp(e), ParentIndex: intp(addedSeason),
			ParentRow: 2, ChildKey: fmt.Sprint("e", e), Keys: []db.ItemKey{db.FileKey(lib.path, season+"/"+f.Name, 0)},
			Credits: []db.ImportCredit{seedPerson}, Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: f.FileRef}}}}})
	}
	w.importBatch(b)
	s.item = w.idOf(`SELECT i.public_id FROM item_keys k JOIN items i ON i.id = k.item_id WHERE k.key = ?`, db.FolderKey(lib.path, s.dir).Key)
	return s
}

// addTitles adds what a scan of new folders adds, beside the seeded titles.
func (w *world) addTitles() {
	w.t.Helper()
	w.film(intp(8), []db.TagName{w.label(1)}, "film.mkv")
	w.film(nil, nil, "film.mkv")
	w.addShow("Rated", 18)
	// An episode turns up in a season of a seeded show for adults, and an album under a seeded artist.
	lib := w.library(db.LibraryShows)
	var season string
	if err := w.raw.QueryRow(`SELECT k.key FROM item_keys k JOIN items s ON s.id = k.item_id JOIN items sh ON sh.id = s.parent_id
		WHERE s.kind = ? AND k.kind = ? AND sh.rating_age >= 17 ORDER BY s.id LIMIT 1`, db.KindSeason, db.KeyName).Scan(&season); err != nil {
		w.t.Fatal(err)
	}
	f := mediaFile(season, "late arrival.mkv", 1920)
	w.importBatch(db.ImportBatch{LibraryID: lib.id, PathID: lib.path, Files: []db.ImportFile{f}, Items: []db.ImportItem{{Kind: db.KindEpisode,
		Title: "Late arrival", Parent: &db.ItemKey{Kind: db.KeyName, Key: season}, ChildKey: "e90", IndexNumber: intp(90),
		Keys: []db.ItemKey{db.FileKey(lib.path, season+"/"+f.Name, 0)}, Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: f.FileRef}}}}}}})
	music := w.library(db.LibraryMusic)
	var artist string
	if err := w.raw.QueryRow(`SELECT k.key FROM item_keys k JOIN items a ON a.id = k.item_id WHERE a.kind = ? AND k.kind = ? ORDER BY a.id LIMIT 1`,
		db.KindArtist, db.KeyName).Scan(&artist); err != nil {
		w.t.Fatal(err)
	}
	album := artist + "/" + w.name("Added album")
	b := db.ImportBatch{LibraryID: music.id, PathID: music.path, Dirs: []db.ImportDir{{RelPath: album}}, Items: []db.ImportItem{{Kind: db.KindAlbum,
		Title: album, Parent: &db.ItemKey{Kind: db.KeyName, Key: artist}, ChildKey: "album|" + album, Keys: []db.ItemKey{db.FolderKey(music.path, album)},
		Tags: []db.TagName{w.label(2), {Kind: db.TagGenre, Name: "Jazz"}}}}}
	for n := 1; n <= 2; n++ {
		f := db.ImportFile{FileRef: db.FileRef{Dir: album, Name: fmt.Sprintf("%02d.flac", n)}, Role: db.FileMedia, Size: int64(n), ModifiedNS: 1,
			Container: "flac", DurationMS: 200e3, Streams: []db.Stream{{Kind: db.StreamAudio, Codec: "flac", Channels: 2}}}
		b.Files = append(b.Files, f)
		b.Items = append(b.Items, db.ImportItem{Kind: db.KindTrack, Title: f.Name, ParentRow: 1, ChildKey: fmt.Sprintf("d1t%d", n), IndexNumber: intp(n),
			ParentIndex: intp(1), Keys: []db.ItemKey{db.FileKey(music.path, album+"/"+f.Name, 0)},
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: f.FileRef}}}}})
	}
	w.importBatch(b)
}

// moveEpisode moves the first episode's file of one show into the season folder of another, as
// a scan finds it: the same file at a new place, under the other show's names.
func (w *world) moveEpisode(from, to show) {
	w.t.Helper()
	season := fmt.Sprintf("%s/Season %02d", to.dir, addedSeason)
	var file int64
	if err := w.raw.QueryRow(`SELECT f.id FROM files f JOIN directories d ON d.id = f.directory_id WHERE d.rel_path = ? AND f.name LIKE '%E01.mkv'`,
		fmt.Sprintf("%s/Season %02d", from.dir, addedSeason)).Scan(&file); err != nil {
		w.t.Fatal(err)
	}
	moved := db.FileRef{Dir: season, Name: fmt.Sprintf("%s S%02dE09.mkv", to.dir, addedSeason)}
	items := append(w.showItems(to), db.ImportItem{Kind: db.KindEpisode, Title: "Episode 9", IndexNumber: intp(9), ParentIndex: intp(addedSeason),
		ParentRow: 2, ChildKey: "e9", Keys: []db.ItemKey{db.FileKey(to.path, season+"/"+moved.Name, 0)},
		Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: moved}}}}})
	w.importBatch(db.ImportBatch{LibraryID: to.library.id, PathID: to.path, Dirs: []db.ImportDir{{RelPath: season}},
		Moves: []db.ImportMove{{FileID: file, To: moved}}, Items: items})
}

// goneAndBack removes a rated, labelled film's file, empties the trash, and puts the file back.
func (w *world) goneAndBack() map[string]string {
	w.t.Helper()
	before := w.film(intp(17), []db.TagName{w.label(0)}, "film.mkv")
	lib := w.library(db.LibraryMovies)
	dir := fmt.Sprintf("Added film %d", w.n)
	w.check("the film is added")
	w.importBatch(db.ImportBatch{LibraryID: lib.id, PathID: lib.path, Gone: []db.FileRef{{Dir: dir, Name: "film.mkv"}}})
	w.check("its file is gone")
	if _, err := w.s.db.EmptyTrash(background, lib.id, time.Now().Add(time.Minute)); err != nil {
		w.t.Fatal(err)
	}
	w.check("the trash is emptied")
	f := mediaFile(dir, "film.mkv", 1920)
	w.importBatch(db.ImportBatch{LibraryID: lib.id, PathID: lib.path, Dirs: []db.ImportDir{{RelPath: dir}}, Files: []db.ImportFile{f},
		Items: []db.ImportItem{{Kind: db.KindMovie, Keys: []db.ItemKey{db.FolderKey(lib.path, dir)}, Title: dir, Year: 2001, RatingAge: intp(17),
			Tags: []db.TagName{w.label(0)}, Credits: []db.ImportCredit{seedPerson}, Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: f.FileRef}}}}}}})
	return map[string]string{w.idOf(`SELECT i.public_id FROM item_keys k JOIN items i ON i.id = k.item_id WHERE k.key = ?`,
		db.FolderKey(lib.path, dir).Key): before}
}

// seasonGone marks the last season folder of a seeded show with two seasons or more gone, then
// empties the trash, which takes the season off the show.
func (w *world) seasonGone() {
	w.t.Helper()
	lib := w.library(db.LibraryShows)
	var season string
	if err := w.raw.QueryRow(`SELECT k.key FROM item_keys k JOIN items s ON s.id = k.item_id JOIN items sh ON sh.id = s.parent_id
		WHERE s.kind = ? AND k.kind = ? AND sh.child_count > 1 ORDER BY sh.title, s.index_number DESC LIMIT 1`, db.KindSeason, db.KeyName).Scan(&season); err != nil {
		w.t.Fatal(err)
	}
	if err := w.s.db.MarkFolderGone(background, lib.id, lib.path, season); err != nil {
		w.t.Fatal(err)
	}
	w.check("the season is gone")
	if _, err := w.s.db.EmptyTrash(background, lib.id, time.Now().Add(time.Minute)); err != nil {
		w.t.Fatal(err)
	}
}

// probeUnread stores a film the scan has not read, then what the probe pass finds in it.
func (w *world) probeUnread() {
	w.t.Helper()
	film := w.film(intp(8), []db.TagName{w.label(1)}, "unread.mkv")
	w.check("the unread film is added")
	var file, size, modified int64
	if err := w.raw.QueryRow(`SELECT f.id, f.size, f.modified_ns FROM files f JOIN media_parts p ON p.file_id = f.id JOIN media m ON m.id = p.media_id
		JOIN items i ON i.id = m.item_id WHERE i.public_id = ?`, film).Scan(&file, &size, &modified); err != nil {
		w.t.Fatal(err)
	}
	if err := w.s.db.StoreProbes(background, []db.Probe{{FileID: file, Size: size, ModifiedNS: modified, Container: "matroska", DurationMS: 5400e3,
		Streams: mediaFile("", "", 3840).Streams}}); err != nil {
		w.t.Fatal(err)
	}
}

// split splits the second version off a film with a label.
func (w *world) split() map[string]string {
	w.t.Helper()
	film := w.film(intp(8), []db.TagName{w.label(0)}, "film.mkv", "film - extended.mkv")
	w.check("the film is added")
	version := w.idOf(`SELECT m.public_id FROM media m JOIN items i ON i.id = m.item_id WHERE i.public_id = ? ORDER BY m.position LIMIT 1 OFFSET 1`, film)
	made, err := w.s.db.SplitVersion(background, version)
	if err != nil {
		w.t.Fatal(err)
	}
	return map[string]string{made: film}
}

// moods gives a film a mood tag beside a label, then turns title data off.
func (w *world) moods() {
	w.t.Helper()
	w.film(intp(8), []db.TagName{w.label(1), {Kind: db.TagMood, Name: "Cozy"}}, "film.mkv")
	w.check("the film is added")
	if _, err := w.s.db.ApplyTitleData(background, 0, 100000); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.s.db.RemoveTitleDataTags(background, 100000); err != nil {
		w.t.Fatal(err)
	}
}

// markAndUndo has the profile with a rating limit mark a show it may see watched, then take it back.
func (w *world) markAndUndo() {
	w.t.Helper()
	v, f := w.viewers[4], w.read()
	var target string
	for _, id := range f.byTitle() {
		if f.items[id].kind == db.KindShow && !f.hidden(v.profile, id) {
			target = id
			break
		}
	}
	if target == "" {
		w.t.Fatal("the profile with a rating limit may see no show")
	}
	// Every profile starts with every show watched, so it is first made unwatched.
	if code := status(w.t, w.s, "PUT", "/v1/items/"+target+"/state", v.token, map[string]bool{"watched": false}); code != 200 {
		w.t.Fatalf("marking a show unwatched answered %d", code)
	}
	var reply StateReply
	if code := call(w.t, w.s, "PUT", "/v1/items/"+target+"/state", v.token, map[string]bool{"watched": true}, &reply).Code; code != 200 || reply.Undo == nil {
		w.t.Fatalf("marking a show watched answered %d without an undo", code)
	}
	w.check("the show is marked")
	if code := status(w.t, w.s, "POST", "/v1/items/state:undo", v.token, UndoRequest{Token: reply.Undo.Token}); code != 204 {
		w.t.Fatalf("undo answered %d", code)
	}
}

// ownerState has the owner mark a show for adults watched and play a film part-way.
func (w *world) ownerState() {
	w.t.Helper()
	owner, f := w.viewers[0], w.read()
	claims, err := w.s.tokens.CheckAccess(owner.token, time.Now())
	if err != nil {
		w.t.Fatal(err)
	}
	v := db.Viewer{UserID: claims.UserID, ProfileID: claims.ProfileID}
	var show, film string
	for _, id := range f.byTitle() {
		it := f.items[id]
		if it.kind == db.KindShow && it.rating.Int64 >= 17 && show == "" {
			show = id
		}
		if it.kind == db.KindMovie && film == "" {
			film = id
		}
	}
	yes := true
	if _, refusals, err := w.s.db.SetStates(background, db.User, v, []db.ItemEdit{{ItemID: show, StateEdit: db.StateEdit{Watched: &yes}}}); err != nil || refusals[0] != nil {
		w.t.Fatalf("marking a show watched: %v %v", err, refusals)
	}
	var device int64
	if err := w.raw.QueryRow(`SELECT id FROM devices LIMIT 1`).Scan(&device); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.s.db.SaveProgress(background, db.User, []db.Progress{{Viewer: v, ItemID: film, DeviceID: device, PlayID: "play-1",
		PositionMS: 600e3, At: time.Now(), WatchedMS: 600e3}}); err != nil {
		w.t.Fatal(err)
	}
}

// narrow has the owner tighten one member profile's restrictions and limit another member to
// two libraries, through the routes an app uses.
func (w *world) narrow() {
	w.t.Helper()
	members := get[Page[Member]](w.t, w.s, w.owner, "/v1/admin/members").Items
	memberID := func(username string) string {
		for _, m := range members {
			if m.Username == username {
				return m.ID
			}
		}
		w.t.Fatalf("no member %s", username)
		return ""
	}
	libraries := map[string]string{}
	for _, l := range get[Libraries](w.t, w.s, w.owner, "/v1/libraries").Items {
		libraries[l.Kind] = l.ID
	}
	labels, err := w.s.db.Tags(background, db.TagLabel)
	if err != nil {
		w.t.Fatal(err)
	}
	limited := memberID("viewer-3")
	profile := get[Page[Profile]](w.t, w.s, w.owner, "/v1/profiles?member="+limited).Items[0].ID
	age := 8
	body := Restrictions{MaxRatingAge: &age, BlockedLabels: []string{labels[len(labels)-1].PublicID}, Libraries: []string{libraries[db.LibraryMovies], libraries[db.LibraryShows]}}
	if code := status(w.t, w.s, "PUT", "/v1/profiles/"+profile+"/restrictions?member="+limited, w.owner, body); code != 200 {
		w.t.Fatalf("narrowing the restrictions answered %d", code)
	}
	no, music := false, []string{libraries[db.LibraryMusic], libraries[db.LibraryBooks]}
	if code := status(w.t, w.s, "PATCH", "/v1/admin/members/"+memberID("viewer-0"), w.owner, MemberEdit{AllLibraries: &no, Libraries: &music}); code != 200 {
		w.t.Fatalf("limiting a member's libraries answered %d", code)
	}
	w.remint()
}

// remint gives the seeded members new tokens, for a change that ends their sign-ins.
func (w *world) remint() {
	var rows []db.Viewer
	w.query(func(r *sql.Rows) error {
		var v db.Viewer
		err := r.Scan(&v.UserID, &v.ProfileID)
		rows = append(rows, v)
		return err
	}, `SELECT user_id, id FROM profiles`)
	for n, v := range w.viewers[1:] {
		for _, row := range rows {
			if row.ProfileID == v.profile {
				w.viewers[n+1].token = memberToken(w.t, w.s, w.owner, row)
			}
		}
	}
}
