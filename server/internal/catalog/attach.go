package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"regexp"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// The attach point: where a scanned file becomes a version of a title.
//
// Whoever reads the file says three things: which file it is, what its name,
// folder and tags were read as (the parsed identity), and which provider
// identity it carries if a name hint or local metadata gave one. This file
// turns those into "that title" and links the file to it. The name reader, the
// matcher and the scan loop decide what a file is; nothing here reads a name.
//
//   - AttachMovieTx     a film file
//   - AttachTrackTx     a music track or an audiobook part (called again when
//                       tags are read, with the fuller identity)
//   - commitEpisodes    an episode file (it calls resolveShowTx for the show)
//
// A file that resolves to a title the catalogue already has becomes another
// version of it. A title that had lost its files is whole again, with the id,
// metadata and personal state it kept. A title that has to be created gets
// back whatever state was kept for it when it was last removed.

// MovieFile is a film file with what it was read as.
type MovieFile struct {
	Library string // libraries.id
	Asset   int64  // catalog_assets.id
	Path    string // where the file is

	// The parsed identity.
	Title   string
	Year    int
	Edition string // "Director's Cut", when the name or local metadata said so
	Part    int    // 0 for a whole film; 1, 2, … for the parts of a split one

	// Provider identities from a name hint ({tmdb-348}) or local metadata.
	Provider []compactcatalog.ProviderIdentity
}

// AttachMovieTx makes a film file a version of the film it was read as and
// returns the film. Two files read as the same title and year, or carrying
// the same provider identity, are versions of one film.
func AttachMovieTx(ctx context.Context, tx *sql.Tx, f MovieFile) (int64, error) {
	handle, err := compactcatalog.LibraryTx(ctx, tx, f.Library)
	if err != nil {
		return 0, err
	}
	claim := compactcatalog.TitleClaim{Library: handle, Kind: compactcatalog.Movie, Provider: f.Provider, Title: f.Title, Year: f.Year, Added: catalogedNow()}
	if compactcatalog.IdentityText(f.Title) != "" {
		claim.Keys = []string{compactcatalog.MovieKey(f.Title, f.Year)}
	} else {
		// A name that says nothing still has a place.
		root, e := compactcatalog.LibraryRootTx(ctx, tx, handle)
		if e != nil {
			return 0, e
		}
		claim.Locator = compactcatalog.ItemKey(root, f.Path, 0)
	}
	item, created, err := compactcatalog.AttachFileTx(ctx, tx, claim, f.Asset, compactcatalog.Link{Part: f.Part, Edition: f.Edition})
	if err != nil {
		return 0, err
	}
	if created || len(f.Provider) > 0 {
		err = restoreRetainedTx(ctx, tx, item)
	}
	return item, err
}

// TrackFile is a music track or audiobook part with what it was read as.
type TrackFile struct {
	CueOrdinal int
	Library    int64               // catalog_libraries.id
	Kind       compactcatalog.Kind // Track or Part
	Asset      int64
	Path       string
	Title      string

	// TagKey is the track's identity from its tags (compactcatalog.TrackKey),
	// or "" while tags have not been read or do not say enough.
	TagKey   string
	Provider []compactcatalog.ProviderIdentity
}

// AttachTrackTx makes an audio file a version of the track it is. Before tags
// are read the file is known only by its place; once they are, the track is
// known by what it is, and a second file with the same tags is a version of it.
func AttachTrackTx(ctx context.Context, tx *sql.Tx, f TrackFile) (int64, error) {
	root, err := compactcatalog.LibraryRootTx(ctx, tx, f.Library)
	if err != nil {
		return 0, err
	}
	claim := compactcatalog.TitleClaim{Library: f.Library, Kind: f.Kind, Provider: f.Provider, Title: f.Title, Added: catalogedNow(), Locator: compactcatalog.ItemKey(root, f.Path, f.CueOrdinal)}
	if f.TagKey != "" {
		claim.Keys = []string{f.TagKey}
	}
	item, created, err := compactcatalog.AttachFileTx(ctx, tx, claim, f.Asset, compactcatalog.Link{})
	if err != nil {
		return 0, err
	}
	if created || len(f.Provider) > 0 {
		err = restoreRetainedTx(ctx, tx, item)
	}
	return item, err
}

// IdentifyTrackTx tells the catalogue what an already attached audio file
// turned out to be once its tags were read. When another track of the library
// already has that identity the two are one: the file's track is merged into
// it and the surviving track is returned.
func IdentifyTrackTx(ctx context.Context, tx *sql.Tx, item int64, tagKey string) (int64, error) {
	if tagKey == "" {
		return item, nil
	}
	held, err := compactcatalog.AddIdentityTx(ctx, tx, item, tagKey)
	if err != nil {
		return item, err
	}
	if held == 0 {
		return item, restoreRetainedTx(ctx, tx, item)
	}
	var same bool
	if err = tx.QueryRowContext(ctx, `SELECT a.kind=b.kind AND a.library_id=b.library_id FROM catalog_entities a,catalog_entities b WHERE a.id=? AND b.id=?`, item, held).Scan(&same); err != nil || !same {
		return item, err
	}
	// The title that has been here longer keeps its id: it is the one people
	// have state on and lists pointing at.
	deferred, e := deferAudioMergeTx(ctx, tx, item, held, tagKey)
	if e != nil {
		return 0, e
	}
	if deferred {
		// During file publication the source must finish its own facts first.
		// The final writer tail admits the captured pair. A direct call has
		// already durably admitted it; both return the live source until absorb.
		return item, nil
	}
	if err = MergeTitlesTx(ctx, tx, item, held); err != nil {
		return 0, err
	}
	return held, restoreRetainedTx(ctx, tx, held)
}

var bracketed = regexp.MustCompile(`[\[({][^\])}]*[\])}]`)

// showFolder is the folder a show's episodes sit in, from an episode file's
// path relative to its source: the nearest folder above the file that is not
// a season folder. Empty for a loose file.
func showFolder(relative string) string {
	reading := naming.Read(relative, "tv")
	for _, title := range reading.Titles {
		if title.Source != naming.FileName {
			return filepath.FromSlash(title.Path)
		}
	}
	return ""
}

// folderNamesShow reports whether a folder's name is the show's name: equal
// to the title the episode was read as, or one extending the other ("The
// Office" and "The Office US"). A folder that only holds assorted shows
// ("Misc", "New") names none of them.
func folderNamesShow(folder, title string) bool {
	a := compactcatalog.IdentityText(title)
	b := compactcatalog.IdentityText(bracketed.ReplaceAllString(folder, " "))
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(b, a+" ") || strings.HasPrefix(a, b+" ") || oneSpellingDifference(a, b)
}

// One small spelling error does not turn a show folder into assorted loose
// files. Short or wholly different names still use their own parsed identity.
func oneSpellingDifference(a, b string) bool {
	x, y := []rune(a), []rune(b)
	if len(x) < 6 || len(y) < 6 || len(x) > 128 || len(y) > 128 || len(x)-len(y) > 1 || len(y)-len(x) > 1 {
		return false
	}
	i, j, edits := 0, 0, 0
	for i < len(x) && j < len(y) {
		if x[i] == y[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > 1 {
			return false
		}
		switch {
		case len(x) > len(y):
			i++
		case len(y) > len(x):
			j++
		case i+1 < len(x) && j+1 < len(y) && x[i] == y[j+1] && x[i+1] == y[j]:
			i += 2
			j += 2
		default:
			i++
			j++
		}
	}
	return edits+(len(x)-i)+(len(y)-j) <= 1
}

// resolveShowTx returns the show an episode file belongs to and the show's
// local key (the stem of its seasons' and episodes' identity keys).
//
// A show is the folder its episodes sit in, within one source. Episodes in
// that folder belong to that show whatever years or spellings their own names
// carry, and a folder of the same title elsewhere is another show until a
// provider says the two are the same series (AdoptProviderIdentityTx merges
// them). Only loose files, and files an owner assigns by hand, find their show
// by title and year.
func resolveShowTx(ctx context.Context, tx *sql.Tx, library string, handle, asset int64, plan EpisodeNaming, manual bool) (int64, string, error) {
	edition := plan.ShowEdition
	if manual {
		edition = ""
	}
	titleKey := compactcatalog.ShowEditionIdentityKey(compactcatalog.ShowTitleKey(plan.ShowTitle, plan.Year), edition)
	primary := titleKey
	if !manual {
		source, relative, err := assetSourceTx(ctx, tx, library, asset)
		if err != nil {
			return 0, "", err
		}
		if folder := showFolder(relative); source != "" && folder != "" && folderNamesShow(filepath.Base(folder), plan.ShowTitle) {
			primary = "folder:" + source + ":" + filepath.ToSlash(folder)
		}
	}
	show, created, err := compactcatalog.ResolveTitleTx(ctx, tx, compactcatalog.TitleClaim{Library: handle, Kind: compactcatalog.Show, SourceEdition: edition, Provider: plan.Provider, Keys: []string{compactcatalog.ShowKey(primary)}, Title: plan.ShowTitle, Year: plan.Year})
	if err != nil {
		return 0, "", err
	}
	if created {
		if err = compactcatalog.SetFactsTx(ctx, tx, show, map[string]any{"local_key": primary}); err != nil {
			return 0, "", err
		}
		// The title also names the show while no other show has it, so a loose
		// file or a hand assignment by title finds it.
		if _, err = compactcatalog.AddIdentityTx(ctx, tx, show, compactcatalog.ShowKey(titleKey)); err != nil {
			return 0, "", err
		}
		return show, primary, restoreRetainedTx(ctx, tx, show)
	}
	var key string
	err = tx.QueryRowContext(ctx, `SELECT local_key FROM catalog_shows WHERE entity_id=?`, show).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		key, err = primary, compactcatalog.SetFactsTx(ctx, tx, show, map[string]any{"local_key": primary})
	}
	return show, key, err
}

// assetSourceTx finds which of a library's sources a file sits under and its
// path relative to that source ("" when it sits under none: a recording, a
// file whose place was taken).
func assetSourceTx(ctx context.Context, tx *sql.Tx, library string, asset int64) (source, relative string, err error) {
	var path string
	if err = tx.QueryRowContext(ctx, `SELECT path FROM catalog_assets WHERE id=?`, asset).Scan(&path); err != nil {
		return "", "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,root FROM library_sources WHERE library_id=? ORDER BY length(root) DESC,id`, library)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id, root string
		if err = rows.Scan(&id, &root); err != nil {
			return "", "", err
		}
		rel, e := filepath.Rel(root, path)
		if e == nil && filepath.IsLocal(rel) {
			return id, rel, rows.Err()
		}
	}
	return "", "", rows.Err()
}

// reinterpretMovieTx is called when a film file was renamed or moved. The new
// name is read as a title and year (by the caller); if that is still the film
// the file belongs to, nothing changes. Otherwise:
//
//   - a film that has only this file is the same film with a corrected name:
//     it takes the new name (and its title, unless the owner locked it), and
//     keeps its id and everyone's state;
//   - a file leaving a film that has other files, or a name that belongs to
//     another film the library already has, moves to that film.
//
// A file an owner attached by hand stays where the owner put it.
func reinterpretMovieTx(ctx context.Context, tx *sql.Tx, library string, asset int64, token, path, title string, year int) error {
	if compactcatalog.IdentityText(title) == "" {
		return nil
	}
	handle, err := compactcatalog.LibraryTx(ctx, tx, library)
	if err != nil {
		return err
	}
	var manual bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_version_links WHERE library_id=? AND asset_id=?)`, library, token).Scan(&manual); err != nil || manual {
		return err
	}
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT l.entity_id FROM catalog_asset_links l INDEXED BY catalog_asset_links_asset JOIN catalog_entities e ON e.id=l.entity_id WHERE l.asset_id=? AND e.library_id=? AND e.kind=? ORDER BY l.entity_id LIMIT 1`, asset, handle, int(compactcatalog.Movie)).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	key := compactcatalog.MovieKey(title, year)
	keys, err := compactcatalog.IdentitiesTx(ctx, tx, current)
	if err != nil {
		return err
	}
	for _, have := range keys {
		if have == key {
			return nil
		}
	}
	var others int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND asset_id<>?`, current, asset).Scan(&others); err != nil {
		return err
	}
	if others == 0 {
		held, e := compactcatalog.AddIdentityTx(ctx, tx, current, key)
		if e != nil {
			return e
		}
		if held == 0 {
			// The names the film was read as before no longer describe a file.
			for _, old := range keys {
				if strings.HasPrefix(old, "movie:") {
					if e = compactcatalog.DropIdentityTx(ctx, tx, current, old); e != nil {
						return e
					}
				}
			}
			var matched bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_external_ids WHERE entity_id=?)`, current).Scan(&matched); e != nil || matched {
				// A provider named this film; its title is the provider's.
				return e
			}
			return compactcatalog.SetFieldsTx(ctx, tx, current, compactcatalog.Automatic, map[string]any{"title": title, "year": year})
		}
	}
	var part int
	var edition string
	if err = tx.QueryRowContext(ctx, `SELECT part_index,edition FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, current, asset).Scan(&part, &edition); err != nil {
		return err
	}
	if err = compactcatalog.UnlinkAssetTx(ctx, tx, current, asset); err != nil {
		return err
	}
	_, err = AttachMovieTx(ctx, tx, MovieFile{Library: library, Asset: asset, Path: path, Title: title, Year: year, Edition: edition, Part: part})
	return err
}
