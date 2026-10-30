package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// A title is its own thing; its files come and go. What makes two files the
// same title is the title's identity, never the place a file happens to sit:
//
//   - a provider identity, where one is known ("tmdb movie 348");
//   - otherwise the identity the names were read as (a film's title and year,
//     a show's folder, an episode's place in its show, a track's place on its
//     album).
//
// The ledger (catalog_identities) holds every such name a title is known by.
// This file is the one place that turns an identity into a title.

// IdentityText is a name in the form identities compare: case and accents
// folded, punctuation and spacing reduced to single spaces. "WALL·E", "Wall-E"
// and "wall e" are one name; so are "Amélie" and "Amelie".
func IdentityText(s string) string {
	var b strings.Builder
	space := false
	for _, r := range sorttext.Fold(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		// An apostrophe joins ("Schindler's" and "Schindlers" are one word).
		if r == '\'' || r == '’' {
			continue
		}
		space = true
	}
	return b.String()
}

// MovieKey names a film by the title and year its file or folder was read as.
// Two files read as the same film are versions of it.
func MovieKey(title string, year int) string {
	return "movie:" + IdentityText(title) + ":" + strconv.Itoa(year)
}

// ShowFolderKey names a show by the folder its episodes sit in (the folder's
// own name, whichever source it is under). A show is one folder; two folders
// become one show only when a provider says they are the same series.
func ShowFolderKey(folder string) string { return "folder:" + IdentityText(folder) }

// ShowTitleKey names a show that has no folder of its own (loose episode
// files) by the title and year the files were read as.
func ShowTitleKey(title string, year int) string {
	return IdentityText(title) + ":" + strconv.Itoa(year)
}

// TrackKey names a track by its place on a release: the album artist, the
// album, the disc and track numbers and the track's title, all from tags.
// Empty when the tags do not say enough to tell two tracks apart.
func TrackKey(albumArtist, album string, disc, track int, title string) string {
	artist, name, song := IdentityText(albumArtist), IdentityText(album), IdentityText(title)
	if artist == "" || name == "" || song == "" || track <= 0 {
		return ""
	}
	// Placeholder tags written by rippers describe every untagged disc alike.
	if artist == "unknown artist" || artist == "unknown" || name == "unknown album" || name == "unknown" {
		return ""
	}
	return fmt.Sprintf("track:%s/%s/%d/%d/%s", artist, name, disc, track, song)
}

// ProviderKey names a title by a provider's identity for it. kind is the
// provider's own kind word (movie, series, episode, recording, …).
func ProviderKey(provider, kind, id string) string {
	return "provider:" + strings.ToLower(provider) + ":" + strings.ToLower(kind) + ":" + id
}

// IsLocatorKey reports whether an identity key names a place (a file's path)
// rather than a title. A place can be vacated and reused by another title.
func IsLocatorKey(key string) bool {
	return strings.HasPrefix(key, "file:") || strings.HasPrefix(key, "extra:")
}

// AddIdentityTx records another name for an entity. A name already held by
// the entity is left alone; one held by another live entity is reported as
// taken (the caller decides whether that means the two are one title).
func AddIdentityTx(ctx context.Context, tx *sql.Tx, entity int64, key string) (taken int64, err error) {
	if err = RequireEntityAcquirableTx(ctx, tx, entity); err != nil {
		return 0, err
	}
	if key == "" {
		return 0, nil
	}
	var public []byte
	var library int64
	var kind Kind
	if err = tx.QueryRowContext(ctx, `SELECT public_id,library_id,kind FROM catalog_entities WHERE id=?`, entity).Scan(&public, &library, &kind); err != nil {
		return 0, err
	}
	root, err := libraryRoot(ctx, tx, library)
	if err != nil {
		return 0, err
	}
	var holder []byte
	var live sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT i.public_id,e.id FROM catalog_identities i LEFT JOIN catalog_entities e ON e.public_id=i.public_id WHERE i.root=? AND i.source_key=?`, root, key).Scan(&holder, &live)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_identities(root,source_key,public_id,kind) VALUES(?,?,?,?)`, root, key, public, int(kind))
		return 0, err
	case err != nil:
		return 0, err
	case live.Valid && live.Int64 == entity:
		return 0, nil
	case live.Valid:
		return live.Int64, nil
	}
	// The name belonged to a title that is gone: it is this title's now.
	_, err = tx.ExecContext(ctx, `UPDATE catalog_identities SET public_id=?,kind=? WHERE root=? AND source_key=?`, public, int(kind), root, key)
	return 0, err
}

// MoveIdentityTx gives a name to an entity whoever held it.
func MoveIdentityTx(ctx context.Context, tx *sql.Tx, entity int64, key string) error {
	if err := RequireEntityAcquirableTx(ctx, tx, entity); err != nil {
		return err
	}
	var public []byte
	var library int64
	var kind Kind
	if err := tx.QueryRowContext(ctx, `SELECT public_id,library_id,kind FROM catalog_entities WHERE id=?`, entity).Scan(&public, &library, &kind); err != nil {
		return err
	}
	root, err := libraryRoot(ctx, tx, library)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_identities(root,source_key,public_id,kind) VALUES(?,?,?,?)
 ON CONFLICT(root,source_key) DO UPDATE SET public_id=excluded.public_id,kind=excluded.kind`, root, key, public, int(kind))
	return err
}

// DropIdentityTx removes one name of an entity (a place it no longer occupies).
func DropIdentityTx(ctx context.Context, tx *sql.Tx, entity int64, key string) error {
	if err := RequireEntityAcquirableTx(ctx, tx, entity); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_identities WHERE source_key=?2 AND public_id=(SELECT public_id FROM catalog_entities WHERE id=?1)
 AND root=(SELECT l.root FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.id=?1)`, entity, key)
	return err
}

// IdentitiesTx lists the names an entity is known by.
func IdentitiesTx(ctx context.Context, tx *sql.Tx, entity int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT i.source_key FROM catalog_entities e JOIN catalog_identities i INDEXED BY catalog_identities_public ON i.public_id=e.public_id WHERE e.id=? ORDER BY i.source_key`, entity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

// ForgetIdentitiesTx removes every name of an entity that is being removed for
// good, so the ledger holds only titles the catalogue has.
func ForgetIdentitiesTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_identities WHERE public_id=(SELECT public_id FROM catalog_entities WHERE id=?)`, entity)
	return err
}

// ProviderIdentity is one provider's name for a title.
type ProviderIdentity struct {
	Provider string // tmdb, tvdb, imdb, musicbrainz, …
	Kind     string // the provider's kind word: movie, series, episode, recording, …
	ID       string
}

func normalizedProvider(p ProviderIdentity) (ProviderIdentity, error) {
	p.Provider, p.Kind, p.ID = strings.ToLower(strings.TrimSpace(p.Provider)), strings.ToLower(strings.TrimSpace(p.Kind)), strings.TrimSpace(p.ID)
	if p.Provider == "" || p.Kind == "" || p.ID == "" || len(p.Provider) > 64 || len(p.Kind) > 64 || len(p.ID) > 256 {
		return p, ErrIdentityConflict
	}
	for _, value := range []string{p.Provider, p.Kind, p.ID} {
		for _, r := range value {
			if unicode.IsControl(r) {
				return p, ErrIdentityConflict
			}
		}
	}
	return p, nil
}

func providerCompatibleTx(ctx context.Context, tx *sql.Tx, entity int64, p ProviderIdentity) (bool, error) {
	var conflicting bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_external_ids WHERE entity_id=? AND provider=? AND provider_kind=? AND provider_id<>?)`, entity, p.Provider, p.Kind, p.ID).Scan(&conflicting)
	return !conflicting, err
}

// RecordProviderIdentityTx records proven identity without silently replacing
// a different identity already accepted for this provider and kind.
func RecordProviderIdentityTx(ctx context.Context, tx *sql.Tx, entity int64, p ProviderIdentity) error {
	if err := RequireEntityAcquirableTx(ctx, tx, entity); err != nil {
		return err
	}
	p, err := normalizedProvider(p)
	if err != nil {
		return err
	}
	// An admitted adoption walks the live identity index under frozen
	// acquisition authority. A competing family cannot add a new candidate
	// behind its completed key (including an integer ID reused below it).
	var adopting bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entity_mutations m JOIN catalog_entities e ON e.id=? WHERE m.status<>'terminal' AND m.mode='adopt' AND m.family_id<>? AND m.library_id=e.library_id AND m.entity_kind=e.kind AND m.provider=? AND m.provider_kind=? AND m.provider_id=?)`, entity, mutationFamily(ctx), p.Provider, p.Kind, p.ID).Scan(&adopting); err != nil {
		return err
	}
	if adopting {
		return ErrEntityMutating
	}
	ok, err := providerCompatibleTx(ctx, tx, entity, p)
	if err != nil {
		return err
	}
	if !ok {
		return ErrIdentityConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_external_ids(entity_id,provider,provider_kind,provider_id) VALUES(?,?,?,?)`, entity, p.Provider, p.Kind, p.ID)
	return err
}

// FindByProviderTx returns the live entity of a library and kind that carries
// a provider identity (0 when none does). When several do, the oldest wins.
func FindByProviderTx(ctx context.Context, tx *sql.Tx, library int64, kind Kind, p ProviderIdentity) (int64, error) {
	if p.Provider == "" || p.ID == "" {
		return 0, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT e.id FROM catalog_external_ids x INDEXED BY catalog_external_ids_lookup JOIN catalog_entities e ON e.id=x.entity_id
 WHERE x.provider=? AND x.provider_kind=? AND x.provider_id=? AND e.library_id=? AND e.kind=? ORDER BY e.id LIMIT 1`, p.Provider, p.Kind, p.ID, library, int(kind)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err == nil {
		err = RequireEntityAcquirableTx(ctx, tx, id)
	}
	return id, err
}

// TitleClaim is what a file (or a writer that knows a title some other way)
// says a title is: its kind and place in the catalogue, and every identity it
// is known by. It is the single description the attach point works from; the
// name reader, the tag reader and the matcher each fill the part they know.
type TitleClaim struct {
	Library int64 // catalog_libraries.id
	Kind    Kind
	Parent  int64 // 0: none

	// Keys are the names the title was read as, most trusted first (MovieKey,
	// TrackKey, EpisodeKey, …). The first is the name a new title is created
	// under. May be empty when only Locator or Provider is known.
	Keys []string
	// Locator is the place the file sits (ItemKey), or "". It finds the title
	// again when a file is replaced in place, and only then: a title whose file
	// now lives somewhere else has given the place up.
	Locator string
	// Provider identities, when a name hint, local metadata or a match gave one.
	Provider []ProviderIdentity
	// SourceEdition is the explicit nearest show-folder edition. It scopes
	// automatic show identity only; movie editions remain file versions.
	SourceEdition string

	// Facts for a title that has to be created.
	Title string
	Year  int
	Added string
}

// ResolveTitleTx returns the title a claim names, creating it when the
// catalogue has none. Order: a provider identity, then the names it was read
// as, then the place (see TitleClaim.Locator). Every name in the claim is then
// recorded for the title, so the next file that says any of them finds it.
func ResolveTitleTx(ctx context.Context, tx *sql.Tx, c TitleClaim) (id int64, created bool, err error) {
	if c.Library == 0 || c.Kind < 1 || len(c.Keys) == 0 && c.Locator == "" && len(c.Provider) == 0 || len(c.Provider) > 12 {
		return 0, false, fmt.Errorf("a title claim needs a library, a kind and at least one identity")
	}
	if c.SourceEdition != "" && c.Kind != Show {
		return 0, false, ErrIdentityConflict
	}
	providers := make([]ProviderIdentity, len(c.Provider))
	for i, p := range c.Provider {
		if providers[i], err = normalizedProvider(p); err != nil {
			return 0, false, err
		}
	}
	ownerAlias := false
	if c.Kind == Show {
		id, err = mergedShowAliasTx(ctx, tx, c, providers)
		if err != nil {
			return 0, false, err
		}
		ownerAlias = id != 0
	}
	for _, p := range providers {
		if ownerAlias {
			break
		}
		held, e := findClaimProviderTx(ctx, tx, c.Library, c.Kind, p, c.SourceEdition)
		if e != nil {
			return 0, false, e
		}
		if held != 0 && id != 0 && held != id {
			return 0, false, ErrIdentityConflict
		}
		if held != 0 {
			id = held
		}
	}
	compatible := func(candidate int64) (bool, error) {
		if err := RequireEntityAcquirableTx(ctx, tx, candidate); err != nil {
			return false, err
		}
		if candidate == 0 {
			return true, nil
		}
		if c.Kind == Show && !(ownerAlias && candidate == id) {
			var edition string
			if err := tx.QueryRowContext(ctx, `SELECT source_edition FROM catalog_shows WHERE entity_id=?`, candidate).Scan(&edition); err != nil {
				return false, err
			}
			if edition != c.SourceEdition {
				return false, nil
			}
		}
		for _, p := range providers {
			ok, e := providerCompatibleTx(ctx, tx, candidate, p)
			if e != nil || !ok {
				return false, e
			}
		}
		return true, nil
	}
	if id != 0 {
		ok, e := compatible(id)
		if e != nil {
			return 0, false, e
		}
		if !ok {
			return 0, false, ErrIdentityConflict
		}
	}
	for i := 0; id == 0 && i < len(c.Keys); i++ {
		candidate, e := findLiveTx(ctx, tx, c.Library, c.Kind, c.Keys[i])
		if e != nil {
			return 0, false, e
		}
		if candidate == 0 {
			continue
		}
		ok, e := compatible(candidate)
		if e != nil {
			return 0, false, e
		}
		if ok {
			id = candidate
		}
	}
	if id == 0 && c.Locator != "" {
		held, e := findLiveTx(ctx, tx, c.Library, c.Kind, c.Locator)
		if e != nil {
			return 0, false, e
		}
		ok, e := compatible(held)
		if e != nil {
			return 0, false, e
		}
		if held != 0 && ok {
			var elsewhere bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? AND `+presentFileSQL+`)`, held).Scan(&elsewhere); err != nil {
				return 0, false, err
			}
			if !elsewhere {
				id = held
			} else if err = DropIdentityTx(ctx, tx, held, c.Locator); err != nil {
				// The title that was here has its file somewhere else now: the
				// place is free for whatever this file turns out to be.
				return 0, false, err
			}
		}
	}
	if id == 0 {
		primary := c.Locator
		if len(c.Keys) > 0 {
			primary = c.Keys[0]
		}
		if len(providers) > 0 {
			p := providers[0]
			primary = ProviderKey(p.Provider, p.Kind, p.ID)
			if c.Kind == Show {
				primary = ShowEditionIdentityKey(primary, c.SourceEdition)
			}
		}
		// A name held by a live title of another kind or library cannot be shared.
		if id, created, err = EnsureEntityTx(ctx, tx, Entity{Library: c.Library, Kind: c.Kind, Parent: c.Parent, Key: primary, Title: c.Title, Year: c.Year, Added: c.Added}); err != nil {
			return 0, false, err
		}
		if created && c.Kind == Show && c.SourceEdition != "" {
			if err = SetFactsTx(ctx, tx, id, map[string]any{"source_edition": c.SourceEdition, "local_key": primary}); err != nil {
				return 0, false, err
			}
		}
	} else {
		// A title that had left browse and search because it lost its last
		// member is live again.
		res, e := tx.ExecContext(ctx, `UPDATE catalog_entities SET retired=0 WHERE id=? AND retired<>0`, id)
		if e != nil {
			return 0, false, e
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if err = touchEntity(ctx, tx, id, c.Kind); err != nil {
				return 0, false, err
			}
		}
	}
	for _, key := range c.Keys {
		if _, err = AddIdentityTx(ctx, tx, id, key); err != nil {
			return 0, false, err
		}
	}
	if c.Locator != "" {
		if err = MoveIdentityTx(ctx, tx, id, c.Locator); err != nil {
			return 0, false, err
		}
	}
	for _, p := range providers {
		if err = RecordProviderIdentityTx(ctx, tx, id, p); err != nil {
			return 0, false, err
		}
	}
	return id, created, nil
}

// AttachFileTx is the attach point: it resolves the title a file belongs to
// (ResolveTitleTx) and makes the file a version of it. A title that was
// missing is whole again; its public id, metadata and everyone's state were
// kept for exactly this.
func AttachFileTx(ctx context.Context, tx *sql.Tx, c TitleClaim, asset int64, link Link) (id int64, created bool, err error) {
	if id, created, err = ResolveTitleTx(ctx, tx, c); err != nil {
		return 0, false, err
	}
	return id, created, LinkAssetTx(ctx, tx, id, asset, link)
}

// findLiveTx returns the live entity of a library and kind a name belongs to.
func findLiveTx(ctx context.Context, tx *sql.Tx, library int64, kind Kind, key string) (int64, error) {
	if key == "" {
		return 0, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT e.id FROM catalog_libraries l JOIN catalog_identities i ON i.root=l.root AND i.source_key=?2
 JOIN catalog_entities e ON e.public_id=i.public_id AND e.library_id=l.id AND e.kind=?3 WHERE l.id=?1`, library, key, int(kind)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// BestVersionOrder is the ORDER BY that puts a title's best file first, for a
// query whose link and asset rows are aliased link and asset: the first part,
// a file that is there before one that is not, the taller picture, then the
// larger file (the higher bit rate of two encodes of one length). It is the
// version the server plays when a viewer names none. Which version a
// particular device plays best is the playback planner's question; this is
// the answer when nothing else is known.
func BestVersionOrder(link, asset string) string {
	return link + ".part_index," + asset + ".available DESC," + asset + ".height DESC," + asset + ".size DESC," + asset + ".token"
}
