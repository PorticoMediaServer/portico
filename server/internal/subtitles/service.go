package subtitles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type Authorize func(context.Context, *sql.Tx, identity.Principal, string) (identity.Principal, error)
type Service struct {
	life         context.Context
	cancel       context.CancelFunc
	cacheMu      sync.Mutex
	cacheFills   map[string]*cacheFill
	cacheWorkers chan struct{}
	cacheDone    sync.WaitGroup

	db             *sql.DB
	objects        *mediaartifact.Store
	storage        *storage.Client
	authorize      Authorize
	helper, ffmpeg string
	provider       Provider
	// Serializes object sealing/publication with garbage collection, never source
	// or provider I/O. DB authority remains the actual cross-process CAS fence.
	publication  sync.Mutex
	inventory    *mediaartifact.Inventory
	Extractor    SubtitleExtractor
	SourceInputs SourceInputs
	// Configured only by the actual playback composition. A private mapping
	// never falls through to the local/global disc resolver when this is absent.
	DiscPresentations DiscPresentationResolver

	// Shared playback owns the delivery plan and its producer. Selection and
	// delivery change commit together; process replacement runs after commit.
	// PrepareDelivery captures tool/configuration facts outside selection writers.
	PrepareDelivery  func(context.Context) (context.Context, error)
	ChangeDeliveryTx func(context.Context, *sql.Tx, identity.Principal, string, *Resource) error
	RestartDelivery  func(context.Context, string, float64)
}
type Options struct {
	DB                   *sql.DB
	Directory            string
	Storage              *storage.Client
	HelperBinary, FFmpeg string
	Authorize            Authorize
	Provider             Provider
}

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Storage == nil || o.Authorize == nil || o.HelperBinary == "" {
		return nil, ErrInput
	}
	objects, e := mediaartifact.New(o.Directory)
	if e != nil {
		return nil, e
	}
	ffmpeg, _ := exec.LookPath(o.FFmpeg)
	life, cancel := context.WithCancel(context.Background())
	return &Service{life: life, cancel: cancel, cacheFills: map[string]*cacheFill{}, cacheWorkers: make(chan struct{}, 2), db: o.DB, objects: objects, storage: o.Storage, authorize: o.Authorize, helper: o.HelperBinary, ffmpeg: ffmpeg, provider: o.Provider}, nil
}
func (s *Service) Close() error {
	if s.cancel != nil {
		s.cacheMu.Lock()
		s.cancel()
		s.cacheMu.Unlock()
		s.cacheDone.Wait()
	}
	s.publication.Lock()
	defer s.publication.Unlock()
	if s.inventory != nil {
		s.inventory.Close()
		s.inventory = nil
	}
	return s.objects.Close()
}
func actor(p identity.Principal) string {
	return ViewerKey(p.Authority, p.AccountID, p.ProfileID, p.ServerID)
}

func identityDigest(s string) string  { return identity.Digest(s) }
func owner(p identity.Principal) bool { return p.Authority == "local" && p.Role == "owner" }
func validID(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// tx waits for the write gate in the class of the work that called it: a
// viewer's request carries its route's class, so their own subtitle action is
// not queued behind a scan; the scan's import and cleanup name none and stay
// background.
func (s *Service) tx(ctx context.Context, p identity.Principal, item string) (*dbwork.Write, identity.Principal, error) {
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if e != nil {
		return nil, p, e
	}
	return s.authorized(ctx, gated, p, item)
}

// readTx is tx for a read: the same authorization, in a read snapshot that
// takes no write gate.
func (s *Service) readTx(ctx context.Context, p identity.Principal, item string) (*dbwork.Write, identity.Principal, error) {
	snapshot, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return nil, p, e
	}
	return s.authorized(ctx, snapshot, p, item)
}

func (s *Service) authorized(ctx context.Context, gated *dbwork.Write, p identity.Principal, item string) (*dbwork.Write, identity.Principal, error) {
	var e error
	tx := gated.Tx()
	p, e = s.authorize(ctx, tx, p, item)
	if e != nil {
		gated.Rollback()
		return nil, p, e
	}
	if invocation, ok := DiscExtractionInvocationFromContext(ctx); ok {
		if e = invocation.CheckTx(ctx, tx); e != nil {
			gated.Rollback()
			return nil, p, e
		}
	}
	if e = checkPlaybackSourcePublicationTx(ctx, tx); e != nil {
		gated.Rollback()
		return nil, p, e
	}
	return gated, p, nil
}

type query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Source struct {
	ID                              string `json:"id"`
	DurationUS                      string `json:"durationUs"`
	InventoryRevision               int64  `json:"inventoryRevision"`
	Available                       bool   `json:"available"`
	TimingKnown                     bool   `json:"timingKnown"`
	path, root, container           string
	size, modified, facts, originUS int64
	duration                        float64
}

func sourceQuery(ctx context.Context, q query, item, source string) (Source, error) {
	var v Source
	e := q.QueryRowContext(ctx, `SELECT a.token,a.path,COALESCE(physical.root,l.root),a.container,a.size,a.modified_ns,a.duration,a.available,COALESCE(f.revision,0),COALESCE(f.origin_us,0),COALESCE(f.timing_known,0),COALESCE(st.revision,0) FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id JOIN catalog_asset_links link ON link.entity_id=e.id JOIN catalog_assets a ON a.id=link.asset_id LEFT JOIN inventory_objects obj ON obj.asset_id=a.token AND obj.retired=0 AND obj.source_id IN(SELECT id FROM library_sources WHERE library_id=cl.library_id) LEFT JOIN library_sources physical ON physical.id=obj.source_id LEFT JOIN asset_subtitle_facts f ON f.asset_id=a.token AND f.size=a.size AND f.modified_ns=a.modified_ns LEFT JOIN asset_stream_facts st ON st.asset_id=a.token AND st.size=a.size AND st.modified_ns=a.modified_ns WHERE e.public_id=pid_blob(?) AND a.token=? AND (obj.id IS NULL OR physical.enabled=1 AND obj.root_incarnation=physical.incarnation AND obj.state='available') AND (a.video_codec!='' OR a.container='strm') ORDER BY physical.id LIMIT 1`, item, source).Scan(&v.ID, &v.path, &v.root, &v.container, &v.size, &v.modified, &v.duration, &v.Available, &v.InventoryRevision, &v.originUS, &v.TimingKnown, &v.facts)
	v.DurationUS = strconv.FormatInt(int64(v.duration*1e6), 10)
	return v, e
}

type Resource struct {
	Deliveries                                    []Delivery `json:"deliveries"`
	ManifestName                                  string     `json:"manifestName,omitempty"`
	ID                                            string     `json:"id"`
	SourceID                                      string     `json:"sourceId"`
	Revision                                      int64      `json:"revision"`
	Scope                                         string     `json:"scope"`
	Language                                      string     `json:"language"`
	Title                                         string     `json:"title"`
	Format                                        string     `json:"format"`
	Origin                                        string     `json:"origin"`
	Provider                                      string     `json:"provider,omitempty"`
	Attribution                                   string     `json:"attribution,omitempty"`
	Rights                                        string     `json:"rights"`
	OffsetUS                                      string     `json:"offsetUs"`
	CanManage                                     bool       `json:"canManage"`
	Pinned                                        bool       `json:"pinned,omitempty"`
	Retired                                       bool       `json:"retired,omitempty"`
	DiscoveryID                                   string     `json:"discoveryId,omitempty"`
	Enabled                                       bool       `json:"enabled"`
	Reason                                        string     `json:"reason,omitempty"`
	Renderer                                      string     `json:"renderer"`
	Default                                       bool       `json:"default"`
	HearingImpaired                               bool       `json:"hearingImpaired"`
	Forced                                        bool       `json:"forced"`
	sourceEvidence                                string
	digest                                        string
	size, sourceSize, sourceModified, sourceFacts int64
	resourceOwner                                 string
	discScoped                                    bool
	deleted                                       bool
}

const resourceColumns = `r.id,r.source_id,v.revision,r.scope,r.owner,r.deleted,r.discovery_id,v.language,v.title,v.format,v.origin,v.provider,v.attribution,v.rights,v.offset_us,v.digest,v.size,v.source_size,v.source_modified_ns,v.source_facts_revision,
 COALESCE((SELECT renderer FROM subtitle_render_revisions rr WHERE rr.resource_id=r.id AND rr.revision=v.revision),'external_text'),
 COALESCE((SELECT is_default FROM subtitle_render_revisions rr WHERE rr.resource_id=r.id AND rr.revision=v.revision),0),
 COALESCE((SELECT is_forced FROM subtitle_render_revisions rr WHERE rr.resource_id=r.id AND rr.revision=v.revision),0),
 COALESCE((SELECT source_evidence FROM subtitle_render_revisions rr WHERE rr.resource_id=r.id AND rr.revision=v.revision),''),
 COALESCE((SELECT hearing_impaired FROM subtitle_render_revisions rr WHERE rr.resource_id=r.id AND rr.revision=v.revision),0),
 (EXISTS(SELECT 1 FROM subtitle_disc_resources dr WHERE dr.resource_id=r.id) OR EXISTS(SELECT 1 FROM playback_disc_subtitle_resources private WHERE private.resource_id=r.id))`

type rowScanner interface{ Scan(...any) error }

func scanResource(row rowScanner) (Resource, error) {
	var r Resource
	var offset int64
	e := row.Scan(&r.ID, &r.SourceID, &r.Revision, &r.Scope, &r.resourceOwner, &r.deleted, &r.DiscoveryID, &r.Language, &r.Title, &r.Format, &r.Origin, &r.Provider, &r.Attribution, &r.Rights, &offset, &r.digest, &r.size, &r.sourceSize, &r.sourceModified, &r.sourceFacts, &r.Renderer, &r.Default, &r.Forced, &r.sourceEvidence, &r.HearingImpaired, &r.discScoped)
	r.OffsetUS = strconv.FormatInt(offset, 10)
	r.Deliveries = deliveryFormats(r.Format)
	r.Enabled = !r.deleted
	r.Retired = r.deleted
	return r, e
}
func visible(r Resource, p identity.Principal) bool {
	return r.Scope == "shared" || r.resourceOwner == actor(p)
}
func manageable(r Resource, p identity.Principal) bool {
	if r.discScoped {
		return false
	}
	return r.Scope == "shared" && owner(p) || r.Scope == "personal" && r.resourceOwner == actor(p)
}

type Discovery struct {
	ID              string `json:"id"`
	SourceID        string `json:"sourceId"`
	Revision        int64  `json:"revision"`
	Origin          string `json:"origin"`
	Format          string `json:"format"`
	Language        string `json:"language"`
	Title           string `json:"title"`
	Default         bool   `json:"default"`
	Forced          bool   `json:"forced"`
	HearingImpaired bool   `json:"hearingImpaired"`
	Enabled         bool   `json:"enabled"`
	Reason          string `json:"reason,omitempty"`
	locator, digest string
	index           int
	size, modified  int64
}
type Catalog struct {
	Version    int            `json:"version"`
	ItemID     string         `json:"itemId"`
	Revision   int64          `json:"revision"`
	CanShare   bool           `json:"canShare"`
	Sources    []Source       `json:"sources"`
	Resources  []Resource     `json:"resources"`
	Discovered []Discovery    `json:"discovered"`
	Provider   ProviderStatus `json:"provider"`
}

func (s *Service) List(ctx context.Context, p identity.Principal, item string) (Catalog, error) {
	gated, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return Catalog{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	out, e := s.ListTx(ctx, tx, p, item, "")
	if e != nil {
		return out, e
	}
	return out, gated.Commit()
}
func (s *Service) ListTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, filter string) (Catalog, error) {
	return s.listTx(ctx, tx, p, item, filter, nil)
}
func (s *Service) listTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, filter string, scope *DiscScope) (Catalog, error) {
	out := Catalog{Version: 1, ItemID: item, Revision: 1, CanShare: owner(p), Sources: []Source{}, Resources: []Resource{}, Discovered: []Discovery{}, Provider: s.ProviderStatus()}
	if e := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM subtitle_collections WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))),1)`, item).Scan(&out.Revision); e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT a.token FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND (a.video_codec!='' OR a.container='strm') AND (?='' OR a.token=?) ORDER BY link.part_index,a.token`, item, filter, filter)
	if e != nil {
		return out, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	byID := map[string]Source{}
	for _, id := range ids {
		v, e := sourceQuery(ctx, tx, item, id)
		if e != nil {
			return out, e
		}
		if scope != nil && id == scope.Source {
			var ms int64
			if e = tx.QueryRowContext(ctx, `SELECT duration_ms,origin_us,timing_known FROM inventory_disc_title_media WHERE object_id=? AND title_key=? AND revision=?`, scope.Object, scope.TitleKey, scope.FactsRevision).Scan(&ms, &v.originUS, &v.TimingKnown); e != nil {
				return out, e
			}
			v.duration = float64(ms) / 1000
			v.DurationUS = strconv.FormatInt(ms*1000, 10)
		}
		if s.storage.Guard != nil && s.storage.Guard(v.path) != nil {
			v.Available = false
		}
		v.path = ""
		v.root = ""
		out.Sources = append(out.Sources, v)
		byID[id] = v
	}
	object, key, facts := "", "", ""
	if scope != nil {
		object, key, facts = scope.Object, scope.TitleKey, scope.FactsRevision
	}
	rows, e = tx.QueryContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision WHERE r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND r.deleted=0 AND (r.scope='shared' OR r.owner=?) AND (?='' OR r.source_id=?) AND ((?='' AND `+discDefaultResource+`) OR (?<>'' AND `+discResourceCurrent+` AND EXISTS(SELECT 1 FROM subtitle_disc_resources dr WHERE dr.resource_id=r.id AND dr.object_id=? AND dr.title_key=? AND dr.title_facts_revision=?))) ORDER BY r.id`, item, actor(p), filter, filter, object, object, object, key, facts)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		r, e := scanResource(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		src, ok := byID[r.SourceID]
		r.CanManage = manageable(r, p)
		r.Enabled = ok && src.Available && src.size == r.sourceSize && src.modified == r.sourceModified
		if !r.Enabled {
			r.Reason = "source_changed"
		}
		if r.discScoped {
			extractor, ok := s.Extractor.(DiscSubtitleExtractor)
			if !ok || !extractor.DiscSubtitleAvailable() {
				r.Enabled = false
				r.Reason = "renderer_unavailable"
			}
		}
		out.Resources = append(out.Resources, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT t.id,t.asset_id,f.revision,t.origin,t.format,t.language,t.title,t.is_default,t.is_forced,t.reason,t.locator,t.stream_index,COALESCE(sc.size,0),COALESCE(sc.modified_ns,0),COALESCE(sc.digest,''),t.hearing_impaired FROM asset_subtitles t JOIN asset_subtitle_facts f ON f.asset_id=t.asset_id JOIN catalog_assets a ON a.token=t.asset_id JOIN catalog_asset_links link ON link.asset_id=a.id LEFT JOIN subtitle_sidecars sc ON sc.id=t.sidecar_id WHERE link.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND f.size=a.size AND f.modified_ns=a.modified_ns AND (?='' OR a.token=?) AND NOT EXISTS(SELECT 1 FROM inventory_disc_facts d WHERE d.asset_id=a.token) ORDER BY t.asset_id,t.id`, item, filter, filter)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var t Discovery
		if e = rows.Scan(&t.ID, &t.SourceID, &t.Revision, &t.Origin, &t.Format, &t.Language, &t.Title, &t.Default, &t.Forced, &t.Reason, &t.locator, &t.index, &t.size, &t.modified, &t.digest, &t.HearingImpaired); e != nil {
			rows.Close()
			return out, e
		}
		t.Title = safeTitle(t.Title)
		if lang, e := Language(t.Language); e == nil {
			t.Language = lang
		} else {
			t.Language = "und"
		}
		src, ok := byID[t.SourceID]
		if !ok || !src.Available {
			t.Reason = "source_unavailable"
		}
		if !validFormat(t.Format) {
			t.Reason = "unsupported_format"
		}
		if t.Origin == "embedded" && (s.ffmpeg == "" || !src.TimingKnown) {
			t.Reason = "extraction_unavailable"
		}
		t.Enabled = t.Reason == ""
		out.Discovered = append(out.Discovered, t)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, nil
}

// Mutation is a replacement of exactly one subtitle resource, not filesystem
// write authority and not a change to any source media timeline.
type Mutation struct {
	OperationID      string      `json:"operationId"`
	ResourceID       string      `json:"resourceId,omitempty"`
	SourceID         string      `json:"sourceId"`
	ExpectedRevision int64       `json:"expectedRevision"`
	Scope            string      `json:"scope"`
	Format           string      `json:"format"`
	Language         string      `json:"language"`
	Title            string      `json:"title"`
	Rights           string      `json:"rights"`
	OffsetUS         string      `json:"offsetUs"`
	Content          *string     `json:"content,omitempty"`
	Data             Base64Bytes `json:"data,omitempty"`
	Companion        Base64Bytes `json:"companion,omitempty"`
	Default          *bool       `json:"default,omitempty"`
	Forced           *bool       `json:"forced,omitempty"`
	// Render is "text" (default) or "styled"; see ImportRequest.Render.
	Render string `json:"render,omitempty"`
}
type Receipt struct {
	OperationID     string `json:"operationId"`
	ResourceID      string `json:"resourceId"`
	Revision        int64  `json:"revision"`
	CatalogRevision int64  `json:"catalogRevision"`
	Deleted         bool   `json:"deleted"`
}
type publication struct {
	lookupEpoch                                              int64
	object                                                   mediaartifact.Object
	origin, provider, attribution, originalDigest, discovery string
	discoveryRevision                                        int64
	cacheResource                                            string
	cacheRevision                                            int64
	source                                                   Source
	renderer, evidence                                       string
	defaultTrack, forcedTrack                                bool
	originalAsset                                            []byte
	hearingImpaired                                          bool
	copyOriginalRevision                                     int64
}

func requestDigest(v any) string { b, _ := json.Marshal(v); return digestBytes(b) }
func receiptTx(ctx context.Context, tx *sql.Tx, p identity.Principal, op, digest, item string) (*Receipt, error) {
	var old, oldItem, raw string
	e := tx.QueryRowContext(ctx, `SELECT o.request_digest,COALESCE(pid(e.public_id),''),o.result FROM subtitle_operations o LEFT JOIN catalog_entities e ON e.id=o.item_id WHERE o.actor=? AND o.operation_id=?`, actor(p), op).Scan(&old, &oldItem, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if old != digest || oldItem != item {
		return nil, ErrOperation
	}
	var out Receipt
	if e = json.Unmarshal([]byte(raw), &out); e != nil {
		return nil, e
	}
	return &out, nil
}
func saveReceipt(ctx context.Context, tx *sql.Tx, p identity.Principal, item, digest string, out Receipt) error {
	raw, e := json.Marshal(out)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO subtitle_operations(actor,operation_id,request_digest,item_id,result,created_at) SELECT ?,?,?,id,?,? FROM catalog_entities WHERE public_id=pid_blob(?)`, actor(p), out.OperationID, digest, string(raw), time.Now().UTC().Format(time.RFC3339), item)
	return e
}
func validateMutation(m Mutation) error {
	if len(m.Companion) > 0 && m.Content == nil && m.Data == nil || m.Content != nil && m.Data != nil || len(m.Data)+len(m.Companion) > BinaryLimitBytes() {
		return ErrInput
	}
	if !validID(m.OperationID) || !validID(m.SourceID) || m.ResourceID != "" && !validID(m.ResourceID) || m.ExpectedRevision < 0 || m.ResourceID == "" && m.ExpectedRevision != 0 || m.ResourceID != "" && m.ExpectedRevision < 1 || !textField(m.Title, 160) || !textField(m.Rights, 500) || m.Scope != "shared" && m.Scope != "personal" {
		return ErrInput
	}
	if _, e := Language(m.Language); e != nil {
		return e
	}
	if _, e := Offset(m.OffsetUS); e != nil {
		return e
	}
	if (m.Content != nil || m.Data != nil) && !validFormat(m.Format) {
		return ErrUnsupported
	}
	if !validRender(m.Render) {
		return ErrInput
	}
	return nil
}
func (s *Service) Mutate(ctx context.Context, p identity.Principal, item string, m Mutation) (Receipt, error) {
	if e := validateMutation(m); e != nil {
		return Receipt{}, e
	}
	digest := requestDigest(m)
	gated2, p, e := s.tx(ctx, p, item)
	if e != nil {
		return Receipt{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	previous, e := receiptTx(ctx, tx, p, m.OperationID, digest, item)
	if e != nil {
		return Receipt{}, e
	}
	if previous != nil {
		return *previous, nil
	}
	src, e := s.sourceQuery(ctx, tx, item, m.SourceID)
	if e != nil {
		return Receipt{}, e
	}
	if !src.Available {
		return Receipt{}, ErrUnavailable
	}
	pub := publication{source: src, origin: "upload"}
	if m.Scope == "shared" && !owner(p) {
		return Receipt{}, identity.ErrUnauthorized
	}
	if m.ResourceID != "" {
		r, e := scanResource(tx.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision WHERE r.id=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, m.ResourceID, item))
		if e != nil {
			return Receipt{}, e
		}
		if !manageable(r, p) || r.Scope != m.Scope || r.SourceID != m.SourceID {
			return Receipt{}, identity.ErrUnauthorized
		}
		if r.deleted || r.Revision != m.ExpectedRevision {
			return Receipt{}, ErrConflict
		}
		if m.Content == nil && m.Data == nil {
			if r.sourceSize != src.size || r.sourceModified != src.modified {
				return Receipt{}, ErrConflict
			}
			pub.object = mediaartifact.Object{Digest: r.digest, Size: r.size}
			pub.origin = r.Origin
			pub.renderer = r.Renderer
			pub.evidence = r.sourceEvidence
			pub.defaultTrack = r.Default
			pub.forcedTrack = r.Forced
			pub.hearingImpaired = r.HearingImpaired
			pub.provider = r.Provider
			pub.attribution = r.Attribution
			m.Format = r.Format
			pub.copyOriginalRevision = r.Revision
			pub.originalDigest = ""
			if e = tx.QueryRowContext(ctx, `SELECT original_digest FROM subtitle_revisions WHERE resource_id=? AND revision=?`, r.ID, r.Revision).Scan(&pub.originalDigest); e != nil {
				return Receipt{}, e
			}
		}
	} else if m.Content == nil && m.Data == nil {
		return Receipt{}, ErrInput
	}
	if e = gated2.Commit(); e != nil {
		return Receipt{}, e
	}
	var raw []byte
	if m.Content != nil || m.Data != nil {
		input := m.Data
		if m.Content != nil {
			input = []byte(*m.Content)
		}
		raw, e = CanonicalAssetWithLanguage(input, m.Companion, m.Format, int64(src.duration*1e6), m.Language)
		if e != nil {
			return Receipt{}, e
		}
		pub.originalAsset = raw
		raw = renderChoice(raw, m.Render, int64(src.duration*1e6))
		pub.originalDigest = requestDigest([]any{input, m.Companion})
	}
	return s.publish(ctx, p, item, m, digest, pub, raw)
}
func (s *Service) publish(ctx context.Context, p identity.Principal, item string, m Mutation, digest string, pub publication, raw []byte) (Receipt, error) {
	if pub.source.container == "strm" {
		if s.SourceInputs == nil {
			return Receipt{}, ErrUnavailable
		}
		input, e := s.SourceInputs.OpenSubtitleInput(ctx, item, m.SourceID, "")
		if e != nil {
			return Receipt{}, e
		}
		evidence := input.Evidence()
		valid := input.Validate(ctx)
		input.Close()
		if valid != nil {
			return Receipt{}, valid
		}
		if pub.evidence != "" && pub.evidence != evidence {
			return Receipt{}, ErrConflict
		}
		pub.evidence = evidence
	}
	s.publication.Lock()
	defer s.publication.Unlock()
	if raw != nil {
		pub.renderer = assetRenderer(raw)
		w, e := s.objects.Begin(int64(AssetLimitBytes()))
		if e != nil {
			return Receipt{}, e
		}
		defer w.Abort()
		if _, e = w.Write(raw); e != nil {
			return Receipt{}, e
		}
		pub.object, e = w.Seal(ctx)
		if e != nil {
			return Receipt{}, e
		}
	}
	var originalObject mediaartifact.Object
	var originalFiles []originalFile
	var originalLines Lines
	var originalHeader struct {
		SkippedFonts int `json:"skippedFonts"`
	}
	if len(pub.originalAsset) > 0 {
		_ = json.Unmarshal(pub.originalAsset, &originalHeader)
	}
	if len(pub.originalAsset) > 0 && originalArchive(pub.originalAsset) {
		w, e := s.objects.Begin(int64(AssetLimitBytes()))
		if e != nil {
			return Receipt{}, e
		}
		defer w.Abort()
		if _, e = w.Write(pub.originalAsset); e != nil {
			return Receipt{}, e
		}
		originalObject, e = w.Seal(ctx)
		if e != nil {
			return Receipt{}, e
		}
	}

	if originalObject.Size > 0 {
		var e error
		originalFiles, originalLines, e = s.sealOriginalFiles(ctx, pub.originalAsset, int64(pub.source.duration*1e6))
		if e != nil {
			return Receipt{}, e
		}
	}

	if invocation, ok := DiscExtractionInvocationFromContext(ctx); ok {
		if err := invocation.validateInput(ctx); err != nil {
			return Receipt{}, err
		}
	}
	gated3, p, e := s.tx(ctx, p, item)
	if e != nil {
		return Receipt{}, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	previous, e := receiptTx(ctx, tx, p, m.OperationID, digest, item)
	if e != nil {
		return Receipt{}, e
	}
	if previous != nil {
		return *previous, nil
	}
	src, e := s.sourceQuery(ctx, tx, item, m.SourceID)
	if e != nil {
		return Receipt{}, e
	}
	if !src.Available || src.size != pub.source.size || src.modified != pub.source.modified || src.facts != pub.source.facts {
		return Receipt{}, ErrConflict
	}
	if pub.origin == "provider" {
		if err := localonly.CheckOnline(ctx, tx, pub.lookupEpoch); err != nil {
			return Receipt{}, err
		}
		if local, err := localonly.Item(ctx, tx, item); err != nil || local {
			if err != nil {
				return Receipt{}, err
			}
			return Receipt{}, localonly.Err
		}
	}
	if pub.discovery != "" && src.InventoryRevision != pub.discoveryRevision {
		return Receipt{}, ErrConflict
	}
	if pub.cacheResource != "" {
		result, err := tx.ExecContext(ctx, `UPDATE subtitle_revisions SET digest=?,size=?,original_digest=? WHERE resource_id=? AND revision=? AND digest='' AND size=0 AND source_size=? AND source_modified_ns=?`, pub.object.Digest, pub.object.Size, pub.originalDigest, pub.cacheResource, pub.cacheRevision, src.size, src.modified)
		if err != nil {
			return Receipt{}, err
		}
		n, _ := result.RowsAffected()
		if n != 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE subtitle_render_revisions SET renderer=?,source_evidence=? WHERE resource_id=? AND revision=?`, pub.renderer, pub.evidence, pub.cacheResource, pub.cacheRevision); err != nil {
				return Receipt{}, err
			}
		}
		if originalObject.Size > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_original_revisions(resource_id,revision,digest,size,skipped_fonts,forced_lines,total_lines) VALUES(?,?,?,?,?,?,?)`, pub.cacheResource, pub.cacheRevision, originalObject.Digest, originalObject.Size, originalHeader.SkippedFonts, originalLines.Forced, originalLines.Total); err != nil {
				return Receipt{}, err
			}
		}
		if e := publishOriginalFiles(ctx, tx, pub.cacheResource, pub.cacheRevision, originalFiles); e != nil {
			return Receipt{}, e
		}
		out := Receipt{OperationID: m.OperationID, ResourceID: pub.cacheResource, Revision: pub.cacheRevision}
		return out, gated3.Commit()
	}

	if m.Scope == "shared" && !owner(p) {
		return Receipt{}, identity.ErrUnauthorized
	}
	rev := int64(1)
	id := m.ResourceID
	if id == "" {
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM subtitle_resources WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND deleted=0 AND (scope='shared' OR owner=?)`, item, actor(p)).Scan(&count); e != nil {
			return Receipt{}, e
		}
		if count >= 128 {
			return Receipt{}, ErrCapacity
		}
		id = identity.Token()
		who := ""
		if m.Scope == "personal" {
			who = actor(p)
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,discovery_id,discovery_revision) SELECT ?,id,?,?,?,1,?,? FROM catalog_entities WHERE public_id=pid_blob(?)`, id, m.SourceID, m.Scope, who, pub.discovery, pub.discoveryRevision, item); e != nil {
			return Receipt{}, e
		}
	} else {
		r, e := scanResource(tx.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision WHERE r.id=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, id, item))
		if e != nil {
			return Receipt{}, e
		}
		if !manageable(r, p) || r.Scope != m.Scope || r.SourceID != m.SourceID {
			return Receipt{}, identity.ErrUnauthorized
		}
		if r.deleted || r.Revision != m.ExpectedRevision {
			return Receipt{}, ErrConflict
		}
		rev = r.Revision + 1
		result, e := tx.ExecContext(ctx, `UPDATE subtitle_resources SET current_revision=? WHERE id=? AND current_revision=? AND deleted=0`, rev, id, m.ExpectedRevision)
		if e != nil {
			return Receipt{}, e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return Receipt{}, ErrConflict
		}
	}
	lang, e := Language(m.Language)
	if e != nil {
		return Receipt{}, e
	}
	offset, e := Offset(m.OffsetUS)
	if e != nil {
		return Receipt{}, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO subtitle_revisions VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, rev, pub.object.Digest, pub.object.Size, m.Format, lang, safeTitle(m.Title), pub.origin, pub.provider, pub.attribution, m.Rights, pub.originalDigest, offset, src.size, src.modified, src.facts, time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		return Receipt{}, e
	}
	if pub.renderer == "" {
		pub.renderer = "external_text"
	}
	if m.Default != nil {
		pub.defaultTrack = *m.Default
	}
	if m.Forced != nil {
		pub.forcedTrack = *m.Forced
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_render_revisions VALUES(?,?,?,?,?,?,?)`, id, rev, pub.renderer, pub.defaultTrack, pub.forcedTrack, pub.evidence, pub.hearingImpaired); e != nil {
		return Receipt{}, e
	}
	if originalObject.Size > 0 {
		if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_original_revisions(resource_id,revision,digest,size,skipped_fonts,forced_lines,total_lines) VALUES(?,?,?,?,?,?,?)`, id, rev, originalObject.Digest, originalObject.Size, originalHeader.SkippedFonts, originalLines.Forced, originalLines.Total); e != nil {
			return Receipt{}, e
		}
	}

	if e := publishOriginalFiles(ctx, tx, id, rev, originalFiles); e != nil {
		return Receipt{}, e
	}
	if pub.copyOriginalRevision > 0 {
		if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_original_revisions(resource_id,revision,digest,size,skipped_fonts,forced_lines,total_lines) SELECT resource_id,?,digest,size,skipped_fonts,forced_lines,total_lines FROM subtitle_original_revisions WHERE resource_id=? AND revision=?`, rev, id, pub.copyOriginalRevision); e != nil {
			return Receipt{}, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_original_files SELECT resource_id,?,part,name,format,encoding,digest,size FROM subtitle_original_files WHERE resource_id=? AND revision=?`, rev, id, pub.copyOriginalRevision); e != nil {
			return Receipt{}, e
		}
	}

	out := Receipt{OperationID: m.OperationID, ResourceID: id, Revision: rev}
	if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_collections(item_id,revision) SELECT id,2 FROM catalog_entities WHERE public_id=pid_blob(?) ON CONFLICT(item_id) DO UPDATE SET revision=revision+1`, item); e != nil {
		return out, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM subtitle_collections WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, item).Scan(&out.CatalogRevision); e != nil {
		return out, e
	}
	if e = saveReceipt(ctx, tx, p, item, digest, out); e != nil {
		return out, e
	}
	return out, gated3.Commit()
}

type DeleteMutation struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

func (s *Service) Delete(ctx context.Context, p identity.Principal, item, id string, m DeleteMutation) (Receipt, error) {
	if !validID(m.OperationID) || !validID(id) || m.ExpectedRevision < 1 {
		return Receipt{}, ErrInput
	}
	digest := requestDigest([]any{"delete", item, id, m})
	gated4, p, e := s.tx(ctx, p, item)
	if e != nil {
		return Receipt{}, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	old, e := receiptTx(ctx, tx, p, m.OperationID, digest, item)
	if e != nil {
		return Receipt{}, e
	}
	if old != nil {
		return *old, nil
	}
	r, e := scanResource(tx.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision WHERE r.id=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, id, item))
	if e != nil {
		return Receipt{}, e
	}
	if !manageable(r, p) {
		return Receipt{}, identity.ErrUnauthorized
	}
	if r.deleted || r.Revision != m.ExpectedRevision {
		return Receipt{}, ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `UPDATE subtitle_resources SET deleted=1 WHERE id=? AND current_revision=?`, id, m.ExpectedRevision); e != nil {
		return Receipt{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_collections(item_id,revision) SELECT id,2 FROM catalog_entities WHERE public_id=pid_blob(?) ON CONFLICT(item_id) DO UPDATE SET revision=revision+1`, item); e != nil {
		return Receipt{}, e
	}
	out := Receipt{OperationID: m.OperationID, ResourceID: id, Revision: r.Revision, Deleted: true}
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM subtitle_collections WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, item).Scan(&out.CatalogRevision); e != nil {
		return out, e
	}
	if e = saveReceipt(ctx, tx, p, item, digest, out); e != nil {
		return out, e
	}
	return out, gated4.Commit()
}

// Bounded cleanup never removes a selected revision or one pinned by an active
// session. The object store separately rejects deletion with physical readers.
func (s *Service) Run(ctx context.Context) {
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	// Resource/pin changes wake collection promptly, and the next queued
	// revision's due time wakes it when nothing else does.
	wake := worker.NewSignal()
	unregister := watchSubtitleCollection(wake)
	defer unregister()
	worker.Run(ctx, "subtitles.cleanup", wake, func(ctx context.Context) time.Duration {
		next, _ := s.collect(ctx)
		return next
	})
}

// collectionPage bounds the queue entries one pass settles in its transaction.
const collectionPage = 64

// heldRetry is how long an entry still held by playback waits to be rechecked.
const heldRetry = time.Hour

func (s *Service) Cleanup(ctx context.Context) error {
	_, e := s.collect(ctx)
	return e
}

// collect settles the due part of subtitle_obsolete_revisions and returns how
// long until the next entry is due (zero when nothing is queued). Every entry is
// rechecked: a revision that is current again is forgotten, one still pinned by
// playback or a private disc waits, and only the rest is deleted.
func (s *Service) collect(ctx context.Context) (time.Duration, error) {
	s.publication.Lock()
	defer s.publication.Unlock()
	next, removed, e := s.settleDue(ctx)
	if e != nil {
		return 0, e
	}
	for _, object := range removed {
		if e = s.removeUnreferenced(ctx, object); e != nil {
			return 0, e
		}
	}
	if s.inventory == nil {
		s.inventory, e = s.objects.Inventory()
		if e != nil {
			return 0, e
		}
	}
	candidates, scanErr := s.inventory.Next(ctx, 64)
	if scanErr == io.EOF {
		s.inventory.Close()
		s.inventory = nil
	} else if scanErr != nil {
		return 0, scanErr
	}
	for _, candidate := range candidates {
		if candidate.Modified.After(time.Now().Add(-24 * time.Hour)) {
			continue
		}
		if e = s.removeUnreferenced(ctx, candidate.Object); e != nil {
			return 0, e
		}
	}
	return next, nil
}

// settleDue runs the database half of collection. Catalogue and session writes
// still reach this loop through their cascades into the subtitle tables, so a
// wake first asks, on a read connection, whether anything is due at all; only
// then does it take the writer.
func (s *Service) settleDue(ctx context.Context) (time.Duration, []mediaartifact.Object, error) {
	now := time.Now()
	stamp := now.UTC().Format(time.RFC3339)
	var work bool
	var nextDue sql.NullInt64
	if e := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subtitle_provider_candidates WHERE expires_at<?1)
 OR EXISTS(SELECT 1 FROM playback_subtitle_pins WHERE session_id IN(SELECT id FROM playback_sessions WHERE state IN ('stopped','ended','failed') OR expires_at<?1)),
 (SELECT min(due_ms) FROM subtitle_obsolete_revisions)`, stamp).Scan(&work, &nextDue); e != nil {
		return 0, nil, e
	}
	if !work && (!nextDue.Valid || nextDue.Int64 > now.UnixMilli()) {
		if !nextDue.Valid {
			return 0, nil, nil
		}
		return max(time.Until(time.UnixMilli(nextDue.Int64)), time.Millisecond), nil, nil
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return 0, nil, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if _, e = tx.ExecContext(ctx, `DELETE FROM subtitle_provider_candidates WHERE expires_at<?`, stamp); e != nil {
		return 0, nil, e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM playback_subtitle_pins WHERE session_id IN(SELECT id FROM playback_sessions WHERE state IN ('stopped','ended','failed') OR expires_at<?)`, stamp); e != nil {
		return 0, nil, e
	}
	type entry struct {
		id       string
		revision int64
	}
	rows, e := tx.QueryContext(ctx, `SELECT resource_id,revision FROM subtitle_obsolete_revisions INDEXED BY subtitle_obsolete_revisions_due WHERE due_ms<=? ORDER BY due_ms,revision DESC LIMIT ?`, now.UnixMilli(), collectionPage)
	if e != nil {
		return 0, nil, e
	}
	var due []entry
	for rows.Next() {
		var q entry
		if e = rows.Scan(&q.id, &q.revision); e != nil {
			rows.Close()
			return 0, nil, e
		}
		due = append(due, q)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, nil, e
	}
	var removed []mediaartifact.Object
	for _, q := range due {
		var obsolete, held bool
		var object mediaartifact.Object
		if q.revision > 0 {
			e = tx.QueryRowContext(ctx, `SELECT v.digest,v.size,r.deleted=1 OR v.revision<>r.current_revision,
 EXISTS(SELECT 1 FROM playback_subtitle_pins p WHERE p.resource_id=v.resource_id AND p.resource_revision=v.revision)
 OR EXISTS(SELECT 1 FROM playback_manifest_subtitles m WHERE m.resource_id=v.resource_id AND m.resource_revision=v.revision)
 OR EXISTS(SELECT 1 FROM playback_disc_subtitle_resources d WHERE d.resource_id=v.resource_id)
 FROM subtitle_revisions v JOIN subtitle_resources r ON r.id=v.resource_id WHERE v.resource_id=? AND v.revision=?`, q.id, q.revision).Scan(&object.Digest, &object.Size, &obsolete, &held)
		} else {
			e = tx.QueryRowContext(ctx, `SELECT r.deleted=1,
 EXISTS(SELECT 1 FROM subtitle_revisions v WHERE v.resource_id=r.id) OR EXISTS(SELECT 1 FROM playback_disc_subtitle_resources d WHERE d.resource_id=r.id)
 FROM subtitle_resources r WHERE r.id=?`, q.id).Scan(&obsolete, &held)
		}
		if errors.Is(e, sql.ErrNoRows) {
			obsolete, e = false, nil
		}
		if e != nil {
			return 0, nil, e
		}
		switch {
		case !obsolete:
			_, e = tx.ExecContext(ctx, `DELETE FROM subtitle_obsolete_revisions WHERE resource_id=? AND revision=?`, q.id, q.revision)
		case held:
			_, e = tx.ExecContext(ctx, `UPDATE subtitle_obsolete_revisions SET due_ms=? WHERE resource_id=? AND revision=?`, now.Add(heldRetry).UnixMilli(), q.id, q.revision)
		case q.revision > 0:
			if _, e = tx.ExecContext(ctx, `DELETE FROM subtitle_revisions WHERE resource_id=? AND revision=?`, q.id, q.revision); e == nil {
				_, e = tx.ExecContext(ctx, `DELETE FROM subtitle_obsolete_revisions WHERE resource_id=? AND revision=?`, q.id, q.revision)
				removed = append(removed, object)
			}
		default:
			if _, e = tx.ExecContext(ctx, `DELETE FROM subtitle_resources WHERE id=?`, q.id); e == nil {
				_, e = tx.ExecContext(ctx, `DELETE FROM subtitle_obsolete_revisions WHERE resource_id=? AND revision=0`, q.id)
			}
		}
		if e != nil {
			return 0, nil, e
		}
	}
	next := time.Duration(0)
	if len(due) == collectionPage {
		next = time.Millisecond
	} else {
		if e = tx.QueryRowContext(ctx, `SELECT min(due_ms) FROM subtitle_obsolete_revisions`).Scan(&nextDue); e != nil {
			return 0, nil, e
		}
		if nextDue.Valid {
			next = max(time.Until(time.UnixMilli(nextDue.Int64)), time.Millisecond)
		}
	}
	if e = gated2.Commit(); e != nil {
		return 0, nil, e
	}
	return next, removed, nil
}

// removeUnreferenced deletes a stored object no revision or original names.
// Each count is one seek on its table's digest index.
func (s *Service) removeUnreferenced(ctx context.Context, object mediaartifact.Object) error {
	if object.Size <= 0 {
		return nil
	}
	var references int
	if e := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM subtitle_revisions WHERE digest=?1)+(SELECT count(*) FROM subtitle_original_revisions WHERE digest=?1)+(SELECT count(*) FROM subtitle_original_files WHERE digest=?1)`, object.Digest).Scan(&references); e != nil {
		return e
	}
	if references != 0 {
		return nil
	}
	if e := s.objects.Remove(object); e != nil && !errors.Is(e, mediaartifact.ErrLeased) {
		return e
	}
	return nil
}

// Source paths are private and used only after an authorized catalog lookup.
func relative(root, path string) (string, error) {
	r, e := filepath.Rel(root, path)
	if e != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", ErrInput
	}
	return r, nil
}

// watchSubtitleCollection follows this runtime's durable inputs; clock-only expiry
// still runs through worker.SafetyTick. Unrelated telemetry is not work.
func watchSubtitleCollection(signal *worker.Signal) func() {
	return dbwork.WakeOnTables(signal,
		"subtitle_*", "playback_subtitle_pins", "playback_manifest_subtitles", "playback_disc_subtitle_resources",
	)
}
