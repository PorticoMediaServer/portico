package catalogtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// TasteShape counts works; EpisodesPerShow is the number of playable episodes
// in each show. Seed controls every content choice and fixture date.
type TasteShape struct {
	Movies, Shows, EpisodesPerShow, Anime int
	Seed                                  int64
}

// Taste names the deliberately nonuniform works and the ready-made viewers.
type Taste struct {
	Catalog                                  *Catalog
	Movies, Shows, Episodes, Anime           []Item
	SciFiDirector                            []Item
	CozyAnime                                []Item
	HiddenGem, Blockbuster, RestrictedHorror Item
	Franchise                                Item
	FranchiseFilms                           []Item
	Profiles                                 TasteProfiles
	sequence                                 int
}

type TasteProfiles struct{ SciFiFan, AnimeFan, Kids, New string }

var tasteGenres = []string{"Drama", "Comedy", "Action", "Thriller", "Science Fiction", "Crime", "Romance", "Adventure", "Fantasy", "Mystery", "Horror", "Animation", "Documentary", "Family", "History", "War", "Music", "Sport", "Western", "Musical"}
var tasteWords = []string{"Harbor", "Midnight", "Summer", "Glass", "River", "Quiet", "Signal", "Garden", "Winter", "Golden", "Orbit", "Secret", "Moon", "Last", "North", "Blue"}

// BuildTaste writes through the catalogue API in 250-work transactions. Only
// provider publication facts and personal facts use direct writes, as they do
// in the server. It does not drain derivations; callers decide when to drain.
func BuildTaste(t testing.TB, c *Catalog, shape TasteShape) *Taste {
	t.Helper()
	if shape.Movies < 16 || shape.Shows < 2 || shape.EpisodesPerShow < 1 || shape.Anime < 1 || shape.Anime > shape.Shows {
		t.Fatal("taste shape needs >=16 movies, >=2 shows, >=1 episode/show, and 1..Shows anime shows")
	}
	rng := rand.New(rand.NewSource(shape.Seed))
	out := &Taste{Catalog: c, Profiles: TasteProfiles{"taste-scifi", "taste-anime", "taste-kids", "taste-new"}}
	ctx := context.Background()
	libs := map[string]int64{
		"movie": c.Library("taste-movies", "Films", "movie", "/taste/movies"),
		"tv":    c.Library("taste-shows", "Shows", "tv", "/taste/shows"),
		"anime": c.Library("taste-anime", "Anime", "anime", "/taste/anime"),
	}
	// Fixed fixture clock keeps added dates, evidence times and activity stable.
	base := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	stamp := func(days int) string { return base.AddDate(0, 0, -days).Format("2006-01-02T15:04:05.000Z") }
	var batch []func(*sql.Tx) error
	flush := func() {
		if len(batch) == 0 {
			return
		}
		steps := batch
		batch = nil
		c.Write(func(_ context.Context, tx *sql.Tx) error {
			for _, step := range steps {
				if err := step(tx); err != nil {
					return err
				}
			}
			return nil
		})
	}
	add := func(step func(*sql.Tx) error) {
		batch = append(batch, step)
		if len(batch) == 250 {
			flush()
		}
	}
	makeWork := func(tx *sql.Tx, lib int64, kind compactcatalog.Kind, parent int64, key, path, title string, year, addedDays, ordinal int, genres []string, director string, popular bool, contentRating string, side map[string]any) (Item, error) {
		var it Item
		var err error
		it.ID, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: lib, Kind: kind, Parent: parent, Key: key, Title: title, Year: year, Added: stamp(addedDays)})
		if err != nil {
			return it, err
		}
		facts := map[string]any{"overview": "A story about people, places, and the choices that connect them.", "content_rating": contentRating}
		if kind == compactcatalog.Movie {
			facts["studio"] = fmt.Sprintf("Studio %02d", ordinal%17)
		}
		if kind == compactcatalog.Show {
			facts["network"] = fmt.Sprintf("Network %02d", ordinal%9)
		}
		for key, value := range side {
			facts[key] = value
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, it.ID, facts); err != nil {
			return it, err
		}
		if path != "" {
			it.Asset, it.Token, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1 << 24, ModifiedNS: int64(ordinal + 1), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 5400})
			if err != nil {
				return it, err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, it.ID, it.Asset, compactcatalog.Link{}); err != nil {
				return it, err
			}
			if err = compactcatalog.SetAttributesTx(ctx, tx, it.ID, "contentRating", []string{contentRating}); err != nil {
				return it, err
			}
		}
		if kind == compactcatalog.Movie {
			terms := make([]compactcatalog.Term, 0, len(genres))
			for _, g := range genres {
				terms = append(terms, compactcatalog.Term{SourceID: strings.ToLower(g), Name: g})
			}
			if err = compactcatalog.SetTermsTx(ctx, tx, it.ID, compactcatalog.VocabGenre, "tmdb", terms); err != nil {
				return it, err
			}
		}
		if kind == compactcatalog.Movie || kind == compactcatalog.Show {
			credits := make([]compactcatalog.Credit, 0, 12)
			credits = append(credits, compactcatalog.Credit{PersonKey: "taste:" + director, PersonName: director, ProviderPersonID: director, CreditID: director + "-director", CreditedName: director, Role: "Director", Department: "Directing", Ordinal: 0})
			for j := 0; j < 11; j++ {
				person := fmt.Sprintf("Actor %03d", (ordinal*7+j*13)%(shape.Movies/8+25))
				if j < 2 {
					person = fmt.Sprintf("Actor Star %02d", (ordinal+j)%4)
				}
				credits = append(credits, compactcatalog.Credit{PersonKey: "taste:" + person, PersonName: person, ProviderPersonID: person, CreditID: fmt.Sprintf("%d-%d", ordinal, j), CreditedName: person, Role: "Actor", Department: "Acting", Ordinal: j + 1})
			}
			if err = compactcatalog.SetCreditsTx(ctx, tx, it.ID, "tmdb", credits); err != nil {
				return it, err
			}
		}
		if kind == compactcatalog.Show {
			if _, err = tx.ExecContext(ctx, `INSERT INTO screen_metadata_work(target_kind,target_id,library_id,title_seed,year_seed,status,provider,provider_type,provider_id,selection_mode) VALUES('show',?,?,?,?,'accepted','tmdb','tv',?,'automatic') ON CONFLICT(target_kind,target_id) DO UPDATE SET status='accepted',provider='tmdb',provider_type='tv',provider_id=excluded.provider_id,selection_mode='automatic'`, it.ID, map[bool]string{true: "taste-anime", false: "taste-shows"}[lib == libs["anime"]], title, year, fmt.Sprint(200000+ordinal)); err != nil {
				return it, err
			}
			genreList := make([]map[string]string, 0, len(genres))
			for _, g := range genres {
				genreList = append(genreList, map[string]string{"name": g})
			}
			for field, value := range map[string]any{"genres": genreList, "credits": []map[string]string{{"name": director, "department": "Directing"}}} {
				raw, _ := json.Marshal(value)
				if _, err = tx.ExecContext(ctx, `INSERT INTO screen_metadata_fields(target_kind,target_id,field,value,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision) VALUES('show',?,?,?,'tmdb','provider','',1,?,'en','US',1)`, it.ID, field, string(raw), stamp(0)); err != nil {
					return it, err
				}
			}
		}
		if kind == compactcatalog.Movie || kind == compactcatalog.Show {
			providerKind := "movie"
			entityKind := "item"
			if kind == compactcatalog.Show {
				providerKind = "tv"
				entityKind = "show"
			}
			providerID := fmt.Sprint(100000 + ordinal)
			if kind == compactcatalog.Show {
				providerID = fmt.Sprint(200000 + ordinal)
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_external_ids VALUES(?,?,?,?)`, it.ID, "tmdb", providerKind, providerID); err != nil {
				return it, err
			}
			otherProvider, otherKind, otherID := "imdb", "title", fmt.Sprintf("tt%07d", ordinal+1000000)
			if kind == compactcatalog.Show {
				otherProvider, otherKind, otherID = "tvdb", "series", fmt.Sprint(300000+ordinal)
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_external_ids VALUES(?,?,?,?)`, it.ID, otherProvider, otherKind, otherID); err != nil {
				return it, err
			}
			if lib == libs["anime"] {
				if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_external_ids VALUES(?,?,?,?)`, it.ID, "anilist", "anime", fmt.Sprint(400000+ordinal)); err != nil {
					return it, err
				}
			}
			if kind == compactcatalog.Movie {
				if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_details VALUES(?,'tmdb',?,'',?)`, it.ID, providerID, stamp(0)); err != nil {
					return it, err
				}
			}
			if ordinal < 12 {
				for rank, target := range []int{(ordinal + 1) % shape.Movies, shape.Movies + ordinal + 100} {
					if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_similar(entity_id,source,rank,provider,provider_kind,provider_id) VALUES(?,'tmdb',?,'tmdb','movie',?)`, it.ID, rank+1, fmt.Sprint(100000+target)); err != nil {
						return it, err
					}
				}
			}
			votes := rng.Intn(50)
			value := 5.5 + rng.Float64()*2.8
			if ordinal%19 == 0 {
				votes = 30000 + rng.Intn(80000)
			}
			if ordinal%23 == 0 {
				votes = 8 + rng.Intn(18)
				value = 9.2
			}
			if ordinal == 6 {
				votes = 12
				value = 9.4
			}
			if ordinal == 7 {
				votes = 80000
				value = 5.8
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_ratings VALUES(?,'tmdb',?,10,?,'',?)`, it.ID, value, votes, stamp(0)); err != nil {
				return it, err
			}
			keywords := rng.Intn(4)
			if popular {
				keywords = 15 + rng.Intn(26)
			}
			for k := 0; k < keywords; k++ {
				word := fmt.Sprintf("%s-%02d", tasteWords[(ordinal+k)%len(tasteWords)], k)
				if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_tags VALUES(?,?,?,?,?)`, entityKind, it.ID, "tmdb", fmt.Sprint(k), word); err != nil {
					return it, err
				}
			}
		}
		return it, nil
	}
	// The first works are named anchors. Later titles sample a skewed genre set.
	for i := 0; i < shape.Movies; i++ {
		i := i
		add(func(tx *sql.Tx) error {
			genres := []string{tasteGenres[int(math.Pow(rng.Float64(), 2)*float64(len(tasteGenres)))], tasteGenres[rng.Intn(12)]}
			if i < 6 {
				genres = []string{"Science Fiction", "Drama", "Adventure"}
			}
			if i >= 12 && i < 16 {
				genres = []string{"Animation", "Fantasy", "Family"}
			}
			if i == 8 {
				genres = []string{"Horror", "Thriller"}
			}
			director := "Mira Solis"
			if i >= 6 {
				directorCount := (shape.Movies - 6 + 7) / 8
				director = fmt.Sprintf("Director %03d", (i-6)%directorCount)
			}
			title := fmt.Sprintf("%s %s %05d", tasteWords[rng.Intn(len(tasteWords))], tasteWords[rng.Intn(len(tasteWords))], i)
			if i < 6 {
				title = fmt.Sprintf("Far Orbit %d", i+1)
			}
			if i == 6 {
				title = "The Hidden Lantern"
			}
			if i == 7 {
				title = "Empire of Tomorrow"
			}
			if i == 8 {
				title = "The Hollow House"
			}
			year := 1950 + rng.Intn(77)
			if i < 4 {
				year = 2009 + i*3
			} else if i >= 8 {
				year = 1950 + (i/8)%70 + i%8
			}
			age := rng.Intn(1095)
			if i%13 == 0 {
				age = rng.Intn(20)
			}
			rating := "PG-13"
			if i == 8 {
				rating = "R"
			} else if i >= 12 && i < 16 {
				rating = "PG"
			} else if i%11 == 0 {
				rating = "G"
			}
			root, lib := "/taste/movies", libs["movie"]
			if i >= 12 && i < 16 {
				root, lib = "/taste/anime", libs["anime"]
			}
			path := fmt.Sprintf("%s/movie-%07d.mp4", root, i)
			it, err := makeWork(tx, lib, compactcatalog.Movie, 0, compactcatalog.ItemKey(root, path, 0), path, title, year, age, i, genres, director, i%19 == 0 || i == 7, rating, nil)
			if err != nil {
				return err
			}
			out.Movies = append(out.Movies, it)
			if i >= 12 && i < 16 {
				out.Anime = append(out.Anime, it)
			}
			if i < 6 {
				out.SciFiDirector = append(out.SciFiDirector, it)
			}
			if i == 6 {
				out.HiddenGem = it
			}
			if i == 7 {
				out.Blockbuster = it
			}
			if i == 8 {
				out.RestrictedHorror = it
			}
			return nil
		})
	}
	flush()
	// Film collections use their catalogue entity and membership write paths.
	collectionCount := max(1, shape.Movies/70)
	for n := 0; n < collectionCount; n++ {
		n := n
		add(func(tx *sql.Tx) error {
			name := fmt.Sprintf("Taste Saga %03d", n)
			key := strings.ToLower(name)
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: libs["movie"], Kind: compactcatalog.Collection, Key: compactcatalog.CollectionKey(key), Title: name})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"library_id": libs["movie"], "name_key": key, "created_at": stamp(0)}); err != nil {
				return err
			}
			members := 2 + n%7
			if n == 0 {
				members = 4
			}
			for j := 0; j < members; j++ {
				index := n*8 + j
				if n == 0 {
					index = j
				}
				if index >= len(out.Movies) {
					break
				}
				if err = compactcatalog.SetCollectionMemberTx(ctx, tx, id, out.Movies[index].ID, fmt.Sprintf("%03d", j), true); err != nil {
					return err
				}
			}
			if n == 0 {
				out.Franchise = Item{ID: id}
				out.FranchiseFilms = append(out.FranchiseFilms, out.Movies[:4]...)
			}
			return nil
		})
	}
	flush()
	for s := 0; s < shape.Shows; s++ {
		s := s
		add(func(tx *sql.Tx) error {
			anime := s < shape.Anime
			lib := libs["tv"]
			root := "/taste/shows"
			if anime {
				lib = libs["anime"]
				root = "/taste/anime"
			}
			name := fmt.Sprintf("Series %05d", s)
			genres := []string{"Drama", "Mystery"}
			if anime {
				name = fmt.Sprintf("Cozy Anime %03d", s)
				genres = []string{"Animation", "Fantasy", "Family"}
			}
			key := fmt.Sprintf("taste-%05d", s)
			director := fmt.Sprintf("Show Director %03d", s/6)
			show, err := makeWork(tx, lib, compactcatalog.Show, 0, compactcatalog.ShowKey(key), "", name, 2000+s%27, rng.Intn(1095), shape.Movies+s, genres, director, s%13 == 0, "PG", map[string]any{"local_key": key})
			if err != nil {
				return err
			}
			out.Shows = append(out.Shows, show)
			if anime {
				out.Anime = append(out.Anime, show)
				if s == 0 {
					out.CozyAnime = append(out.CozyAnime, show)
				}
			}
			seasonCount := (shape.EpisodesPerShow + 7) / 8
			for season := 1; season <= seasonCount; season++ {
				seasonID, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: lib, Kind: compactcatalog.Season, Parent: show.ID, Key: compactcatalog.SeasonKey(key, season), Title: fmt.Sprintf("Season %d", season)})
				if e != nil {
					return e
				}
				if e = compactcatalog.SetFactsTx(ctx, tx, seasonID, map[string]any{"show_id": show.ID, "number": season}); e != nil {
					return e
				}
				for ep := 1; ep <= 8; ep++ {
					number := (season-1)*8 + ep
					if number > shape.EpisodesPerShow {
						break
					}
					path := fmt.Sprintf("%s/show-%05d/s%02de%02d.mp4", root, s, season, ep)
					item, e := makeWork(tx, lib, compactcatalog.Episode, seasonID, compactcatalog.EpisodeKey(key, "seasonal", season, ep), path, fmt.Sprintf("%s: Episode %d", name, number), 0, rng.Intn(1095), shape.Movies+shape.Shows+s*shape.EpisodesPerShow+number, nil, "", false, "PG", map[string]any{"show_id": show.ID, "season_id": seasonID, "numbering": "seasonal", "number": ep})
					if e != nil {
						return e
					}
					out.Episodes = append(out.Episodes, item)
					if s == 0 && number <= 3 {
						out.CozyAnime = append(out.CozyAnime, item)
					}
				}
			}
			return nil
		})
	}
	flush()
	for _, it := range out.SciFiDirector[:5] {
		out.Watch(out.Profiles.SciFiFan, it)
	}
	for _, it := range out.CozyAnime[1:] {
		out.Watch(out.Profiles.AnimeFan, it)
	}
	out.Watchlist(out.Profiles.Kids, out.CozyAnime[0])
	// Direct profiles are needed for the real restriction predicate's FK.
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('taste-account','taste-account',X'00',?)`, out.Profiles.New)
	for _, p := range []string{out.Profiles.SciFiFan, out.Profiles.AnimeFan, out.Profiles.Kids, out.Profiles.New} {
		c.Exec(`INSERT OR IGNORE INTO direct_profiles(id,account_id,name) VALUES(?,'taste-account',?)`, p, p)
	}
	c.Exec(`INSERT INTO profile_restrictions(profile_id,rating_system,maximum_age_rating,maximum_age,allow_unrated) VALUES(?,'MPAA','PG',10,0)`, out.Profiles.Kids)
	for _, it := range []struct {
		p  *Item
		id int64
	}{{&out.HiddenGem, out.HiddenGem.ID}, {&out.Blockbuster, out.Blockbuster.ID}, {&out.RestrictedHorror, out.RestrictedHorror.ID}, {&out.Franchise, out.Franchise.ID}} {
		it.p.Public = c.Public(it.id)
	}
	for i := range out.SciFiDirector {
		out.SciFiDirector[i].Public = c.Public(out.SciFiDirector[i].ID)
	}
	for i := range out.FranchiseFilms {
		out.FranchiseFilms[i].Public = c.Public(out.FranchiseFilms[i].ID)
	}
	for i := range out.CozyAnime {
		out.CozyAnime[i].Public = c.Public(out.CozyAnime[i].ID)
	}
	return out
}

func (taste *Taste) mutate(profile string, item Item, column string, value any) {
	taste.Catalog.T.Helper()
	allowed := map[string]bool{"favorite": true, "rating": true, "watchlisted": true, "not_interested": true}
	if !allowed[column] {
		taste.Catalog.T.Fatal("invalid personal field")
	}
	taste.Catalog.Exec(`INSERT INTO personal_items(profile_id,item_id,revision,`+column+`) VALUES(?,?,1,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET `+column+`=excluded.`+column+`,revision=personal_items.revision+1`, profile, item.ID, value)
}

func (taste *Taste) Watch(profile string, item Item) {
	taste.Catalog.T.Helper()
	taste.sequence++
	stamp := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC).Add(time.Duration(taste.sequence) * time.Minute).Format("2006-01-02T15:04:05.000Z")
	id := fmt.Sprintf("taste-play-%08d", taste.sequence)
	taste.Catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,completed) VALUES(?,?,?,?,?,1,5400000,1)`, id, profile, item.ID, stamp, stamp); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO personal_items(profile_id,item_id,revision,watched,last_played_at) VALUES(?,?,1,1,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET watched=1,last_played_at=excluded.last_played_at,revision=personal_items.revision+1`, profile, item.ID, stamp); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO progress(profile_id,item_id,position,playback_id) VALUES(?,?,0,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET position=0,playback_id=excluded.playback_id`, profile, item.ID, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) SELECT ?,cl.library_id,?,?,'ended' FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=? ON CONFLICT(profile_id,item_id) DO UPDATE SET updated_at=excluded.updated_at,state=excluded.state`, profile, item.ID, stamp, item.ID)
		return err
	})
}

func (taste *Taste) Progress(profile string, item Item, fraction float64) {
	taste.Catalog.T.Helper()
	if math.IsNaN(fraction) || fraction <= 0 || fraction >= 1 {
		taste.Catalog.T.Fatal("progress fraction must be between 0 and 1")
	}
	taste.sequence++
	id := fmt.Sprintf("taste-progress-%08d", taste.sequence)
	stamp := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC).Format("2006-01-02T15:04:05.000Z")
	position := int64(math.Round(fraction * 5400000))
	taste.Catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,completed) VALUES(?,?,?,?,?,1,?,0)`, id, profile, item.ID, stamp, stamp, position); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO progress(profile_id,item_id,position,playback_id) VALUES(?,?,?,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET position=excluded.position,playback_id=excluded.playback_id`, profile, item.ID, position, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO personal_items(profile_id,item_id,revision,last_played_at) VALUES(?,?,1,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET last_played_at=excluded.last_played_at,revision=personal_items.revision+1`, profile, item.ID, stamp); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) SELECT ?,cl.library_id,?,?,'paused' FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=? ON CONFLICT(profile_id,item_id) DO UPDATE SET updated_at=excluded.updated_at,state=excluded.state`, profile, item.ID, stamp, item.ID)
		return err
	})
}

func (taste *Taste) Favorite(profile string, item Item) { taste.mutate(profile, item, "favorite", 1) }
func (taste *Taste) Watchlist(profile string, item Item) {
	taste.mutate(profile, item, "watchlisted", 1)
}
func (taste *Taste) NotInterested(profile string, item Item) {
	taste.mutate(profile, item, "not_interested", 1)
}
func (taste *Taste) Rate(profile string, item Item, stars float64) {
	taste.Catalog.T.Helper()
	if math.IsNaN(stars) || stars < 0.5 || stars > 5 || stars*2 != math.Trunc(stars*2) {
		taste.Catalog.T.Fatal("rating must be 0.5..5 in half stars")
	}
	taste.mutate(profile, item, "rating", stars)
}
func (taste *Taste) MarkWatched(profile string, item Item) {
	taste.Catalog.T.Helper()
	taste.Catalog.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES(?,?,1,'2026-09-25T12:00:00.000Z') ON CONFLICT(profile_id,item_id) DO UPDATE SET watched=1,authored_at=excluded.authored_at`, profile, item.ID)
}
