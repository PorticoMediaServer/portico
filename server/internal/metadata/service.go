package metadata

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/progressio"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/worker"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

const Attribution = "This product uses the TMDB API but is not endorsed or certified by TMDB."

type Movie struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path"`
	BackdropPath  string `json:"backdrop_path"`
}
type Service struct {
	EpisodeThumbnails      *EpisodeThumbnailRuntime
	ConfigurationCommitted func(context.Context) error

	Admission func(context.Context, *sql.Tx, string) (bool, error)
	// musicArtistCursor is where the artist enrichment walk resumes.
	musicArtistCursor atomic.Int64
	// mbReconcileAfter is the album the last reconciliation turn served.
	mbReconcileAfter atomic.Int64
	mbRetryDue       atomic.Int64     // one transient scheduling hint, never publication authority
	now              func() time.Time // private deterministic lease clock; production uses UTC wall time

	screenProviders map[string]ScreenProvider
	// tmdbPaced is the one door to TMDB: every request, from any worker, shares
	// its pacing clock, in-flight gate, Retry-After deferral and short cache.
	tmdbPaced *metadataprovider.TMDB
	// anilistPaced is the same one-door policy for AniList trend and anime
	// evidence; the screen matcher and the discovery worker share its transport.
	anilistPaced    *metadataprovider.AniList
	trendingSources map[string]trendingSource
	mb              MusicBrainzProvider
	tvdb            TVDBProvider
	// The artist chain's sources (Spec — Page Content §8): MusicBrainz artist
	// details plus Wikidata, Wikipedia and Commons. One transport each, so
	// each keeps the standard pacing. Tests substitute bounded fakes.
	wikidata          wikidataArtistSource
	wikipedia         wikipediaSource
	commons           commonsSource
	LocalArtworkUsage func(context.Context) (int64, error)
	// SubtitleCredentials receives the owner's saved OpenSubtitles key and
	// account whenever the settings change (empty: the server's own).
	SubtitleCredentials func(key, username, password string)
	// SubtitleStatus and SubtitleCheck report on OpenSubtitles for the owner:
	// the last exchange, and a fresh test of the credentials in force.
	SubtitleStatus       func() OpenSubtitlesStatus
	SubtitleCheck        func(context.Context) OpenSubtitlesStatus
	LocalArtwork         func(string) (*os.File, string, error)
	db                   *sql.DB
	token                string
	client               *http.Client
	base                 string
	cacheRoot            string
	settingsMu           sync.Mutex
	settingsRevision     int64
	artDirMu             sync.Mutex
	compactionMu         sync.Mutex
	artSweepMu           sync.Mutex
	artSourceCursor      artworkSweepCursor
	artBlobCursor        artworkSweepCursor
	compactionEdge       int
	compactionCursor     string
	compactionCursorEdge int
	compactionDone       bool
	missingMu            sync.Mutex
	missingArtwork       map[missingArtwork]struct{}
	missingWake          worker.Broadcast
	// acoustid is swapped when the owner saves or clears their own key, so it is
	// read through acoustidState under acoustidMu, never directly.
	acoustidMu     sync.RWMutex
	acoustid       AcousticProvider
	acoustidConfig string
	acoustidKey    string
	acoustidEnvKey string
	// The owner's Advanced metadata choices the workers consult on every pass.
	refreshPolicy atomic.Value // string
	castPhotos    atomic.Bool
	artClient     *http.Client
	artDirectory  *os.File
}

func New(db *sql.DB, token string) *Service {
	if token == "" {
		token = ProjectToken
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxConnsPerHost = 2
	transport.ResponseHeaderTimeout = 10 * time.Second
	s := &Service{db: db, token: token, client: &http.Client{Transport: progressio.Transport{Base: transport, Idle: 30 * time.Second, Overall: 15 * time.Minute}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: "https://api.themoviedb.org/3"}
	s.tvdb, _ = metadataprovider.NewTVDB(TVDBProjectKey)
	s.mb = metadataprovider.NewMusicBrainz()
	s.wikidata = metadataprovider.NewWikidata()
	s.wikipedia = metadataprovider.NewWikipedia()
	s.commons = metadataprovider.NewCommons()
	tmdb, _ := metadataprovider.NewTMDB(token)
	anilist := metadataprovider.NewAniList()
	s.tmdbPaced = tmdb
	s.anilistPaced = anilist
	s.screenProviders = map[string]ScreenProvider{"tmdb": tmdb, "anilist": anilist}
	// Trend feeds are keyed by the persisted discovery (provider, media_kind)
	// pair. Every entry goes through the same paced adapters as item matching;
	// tests substitute bounded fakes for a key, never a second client.
	s.trendingSources = map[string]trendingSource{
		"tmdb/movie":    tmdbTrendingSource{tmdb, "movie"},
		"tmdb/tv":       tmdbTrendingSource{tmdb, "tv"},
		"anilist/anime": anilist,
	}
	if tvdb, ok := s.tvdb.(ScreenProvider); ok {
		s.screenProviders["tvdb"] = tvdb
	}
	s.refreshPolicy.Store("regular")
	s.castPhotos.Store(true)
	s.artClient = newArtworkClient()
	return s
}
func imageURL(path, size string) string {
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.ContainsAny(path, "?#\\") {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + path
}
func (s *Service) Run(ctx context.Context) {
	ctx = dbwork.WithCatalogIngest(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
	_ = s.loadProviderSettings(ctx)
	s.logProviderState(ctx)
	// Woken by any committed write rather than asking once a second whether
	// anything changed. On a server with nothing queued this runs at the safety
	// tick and no more; on a busy one it hears about work as it is enqueued,
	// which is sooner than the timer it replaces.
	wake := worker.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "metadata_*", "screen_metadata_*", "mb_*", "tvdb_*", "catalog_entity_mutations", "audio_metadata_*", "audio_genre_*", "maintenance_*", "server_settings", "console_settings_revision", "admin_documents")
	defer unregister()
	ctx, progress := metadataProgress(ctx)
	worker.Run(ctx, "metadata.providers", wake, func(ctx context.Context) time.Duration {
		if err := dbwork.WaitCatalogIngest(ctx, s.db, dbwork.ClassBackgroundMedia); err != nil {
			return 0
		}
		// A provider setting cannot strand an accepted accounting fold or a
		// cancelled private owner. Progress still uses this worker's one wake.
		progress.Reset()
		_, accountingWorked, accountingErr := s.mbAccountingMaintenance(ctx)
		if accountingErr != nil && ctx.Err() == nil {
			log.Printf("warning: [metadata] accounting maintenance: %v", accountingErr)
		}
		if s.Admission != nil {
			allowed, e := s.Admission(ctx, nil, "metadata-refresh")
			if e != nil || !allowed {
				if accountingWorked {
					return 10 * time.Millisecond
				}
				return time.Minute
			}
		}
		// Provider refresh remains a bounded background batch under load.
		_ = s.loadProviderSettings(ctx)
		if !dbwork.Yield(ctx) {
			return 0
		}
		ctx = dbwork.WithBackgroundSpeed(ctx, dbwork.ReadBackgroundSpeed(ctx, s.db))
		for _, provider := range s.screenProviders {
			if paced, ok := provider.(interface{ SetBackgroundSpeed(string) }); ok {
				paced.SetBackgroundSpeed(dbwork.BackgroundSpeed(ctx))
			}
		}
		batchStarted := time.Now()
		if err := s.LookupFanoutStep(ctx); err != nil && ctx.Err() == nil {
			log.Printf("warning: [metadata] global lookup invalidation: %v", err)
		}
		if !dbwork.Yield(ctx) {
			return 0
		}
		_ = s.ScreenFanoutStep(ctx)
		var batch sync.WaitGroup
		for i := 0; i < dbwork.BackgroundConcurrency(ctx); i++ {
			batch.Add(1)
			supervise.Go("metadata.screen", func() { defer batch.Done(); _ = s.ScreenStep(ctx) })
		}
		batch.Wait()
		if !dbwork.PaceBackground(ctx, batchStarted) {
			return 0
		}
		if !dbwork.Yield(ctx) {
			return 0
		}
		_ = s.screenTVDBRun(ctx)
		if !dbwork.Yield(ctx) {
			return 0
		}
		_ = s.MusicBrainzStep(ctx)
		if !dbwork.Yield(ctx) {
			return 0
		}
		// Local audio genre catch-up: projects pre-upgrade catalogs and policy
		// revisions in durable batches through the scanner's shared projection.
		// Reads nothing and writes nothing when every audio library is current.
		_ = s.AudioGenreStep(ctx)
		if !dbwork.Yield(ctx) {
			return 0
		}
		// Provider Trending Now: one bounded feed pass per wake. The step itself
		// reads a few rows and returns without writing when nothing is due, so
		// an idle server keeps its wake floor and never loops on itself.
		_ = s.DiscoveryStep(ctx)
		return s.providerPassRetry(progress)
	})
}

// Separate bounded lanes keep artwork and local repair independent of metadata
// providers that are slow or offline. Shutdown cancels I/O and releases handles.
func (s *Service) RunArtwork(ctx context.Context) {
	ctx = dbwork.WithCatalogIngest(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
	var wg sync.WaitGroup
	for i, step := range []func(context.Context) error{s.RepairMissingArtworkStep, s.ArtworkDiscoveryStep, s.ArtworkStep, s.CompactArtworkStep, s.RepairCascadeStep, s.GlobalArtworkSizeStep, s.RefreshAllStep} {
		wg.Add(1)
		supervise.Go("metadata.artwork-lane", func() {
			run := step
			compaction := i == 3
			defer wg.Done()
			urgent, unsubscribe := s.missingWake.Subscribe()
			defer unsubscribe()
			wake := worker.NewSignal()
			unregister := dbwork.WakeOnTables(wake, "artwork_*", "metadata_runtime_settings", "metadata_artwork_size_*", "screen_metadata_consent", "metadata_owner_fields", "metadata_relationship_decisions", "screen_metadata_publications", "metadata_publication_heads", "maintenance_*", "server_settings", "console_settings_revision", "admin_documents")
			defer unregister()
			ctx, progress := metadataProgress(ctx)
			worker.RunWith(ctx, "metadata.artwork-lane", urgent, wake, func(ctx context.Context) time.Duration {
				if s.Admission != nil {
					allowed, e := s.Admission(ctx, nil, "metadata-refresh")
					if e != nil || !allowed {
						return time.Minute
					}
				}
				progress.Reset()
				ctx = dbwork.WithBackgroundSpeed(ctx, dbwork.ReadBackgroundSpeed(ctx, s.db))
				started := time.Now()
				if !dbwork.Yield(ctx) {
					return 0
				}
				_ = run(ctx)
				if !dbwork.PaceBackground(ctx, started) {
					return 0
				}
				if progress.Moved() || compaction && s.artworkCompactionHasBacklog() {
					return 10 * time.Millisecond
				}
				return 0
			})
		})
	}
	wg.Wait()
	s.artDirMu.Lock()
	defer s.artDirMu.Unlock()
	if s.artDirectory != nil {
		s.artDirectory.Close()
		s.artDirectory = nil
	}
}

// tmdbGet is how the catalogue paths reach TMDB. A provider refusal comes back
// as a *metadataprovider.Error, so a 429's Retry-After reaches the work row's
// backoff and the provider cooldown instead of being flattened into a string.
func (s *Service) tmdbGet(ctx context.Context, route string) ([]byte, error) {
	ctx = s.lookupContext(ctx)
	if s.tmdbPaced == nil {
		return nil, errors.New("metadata provider unavailable")
	}
	base := ""
	if s.base != "https://api.themoviedb.org/3" {
		base = s.base
	}
	return s.tmdbPaced.Raw(ctx, s.client, base, route, 1<<20)
}

// Each worker follows only its own committed progress. The shared table wake
// still provides the normal floored notification for work other lanes enqueue.
func metadataProgress(ctx context.Context) (context.Context, *worker.Progress) {
	ctx, count := dbwork.TrackCommitProgress(ctx)
	return ctx, worker.NewProgress(count)
}

// A bounded read-only MB window can have more raw keys even when this pass
// committed nothing. Consume that continuation once; other lanes and an idle
// or throttled next pass cannot make this provider loop spin.
func (s *Service) providerPassRetry(progress *worker.Progress) time.Duration {
	more := s.takeMBMutationPickContinuation()
	if progress.Moved() || more {
		return 10 * time.Millisecond
	}
	return s.mbAdoptionRetryDelay()
}
