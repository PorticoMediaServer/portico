package scan

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/naming"
)

// fold reduces a title to its letters and digits in lower case, so "Heat", "heat." and "HEAT"
// are one name.
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, norm.NFC.String(s))
}

func nameKey(kind, title string, year int) db.ItemKey {
	return db.ItemKey{Kind: db.KeyName, Key: fmt.Sprintf("%s|%s|%d", kind, fold(title), year)}
}

// providerKeys are the ids a name carries ("{tmdb-603}"), as keys and as external ids. An id
// on a folder is not taken when the file names a different title: the file wins.
func providerKeys(r *naming.Reading, kind string) ([]db.ItemKey, []db.ExternalID) {
	preferred, _ := r.PreferredTitle()
	fileWins := false
	for _, t := range r.Titles {
		fileWins = fileWins || t.Source == naming.FileName && t.Confidence >= .8
	}
	var keys []db.ItemKey
	var ids []db.ExternalID
	seen := map[string]bool{}
	for _, e := range r.ProviderIDs {
		if fileWins && e.Source != naming.FileName {
			agrees := false
			for _, t := range r.Titles {
				agrees = agrees || t.Path == e.Path && fold(t.Value) == fold(preferred)
			}
			if !agrees {
				continue
			}
		}
		if seen[e.Value.Provider] {
			continue
		}
		seen[e.Value.Provider] = true
		keys = append(keys, db.ItemKey{Kind: db.KeyProvider, Key: e.Value.Provider + ":" + kind + ":" + e.Value.ID})
		ids = append(ids, db.ExternalID{Provider: e.Value.Provider, ID: e.Value.ID})
	}
	return keys, ids
}

// title is the title and year the names give a file, falling back to its cleaned file name.
func title(file *found) (string, int) {
	name, year := file.reading.PreferredTitle()
	if name == "" {
		name = strings.TrimSpace(strings.NewReplacer(".", " ", "_", " ").Replace(strings.TrimSuffix(path.Base(file.rel), path.Ext(file.rel))))
	}
	if name == "" {
		name = path.Base(file.rel)
	}
	return name, year
}

func first[T any](evidence []naming.Evidence[T]) (value T) {
	if len(evidence) > 0 {
		value = evidence[0].Value
	}
	return value
}

// version is the files that play one version of one title, in order: one file, or the parts
// of a film split across several.
type version struct {
	parts   []*found
	numbers []int // each part's number
	side    []*found
	edition string
	label   string
}

func (v *version) touched() bool {
	for _, file := range v.parts {
		if file.touched() {
			return true
		}
	}
	for _, file := range v.side {
		if file.state != same {
			return true
		}
	}
	return false
}

// order puts a split film's parts in the order they play.
func (v *version) order() {
	sort.SliceStable(v.parts, func(a, b int) bool { return v.numbers[a] < v.numbers[b] })
}

func (v *version) files() []*found { return append(append([]*found(nil), v.parts...), v.side...) }

// stored is the version as the database takes it, and the file keys that name its item.
func (r *run) stored(v *version, shared bool, n int) (db.ImportVersion, []db.ItemKey) {
	out := db.ImportVersion{Name: v.label, Edition: v.edition, Shared: shared}
	var keys []db.ItemKey
	for _, file := range v.parts {
		out.Parts = append(out.Parts, db.ImportPart{File: file.ref()})
		keys = append(keys, db.FileKey(r.path.ID, file.rel, n))
	}
	for _, file := range v.side {
		out.Sidecars = append(out.Sidecars, file.ref())
	}
	return out, keys
}

// versions groups a folder's media files into versions by what makes two files the same
// version: the grouping key and, for a split film, everything but the part number. Subtitle
// and lyrics files join the version of the media file whose name theirs starts with.
func (r *run) versions(f *folder, key func(*found) (string, bool)) []*version {
	var out []*version
	byKey := map[string]*version{}
	byStem := map[string]*version{}
	for _, file := range f.media {
		group, ok := key(file)
		if !ok {
			continue
		}
		part := first(file.reading.Parts)
		v := byKey[group]
		if v == nil || part == 0 {
			v = &version{edition: first(file.reading.Editions), label: first(file.reading.Versions)}
			out = append(out, v)
			if part != 0 {
				byKey[group] = v
			}
		}
		v.parts = append(v.parts, file)
		v.numbers = append(v.numbers, part)
		byStem[stem(file.entry.Name)] = v
	}
	for _, v := range out {
		v.order()
	}
	for _, file := range f.files {
		if file.role != db.FileSubtitle && file.role != db.FileLyrics {
			continue
		}
		// The longest media name this name starts with: "Film.en.srt" is Film's, not Fi's.
		name, best := stem(file.entry.Name), ""
		for candidate := range byStem {
			if (name == candidate || strings.HasPrefix(name, candidate+".")) && len(candidate) > len(best) {
				best = candidate
			}
		}
		if best == "" {
			continue
		}
		if file.role == db.FileSubtitle {
			stream := subtitleStream(best, file.entry.Name)
			file.stream = &stream
		}
		byStem[best].side = append(byStem[best].side, file)
	}
	return out
}

// folderOf is the folder a file's title has to itself, or "" for a file at the top of the
// library. A disc's title folder is the one that holds the disc.
func (r *run) folderOf(file *found) string {
	dir := path.Dir(r.readPath(file))
	if dir == "." {
		return ""
	}
	return dir
}

// filmUnits turns a film library's folder into titles: one item for each version, found by
// its own file first, so that a version the owner split off stays its own title, and by the
// film's ids and name next, so that two files of one film are two versions of it.
func (r *run) filmUnits(f *folder) []unit {
	versions := r.versions(f, func(file *found) (string, bool) {
		name, year := title(file)
		return fmt.Sprintf("%s|%d|%s|%s", fold(name), year, first(file.reading.Editions), first(file.reading.Versions)), true
	})
	// A film has the folder to itself when every file in it is that film.
	alone := f.rel != "" && len(versions) > 0
	for _, v := range versions {
		a, yearA := title(versions[0].parts[0])
		b, yearB := title(v.parts[0])
		alone = alone && fold(a) == fold(b) && yearA == yearB
	}
	var units []unit
	for _, v := range versions {
		// A film whose NFO file or picture changed is read again, though its own file did not.
		reread := !v.touched() && f.sidecarChanged(stem(v.parts[0].entry.Name), alone)
		if !v.touched() && !reread {
			continue
		}
		u := r.filmUnit(v)
		if item := &u.items[0]; item.Kind == db.KindMovie {
			item.Reread = reread
			item.Images = r.pictures(f.files, stem(v.parts[0].entry.Name), db.ImagePoster, alone, reread)
		}
		units = append(units, u)
	}
	return units
}

func (r *run) filmUnit(v *version) unit {
	lead := v.parts[0]
	stored, keys := r.stored(v, false, 0)
	if extra, ok := r.extra(lead, stored, keys); ok {
		return unit{files: v.files(), items: []db.ImportItem{extra}}
	}
	name, year := title(lead)
	provider, ids := providerKeys(lead.reading, "movie")
	keys = append(append(keys, provider...), nameKey("movie", name, year))
	if folder := r.folderOf(lead); folder != "" {
		keys = append(keys, db.FolderKey(r.path.ID, folder))
	}
	item := db.ImportItem{Kind: db.KindMovie, Title: name, Year: year, Keys: keys, ExternalIDs: ids,
		Versions: []db.ImportVersion{stored}}
	describe(&item, r.nfo("movie", filmNFOs(r.readPath(lead))...), "movie")
	return unit{files: v.files(), items: []db.ImportItem{item}}
}

// extra makes the item of a file that is an extra of another title: a trailer, a featurette,
// anything in a title's Extras folder. It is tied to the title that owns its folder.
func (r *run) extra(file *found, stored db.ImportVersion, keys []db.ItemKey) (db.ImportItem, bool) {
	extras := naming.ReadExtras(file.rel, r.names)
	if len(extras) == 0 {
		return db.ImportItem{}, false
	}
	relation := db.RelationExtra
	if extras[0].Value.Kind == "trailer" {
		relation = db.RelationTrailer
	}
	name := naming.ExtraTitle(path.Base(file.rel))
	if name == "" {
		name, _ = title(file)
	}
	return db.ImportItem{Kind: db.KindExtra, Title: name, Keys: keys, Versions: []db.ImportVersion{stored},
		ExtraOf: []db.ItemKey{db.FolderKey(r.path.ID, extras[0].Value.ParentPath)}, ExtraKind: relation}, true
}

// episode is where the names put a file in a show.
type episode struct {
	show    string
	year    int
	season  int
	numbers []int
	date    string
	placed  bool
}

// readEpisode places a file by season and episode number, by air date, or by a lone number
// for a show without seasons. A file that cannot be placed is still a title.
func readEpisode(file *found) episode {
	r := file.reading
	ep := episode{season: -1}
	ep.show, ep.year = r.PreferredTitle()
	switch {
	case len(r.Diagnostics) > 0 || ep.show == "":
		return ep
	case len(r.AirDates) > 0:
		ep.date = r.AirDates[0].Value
		date, err := time.Parse("2006-01-02", ep.date)
		if err != nil {
			return ep
		}
		ep.season, ep.numbers = date.Year(), []int{date.YearDay()}
	case len(r.Episodes) > 0 && len(r.Seasons) > 0:
		ep.season, ep.numbers = r.Seasons[0].Value, r.Episodes[0].Value
	case len(r.AbsoluteNumbers) > 0:
		ep.season, ep.numbers = 1, []int{r.AbsoluteNumbers[0].Value}
	default:
		return ep
	}
	for _, n := range ep.numbers {
		if n < 0 || n > 9999 {
			return ep
		}
	}
	ep.placed = len(ep.numbers) > 0
	return ep
}

// showFolder is the show's own folder: the nearest folder above the file that is not a
// season folder, or "" for a file loose at the top of the library.
func showFolder(rel string) string {
	dir := path.Dir(rel)
	for dir != "." {
		if _, season := naming.SeasonFolder(path.Base(dir)); !season {
			return dir
		}
		dir = path.Dir(dir)
	}
	return ""
}

// showUnits turns a show library's folder into titles: for each file its show, its season and
// the episodes it holds. The show is found by its ids, its name, then its folder; a season and
// an episode by their numbers under what holds them.
func (r *run) showUnits(f *folder) []unit {
	episodes := map[*found]episode{}
	versions := r.versions(f, func(file *found) (string, bool) {
		ep := readEpisode(file)
		episodes[file] = ep
		return fmt.Sprintf("%s|%d|%d|%v|%s", fold(ep.show), ep.year, ep.season, ep.numbers, first(file.reading.Editions)), true
	})
	var units []unit
	for _, v := range versions {
		lead := v.parts[0]
		ep := episodes[lead]
		// An episode whose own NFO file or picture changed is read again.
		reread := !v.touched() && ep.placed && len(ep.numbers) == 1 && f.sidecarChanged(stem(lead.entry.Name), false)
		if !v.touched() && !reread {
			continue
		}
		if reread {
			units = append(units, unit{items: r.episodeReread(f, v, ep)})
			continue
		}
		u := unit{files: v.files()}
		stored, keys := r.stored(v, false, 0)
		if extra, ok := r.extra(lead, stored, keys); ok {
			u.items = []db.ImportItem{extra}
		} else if !ep.placed {
			// It plays, so it is in the library, under its cleaned file name.
			name, year := title(lead)
			u.items = []db.ImportItem{{Kind: db.KindMovie, Title: name, Year: year, Keys: append(keys, nameKey("movie", name, year)),
				Versions: []db.ImportVersion{stored}}}
		} else {
			u.items = r.episodeItems(f, v, ep)
		}
		units = append(units, u)
	}
	return units
}

func (r *run) episodeItems(f *folder, v *version, ep episode) []db.ImportItem {
	lead := v.parts[0]
	provider, ids := providerKeys(lead.reading, "show")
	showKeys := append(provider, nameKey("show", ep.show, ep.year))
	if folder := showFolder(lead.rel); folder != "" {
		showKeys = append(showKeys, db.FolderKey(r.path.ID, folder))
	}
	show := db.ImportItem{Kind: db.KindShow, Title: ep.show, Year: ep.year, Keys: showKeys, ExternalIDs: ids}
	var local showLocal
	if folder := showFolder(lead.rel); folder != "" {
		describe(&show, r.nfo("show", path.Join(folder, "tvshow.nfo")), "show")
		// The show's and its seasons' pictures are in the show's own folder, which is this
		// folder or one above it.
		if folder == f.rel {
			local = r.showPictures(folder, f.files, false)
		} else {
			local = r.showPictures(folder, nil, false)
		}
		show.Images = local.show
	}
	if lead.state == moved {
		// The show follows its files when their old folder is gone: the folder was renamed.
		oldFolder := path.Dir(lead.from.Rel)
		if there, err := r.source(lead.from.Root).Exists(r.ctx, oldFolder); err == nil && !there {
			ref := lead.ref()
			show.Follow = &ref
		}
	}
	seasonTitle := fmt.Sprintf("Season %d", ep.season)
	if ep.season == 0 {
		seasonTitle = "Specials"
	}
	season := db.ImportItem{Kind: db.KindSeason, Title: seasonTitle, IndexNumber: &ep.season, ParentRow: 1, ChildKey: seasonKey(ep.season),
		Images: local.seasons[ep.season]}
	if _, isSeason := naming.SeasonFolder(path.Base(f.rel)); isSeason {
		season.Images = append(season.Images, r.pictures(f.files, "", 0, true, false)...)
	}
	items := []db.ImportItem{show, season}
	for n, number := range ep.numbers {
		place := 0
		if len(ep.numbers) > 1 {
			place = n + 1
		}
		stored, keys := r.stored(v, len(ep.numbers) > 1, place)
		name := fmt.Sprintf("Episode %d", number)
		if ep.date != "" {
			name = ep.date
		}
		item := db.ImportItem{Kind: db.KindEpisode, Title: name, IndexNumber: &number, ParentIndex: &ep.season,
			ReleaseDate: ep.date, ParentRow: 2, ChildKey: fmt.Sprintf("e%d", number), Keys: keys, Versions: []db.ImportVersion{stored}}
		if len(ep.numbers) == 1 {
			r.episodeLocal(f, lead, &item, false)
		}
		items = append(items, item)
	}
	return items
}

// episodeLocal gives an episode what is beside its file: an NFO file of the same name, and
// a picture ("<name>-thumb.jpg" or "<name>.jpg").
func (r *run) episodeLocal(f *folder, lead *found, item *db.ImportItem, changedOnly bool) {
	describe(item, r.nfo("episode", strings.TrimSuffix(lead.rel, path.Ext(lead.rel))+".nfo"), "episode")
	item.Images = r.pictures(f.files, stem(lead.entry.Name), db.ImageThumb, false, changedOnly)
}

// episodeReread is an episode already stored, found by its file, with what is beside it now.
func (r *run) episodeReread(f *folder, v *version, ep episode) []db.ImportItem {
	_, keys := r.stored(v, false, 0)
	item := db.ImportItem{Kind: db.KindEpisode, Keys: keys, Reread: true}
	r.episodeLocal(f, v.parts[0], &item, true)
	return []db.ImportItem{item}
}
