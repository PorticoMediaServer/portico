package metadata

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server-next/internal/contentrating"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// Service gives titles their details from the providers it is handed.
type Service struct {
	DB        *db.DB
	Providers map[string]Provider // by Name
	// IncludeAdult is the owner's "Include adult titles" switch.
	IncludeAdult bool
}

// defaultProviders is the provider a library of each kind uses until the owner chooses.
// A kind that is not here is local only.
var defaultProviders = map[string]string{db.LibraryMovies: "tmdb", db.LibraryShows: "tmdb", db.LibraryAnime: "tmdb"}

// provider is the one provider a library uses, or nil for a library that uses local metadata
// only or whose provider this server has no key for.
func (s *Service) provider(src db.LibrarySource) Provider {
	name := src.Provider
	if name == "" {
		name = defaultProviders[src.Kind]
	}
	if name == db.ProviderLocal {
		return nil
	}
	return s.Providers[name]
}

// Asks reports whether a library (a public id) has a provider to ask: one that uses local
// metadata only, or whose provider this server has no key for, has none.
func (s *Service) Asks(ctx context.Context, library string) (bool, error) {
	id, err := s.DB.LibraryID(ctx, library)
	if err != nil {
		return false, err
	}
	src, err := s.DB.LibrarySource(ctx, id)
	if err != nil {
		return false, err
	}
	return s.provider(src) != nil, nil
}

// providerKinds are the kinds of top-level title a provider is asked about, and its word for each.
var providerKinds = map[db.ItemKind]string{db.KindMovie: "movie", db.KindShow: "show"}

// MatchStats says what one run of MatchLibrary did.
type MatchStats struct {
	Matched   int
	Unmatched int
	// Failed counts the titles the provider failed on this time: an error, a refusal, no
	// answer in time. Each is asked again after a wait (see failures).
	Failed int
}

// MatchLibrary asks the library's provider about every title it has not been asked about:
// a confident answer's details are stored, and anything else goes on the library's Unmatched
// items list. It is background work: each title is one small transaction at Background
// priority, and no transaction is open while the provider is asked.
//
// A title the provider fails on while it answers for others is passed over and asked again
// later, after a wait that grows each time, so one title can never keep the rest from being
// looked up. A provider that fails on several titles in a row, or on every title of a run, is
// down: the run stops (ErrProviderDown) and the titles it failed on or did not reach are as they
// were, so the next run asks about them again.
func (s *Service) MatchLibrary(ctx context.Context, libraryID int64) (MatchStats, error) {
	var stats MatchStats
	src, err := s.DB.LibrarySource(ctx, libraryID)
	if err != nil {
		return stats, err
	}
	p := s.provider(src)
	if p == nil {
		return stats, nil
	}
	kinds := make([]db.ItemKind, 0, len(providerKinds))
	for kind := range providerKinds {
		kinds = append(kinds, kind)
	}
	locale := Locale{Language: src.Language, Region: src.Region}
	f := &failures{s: s}
	after := int64(0)
	for {
		items, err := s.DB.ToMatch(ctx, libraryID, kinds, after, 50)
		if err != nil {
			return stats, err
		}
		if len(items) == 0 {
			stats.Failed = f.count
			return stats, f.end(ctx)
		}
		for _, item := range items {
			after = item.ID
			matched, err := s.match(ctx, p, locale, src.Kind, item)
			if ctx.Err() != nil {
				return stats, ctx.Err()
			}
			handled, stop := f.took(ctx, item.ID, err)
			stats.Failed = f.count
			if stop != nil {
				return stats, stop
			}
			if handled {
				continue
			}
			if matched {
				stats.Matched++
			} else {
				stats.Unmatched++
			}
		}
	}
}

func (s *Service) match(ctx context.Context, p Provider, locale Locale, libraryKind string, item db.MatchItem) (bool, error) {
	title, year, err := s.searchName(ctx, libraryKind, item)
	if err != nil {
		return false, err
	}
	q := Query{Kind: providerKinds[item.Kind], Title: title, Year: year, Locale: locale, IncludeAdult: s.IncludeAdult}
	ref := Ref{Provider: p.Name(), Kind: q.Kind}
	for _, id := range item.ExternalIDs {
		if id.Provider == p.Name() {
			ref.ID = id.ID // an id in the name or an NFO file is exact
		} else {
			q.IDs = append(q.IDs, Ref{Provider: id.Provider, Kind: q.Kind, ID: id.ID})
		}
	}
	if ref.ID == "" {
		candidates, err := p.Search(ctx, q)
		if err != nil {
			return false, providerError{err}
		}
		best, reason := Choose(q, candidates)
		if best < 0 {
			return false, s.unmatched(ctx, item.ID, reason, candidates)
		}
		ref = candidates[best].Ref
	}
	record, err := p.Details(ctx, ref, locale)
	if errors.Is(err, ErrNotFound) {
		return false, s.unmatched(ctx, item.ID, ReasonIDNotFound, nil)
	}
	if err != nil {
		return false, providerError{err}
	}
	if err := s.DB.ApplyDetails(ctx, db.Background, item.ID, details(record, db.MatchFound)); err != nil {
		return false, err
	}
	s.moods(ctx, item.ID)
	// A season the provider fails on leaves the show matched: its episodes come with the
	// show's next refresh, and the failure counts as one for the run.
	return true, s.children(ctx, p, locale, item.ID, ref)
}

// children fetches and stores the details of what the library has under a matched title:
// the episodes of each season of a show. One season is one provider call and one transaction.
func (s *Service) children(ctx context.Context, p Provider, locale Locale, itemID int64, ref Ref) error {
	if ref.Kind != "show" {
		return nil
	}
	// The owner's episode order goes to the provider; the offset turns the files' episode
	// numbers into the provider's and back.
	numbering, err := s.DB.EpisodeNumbering(ctx, itemID)
	if err != nil {
		return err
	}
	ref.Order = numbering.Order
	err = s.childGroups(ctx, p, locale, itemID, ref, numbering.Offset)
	if errors.Is(err, ErrNoOrder) {
		// The provider no longer has the show in the owner's order: this run uses aired order,
		// and the owner's choice is kept for the next.
		ref.Order = OrderAired
		err = s.childGroups(ctx, p, locale, itemID, ref, numbering.Offset)
	}
	return err
}

// childGroups fetches and stores a show's episodes in ref.Order, group by group.
func (s *Service) childGroups(ctx context.Context, p Provider, locale Locale, itemID int64, ref Ref, offset int) error {
	var err error
	groups, apply := []int{1}, -1 // in absolute order the provider has one group, numbered through the show
	if ref.Order != OrderAbsolute {
		if groups, err = s.DB.ChildNumbers(ctx, itemID); err != nil {
			return err
		}
	}
	for _, group := range groups {
		records, err := p.Children(ctx, ref, group, locale)
		if errors.Is(err, ErrNotFound) {
			continue // a season the provider does not know keeps the names its files gave
		}
		if err != nil {
			return providerError{err}
		}
		children := make([]db.ItemDetails, len(records))
		for i, record := range records {
			children[i] = details(record, db.MatchFound)
			children[i].Index = record.Index - offset
		}
		if ref.Order != OrderAbsolute {
			apply = group
		}
		if err := s.DB.ApplyChildren(ctx, db.Background, itemID, db.KindEpisode, apply, children); err != nil {
			return err
		}
	}
	return nil
}

// unmatched puts a title on the Unmatched items list with the three closest answers.
func (s *Service) unmatched(ctx context.Context, itemID int64, reason string, candidates []Candidate) error {
	type match struct {
		Provider  string `json:"provider"`
		ID        string `json:"id"`
		Title     string `json:"title"`
		Year      int    `json:"year,omitempty"`
		PosterURL string `json:"posterUrl,omitempty"`
	}
	closest := []match{}
	for _, c := range candidates[:min(len(candidates), 3)] {
		closest = append(closest, match{c.Ref.Provider, c.Ref.ID, c.Title, c.Year, c.PosterURL})
	}
	raw, err := json.Marshal(closest)
	if err != nil {
		return err
	}
	return s.DB.SetUnmatched(ctx, db.Background, itemID, reason, string(raw))
}

var creditRoles = map[string]db.CreditRole{
	"actor": db.RoleActor, "director": db.RoleDirector, "writer": db.RoleWriter, "producer": db.RoleProducer,
	"composer": db.RoleComposer, "author": db.RoleAuthor, "narrator": db.RoleNarrator, "artist": db.RoleArtist,
	"guest": db.RoleGuest, "crew": db.RoleCrew,
}

// details turns a provider's record into what the database stores.
func details(r Record, match db.MatchState) db.ItemDetails {
	d := db.ItemDetails{Match: match, Title: r.Title, OriginalTitle: r.OriginalTitle, Summary: r.Overview, Tagline: r.Tagline,
		Year: r.Year, ReleaseDate: r.ReleaseDate, ContentRating: r.ContentRating, CommunityRating: r.Rating, Index: r.Index}
	if age, ok := contentrating.Age(r.ContentRating); ok {
		d.RatingAge = &age
	}
	if r.Ref.Kind == "movie" || r.Ref.Kind == "show" {
		d.ProviderKey = r.Ref.Provider + ":" + r.Ref.Kind + ":" + r.Ref.ID
	}
	if r.Ref.ID != "" {
		d.ExternalIDs = append(d.ExternalIDs, db.ExternalID{Provider: r.Ref.Provider, ID: r.Ref.ID})
	}
	for _, other := range r.OtherIDs {
		d.ExternalIDs = append(d.ExternalIDs, db.ExternalID{Provider: other.Provider, ID: other.ID})
	}
	for kind, names := range map[string][]string{db.TagGenre: r.Genres, db.TagStudio: r.Studios, db.TagNetwork: r.Networks, db.TagCountry: r.Countries} {
		for _, name := range names {
			d.Tags = append(d.Tags, db.TagName{Kind: kind, Name: name})
		}
	}
	for _, c := range r.Credits {
		role, known := creditRoles[c.Role]
		if !known || c.Name == "" {
			continue
		}
		d.Credits = append(d.Credits, db.ImportCredit{Name: c.Name, Role: role, Character: c.Character,
			Person: db.ExternalID{Provider: c.Person.Provider, ID: c.Person.ID}})
	}
	return d
}

// ErrMatchLocked is the answer to looking a title up again when a person chose its match:
// the server never changes a match a person made.
var ErrMatchLocked = errors.New("this title's match was chosen by a person")

// target reads a title and the provider of its library for a request about its match.
func (s *Service) target(ctx context.Context, itemID string) (db.MatchTarget, Provider, Locale, error) {
	t, err := s.DB.MatchTarget(ctx, itemID)
	if err != nil {
		return t, nil, Locale{}, err
	}
	src, err := s.DB.LibrarySource(ctx, t.LibraryID)
	if err != nil {
		return t, nil, Locale{}, err
	}
	p := s.provider(src)
	if p == nil || providerKinds[t.Kind] == "" {
		return t, nil, Locale{}, ErrNoProvider
	}
	return t, p, Locale{Language: src.Language, Region: src.Region}, nil
}

// ErrNotAShow is the answer to an episode order or offset given with the match of a title
// that is not a show.
var ErrNotAShow = errors.New("only a show has an episode order and offset")

// ErrUnknownOrder is the answer to an episode order other than aired, DVD or absolute.
var ErrUnknownOrder = errors.New("not an episode order")

// ErrNoProvider is the answer for a title no provider can be asked about: its library uses
// local metadata only, or it is of a kind the library's provider does not know.
var ErrNoProvider = errors.New("this title's library has no provider to ask")

// Identify makes a title the one a person chose from the provider. The title keeps its id,
// everyone's state, its lists, locked fields and chosen pictures; its details are fetched
// again, and the match is locked against every scan and refresh. If the library already has
// that title, the two become one and the id of the one that remains is returned.
//
// The provider is asked first; the title changes in one transaction after it has answered.
// A show's episodes then follow as background work before Identify returns.
func (s *Service) Identify(ctx context.Context, itemID string, ref Ref) (string, error) {
	remaining, _, err := s.IdentifyShow(ctx, itemID, ref, 0)
	return remaining, err
}

// IdentifyShow is Identify with a show's number offset. For a show, ref.Order and offset are
// the episode order and number offset the owner chose with the match: they are kept on the
// show and used by every later refresh. A title that is not a show takes neither. A show the
// provider does not have in the chosen order gets aired order instead, never blank episodes;
// order is the one kept, so a caller can tell the person.
func (s *Service) IdentifyShow(ctx context.Context, itemID string, ref Ref, offset int) (remaining, order string, err error) {
	t, p, locale, err := s.target(ctx, itemID)
	if err != nil {
		return "", "", err
	}
	if ref.Provider != "" && ref.Provider != p.Name() {
		return "", "", ErrUnknownRef
	}
	order = ref.Order
	ref.Provider, ref.Kind, ref.Order = p.Name(), providerKinds[t.Kind], OrderAired
	if ref.Kind != "show" && (order != OrderAired || offset != 0) {
		return "", "", ErrNotAShow
	}
	if order != OrderAired && order != OrderDVD && order != OrderAbsolute {
		return "", "", ErrUnknownOrder
	}
	if order != OrderAired {
		// Asked before the match changes, so a show is never left with its episodes blanked.
		in := ref
		in.Order = order
		if _, err := p.Children(ctx, in, 1, locale); errors.Is(err, ErrNoOrder) {
			order = OrderAired
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return "", "", err
		}
	}
	record, err := p.Details(ctx, ref, locale)
	if err != nil {
		return "", "", err
	}
	chosen := details(record, db.MatchChosen)
	if ref.Kind == "show" {
		chosen.Numbering = &db.EpisodeNumbering{Order: order, Offset: offset}
	}
	remaining, err = s.DB.ChangeMatch(ctx, db.User, t.ID, chosen)
	if err != nil || remaining != itemID {
		return remaining, order, err // merged into a title that has its details already
	}
	s.moods(ctx, t.ID)
	return remaining, order, s.children(ctx, p, locale, t.ID, record.Ref)
}

// Rematch looks a title up again as if it had just been scanned: "Look it up again" on the
// Unmatched items list. It reports whether the title is now matched.
func (s *Service) Rematch(ctx context.Context, itemID string) (bool, error) {
	t, p, locale, err := s.target(ctx, itemID)
	if err != nil {
		return false, err
	}
	if t.Match == db.MatchChosen {
		return false, ErrMatchLocked
	}
	// Searched by the same name a scan's lookup uses, never a title written inside the file.
	src, err := s.DB.LibrarySource(ctx, t.LibraryID)
	if err != nil {
		return false, err
	}
	title, year, err := s.searchName(ctx, src.Kind, t.MatchItem)
	if err != nil {
		return false, err
	}
	q := Query{Kind: providerKinds[t.Kind], Title: title, Year: year, Locale: locale, IncludeAdult: s.IncludeAdult}
	ref := Ref{Provider: p.Name(), Kind: q.Kind}
	if t.Match != db.MatchFound {
		// The ids of a title the server has not matched are the ones its name or NFO file
		// carries, and an id is exact, as it is the first time a title is looked up. A matched
		// title's ids are the match being questioned, so it is looked up by name.
		for _, id := range t.ExternalIDs {
			if id.Provider == p.Name() {
				ref.ID = id.ID
			} else {
				q.IDs = append(q.IDs, Ref{Provider: id.Provider, Kind: q.Kind, ID: id.ID})
			}
		}
	}
	if ref.ID == "" {
		candidates, err := p.Search(ctx, q)
		if err != nil {
			return false, err
		}
		best, reason := Choose(q, candidates)
		if best < 0 {
			// A title that was matched stays as it is; one that was not is listed with the new answers.
			return false, s.unmatched(ctx, t.ID, reason, candidates)
		}
		ref = candidates[best].Ref
	}
	record, err := p.Details(ctx, ref, locale)
	if errors.Is(err, ErrNotFound) {
		return false, s.unmatched(ctx, t.ID, ReasonIDNotFound, nil)
	}
	if err != nil {
		return false, err
	}
	if _, err := s.DB.ChangeMatch(ctx, db.User, t.ID, details(record, db.MatchFound)); err != nil {
		return false, err
	}
	s.moods(ctx, t.ID)
	return true, s.children(ctx, p, locale, t.ID, record.Ref)
}

// moods gives a title that has just been matched its moods from the title data. It cannot fail
// the match: a title it misses gets them at the daily pass.
func (s *Service) moods(ctx context.Context, itemID int64) {
	_ = s.DB.TagFromTitleData(ctx, itemID)
}

// searchName is what a title is looked up by: the title it has, or, when that is the title
// written inside its file (shown after a lookup that did not match), the name and year its
// file's name gives, read again with the scanner's own naming. A title an NFO file, a person
// or a provider gave is looked up as it is, however badly its file is named.
func (s *Service) searchName(ctx context.Context, libraryKind string, item db.MatchItem) (string, int, error) {
	rel, err := s.DB.EmbeddedTitleFile(ctx, item.ID)
	if errors.Is(err, db.ErrNotFound) {
		return item.Title, item.Year, nil
	}
	if err != nil {
		return "", 0, err
	}
	title, year := scan.FileTitle(libraryKind, rel)
	if year == 0 {
		year = item.Year
	}
	return title, year, nil
}
