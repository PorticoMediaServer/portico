package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

const screenAlgorithm = "screen-evidence-confidence-v2"

var ErrScreenConflict = errors.New("screen metadata state changed; refresh before selecting")
var errScreenStale = errors.New("screen publication authority changed")

type ScreenProvider interface {
	SearchScreen(context.Context, string, string, int, string, string) ([]metadataprovider.ScreenRecord, error)
	ScreenDetails(context.Context, string, string, string, string) (metadataprovider.ScreenRecord, error)
}
type screenEpisodeProvider interface {
	ScreenEpisode(context.Context, string, int, int, string, string) (metadataprovider.ScreenRecord, error)
}
type screenEdition struct {
	AssetID string `json:"assetId"`
	Label   string `json:"label"`
	Source  string `json:"source"`
}
type screenBase struct {
	CreditRecordReference                                                      string
	LookupEpoch                                                                int64
	ForceRefresh                                                               bool
	IdentityLocked                                                             bool
	OwnerDigest                                                                string
	TargetKind, TargetID, Library, LibraryKind, ItemKind, Title, Query, Status string
	// Entity is the integer catalog_entities row TargetID (the public id)
	// names. Every INTEGER catalogue reference in these queries uses it.
	Entity int64
	// ParentEntity is the integer row of an episode's show.
	ParentEntity                                                                                    int64
	RuntimeMinutes                                                                                  float64
	Evidence                                                                                        []MatchEvidence
	Year                                                                                            int
	Revision, Selection, Generation                                                                 int64
	Provider, ProviderType, ProviderID, Mode, Order, Requested, Accepted, Parent, ParentPublication string
	ParentProvider, ParentProviderType, ParentProviderID, ParentOrder                               string
	ParentSelection                                                                                 int64
	Season, Number                                                                                  int
	Numbering, LocalIdentity                                                                        string
	Incarnation                                                                                     string
	SourceRevision, IdentityRevision, DescriptionRevision, HierarchyRevision                        int64
	Configuration, ScanRevision, PolicyRevision, ConsentRevision                                    int64
	Confirmed, Enabled, Available, LocalAllowed                                                     bool
	Language, Region, RefreshMode                                                                   string
	Providers                                                                                       []string
	SourceDigest                                                                                    string
	Names                                                                                           []string
	NFO                                                                                             []assets.VideoNFO
	Existing                                                                                        []metadataprovider.ScreenID
	Editions                                                                                        []screenEdition
	AnimeSeasonID                                                                                   string
	AnimeSeasonRevision                                                                             int64
}
type screenClaim struct {
	Base                 screenBase
	Token, Digest, Until string
	Attempts             int
	// Fence is what screenCurrent compares to decide whether the result of
	// this claim may still be published. For a film or an episode it is the
	// input digest. For a show it covers only the files the show had when it
	// was claimed (Bound is the highest file id then): a scan adding the
	// show's episodes while its match is in flight must not throw that match
	// away. The new episodes are picked up when the match completes (the
	// show's dispatch walks every episode) or on their own work rows.
	Fence string
	Bound int64
}

// screenUnbounded reads every file of a target.
const screenUnbounded = int64(math.MaxInt64)

// screenFence is a claim's publication fence (see screenClaim.Fence). A show's
// source and hierarchy revisions move whenever an episode is added, so they
// are left out; what they stand for among the files the show was claimed
// with is in the source digest, and removing or changing any of those files
// moves the show's generation or its digest.
func screenFence(b screenBase) string {
	a := screenBaseAuthority(b)
	if a.TargetKind == "show" {
		a.SourceRevision, a.HierarchyRevision = 0, 0
	}
	return screenDigest(a)
}

// screenSourceHealthClass is what a source's health means to the matcher:
// whether its files can be read. Other health changes (a scan starting or
// finishing) are not a change of the title's inputs.
func screenSourceHealthClass(health string) string {
	switch health {
	case "offline", "removing", "root_changed":
		return health
	}
	return "readable"
}

// resolveEntity maps a public catalogue id to its integer entity row. An
// unknown or malformed id reads as not found, preserving the old
// sql.ErrNoRows paths. Every function in this lane resolves once and passes
// the int64 to its statements (Rewrite Map §1).
func resolveEntity(ctx context.Context, tx *sql.Tx, public string) (int64, error) {
	id, err := entityid.Resolve(ctx, tx, public)
	if errors.Is(err, entityid.ErrNotFound) {
		return 0, sql.ErrNoRows
	}
	return id, err
}

// entityPublic maps an integer entity row back to its public id. A row that
// lost its entity reads as empty, never as an error.
func entityPublic(ctx context.Context, tx *sql.Tx, id int64) string {
	public, err := entityid.Public(ctx, tx, id)
	if err != nil {
		return ""
	}
	return public
}

// scanParentID reads screen_metadata_work.parent_id, which the baseline
// types INTEGER but defaults to ”.
func scanParentID(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case []byte:
		parsed, _ := strconv.ParseInt(string(n), 10, 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(n, 10, 64)
		return parsed
	}
	return 0
}
func screenDigest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func screenTargetAssets(kind string) string {
	if kind == "show" {
		return `SELECT DISTINCT a.token FROM catalog_asset_links al JOIN catalog_assets a ON a.id=al.asset_id JOIN catalog_episodes e ON e.entity_id=al.entity_id WHERE e.show_id=?`
	}
	return `SELECT a.token FROM catalog_asset_links al JOIN catalog_assets a ON a.id=al.asset_id WHERE al.entity_id=?`
}

// screenTargetAssetsWithin is screenTargetAssets limited to files whose id is
// at most a bound (the second parameter).
func screenTargetAssetsWithin(kind string) string {
	return screenTargetAssets(kind) + ` AND al.asset_id<=?`
}

// Reads only durable observations. The matcher never opens a source, sidecar or
// media file. Unbounded show inventories are streamed into a bounded digest.
func readScreenBase(ctx context.Context, tx *sql.Tx, kind, id string) (screenBase, error) {
	return readScreenBaseWithin(ctx, tx, kind, id, screenUnbounded)
}

// readScreenBaseWithin reads a target's inputs as if its files with an id
// above bound did not exist yet (screenClaim.Bound).
func readScreenBaseWithin(ctx context.Context, tx *sql.Tx, kind, id string, bound int64) (screenBase, error) {
	var b screenBase
	b.TargetKind, b.TargetID = kind, id
	entity, err := resolveEntity(ctx, tx, id)
	if err != nil {
		return b, err
	}
	b.Entity = entity
	var providers, operations, tier string
	var parentRaw any
	err = tx.QueryRowContext(ctx, `SELECT w.library_id,l.kind,w.title_seed,CASE WHEN w.year_override>0 THEN w.year_override ELSE w.year_seed END,w.query_override,w.revision,w.selection_revision,w.generation,w.status,w.provider,w.provider_type,w.provider_id,w.selection_mode,w.episode_order,w.requested_provider,w.accepted_publication,w.parent_id,p.revision,p.enabled,p.providers,p.language,p.region,p.refresh_mode,c.revision,c.confirmed,COALESCE(lc.revision,0),sp.revision,sp.tier,sp.operations_json,w.force_refresh
  FROM screen_metadata_work w JOIN libraries l ON l.id=w.library_id JOIN screen_metadata_policies p ON p.library_id=w.library_id CROSS JOIN screen_metadata_consent c LEFT JOIN library_configuration lc ON lc.library_id=l.id JOIN library_scan_policies sp ON sp.library_id=l.id WHERE w.target_kind=? AND w.target_id=? AND c.singleton=1 AND l.kind IN('movie','tv','anime')`, kind, entity).Scan(&b.Library, &b.LibraryKind, &b.Title, &b.Year, &b.Query, &b.Revision, &b.Selection, &b.Generation, &b.Status, &b.Provider, &b.ProviderType, &b.ProviderID, &b.Mode, &b.Order, &b.Requested, &b.Accepted, &parentRaw, &b.PolicyRevision, &b.Enabled, &providers, &b.Language, &b.Region, &b.RefreshMode, &b.ConsentRevision, &b.Confirmed, &b.Configuration, &b.ScanRevision, &tier, &operations, &b.ForceRefresh)
	if err != nil {
		return b, err
	}
	online, epoch, policyErr := onlineLookupPolicyTx(ctx, tx)
	if policyErr != nil {
		return b, policyErr
	}
	b.Enabled = b.Enabled && online
	b.LookupEpoch = epoch
	b.Parent = entityPublic(ctx, tx, scanParentID(parentRaw))
	if err = json.Unmarshal([]byte(providers), &b.Providers); err != nil {
		return b, err
	}
	var ops []string
	if err = json.Unmarshal([]byte(operations), &ops); err != nil {
		return b, err
	}
	for _, op := range ops {
		if op == "local_metadata" && tier != "file_list_only" {
			b.LocalAllowed = true
		}
	}
	var localMode, titleSource string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(p.local_tags,'prefer'),COALESCE(p.title_source,'tags') FROM libraries l LEFT JOIN library_preferences p ON p.library_id=l.id WHERE l.id=?`, b.Library).Scan(&localMode, &titleSource); err != nil {
		return b, err
	}
	b.LocalAllowed = b.LocalAllowed && localMode != "off"
	if kind == "show" {
		b.ItemKind = "show"
		err = tx.QueryRowContext(ctx, `SELECT incarnation,source_revision,hierarchy_revision,selection_revision,descriptive_revision FROM tvdb_publication_heads WHERE show_id=?`, entity).Scan(&b.Incarnation, &b.SourceRevision, &b.HierarchyRevision, &b.IdentityRevision, &b.DescriptionRevision)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT k.name,h.incarnation,h.source_revision,h.identity_revision,h.metadata_revision FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN metadata_publication_heads h ON h.item_id=e.id WHERE e.id=?`, entity).Scan(&b.ItemKind, &b.Incarnation, &b.SourceRevision, &b.IdentityRevision, &b.DescriptionRevision)
	}
	if err != nil {
		return b, err
	}
	if kind == "show" {
		if err = tx.QueryRowContext(ctx, `SELECT CASE WHEN count(DISTINCT numbering)=1 THEN min(numbering) ELSE 'mixed' END FROM catalog_episodes WHERE show_id=?`, entity).Scan(&b.Numbering); err != nil {
			return b, err
		}
	}
	if b.ItemKind == "episode" {
		var show int64
		err = tx.QueryRowContext(ctx, `SELECT e.show_id,COALESCE(s.number,-1),e.number,e.numbering,e.local_identity_status,w.accepted_publication,w.selection_revision,w.provider,w.provider_type,w.provider_id,w.episode_order FROM catalog_episodes e LEFT JOIN catalog_seasons s ON s.entity_id=e.season_id JOIN screen_metadata_work w ON w.target_kind='show' AND w.target_id=e.show_id WHERE e.entity_id=?`, entity).Scan(&show, &b.Season, &b.Number, &b.Numbering, &b.LocalIdentity, &b.ParentPublication, &b.ParentSelection, &b.ParentProvider, &b.ParentProviderType, &b.ParentProviderID, &b.ParentOrder)
		if err != nil {
			return b, err
		}
		b.Parent = entityPublic(ctx, tx, show)
		b.ParentEntity = show
		err = tx.QueryRowContext(ctx, `SELECT anilist_id,revision FROM screen_anime_seasons WHERE show_id=? AND season_number=?`, show, b.Season).Scan(&b.AnimeSeasonID, &b.AnimeSeasonRevision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return b, err
		}
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	rows, err := tx.QueryContext(ctx, `SELECT id,kind,generation,incarnation,root_identity,identity_confirmed,enabled,health,configured_root,root,follow_symlinks FROM library_sources WHERE library_id=? ORDER BY id`, b.Library)
	if err != nil {
		return b, err
	}
	enabledSource := false
	for rows.Next() {
		var sid, sk, inc, rootID, health, configured, root string
		var gen int64
		var confirmed, enabled, follow bool
		if err = rows.Scan(&sid, &sk, &gen, &inc, &rootID, &confirmed, &enabled, &health, &configured, &root, &follow); err != nil {
			rows.Close()
			return b, err
		}
		if enabled && health != "offline" && health != "removing" && health != "root_changed" {
			enabledSource = true
		}
		_ = enc.Encode([]any{sid, sk, gen, inc, rootID, confirmed, enabled, screenSourceHealthClass(health), configured, root, follow})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return b, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT a.token,a.path,a.size,a.modified_ns,a.available,COALESCE(o.id,''),COALESCE(o.revision,''),COALESCE(o.root_incarnation,''),COALESCE(o.state,''),COALESCE(o.retired,0),COALESCE(s.enabled,0),COALESCE(s.incarnation,''),COALESCE(s.health,''),COALESCE(o.relative_path,''),json_object('Title',vn.title,'ShowTitle',vn.show_title,'Year',vn.year,'Season',vn.season,'Number',vn.number,'Edition',vn.edition,'IDs',json(COALESCE((SELECT json_group_array(json_object('Provider',vi.provider,'ID',vi.provider_id)) FROM inventory_video_ids vi WHERE vi.asset_id=vn.asset_id AND vi.revision=vn.revision),'[]'))) FROM catalog_assets a LEFT JOIN inventory_objects o ON o.asset_id=a.token LEFT JOIN library_sources s ON s.id=o.source_id LEFT JOIN inventory_video_names vn ON vn.asset_id=a.token AND vn.revision=COALESCE(o.revision,'') WHERE a.token IN(`+screenTargetAssetsWithin(kind)+`) ORDER BY a.id,o.id`, entity, bound)
	if err != nil {
		return b, err
	}
	names := map[string]bool{}
	for rows.Next() {
		var aid, path, oid, rev, inc, state, sinc, health, relative, embeddedJSON string
		var size, modified int64
		var available, retired, enabled bool
		if err = rows.Scan(&aid, &path, &size, &modified, &available, &oid, &rev, &inc, &state, &retired, &enabled, &sinc, &health, &relative, &embeddedJSON); err != nil {
			rows.Close()
			return b, err
		}
		_ = enc.Encode([]any{aid, path, size, modified, available, oid, rev, inc, state, retired, enabled, sinc, screenSourceHealthClass(health)})
		good := available && (oid == "" && enabledSource || oid != "" && !retired && state == "available" && enabled && inc == sinc && health != "offline" && health != "removing" && health != "root_changed")
		if good {
			b.Available = true
		}
		if relative == "" {
			relative = filepath.Base(path)
		}
		var embedded naming.Embedded
		if err = json.Unmarshal([]byte(embeddedJSON), &embedded); err != nil {
			rows.Close()
			return b, err
		}
		// An episode is matched by its show's identity and its own numbers, not
		// by what its file's tags say, so a file's analysis arriving while its
		// fetch is in flight does not discard that fetch.
		if b.ItemKind != "episode" {
			_ = enc.Encode(embedded)
		}
		if titleSource == "names" {
			embedded = naming.Embedded{}
		}
		r := naming.ReadWithEmbedded(relative, b.LibraryKind, embedded)
		for _, e := range r.Editions {
			if len(b.Editions) < 64 {
				b.Editions = append(b.Editions, screenEdition{AssetID: aid, Label: e.Value, Source: string(e.Source)})
			}
		}
		for _, e := range r.Titles {
			if len(b.Evidence) >= 16 {
				break
			}
			if e.Confidence < .4 {
				continue
			}
			key := string(e.Source) + ":" + e.Value
			if names[key] {
				continue
			}
			names[key] = true
			evidence := MatchEvidence{Title: e.Value, Source: string(e.Source), Confidence: e.Confidence}
			_, preferredYear := r.PreferredTitle()
			if e.Source == naming.FileName || e.Source == naming.EmbeddedMetadata {
				evidence.Year = preferredYear
			}
			for _, y := range r.Years {
				if y.Path == e.Path && y.Confidence >= .6 {
					evidence.Year = y.Value
					break
				}
			}
			for _, id := range r.ProviderIDs {
				if id.Path == e.Path {
					typ := "movie"
					if kind == "show" {
						typ = "show"
					}
					if id.Value.Provider == "imdb" {
						typ = "title"
					}
					if id.Value.Provider == "anilist" {
						typ = "anime"
					}
					evidence.IDs = append(evidence.IDs, metadataprovider.ScreenID{Provider: id.Value.Provider, Type: typ, ID: id.Value.ID})
				}
			}
			for _, edition := range r.Editions {
				if edition.Path == e.Path {
					evidence.Edition = edition.Value
					break
				}
			}
			b.Evidence = append(b.Evidence, evidence)
		}

	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return b, err
	}
	// Ignore stale observations from a different media-file revision. Duplicate
	// tvshow.nfo copies collapse by digest; conflicting copies remain visible.
	if b.LocalAllowed {
		nfoKind := "movie"
		if kind == "show" {
			nfoKind = "show"
		} else if b.ItemKind == "episode" {
			nfoKind = "episode"
		}
		rows, err = tx.QueryContext(ctx, `SELECT DISTINCT n.digest,n.payload FROM video_nfo_evidence n JOIN catalog_assets a ON a.token=n.asset_id WHERE n.asset_id IN(`+screenTargetAssetsWithin(kind)+`) AND n.kind=? AND n.source_size=a.size AND n.source_modified=a.modified_ns ORDER BY n.digest,n.payload`, entity, bound, nfoKind)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			var digest, payload string
			if err = rows.Scan(&digest, &payload); err != nil {
				rows.Close()
				return b, err
			}
			_ = enc.Encode([]string{digest, payload})
			if len(b.NFO) < 64 {
				var n assets.VideoNFO
				if err = json.Unmarshal([]byte(payload), &n); err != nil {
					rows.Close()
					return b, err
				}
				if b.ItemKind == "episode" && !screenNFOCoordinates(n, b) {
					continue
				}
				b.NFO = append(b.NFO, n)
			} else {
				rows.Close()
				return b, errors.New("conflicting_local_metadata_budget")
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return b, err
		}
	}
	if b.TargetKind == "show" {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(a.duration),0)/60 FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_episodes e ON e.entity_id=l.entity_id WHERE e.show_id=? AND a.id<=? AND a.available=1`, entity, bound).Scan(&b.RuntimeMinutes); err != nil {
			return b, err
		}
	}
	if b.ItemKind == "movie" {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(a.duration),0)/60 FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE l.entity_id=? AND a.available=1`, entity).Scan(&b.RuntimeMinutes); err != nil {
			return b, err
		}
	}
	if b.Accepted != "" {
		var payload string
		if err = tx.QueryRowContext(ctx, `SELECT payload FROM screen_metadata_publications WHERE id=?`, b.Accepted).Scan(&payload); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return b, err
		}
		var r metadataprovider.ScreenRecord
		if json.Unmarshal([]byte(payload), &r) == nil {
			b.Existing = append(b.Existing, r.Crosswalk...)
		}
	}
	b.SourceDigest = hex.EncodeToString(h.Sum(nil))
	if kind == "item" {
		rows, err = tx.QueryContext(ctx, `SELECT provider,CAST(provider_id AS TEXT) FROM provider_evidence WHERE item_id=? AND provider IN('tmdb','tvdb','anilist') ORDER BY provider`, entity)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			v := metadataprovider.ScreenID{Type: b.ItemKind}
			if err = rows.Scan(&v.Provider, &v.ID); err != nil {
				rows.Close()
				return b, err
			}
			if v.Provider == "anilist" {
				v.Type = "anime"
			}
			if metadataprovider.ValidScreenID(v) {
				b.Existing = append(b.Existing, v)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return b, err
		}
	} else if b.Provider == "" {
		var id int64
		err = tx.QueryRowContext(ctx, `SELECT provider_id FROM tvdb_jobs WHERE show_id=?`, entity).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return b, err
		}
		if id > 0 {
			b.Existing = append(b.Existing, metadataprovider.ScreenID{Provider: "tvdb", Type: "show", ID: strings.TrimSpace(intString(id))})
		}
	}
	// Owner changes participate independently of catalog projection triggers.
	locks, err := relationshipLocks(ctx, tx, RepairTarget{kind, id})
	if err != nil {
		return b, err
	}
	b.IdentityLocked = locks["identity"]
	b.OwnerDigest = screenDigest(locks)
	return b, nil
}
func screenNFOCoordinates(n assets.VideoNFO, b screenBase) bool {
	if b.Numbering == "absolute" {
		return n.Absolute != nil && *n.Absolute == b.Number || n.Absolute == nil && n.Episode != nil && *n.Episode == b.Number && n.Season == nil
	}
	return n.Episode != nil && *n.Episode == b.Number && (n.Season == nil || *n.Season == b.Season)
}
func screenBaseAuthority(b screenBase) screenBase {
	// Job revision/status track UI progress, not the authority of an observed
	// candidate. Generation and selection revision still fence every publication.
	b.Revision = 0
	b.Status = ""
	return b
}
func screenInputDigest(b screenBase) string { return screenDigest(screenBaseAuthority(b)) }
