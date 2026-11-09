// Package seed makes a synthetic library for load tests and local use: films, shows, music and
// books with the awkward cases a real library has (two versions of a film, a film split across
// two files, one file holding two episodes, a whole album in one file with a cue sheet). The
// same seed and size always give the same titles, files and credits. It runs no SQL: it hands
// batches to the same import a scan uses.
package seed

import (
	"fmt"
	"math/rand/v2"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Config sizes the library. Items is the number of playable items (films, episodes, tracks
// and books); shows, seasons, artists and albums come on top. At 1,000,000 the result is about
// 1.2M files, 3M streams, 6M credits and 300,000 people.
type Config struct {
	Seed       uint64
	Items      int
	BatchFiles int // files per import batch; 0 means 40
}

// LibrarySpec is one of the libraries the seeder fills.
type LibrarySpec struct {
	Name string
	Kind string
	Path string
}

// Libraries are the four libraries of the synthetic server.
var Libraries = []LibrarySpec{
	{"Films", db.LibraryMovies, "/media/films"},
	{"Shows", db.LibraryShows, "/media/shows"},
	{"Music", db.LibraryMusic, "/media/music"},
	{"Books", db.LibraryBooks, "/media/books"},
}

// share is each library's part of the playable items.
var share = map[string]float64{db.LibraryMovies: 0.15, db.LibraryShows: 0.45, db.LibraryMusic: 0.35, db.LibraryBooks: 0.05}

// Leaves is how many playable items a library kind gets.
func (c Config) Leaves(kind string) int {
	if kind == db.LibraryBooks {
		return c.Items - c.Leaves(db.LibraryMovies) - c.Leaves(db.LibraryShows) - c.Leaves(db.LibraryMusic)
	}
	return int(float64(c.Items) * share[kind])
}

// people is the size of the pool credits draw from.
func (c Config) people() int { return max(50, c.Items*3/10) }

// builder collects one batch and hands it on when it is full.
type builder struct {
	cfg   Config
	lib   db.Library
	batch db.ImportBatch
	emit  func(db.ImportBatch) error
}

// Batches generates the whole of one library, calling emit for each batch in order.
func Batches(cfg Config, lib db.Library, emit func(db.ImportBatch) error) error {
	if cfg.BatchFiles <= 0 {
		cfg.BatchFiles = 40
	}
	b := &builder{cfg: cfg, lib: lib, emit: emit}
	b.reset()
	b.dir("")
	var err error
	switch lib.Kind {
	case db.LibraryMovies:
		err = b.movies()
	case db.LibraryShows:
		err = b.shows()
	case db.LibraryMusic:
		err = b.music()
	case db.LibraryBooks:
		err = b.books()
	}
	if err != nil {
		return err
	}
	return b.flush(true)
}

func (b *builder) reset() {
	b.batch = db.ImportBatch{LibraryID: b.lib.ID, PathID: b.lib.Paths[0].ID, Scan: 1}
}

// flush hands the batch on if it is full, or if force is set and it holds anything.
func (b *builder) flush(force bool) error {
	if len(b.batch.Files) < b.cfg.BatchFiles && !force || len(b.batch.Files)+len(b.batch.Items)+len(b.batch.Dirs) == 0 {
		return nil
	}
	batch := b.batch
	b.reset()
	return b.emit(batch)
}

// rng is the random source for one title, so a title is the same whatever came before it.
func (b *builder) rng(kind string, n int) *rand.Rand {
	return rand.New(rand.NewPCG(b.cfg.Seed, uint64(len(kind))<<56|uint64(kind[0])<<48|uint64(n)))
}

// dir adds a folder. Its modified time is fixed, so a second run sees it unchanged.
func (b *builder) dir(rel string) {
	b.batch.Dirs = append(b.batch.Dirs, db.ImportDir{RelPath: rel, ModifiedNS: baseTime * 1e6})
}

func (b *builder) file(f db.ImportFile) db.FileRef {
	f.ModifiedNS = baseTime * 1e6
	b.batch.Files = append(b.batch.Files, f)
	return f.FileRef
}

func (b *builder) item(it db.ImportItem) { b.batch.Items = append(b.batch.Items, it) }

// baseTime is 1 January 2024 in milliseconds; titles are "added" over the three years after.
const baseTime = 1704067200000

func addedAt(r *rand.Rand) int64 { return baseTime + r.Int64N(3*365*24*3600*1000) }

func intp(n int) *int       { return &n }
func int64p(n int64) *int64 { return &n }

var ratings = []struct {
	name string
	age  *int
}{{"G", intp(0)}, {"PG", intp(8)}, {"PG-13", intp(13)}, {"R", intp(17)}, {"NC-17", intp(18)}, {"", nil}}

var genres = []string{"Action", "Adventure", "Animation", "Comedy", "Crime", "Documentary", "Drama", "Family", "Fantasy",
	"History", "Horror", "Music", "Mystery", "Romance", "Sci-Fi", "Thriller", "War", "Western", "Sport", "Musical"}

var musicGenres = []string{"Rock", "Pop", "Jazz", "Classical", "Hip-Hop", "Electronic", "Folk", "Metal", "Blues", "Country",
	"Reggae", "Soul", "Ambient", "Punk"}

// Labels are the owner's own tags. A restricted profile can have any of them blocked.
var Labels = []string{"Late night", "Kids", "Holiday"}

func tags(r *rand.Rand, pool []string, studios bool) []db.TagName {
	out := []db.TagName{{Kind: db.TagGenre, Name: pool[r.IntN(len(pool))]}}
	if second := pool[r.IntN(len(pool))]; second != out[0].Name {
		out = append(out, db.TagName{Kind: db.TagGenre, Name: second})
	}
	if studios {
		out = append(out, db.TagName{Kind: db.TagStudio, Name: "Studio " + word(r) + fmt.Sprint(r.IntN(40))})
	}
	if r.IntN(12) == 0 {
		out = append(out, db.TagName{Kind: db.TagLabel, Name: Labels[r.IntN(len(Labels))]})
	}
	return out
}

var syllables = []string{"ka", "lo", "mi", "ren", "sta", "vor", "an", "el", "bri", "dor", "fen", "gal", "har", "is", "jun", "kel",
	"lum", "mar", "nor", "ost", "pel", "quin", "ros", "sil", "tor", "ul", "ven", "wis", "yar", "zen"}

func word(r *rand.Rand) string {
	w := ""
	for range 2 + r.IntN(2) {
		w += syllables[r.IntN(len(syllables))]
	}
	return string(w[0]-32) + w[1:]
}

var openings = []string{"The ", "A ", "", "", "", "", "Return to ", "Beyond the ", "Last ", "My "}

// title makes names that exercise sorting: leading articles, numbers and repeats.
func title(r *rand.Rand) string {
	t := openings[r.IntN(len(openings))] + word(r)
	switch r.IntN(6) {
	case 0:
		t += " " + word(r)
	case 1:
		t += fmt.Sprint(" ", 2+r.IntN(12))
	case 2:
		t += " of " + word(r)
	}
	return t
}

// credits draws people from the pool. Squaring the draw makes a few people very busy and most
// people rare, as in a real library.
func (b *builder) credits(r *rand.Rand, roles ...db.CreditRole) []db.ImportCredit {
	out := make([]db.ImportCredit, len(roles))
	pool := float64(b.cfg.people())
	for n, role := range roles {
		u := r.Float64()
		person := int(u * u * pool)
		pr := rand.New(rand.NewPCG(b.cfg.Seed, uint64(person)))
		out[n] = db.ImportCredit{Name: word(pr) + " " + word(pr), Person: db.ExternalID{Provider: "seed", ID: fmt.Sprint("p", person)}, Role: role}
		if role == db.RoleActor {
			out[n].Character = word(r)
		}
	}
	return out
}

func repeat(role db.CreditRole, n int) []db.CreditRole {
	out := make([]db.CreditRole, n)
	for i := range out {
		out[i] = role
	}
	return out
}

func video(r *rand.Rand, width, height int) []db.Stream {
	codec := []string{"h264", "hevc", "av1"}[r.IntN(3)]
	s := []db.Stream{{Kind: db.StreamVideo, Codec: codec, Width: width, Height: height, FrameRate: 23.976, BitDepth: 8, Default: true}}
	if width >= 3800 && r.IntN(2) == 0 {
		s[0].HDR, s[0].BitDepth = "hdr10", 10
	}
	languages := []string{"en", "fr", "de", "es", "ja"}
	for n := range 1 + r.IntN(2) {
		s = append(s, db.Stream{Kind: db.StreamAudio, Codec: []string{"aac", "ac3", "eac3", "dts"}[r.IntN(4)], Language: languages[n],
			Channels: []int{2, 6, 8}[r.IntN(3)], SampleRate: 48000, Default: n == 0})
	}
	for n := range r.IntN(4) {
		s = append(s, db.Stream{Kind: db.StreamSubtitle, Codec: "subrip", Language: languages[n], Forced: n == 3})
	}
	return s
}

func resolution(r *rand.Rand) (int, int) {
	switch n := r.IntN(20); {
	case n < 2:
		return 720, 480
	case n < 6:
		return 1280, 720
	case n < 17:
		return 1920, []int{1080, 800}[r.IntN(2)]
	}
	return 3840, 2160
}

func (b *builder) videoFile(r *rand.Rand, dir, name string, durationMS int64) db.FileRef {
	w, h := resolution(r)
	return b.file(db.ImportFile{FileRef: db.FileRef{Dir: dir, Name: name}, Role: db.FileMedia, Size: 700e6 + r.Int64N(30e9),
		Container: "matroska", DurationMS: durationMS, Bitrate: 2e6 + r.Int64N(20e6), Streams: video(r, w, h)})
}

func (b *builder) subtitleFile(dir, name string) db.FileRef {
	return b.file(db.ImportFile{FileRef: db.FileRef{Dir: dir, Name: name}, Role: db.FileSubtitle, Size: 60e3,
		Streams: []db.Stream{{Kind: db.StreamSubtitle, Codec: "subrip", Language: "en"}}})
}

func (b *builder) movies() error {
	for n := range b.cfg.Leaves(db.LibraryMovies) {
		r := b.rng("movie", n)
		name, year := title(r), 1950+r.IntN(76)
		dir := fmt.Sprintf("%s (%d) [%d]", name, year, n)
		b.dir(dir)
		duration := int64(70+r.IntN(110)) * 60000
		rating := ratings[r.IntN(len(ratings))]
		it := db.ImportItem{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyFolder, Key: dir}}, Title: name, Year: year,
			ReleaseDate: fmt.Sprintf("%d-%02d-%02d", year, 1+r.IntN(12), 1+r.IntN(28)), AddedAt: addedAt(r),
			ContentRating: rating.name, RatingAge: rating.age, CommunityRating: float64(r.IntN(100)) / 10,
			Summary: "A film about " + word(r) + " and " + word(r) + ".", Tags: tags(r, genres, true),
			ExternalIDs: []db.ExternalID{{Provider: "tmdb", ID: fmt.Sprint(100000 + n)}},
			Credits:     b.credits(r, append(repeat(db.RoleActor, 10), db.RoleDirector, db.RoleWriter, db.RoleWriter, db.RoleProducer)...)}
		if r.IntN(10) == 0 {
			it.ReleaseDate = "" // some titles have a year but no date
		}
		switch pick := r.IntN(100); {
		case pick < 2: // one version split across two files
			first := b.videoFile(r, dir, name+" - cd1.mkv", duration/2)
			second := b.videoFile(r, dir, name+" - cd2.mkv", duration/2)
			it.Versions = []db.ImportVersion{{Parts: []db.ImportPart{{File: first}, {File: second}}}}
		case pick < 12: // two versions
			it.Versions = []db.ImportVersion{
				{Name: "1080p", Parts: []db.ImportPart{{File: b.videoFile(r, dir, name+" - 1080p.mkv", duration)}}},
				{Name: "Extended", Edition: "Extended", Parts: []db.ImportPart{{File: b.videoFile(r, dir, name+" - extended.mkv", duration+9*60000)}}},
			}
		default:
			it.Versions = []db.ImportVersion{{Parts: []db.ImportPart{{File: b.videoFile(r, dir, name+".mkv", duration)}}}}
		}
		it.Versions[0].Markers = []db.Marker{{Kind: db.MarkerCredits, Source: db.MarkerFromAnalysis, StartMS: duration - 240000, EndMS: duration}}
		if r.IntN(10) < 3 {
			it.Versions[0].Sidecars = []db.FileRef{b.subtitleFile(dir, name+".en.srt")}
		}
		if r.IntN(2) == 0 {
			b.file(db.ImportFile{FileRef: db.FileRef{Dir: dir, Name: "poster.jpg"}, Role: db.FileArtwork, Size: 400e3})
			it.Images = []db.ImportImage{{Role: db.ImagePoster, Source: db.ImageFromFile, FileHash: fmt.Sprintf("%016x", r.Uint64()), Width: 1000, Height: 1500}}
		}
		b.item(it)
		if err := b.flush(false); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) shows() error {
	left := b.cfg.Leaves(db.LibraryShows)
	for n := 0; left > 0; n++ {
		r := b.rng("show", n)
		name, year := title(r), 1960+r.IntN(66)
		dir := fmt.Sprintf("%s [%d]", name, n)
		b.dir(dir)
		rating := ratings[r.IntN(len(ratings))]
		showKey := db.ItemKey{Kind: db.KeyFolder, Key: dir}
		b.item(db.ImportItem{Kind: db.KindShow, Keys: []db.ItemKey{showKey}, Title: name, Year: year, AddedAt: addedAt(r),
			ContentRating: rating.name, RatingAge: rating.age, CommunityRating: float64(r.IntN(100)) / 10,
			Summary: "A series about " + word(r) + ".", Tags: tags(r, genres, true),
			ExternalIDs: []db.ExternalID{{Provider: "tvdb", ID: fmt.Sprint(200000 + n)}},
			Credits:     b.credits(r, append(repeat(db.RoleActor, 13), db.RoleWriter, db.RoleProducer)...),
			Images:      []db.ImportImage{{Role: db.ImagePoster, Source: db.ImageFromProvider, FileHash: fmt.Sprintf("%016x", r.Uint64()), Width: 1000, Height: 1500}}})
		for season, seasons := 1, 1+r.IntN(6); season <= seasons && left > 0; season++ {
			seasonDir := fmt.Sprintf("%s/Season %02d", dir, season)
			b.dir(seasonDir)
			seasonKey := db.ItemKey{Kind: db.KeyName, Key: seasonDir}
			added := addedAt(r)
			b.item(db.ImportItem{Kind: db.KindSeason, Keys: []db.ItemKey{seasonKey}, Parent: &showKey, Title: fmt.Sprint("Season ", season),
				IndexNumber: intp(season), Year: year + season - 1, AddedAt: added})
			episodes := min(6+r.IntN(20), left)
			for episode := 1; episode <= episodes; episode++ {
				duration := int64(20+r.IntN(40)) * 60000
				double := episode < episodes && r.IntN(50) == 0
				fileName := fmt.Sprintf("%s - S%02dE%02d.mkv", name, season, episode)
				if double {
					fileName = fmt.Sprintf("%s - S%02dE%02dE%02d.mkv", name, season, episode, episode+1)
					duration *= 2
				}
				file := b.videoFile(r, seasonDir, fileName, duration)
				var sidecars []db.FileRef
				if r.IntN(5) == 0 {
					sidecars = []db.FileRef{b.subtitleFile(seasonDir, fileName+".en.srt")}
				}
				for part := 0; part == 0 || double && part == 1; part++ {
					version := db.ImportVersion{Parts: []db.ImportPart{{File: file}}, Sidecars: sidecars}
					if double { // one file, two episodes: each plays its half
						version.Parts[0].StartMS, version.Parts[0].EndMS = int64p(int64(part)*duration/2), int64p(int64(part+1)*duration/2)
					}
					b.item(db.ImportItem{Kind: db.KindEpisode, Parent: &seasonKey, Title: title(r), Year: year + season - 1, AddedAt: added,
						Keys:        []db.ItemKey{{Kind: db.KeyFile, Key: fmt.Sprintf("%s/%s#%d", seasonDir, fileName, episode+part)}},
						IndexNumber: intp(episode + part), ParentIndex: intp(season),
						ReleaseDate: fmt.Sprintf("%d-%02d-%02d", year+season-1, 1+(episode+part)%12, 1+r.IntN(28)),
						Credits:     b.credits(r, append(repeat(db.RoleGuest, 5), db.RoleDirector, db.RoleWriter)...),
						Versions:    []db.ImportVersion{version}})
				}
				if double {
					episode++
				}
				// A long season is split across batches; later episodes find the season by its key.
				if err := b.flush(false); err != nil {
					return err
				}
			}
			left -= episodes
		}
	}
	return nil
}

func (b *builder) music() error {
	left := b.cfg.Leaves(db.LibraryMusic)
	for n := 0; left > 0; n++ {
		r := b.rng("artist", n)
		artist := word(r) + " " + word(r)
		if r.IntN(4) == 0 {
			artist = "The " + artist
		}
		dir := fmt.Sprintf("%s [%d]", artist, n)
		b.dir(dir)
		artistKey := db.ItemKey{Kind: db.KeyName, Key: dir}
		artistCredit := db.ImportCredit{Name: artist, Person: db.ExternalID{Provider: "seed-artist", ID: fmt.Sprint(n)}, Role: db.RoleArtist}
		b.item(db.ImportItem{Kind: db.KindArtist, Keys: []db.ItemKey{artistKey}, Title: artist, AddedAt: addedAt(r), Tags: tags(r, musicGenres, false)})
		for album, albums := 0, 1+r.IntN(12); album < albums && left > 0; album++ {
			name, year := title(r), 1960+r.IntN(66)
			albumDir := fmt.Sprintf("%s/%s (%d)", dir, name, year)
			b.dir(albumDir)
			albumKey := db.ItemKey{Kind: db.KeyFolder, Key: albumDir}
			added := addedAt(r)
			b.item(db.ImportItem{Kind: db.KindAlbum, Keys: []db.ItemKey{albumKey}, Parent: &artistKey, Title: name, Year: year, AddedAt: added,
				Tags: tags(r, musicGenres, false), Credits: []db.ImportCredit{artistCredit},
				Images: []db.ImportImage{{Role: db.ImageCover, Source: db.ImageFromEmbedded, FileHash: fmt.Sprintf("%016x", r.Uint64()), Width: 1200, Height: 1200}}})
			tracks := min(8+r.IntN(9), left)
			audio := []db.Stream{{Kind: db.StreamAudio, Codec: "flac", Channels: 2, SampleRate: 44100, BitDepth: 16, Default: true}}
			var whole *db.FileRef // one file for the whole album, cut into tracks by a cue sheet
			var at int64
			if r.IntN(20) == 0 {
				ref := b.file(db.ImportFile{FileRef: db.FileRef{Dir: albumDir, Name: name + ".flac"}, Role: db.FileMedia, Size: 350e6,
					Container: "flac", DurationMS: int64(tracks) * 240000, Bitrate: 900e3, Streams: audio})
				b.file(db.ImportFile{FileRef: db.FileRef{Dir: albumDir, Name: name + ".cue"}, Role: db.FileCueSheet, Size: 2e3})
				whole = &ref
			}
			for track := 1; track <= tracks; track++ {
				trackName := title(r)
				it := db.ImportItem{Kind: db.KindTrack, Parent: &albumKey, Title: trackName, Year: year, AddedAt: added,
					IndexNumber: intp(track), ParentIndex: intp(1), Credits: []db.ImportCredit{artistCredit}}
				if r.IntN(10) == 0 {
					it.Credits = append(it.Credits, b.credits(r, db.RoleGuest)...)
				}
				if whole != nil {
					it.Keys = []db.ItemKey{{Kind: db.KeyFile, Key: fmt.Sprintf("%s/%s#%d", albumDir, whole.Name, track)}}
					it.Versions = []db.ImportVersion{{Parts: []db.ImportPart{{File: *whole, StartMS: int64p(at), EndMS: int64p(at + 240000)}}}}
					at += 240000
				} else {
					fileName := fmt.Sprintf("%02d - %s.flac", track, trackName)
					duration := int64(120+r.IntN(300)) * 1000
					file := b.file(db.ImportFile{FileRef: db.FileRef{Dir: albumDir, Name: fileName}, Role: db.FileMedia, Size: 20e6 + r.Int64N(30e6),
						Container: "flac", DurationMS: duration, Bitrate: 900e3, Streams: audio})
					it.Keys = []db.ItemKey{{Kind: db.KeyFile, Key: albumDir + "/" + fileName}}
					it.Versions = []db.ImportVersion{{Parts: []db.ImportPart{{File: file}}}}
				}
				b.item(it)
			}
			left -= tracks
			if err := b.flush(false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *builder) books() error {
	left := b.cfg.Leaves(db.LibraryBooks)
	for n := 0; left > 0; n++ {
		r := b.rng("books", n)
		author := word(r) + " " + word(r)
		dir := fmt.Sprintf("%s [%d]", author, n)
		b.dir(dir)
		credit := db.ImportCredit{Name: author, Person: db.ExternalID{Provider: "seed-author", ID: fmt.Sprint(n)}, Role: db.RoleAuthor}
		for book, books := 0, 1+r.IntN(8); book < books && left > 0; book++ {
			name := title(r)
			fileName := fmt.Sprintf("%s - %s.epub", author, name)
			file := b.file(db.ImportFile{FileRef: db.FileRef{Dir: dir, Name: fileName}, Role: db.FileMedia, Size: 500e3 + r.Int64N(5e6), Container: "epub"})
			rating := ratings[r.IntN(len(ratings))]
			b.item(db.ImportItem{Kind: db.KindBook, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: dir + "/" + fileName}}, Title: name,
				Year: 1900 + r.IntN(126), AddedAt: addedAt(r), ContentRating: rating.name, RatingAge: rating.age,
				Tags: tags(r, genres, false), Credits: []db.ImportCredit{credit},
				ExternalIDs: []db.ExternalID{{Provider: "isbn", ID: fmt.Sprint(9780000000000 + int64(n)*10 + int64(book))}},
				Versions:    []db.ImportVersion{{Parts: []db.ImportPart{{File: file}}}}})
			left--
		}
		if err := b.flush(false); err != nil {
			return err
		}
	}
	return nil
}
