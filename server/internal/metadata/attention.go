package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

type AttentionCandidate struct {
	PosterURL  string                         `json:"posterUrl,omitempty"`
	Key        string                         `json:"key"`
	Identity   metadataprovider.ScreenID      `json:"identity"`
	Title      string                         `json:"title"`
	Year       int                            `json:"year"`
	Confidence float64                        `json:"confidence"`
	Reasons    []string                       `json:"reasons"`
	Orders     []metadataprovider.ScreenOrder `json:"orders"`
}
type AttentionEntry struct {
	ArtworkURL   string               `json:"artworkUrl,omitempty"`
	ParentTitle  string               `json:"parentTitle,omitempty"`
	ParentArtist string               `json:"parentArtist,omitempty"`
	LibraryID    string               `json:"libraryId"`
	ItemKind     string               `json:"itemKind"`
	ParentID     string               `json:"parentId"`
	Actions      []string             `json:"actions"`
	EntityID     string               `json:"entityId"`
	Kind         string               `json:"kind"`
	Title        string               `json:"title"`
	Revision     int64                `json:"revision"`
	Reason       string               `json:"reason"`
	Confidence   float64              `json:"confidence"`
	Candidates   []AttentionCandidate `json:"candidates"`
}
type AttentionPage struct {
	Total      int64            `json:"total"`
	LibraryID  string           `json:"libraryId"`
	Entries    []AttentionEntry `json:"entries"`
	NextCursor string           `json:"nextCursor"`
}

// Attention reads one indexed page, then at most three candidates per entry.
func (s *Service) Attention(ctx context.Context, library, cursor string, limit int) (AttentionPage, error) {
	return s.AttentionFiltered(ctx, library, cursor, limit, true)
}

func (s *Service) AttentionFiltered(ctx context.Context, library, cursor string, limit int, includeInProgress bool) (AttentionPage, error) {
	out := AttentionPage{LibraryID: library, Entries: []AttentionEntry{}}
	var after int64
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > 64 {
			return out, ErrRepairInput
		}
		after, err = strconv.ParseInt(string(raw), 10, 64)
		if err != nil || after < 1 {
			return out, ErrRepairInput
		}
	}
	if limit < 1 || limit > 50 {
		return out, ErrRepairInput
	}
	read, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer read.Rollback()
	tx := read.Tx()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries WHERE id=?)`, library).Scan(&exists); err != nil {
		return out, err
	}
	if library != "" && !exists {
		return out, sql.ErrNoRows
	}

	scope, listeningScope := "", ""
	args := []any{after, includeInProgress}
	if library != "" {
		scope, listeningScope = " AND a.library_id=?3", " AND l.library_id=?3"
		args = append(args, library)
	} else {
		args = append(args, "")
	}
	args = append(args, limit+1)
	// Film and TV titles come from the matcher's list, music and audiobooks
	// from their own; one entity is only ever on one of them.
	listSQL := `SELECT library_id,entity_id,target_kind,title,generation,reason,confidence,kind_name,parent,enabled,confirmed,public FROM (
 SELECT a.library_id,a.entity_id,a.target_kind,e.title,w.generation,a.reason,a.confidence,k.name kind_name,COALESCE(pid(parent.public_id),'') parent,p.enabled,c.confirmed,pid(e.public_id) public FROM screen_metadata_attention a JOIN catalog_entities e ON e.id=a.entity_id JOIN screen_metadata_work w ON w.target_kind=a.target_kind AND w.target_id=a.entity_id
 JOIN catalog_kinds k ON k.id=e.kind LEFT JOIN catalog_entities parent ON parent.id=w.parent_id
 JOIN screen_metadata_policies p ON p.library_id=a.library_id CROSS JOIN screen_metadata_consent c
 JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0
 WHERE e.retired=0 AND a.retired=0 AND c.singleton=1 AND a.entity_id>?1 AND NOT EXISTS(SELECT 1 FROM metadata_kept_files kept WHERE kept.entity_id=e.id AND kept.files_fingerprint=` + attentionFileFingerprint("e.id") + `) AND EXISTS(SELECT 1 FROM library_metadata_agents agent WHERE agent.library_id=a.library_id AND agent.agent='online') AND (?2 OR a.reason NOT IN('pending','searching','pending_search','pending_episodes','pending_children','pending_apply'))` + scope + `
 UNION ALL
 SELECT l.library_id,l.entity_id,l.kind,e.title,l.generation,l.reason,0,k.name,COALESCE(pid(parent.public_id),''),1,1,pid(e.public_id) FROM listening_metadata_attention l JOIN catalog_entities e ON e.id=l.entity_id
 JOIN catalog_kinds k ON k.id=e.kind LEFT JOIN catalog_entities parent ON parent.id=e.parent_id
 JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.retired=0
 WHERE e.retired=0 AND l.retired=0 AND l.entity_id>?1 AND l.kind<>'book' AND l.reason<>'no_author' AND NOT EXISTS(SELECT 1 FROM metadata_kept_files kept WHERE kept.entity_id=e.id AND kept.files_fingerprint=` + attentionFileFingerprint("e.id") + `) AND EXISTS(SELECT 1 FROM library_metadata_agents agent WHERE agent.library_id=l.library_id AND agent.agent='online')` + listeningScope + `
 UNION ALL
 SELECT cl.library_id,e.id,'item',e.title,portico_metadata_files_revision(` + attentionFileFingerprint("e.id") + `),'unmatched',0,'episode','',1,1,pid(e.public_id)
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id
 WHERE e.kind=4 AND e.parent_id IS NULL AND e.retired=0 AND cl.retired=0 AND e.id>?1
 AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=e.id AND a.available=1)
 AND NOT EXISTS(SELECT 1 FROM catalog_episodes ep WHERE ep.entity_id=e.id)
 AND NOT EXISTS(SELECT 1 FROM metadata_kept_files kept WHERE kept.entity_id=e.id AND kept.files_fingerprint=` + attentionFileFingerprint("e.id") + `)
 AND EXISTS(SELECT 1 FROM library_metadata_agents agent WHERE agent.library_id=cl.library_id AND agent.agent='online')
 AND (?3='' OR cl.library_id=?3)
) ORDER BY entity_id LIMIT ?4`
	countSQL := listSQL[:strings.LastIndex(listSQL, " ORDER BY entity_id")]
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM (`+countSQL+`)`, 0, false, library).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, listSQL, args...)
	if err != nil {
		return out, err
	}
	ids := []int64{}
	for rows.Next() {
		var e AttentionEntry
		var id int64
		var enabled, confirmed bool
		if err = rows.Scan(&e.LibraryID, &id, &e.Kind, &e.Title, &e.Revision, &e.Reason, &e.Confidence, &e.ItemKind, &e.ParentID, &enabled, &confirmed, &e.EntityID); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Entries) == limit {
			out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(ids[len(ids)-1], 10)))
			break
		}
		e.Actions = []string{"dismiss"}
		if listeningAttentionKind(e.Kind) {
			// Music and books are settled on their own page; an album or track
			// can be looked up again.
			if e.Kind != "book" {
				e.Actions = append(e.Actions, "retry")
			}
		} else if e.ItemKind == "episode" {
			if e.ParentID != "" {
				e.Actions = append(e.Actions, "parent")
			}
		} else if enabled && confirmed {
			e.Actions = append(e.Actions, "accept", "search", "id")
		}
		if e.ParentID != "" {
			if err = tx.QueryRowContext(ctx, `SELECT title FROM catalog_entities WHERE public_id=pid_blob(?)`, e.ParentID).Scan(&e.ParentTitle); err != nil {
				rows.Close()
				return out, err
			}
		}
		if e.ItemKind == "song" {
			var artist sql.NullString
			if err = tx.QueryRowContext(ctx, `SELECT artist.title FROM catalog_entities song JOIN catalog_songs d ON d.entity_id=song.id JOIN catalog_albums album ON album.entity_id=d.album_id JOIN catalog_entities artist ON artist.id=album.artist_id WHERE song.public_id=pid_blob(?)`, e.EntityID).Scan(&artist); err != nil && !errors.Is(err, sql.ErrNoRows) {
				rows.Close()
				return out, err
			}
			e.ParentArtist = artist.String
		}
		e.Candidates = []AttentionCandidate{}
		out.Entries = append(out.Entries, e)
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Select only the three best candidates of each visible page entry in one
	// statement. The driving list is bounded by the page, never the catalogue.
	requested := make([][2]any, len(ids))
	for i, id := range ids {
		requested[i] = [2]any{out.Entries[i].Kind, id}
		if listeningAttentionKind(out.Entries[i].Kind) {
			requested[i] = [2]any{"", id}
		}
	}
	raw, err := json.Marshal(requested)
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT CAST(j.key AS INTEGER),c.candidate_key,c.payload,c.confidence,c.reasons
 FROM json_each(?) j CROSS JOIN screen_metadata_candidates c
 ON c.target_kind=json_extract(j.value,'$[0]') AND c.target_id=json_extract(j.value,'$[1]')
 WHERE c.candidate_key IN(SELECT best.candidate_key FROM screen_metadata_candidates best
 WHERE best.target_kind=c.target_kind AND best.target_id=c.target_id ORDER BY best.confidence DESC,best.candidate_key LIMIT 3)
 ORDER BY CAST(j.key AS INTEGER),c.confidence DESC,c.candidate_key`, string(raw))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var i int
		var c AttentionCandidate
		var payload, reasons string
		var record metadataprovider.ScreenRecord
		if err = rows.Scan(&i, &c.Key, &payload, &c.Confidence, &reasons); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal([]byte(payload), &record); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal([]byte(reasons), &c.Reasons); err != nil {
			rows.Close()
			return out, err
		}
		c.Identity, c.Title, c.Year = record.Identity, record.Title, record.Year
		c.Orders = screenRecordOrders(record)
		if record.PosterPath != "" {
			c.PosterURL = "/v1/metadata/" + url.PathEscape(out.Entries[i].Kind) + "/" + url.PathEscape(out.Entries[i].EntityID) + "/art/poster?candidate=" + url.QueryEscape("attention:"+c.Key) + "&w=400"
		}
		out.Entries[i].Candidates = append(out.Entries[i].Candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for i := range out.Entries {
		if len(out.Entries[i].Candidates) == 0 {
			actions := []string{}
			for _, action := range out.Entries[i].Actions {
				if action != "accept" {
					actions = append(actions, action)
				}
			}
			out.Entries[i].Actions = actions
		}
	}
	for i := range out.Entries {
		entry := &out.Entries[i]
		album := ""
		if entry.Kind == "album" {
			album = entry.EntityID
		} else if entry.Kind == "song" {
			album = entry.ParentID
		}
		if album == "" {
			continue
		}
		choices, err := readArtworkChoices(ctx, tx, RepairTarget{Kind: "album", ID: album})
		if err != nil {
			return out, err
		}
		for _, choice := range choices {
			if choice.Role == "cover" && choice.Subject == "" {
				entry.ArtworkURL = choice.URL
				break
			}
		}
	}
	return out, nil
}

type AttentionCommand struct {
	Kind             string `json:"kind"`
	EntityID         string `json:"entityId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Action           string `json:"action"`
	CandidateKey     string `json:"candidateKey,omitempty"`
	Provider         string `json:"provider,omitempty"`
	ProviderID       string `json:"providerId,omitempty"`
	Query            string `json:"query,omitempty"`
	Order            string `json:"order,omitempty"`
}

// listeningAttentionKind reports the list entries that are music or audiobooks.
func listeningAttentionKind(kind string) bool {
	return kind == "album" || kind == "song" || kind == "book"
}

func (s *Service) AttentionAction(ctx context.Context, library string, in AttentionCommand, actor MBActor, authorize func(*sql.Tx) error) error {
	if listeningAttentionKind(in.Kind) && in.ExpectedRevision >= 1 {
		return s.listeningAttentionAction(ctx, library, in, authorize)
	}
	if in.Kind == "item" && in.Action == "dismiss" {
		handled, err := s.keepLooseEpisode(ctx, library, in, authorize)
		if err != nil || handled {
			return err
		}
	}
	if (in.Kind != "item" && in.Kind != "show") || in.ExpectedRevision < 1 {
		return ErrRepairInput
	}
	// Bind all commands to the listed library, even when a caller supplies another ID.
	var found string
	// Resolve through the public-id codec inside a snapshot, never compare public text to an integer.
	read, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return e
	}
	entity, e := resolveEntity(ctx, read.Tx(), in.EntityID)
	if e == nil {
		e = read.Tx().QueryRowContext(ctx, `SELECT library_id FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, in.Kind, entity).Scan(&found)
	}
	read.Rollback()
	if e != nil {
		return e
	}
	if found != library {
		return ErrRepairInput
	}
	if in.Action != "dismiss" {
		var kind string
		if e = dbwork.QueryRow(ctx, s.db, `SELECT k.name FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.id=?`, entity).Scan(&kind); e != nil {
			return e
		}
		if kind == "episode" {
			return ErrRepairInput
		}
	}
	switch in.Action {
	case "accept", "search":
		return s.attentionObservedAction(ctx, library, in, actor, authorize)
	case "id":
		if hint, ok := metadataprovider.ParseScreenHint(in.ProviderID, "movie"); ok && hint.Provider == "imdb" {
			in.Action = "search"
			in.Query = "imdb:" + hint.ID
			return s.attentionObservedAction(ctx, library, in, actor, authorize)
		}
	case "dismiss":
	default:
		return ErrRepairInput
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if e != nil {
		return e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	b, e := readScreenBase(ctx, tx, in.Kind, in.EntityID)
	if e != nil {
		return e
	}
	if b.Library != library {
		return ErrRepairInput
	}
	if b.Generation != in.ExpectedRevision {
		return ErrScreenConflict
	}
	if in.Action == "dismiss" {
		_, e = tx.ExecContext(ctx, `INSERT INTO metadata_kept_files(entity_id,files_fingerprint) SELECT e.id,`+attentionFileFingerprint("e.id")+` FROM catalog_entities e WHERE e.id=? ON CONFLICT(entity_id) DO UPDATE SET files_fingerprint=excluded.files_fingerprint`, b.Entity)
	} else {
		if b.ItemKind == "episode" {
			return ErrRepairInput
		}
		typ := b.ItemKind
		if in.Provider == "anilist" {
			typ = "anime"
		}
		id := metadataprovider.ScreenID{Provider: in.Provider, Type: typ, ID: in.ProviderID}
		if hint, ok := metadataprovider.ParseScreenHint(in.ProviderID, typ); ok {
			if in.Provider != "" && hint.Provider != in.Provider {
				return ErrRepairInput
			}
			id = hint
		}
		if !metadataprovider.ValidScreenID(id) {
			return ErrRepairInput
		}
		t := RepairTarget{in.Kind, in.EntityID}
		before, ent, e := readRepairSnapshot(ctx, tx, t)
		if e != nil {
			return e
		}
		revision, e := repairRevision(ctx, tx, t, before, ent)
		if e != nil {
			return e
		}
		order := in.Order
		if order == "" {
			order = "official"
		}
		if in.Provider == "anilist" && order == "official" {
			order = "seasonal"
		}
		if e = setScreenOwnerIntent(ctx, tx, b, id, order, actor, false); e != nil {
			return e
		}
		e = recordRepair(ctx, tx, t, before, revision, "identify", actor.Authority+":"+actor.AccountID+":"+actor.ProfileID)
		if e != nil {
			return e
		}
	}
	if e != nil {
		return e
	}
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	return gated.Commit()
}

// The list's revision is the input generation, not worker leases/status. Retry
// only the old route's incidental revision races; candidate input digests still
// reject a choice whose evidence or authority really changed.
func (s *Service) attentionObservedAction(ctx context.Context, library string, in AttentionCommand, actor MBActor, authorize func(*sql.Tx) error) error {
	for attempt := 0; attempt < 3; attempt++ {
		read, e := dbwork.BeginSnapshot(ctx, s.db)
		if e != nil {
			return e
		}
		b, e := readScreenBase(ctx, read.Tx(), in.Kind, in.EntityID)
		read.Rollback()
		if e != nil {
			return e
		}
		if b.Library != library {
			return ErrRepairInput
		}
		if b.Generation != in.ExpectedRevision {
			return ErrScreenConflict
		}
		provider := in.Provider
		if provider == "" && len(b.Providers) > 0 {
			provider = b.Providers[0]
		}
		if in.Action == "accept" {
			e = s.SelectScreen(ctx, in.Kind, in.EntityID, ScreenSelection{Actor: actor, ExpectedRevision: b.Revision, CandidateKey: in.CandidateKey, Order: in.Order}, authorize)
		} else {
			e = s.SearchScreen(ctx, in.Kind, in.EntityID, ScreenSearch{ExpectedRevision: b.Revision, Provider: provider, Query: in.Query}, authorize)
		}
		if !errors.Is(e, ErrScreenConflict) {
			return e
		}
	}
	return ErrScreenConflict
}

// AttentionCounts reports exact waiting counts without loading titles or candidates.
type AttentionLibraryCount struct {
	LibraryID string `json:"libraryId"`
	Total     int64  `json:"total"`
}
type AttentionCounts struct {
	Total     int64                   `json:"total"`
	Libraries []AttentionLibraryCount `json:"libraries"`
}

func (s *Service) AttentionCounts(ctx context.Context) (AttentionCounts, error) {
	out := AttentionCounts{Libraries: []AttentionLibraryCount{}}
	rows, err := s.db.QueryContext(ctx, `SELECT library_id FROM catalog_libraries WHERE retired=0 ORDER BY library_id`)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		page, err := s.AttentionFiltered(ctx, id, "", 1, false)
		if err != nil {
			return out, err
		}
		out.Total += page.Total
		out.Libraries = append(out.Libraries, AttentionLibraryCount{LibraryID: id, Total: page.Total})
	}
	return out, nil
}

// listeningAttentionAction dismisses a music or audiobook entry, or looks an
// album or track up again. The entry must still be what the owner saw.
func (s *Service) listeningAttentionAction(ctx context.Context, library string, in AttentionCommand, authorize func(*sql.Tx) error) error {
	if in.Action != "dismiss" && (in.Action != "retry" || in.Kind == "book") {
		return ErrRepairInput
	}
	var mbRevision int64
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if authorize != nil {
			if err := authorize(tx); err != nil {
				return err
			}
		}
		entity, err := resolveEntity(ctx, tx, in.EntityID)
		if err != nil {
			return err
		}
		var found, kind string
		var generation int64
		if err = tx.QueryRowContext(ctx, `SELECT library_id,kind,generation FROM listening_metadata_attention WHERE entity_id=?`, entity).Scan(&found, &kind, &generation); err != nil {
			return err
		}
		if found != library || kind != in.Kind {
			return ErrRepairInput
		}
		if generation != in.ExpectedRevision {
			return ErrScreenConflict
		}
		if in.Action == "dismiss" {
			_, err = tx.ExecContext(ctx, `INSERT INTO metadata_kept_files(entity_id,files_fingerprint) SELECT e.id,`+attentionFileFingerprint("e.id")+` FROM catalog_entities e WHERE e.id=? ON CONFLICT(entity_id) DO UPDATE SET files_fingerprint=excluded.files_fingerprint`, entity)
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT revision FROM mb_jobs WHERE kind=? AND entity_id=?`, in.Kind, entity).Scan(&mbRevision)
	})
	if err != nil || in.Action == "dismiss" {
		return err
	}
	if err = s.RetryMusicBrainz(in.Kind, in.EntityID, mbRevision, authorize); errors.Is(err, ErrMBConflict) {
		return ErrScreenConflict
	}
	return err
}

// The exact file set and evidence decide when Keep as is expires. Provider
// retries and new matches do not alter this fingerprint.
func attentionFileFingerprint(entity string) string {
	return `(SELECT COALESCE(json_group_array(json(f.file)),'[]') FROM (WITH RECURSIVE members(id) AS(SELECT ` + entity + ` UNION ALL SELECT child.id FROM catalog_entities child JOIN members parent ON child.parent_id=parent.id) SELECT DISTINCT a.id,json_array(a.id,a.path,a.size,a.modified_ns) file FROM members JOIN catalog_asset_links l ON l.entity_id=members.id JOIN catalog_assets a ON a.id=l.asset_id ORDER BY a.id) f)`
}

func (s *Service) keepLooseEpisode(ctx context.Context, library string, in AttentionCommand, authorize func(*sql.Tx) error) (bool, error) {
	handled := false
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if authorize != nil {
			if err := authorize(tx); err != nil {
				return err
			}
		}
		var entity, revision int64
		err := tx.QueryRowContext(ctx, `SELECT e.id,portico_metadata_files_revision(`+attentionFileFingerprint("e.id")+`) FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?) AND cl.library_id=? AND e.kind=4 AND e.parent_id IS NULL AND NOT EXISTS(SELECT 1 FROM catalog_episodes ep WHERE ep.entity_id=e.id)`, in.EntityID, library).Scan(&entity, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		handled = true
		if revision != in.ExpectedRevision {
			return ErrScreenConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO metadata_kept_files(entity_id,files_fingerprint) SELECT e.id,`+attentionFileFingerprint("e.id")+` FROM catalog_entities e WHERE e.id=? ON CONFLICT(entity_id) DO UPDATE SET files_fingerprint=excluded.files_fingerprint`, entity)
		return err
	})
	return handled, err
}

// Candidate previews use the existing allowlisted artwork worker. Listing a
// candidate never fetches its image or makes a browser contact the provider.
func (s *Service) PreviewAttentionArtwork(ctx context.Context, t RepairTarget, key string, size ArtworkSize, authorize func(*sql.Tx) error) (*os.File, string, error) {
	var candidate string
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if authorize == nil {
			return ErrRepairInput
		}
		if err := authorize(tx); err != nil {
			return err
		}
		entity, err := artworkEntity(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		base, err := readScreenBase(ctx, tx, t.Kind, t.ID)
		if err != nil {
			return err
		}
		var raw, digest string
		if err = tx.QueryRowContext(ctx, `SELECT payload,input_digest FROM screen_metadata_candidates WHERE candidate_key=? AND target_kind=? AND target_id=?`, key, t.Kind, entity).Scan(&raw, &digest); err != nil {
			return err
		}
		if digest != screenInputDigest(base) {
			return ErrScreenConflict
		}
		var record metadataprovider.ScreenRecord
		if err = json.Unmarshal([]byte(raw), &record); err != nil {
			return err
		}
		if !base.Confirmed || !base.Enabled || !screenProviderEnabled(base, record.Identity.Provider) {
			return ErrRepairInput
		}
		origin := record.PosterPath
		if record.Identity.Provider == "tmdb" {
			origin = imageURL(origin, "original")
		}
		if !artworkURLAllowed(origin, record.Identity.Provider) {
			return ErrRepairInput
		}
		fence, err := artworkFence(ctx, tx, t)
		if err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		candidate, err = insertArtworkCandidate(ctx, tx, t, "poster", "", record.Identity.Provider, publicationDigest(origin), origin, "", artworkAttribution(record.Identity.Provider), fence, now, 0)
		if err != nil {
			return err
		}
		return queueArtworkPreview(ctx, tx, t, candidate, "", now)
	})
	if err != nil {
		return nil, "", err
	}
	return s.PreviewArtwork(ctx, t, candidate, size)
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("portico_metadata_files_revision", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		raw, ok := args[0].(string)
		if !ok {
			return nil, ErrRepairInput
		}
		sum := sha256.Sum256([]byte(raw))
		return int64(binary.BigEndian.Uint64(sum[:8])%((1<<53)-1)) + 1, nil
	})
}
