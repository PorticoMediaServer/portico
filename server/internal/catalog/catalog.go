package catalog

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrSourceAlreadyConfigured = errors.New("This source is already configured as a library. Open that library to manage or scan it.")

func DefaultLibraryView(kind string) string {
	if kind == "movie" {
		return "discover"
	}
	return "browse"
}

type Library struct {
	Summary     *LibrarySummary `json:"summary,omitempty"`
	DefaultView string          `json:"defaultView"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	// Pinned reflects the viewer's navigation arrangement; it is false on the
	// unscoped administrative listing.
	Pinned bool `json:"pinned"`
}
type Item struct {
	FileInfoOpening string               `json:"fileInfoOpening,omitempty"`
	AudioFacts      *MusicTechnicalFacts `json:"audioFacts,omitempty"`
	Missing         bool                 `json:"missing"`
	Watched         *bool                `json:"watched,omitempty"`
	AddedAt         *string              `json:"addedAt"`
	Song            *SongInfo            `json:"song,omitempty"`
	BookFile        *BookFileInfo        `json:"bookFile,omitempty"`
	Episode         *EpisodeInfo         `json:"episode,omitempty"`
	ID              string               `json:"id"`
	LibraryID       string               `json:"libraryId"`
	Title           string               `json:"title"`
	Kind            string               `json:"kind"`
	Year            int                  `json:"year,omitempty"`
	Duration        float64              `json:"duration"`
	Overview        string               `json:"overview,omitempty"`
	PosterURL       string               `json:"posterUrl,omitempty"`
	BackdropURL     string               `json:"backdropUrl,omitempty"`
	// StillURL is an episode's own still; absent when it has none (never the
	// season's or show's art).
	StillURL        string          `json:"stillUrl,omitempty"`
	ProgressSeconds float64         `json:"progressSeconds"`
	Available       bool            `json:"available"`
	Sources         []assets.Source `json:"sources,omitempty"`
}

// Service is the catalogue read and write surface. Everything mutable lives
// behind `state`, so binding a Service to a request context (WithContext) is a
// copy of four words rather than a copy of a mutex — one Service, one set of
// caches, one per-request view of it.
type Service struct {
	BulkCommands             BulkCommandHooks
	SeparateFolderRetirement SeparateFolderHooks
	// Clock supplies the composition instant; nil uses the system clock.
	Clock func() time.Time
	// browseInstant binds relative conditions to one read or signed continuation.
	browseInstant time.Time
	// OwnerMetadataRevision preserves the shared repair history without a package cycle.
	OwnerMetadataRevision func(context.Context, *sql.Tx, string, string) (func() error, error)
	db                    *sql.DB
	storage               *storage.Client
	state                 *serviceState
	// rctx is the request this Service is answering, or nil outside a request.
	// It carries the deadline and the read snapshot; see reads.go.
	readTx          *sql.Tx
	rctx            context.Context
	recRestrictions identity.ContentRestrictions
	recSources      map[string]homeSource
	recComposition  *recCompositionSources
}

// serviceState is the process-lifetime state one database's catalogue keeps.
type serviceState struct {
	homeScans          homeScanCache
	readPreparations   *readprep.Store
	genreRuntime       *GenreRuntime
	summaries          librarySummaryCache
	readingMobi        atomic.Bool
	visibilityMu       sync.Mutex
	visibilityRequests map[string]visibilityRequest
	visibilityWake     *worker.Signal
	countMu            sync.Mutex
	countCache         map[string]int
	// anchorCache holds non-title position anchors by browse revision (which
	// folds catalogue, viewer, restriction, query and sort), so the bucket
	// count runs once per publication rather than once per first page.
	anchorMu     sync.Mutex
	anchorCache  map[string][]BrowsePositionAnchor
	rangeAnchors map[string][]rangeAnchor
	browseTotals map[string]int
	facetCache   *facetCache
	schemaMu     sync.Mutex
	schemaTables map[string]bool
	cursorOnce   sync.Once
	cursorSecret []byte
	cursorErr    error
	// ratings caches the content-rating age projection for this database; see
	// restrictions.go for why it must not be package-level.
	ratings restrictionProjection
	// groupCursor is the last book id the current listening-group pass has
	// read; "" when no pass is in progress. See listening_groups.go.
	groupMu     sync.Mutex
	groupCursor string
	// recMemo holds recommendation rankings by every revision they read; see
	// rec_engine.go.
	recMu   sync.Mutex
	recMemo map[string][]recCandidate
	// acrossOrders holds recent ordered lists across libraries; see
	// browse_across.go.
	acrossOrders *acrossOrderCache
	// relationOrders holds relation row orders; see relation_rows.go.
	relationOrders *relationOrderCache
}

func New(db *sql.DB) *Service {
	return &Service{db: db, state: &serviceState{countCache: map[string]int{}, facetCache: newFacetCache(facetCacheEntries), acrossOrders: newAcrossOrderCache(), relationOrders: newRelationOrderCache(), visibilityRequests: map[string]visibilityRequest{}, visibilityWake: worker.NewSignal()}}
}
func (s *Service) Create(name, kind, path string) (Library, error) {
	return s.CreateContext(context.Background(), name, kind, path)
}
func (s *Service) CreateContext(ctx context.Context, name, kind, path string) (Library, error) {
	return s.CreateAuthorized(ctx, name, kind, path, nil)
}
func (s *Service) CreateAuthorized(ctx context.Context, name, kind, path string, authorize func(*sql.Tx) error) (Library, error) {
	return s.CreateAuthorizedThen(ctx, name, kind, path, authorize, nil)
}

// CreateAuthorizedThen runs created inside the transaction that inserts the
// library, so work that must accompany a new library (its first scan) commits
// with it or not at all. A library is never left configured with nothing queued.
func (s *Service) CreateAuthorizedThen(ctx context.Context, name, kind, path string, authorize func(*sql.Tx) error, created func(*sql.Tx, Library) error) (Library, error) {
	l := Library{ID: identity.Token(), Name: strings.TrimSpace(name), Kind: kind, DefaultView: DefaultLibraryView(kind)}
	if kind != "movie" && kind != "tv" && kind != "anime" && kind != "music" && kind != "audiobook" && kind != "book" && kind != "comic" || l.Name == "" || len(l.Name) > 100 || !utf8.ValidString(l.Name) || strings.IndexFunc(l.Name, unicode.IsControl) >= 0 {
		return l, ErrAdminQuery
	}
	if s.storage != nil && s.storage.SourceGuard != nil && !s.storage.IsRemote(path) {
		if e := s.storage.SourceGuard(path); e != nil {
			return l, e
		}
	}
	observed, e := s.inspectSource(ctx, path)
	if e != nil || storage.RootIdentity(observed) == "" {
		return l, errors.New("source unavailable or storage operation timed out")
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return l, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return l, e
		}
	}
	// A new library goes last once the owner has ordered them; before that, it sorts by name like the rest.
	_, e = tx.ExecContext(ctx, `INSERT INTO libraries(id,name,kind,root,position) VALUES(?,?,?,?,(SELECT CASE WHEN COALESCE(MAX(position),0)>0 THEN MAX(position)+1 ELSE 0 END FROM libraries))`, l.ID, l.Name, l.Kind, observed.Path)
	if e != nil {
		var exists int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM libraries WHERE root=?`, observed.Path).Scan(&exists) == nil {
			return l, ErrSourceAlreadyConfigured
		}
		return l, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE library_sources SET configured_root=?,root_identity=?,identity_confirmed=1,kind=?,classification=?,remote_source_id=`+remoteSourceIDExpr+` WHERE id=?`, path, storage.RootIdentity(observed), s.sourceKind(observed.Path), s.sourceClassification(observed.Path), path, path, l.ID); e != nil {
		return l, e
	}
	if created != nil {
		if e = created(tx, l); e != nil {
			return l, e
		}
	}
	return l, gated.Commit()
}
func (s *Service) Libraries() ([]Library, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out []Library
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).Libraries()
			return err
		})
		return out, err
	}
	// The owner's order first (position), then by name for libraries not ordered yet.
	rows, e := s.read().Query(`SELECT id,name,kind FROM libraries WHERE NOT EXISTS(SELECT 1 FROM catalog_libraries gone WHERE gone.library_id=libraries.id AND gone.retired=1) ORDER BY position,name,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		var l Library
		if e = rows.Scan(&l.ID, &l.Name, &l.Kind); e != nil {
			return nil, e
		}
		l.DefaultView = DefaultLibraryView(l.Kind)
		out = append(out, l)
	}
	return out, rows.Err()
}

// LibraryForItem is the library of a playable item.
func (s *Service) LibraryForItem(id string) (string, error) {
	var v string
	e := s.read().QueryRow(`SELECT l.library_id FROM catalog_entities i JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1 JOIN catalog_libraries l ON l.id=i.library_id WHERE i.public_id=pid_blob(?) AND l.retired=0`, id).Scan(&v)
	return v, e
}
func (s *Service) List(viewer Viewer, library, cursor string, limit int) ([]Item, string, error) {
	if library != "" && !viewer.AllowsLibrary(library) {
		return nil, "", sql.ErrNoRows
	}
	if err := s.prepareViewer(viewer); err != nil {
		return nil, "", err
	}
	if dbwork.Snapshot(s.Context()) == nil {
		var page []Item
		var next string
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			page, next, readErr = s.WithContext(ctx).List(viewer, library, cursor, limit)
			return readErr
		})
		return page, next, err
	}
	if err := s.compactProjectionReady(compactcatalog.DomainAvailability); err != nil {
		return nil, "", err
	}
	profile := viewer.Profile
	var after int64
	if cursor != "" {
		var err error
		if after, err = strconv.ParseInt(cursor, 10, 64); err != nil || after < 0 {
			return nil, "", ErrCursor
		}
	}
	visibility, visibleArgs := viewer.itemVisibilitySQL("i.id")
	limit = pageLimit(limit)
	args := append([]any{library, library, after}, visibleArgs...)
	args = append(args, limit+1)
	rows, e := s.read().Query(`SELECT i.id,pid(i.public_id) FROM catalog_entities i JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1 JOIN catalog_libraries lib ON lib.id=i.library_id WHERE lib.retired=0 AND i.retired=0 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id) AND (i.kind!=4 OR EXISTS(SELECT 1 FROM catalog_asset_links l WHERE l.entity_id=i.id)) AND (?='' OR lib.library_id=?) AND i.id>? AND `+visibility+` ORDER BY i.id LIMIT ?`, args...)
	if e != nil {
		return nil, "", e
	}
	ids := []string{}
	var last int64
	next := ""
	for rows.Next() {
		var internal int64
		var id string
		if e = rows.Scan(&internal, &id); e != nil {
			rows.Close()
			return nil, "", e
		}
		if len(ids) == limit {
			next = strconv.FormatInt(last, 10)
			break
		}
		ids = append(ids, id)
		last = internal
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, "", e
	}
	if len(ids) == 0 {
		return []Item{}, next, nil
	}
	items, e := s.compactPageItems(profile, ids, false)
	if e != nil {
		return nil, "", e
	}
	out := make([]Item, 0, len(ids))
	for _, id := range ids {
		i := items[id]
		if i == nil {
			return nil, "", ErrStaleContinuation
		}
		// List has never included the source-detail array in its wire shape.
		i.Sources = nil
		i.Watched = nil
		projectArtwork(i)
		out = append(out, *i)
	}
	urls := make([]*string, 0, 2*len(out))
	for i := range out {
		urls = append(urls, &out[i].PosterURL, &out[i].BackdropURL)
	}
	return out, next, s.describeItemArt(urls...)
}
func (s *Service) Get(profile, id string) (Item, error) {
	return s.getItem(profile, id, false)
}

// getItem shares every item fact and enrichment between the full item endpoint
// and Detail, which does not publish the source-detail array.
func (s *Service) getItem(profile, id string, includeSources bool) (Item, error) {
	i, e := s.itemFacts(profile, id)
	if e != nil {
		return i, e
	}
	since, err := s.MissingSince(s.Context(), id)
	if err != nil {
		return i, err
	}
	i.Missing = since != ""
	i.Sources = nil
	i.Available, i.Duration, e = s.compactItemAssetSummary(id)
	if e != nil {
		return i, e
	}
	if i.Kind == "episode" {
		i.Episode, e = s.episodeInfo(i.ID)
		if e != nil {
			return i, e
		}
	}
	if i.Kind == "song" {
		i.Song, e = s.songInfo(i.ID)
		if e == nil {
			i.AudioFacts, e = s.musicFacts(i.ID)
		}
	}
	if i.Kind == "audiobook_file" {
		i.BookFile, e = s.bookFileInfo(i.ID)
	}
	if e != nil {
		return i, e
	}
	projectArtwork(&i)
	if e = s.describeItemArt(&i.PosterURL, &i.BackdropURL); e != nil {
		return i, e
	}
	if i.Kind == "episode" {
		art, err := s.resolveArtwork([]artworkTarget{{"item", i.ID}})
		if err != nil {
			return i, err
		}
		i.StillURL = art[artworkTarget{"item", i.ID}].StillURL
	}
	return i, nil
}

// FilenameTitle is the display adapter for the shared path evidence reader.
// Identification callers should pass library-relative paths to naming.Read.
func FilenameTitle(path string) (string, int) {
	return naming.Read(path, "movie").PreferredTitle()
}

// commitMovieItem returns the movie a file backs in its library. A file that
// backs none yet is read by name and handed to the attach point, which makes
// it a version of the film it names (creating the film when it is new).
func commitMovieItem(ctx context.Context, tx *sql.Tx, library string, asset int64, path string) (int64, error) {
	handle, err := compactcatalog.LibraryTx(ctx, tx, library)
	if err != nil {
		return 0, err
	}
	var item int64
	err = tx.QueryRowContext(ctx, `SELECT l.entity_id FROM catalog_asset_links l INDEXED BY catalog_asset_links_asset JOIN catalog_entities i ON i.id=l.entity_id WHERE l.asset_id=? AND i.library_id=? LIMIT 1`, asset, handle).Scan(&item)
	if !errors.Is(err, sql.ErrNoRows) {
		return item, err
	}
	f, err := movieFileReadingTx(ctx, tx, library, asset, path)
	if err != nil {
		return 0, err
	}
	return AttachMovieTx(ctx, tx, f)
}

func projectArtwork(i *Item) {
	if i.PosterURL != "" {
		i.PosterURL = "/v1/items/" + i.ID + "/art/poster"
	}
	if i.BackdropURL != "" {
		i.BackdropURL = "/v1/items/" + i.ID + "/art/backdrop"
	}
}

func (s *Service) SetStorage(client *storage.Client) { s.storage = client }
