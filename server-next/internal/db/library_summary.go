package db

import (
	"context"
	"database/sql"
)

const librarySummaryCountLimit = 20000

type LibrarySummaryCount struct {
	Kind    string `json:"kind"`
	Count   int    `json:"count"`
	AtLeast bool   `json:"atLeast"`
}

type LibrarySummary struct {
	Library
	FirstScan   bool
	Counts      []LibrarySummaryCount
	CoverHashes []string
}

type librarySummaryKind struct {
	name  string
	kinds []ItemKind
}

var librarySummaryKinds = map[string][]librarySummaryKind{
	LibraryMovies:     {{"movie", []ItemKind{KindMovie}}},
	LibraryShows:      {{"show", []ItemKind{KindShow}}, {"episode", []ItemKind{KindEpisode}}},
	LibraryAnime:      {{"movie", []ItemKind{KindMovie}}, {"show", []ItemKind{KindShow}}, {"episode", []ItemKind{KindEpisode}}},
	LibraryMusic:      {{"artist", []ItemKind{KindArtist}}, {"album", []ItemKind{KindAlbum}}, {"song", []ItemKind{KindTrack}}},
	LibraryAudiobooks: {{"audiobook", []ItemKind{KindAudiobook}}},
	LibraryBooks:      {{"book", []ItemKind{KindBook}}},
	LibraryComics:     {{"comic", []ItemKind{KindComic, KindIssue}}},
}

// LibrarySummaries lists the libraries, title counts and artwork visible to one profile.
func (d *DB) LibrarySummaries(ctx context.Context, v Viewer) ([]LibrarySummary, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	libraries, err := readLibraries(ctx, tx, acc, "")
	if err != nil {
		return nil, err
	}
	out := make([]LibrarySummary, 0, len(libraries))
	for _, library := range libraries {
		summary, err := summarizeLibrary(ctx, tx, v, acc, library)
		if err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, nil
}

func summarizeLibrary(ctx context.Context, tx *sql.Tx, v Viewer, acc access, library Library) (LibrarySummary, error) {
	out := LibrarySummary{Library: library, FirstScan: library.firstScan, Counts: []LibrarySummaryCount{}}
	for _, kind := range librarySummaryKinds[library.Kind] {
		where := newQuery(v)
		q := ItemQuery{Library: library.PublicID, Kinds: kind.kinds}
		found, err := q.filters(ctx, tx, where, acc)
		if err != nil {
			return out, err
		}
		count := 0
		if found {
			count, err = total(ctx, tx, where, acc)
			if err != nil {
				return out, err
			}
		}
		out.Counts = append(out.Counts, LibrarySummaryCount{
			Kind: kind.name, Count: min(count, librarySummaryCountLimit), AtLeast: count > librarySummaryCountLimit,
		})
	}
	covers, err := libraryCoverHashes(ctx, tx, library, acc.visible("i"), 3)
	out.CoverHashes = covers
	return out, err
}
