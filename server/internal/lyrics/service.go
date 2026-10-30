package lyrics

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"
)

type Service struct {
	DB       *sql.DB
	Local    LocalReader
	Provider *LRCLIB
}
type Actor struct {
	Authority   string `json:"authority"`
	AccountID   string `json:"accountId"`
	ProfileID   string `json:"profileId"`
	Owner       bool   `json:"-"`
	SessionHash string `json:"-"`
}

func (a Actor) key() string { return digest([]string{a.Authority, a.AccountID, a.ProfileID}) }

type Access struct {
	Actor                                    Actor
	ServerID, LibraryID, ItemID, ViewerFence string
	Authorize                                func(*sql.Tx) error
}
type Target struct {
	SourceID      string `json:"sourceId"`
	SourceVersion string `json:"sourceVersion"`
	SessionID     string `json:"sessionId"`
}
type Source struct {
	ID               string  `json:"id"`
	Version          string  `json:"version"`
	StartSeconds     float64 `json:"startSeconds"`
	Duration         float64 `json:"duration"`
	SourceDuration   float64 `json:"sourceDuration"`
	Local            bool    `json:"local"`
	Path, Root       string  `json:"-"`
	Size, ModifiedNS int64   `json:"-"`
}
type Scope struct {
	ServerID          string `json:"serverId"`
	LibraryID         string `json:"libraryId"`
	ItemID            string `json:"itemId"`
	ViewerFence       string `json:"viewerFence"`
	SessionID         string `json:"sessionId"`
	SessionGeneration int64  `json:"sessionGeneration"`
}
type Revision struct {
	ID         string     `json:"id"`
	Revision   int64      `json:"revision"`
	Scope      string     `json:"scope"`
	CanManage  bool       `json:"canManage"`
	Deleted    bool       `json:"deleted"`
	Language   string     `json:"language"`
	OffsetMS   int64      `json:"offsetMs"`
	Format     string     `json:"format"`
	Digest     string     `json:"digest"`
	Provenance Provenance `json:"provenance"`
	CreatedAt  string     `json:"createdAt"`
	Document   *Document  `json:"document,omitempty"`
}
type Selection struct {
	Revision int64     `json:"revision"`
	Resource *Revision `json:"resource"`
}
type View struct {
	LibrarySelection *Selection       `json:"librarySelection,omitempty"`
	Scope            Scope            `json:"scope"`
	Sources          []Source         `json:"sources"`
	Source           *Source          `json:"source"`
	Resources        []Revision       `json:"resources"`
	Selection        Selection        `json:"selection"`
	CanShare         bool             `json:"canShare"`
	Providers        ProviderSettings `json:"providers"`
}
type Mutation struct {
	Target
	ResourceID       string `json:"resourceId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Scope            string `json:"scope"`
	Language         string `json:"language"`
	Format           string `json:"format"`
	Text             string `json:"text"`
	ContentBase64    string `json:"contentBase64"`
	OffsetMS         *int64 `json:"offsetMs"`
	Rights           string `json:"rights"`
	CandidateID      string `json:"candidateId"`
}
type Choose struct {
	Scope string `json:"scope,omitempty"`
	Target
	ExpectedSelectionRevision int64  `json:"expectedSelectionRevision"`
	ResourceID                string `json:"resourceId"`
	Revision                  int64  `json:"revision"`
}

func (s *Service) begin(ctx context.Context, a Access) (*dbwork.Write, int64, error) {
	if a.Authorize == nil || a.Actor.AccountID == "" || a.Actor.ProfileID == "" || a.Actor.Authority == "" {
		return nil, 0, identity.ErrUnauthorized
	}
	gated, e := dbwork.Begin(ctx, s.DB, dbwork.ClassBackgroundMedia)
	if e != nil {
		return nil, 0, e
	}
	tx := gated.Tx()
	if e = a.Authorize(tx); e != nil {
		gated.Rollback()
		return nil, 0, e
	}
	entity, e := entityid.Resolve(ctx, tx, a.ItemID)
	if e != nil {
		gated.Rollback()
		return nil, 0, sql.ErrNoRows
	}
	var library string
	var kind int64
	e = tx.QueryRowContext(ctx, `SELECT cl.library_id,e.kind FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=?`, entity).Scan(&library, &kind)
	if e != nil || library != a.LibraryID || kind != 7 {
		gated.Rollback()
		if e != nil {
			return nil, 0, e
		}
		return nil, 0, ErrInput
	}
	return gated, entity, nil
}
func sources(ctx context.Context, tx *sql.Tx, a Access, entity int64) ([]Source, error) {
	rows, e := tx.QueryContext(ctx, `SELECT a.token,a.path,l.root,a.size,a.modified_ns,a.duration,link.start_seconds,COALESCE(link.end_seconds,a.duration),a.container FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id JOIN catalog_entities e ON e.id=link.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id WHERE link.entity_id=? ORDER BY link.part_index,a.token LIMIT 33`, entity)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var v Source
		var end float64
		var container string
		if e = rows.Scan(&v.ID, &v.Path, &v.Root, &v.Size, &v.ModifiedNS, &v.SourceDuration, &v.StartSeconds, &end, &container); e != nil {
			return nil, e
		}
		v.Duration = end - v.StartSeconds
		v.Local = container != "strm"
		if !finite(v.SourceDuration) || !finite(v.StartSeconds) || !finite(v.Duration) || v.Duration <= 0 || v.SourceDuration <= 0 || v.StartSeconds < 0 || end > v.SourceDuration+.1 {
			return nil, ErrSource
		}
		v.Version = digest([]any{a.ItemID, v.ID, v.SourceDuration, v.StartSeconds, end})
		out = append(out, v)
	}
	if len(out) > 32 {
		return nil, ErrCapacity
	}
	return out, rows.Err()
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func resolve(ctx context.Context, tx *sql.Tx, a Access, entity int64, t Target) ([]Source, *Source, int64, error) {
	all, e := sources(ctx, tx, a, entity)
	if e != nil {
		return nil, nil, 0, e
	}
	sourceID := t.SourceID
	var generation int64
	if t.SessionID != "" {
		var size, modified sql.NullInt64
		var expires, state string
		e = tx.QueryRowContext(ctx, `SELECT ps.asset_id,ps.generation,ps.state,ps.expires_at,p.size,p.modified_ns FROM playback_sessions ps LEFT JOIN playback_source_pins p ON p.session_id=ps.id AND p.asset_id=ps.asset_id WHERE ps.id=? AND ps.item_id=? AND ps.session_hash=? AND ps.account_id=? AND ps.profile_id=?`, t.SessionID, entity, a.Actor.SessionHash, a.Actor.AccountID, a.Actor.ProfileID).Scan(&sourceID, &generation, &state, &expires, &size, &modified)
		if e != nil {
			return nil, nil, 0, e
		}
		if state == "ended" || state == "stopped" || state == "failed" || expires <= time.Now().UTC().Format(time.RFC3339) || !size.Valid || !modified.Valid {
			return nil, nil, 0, ErrSource
		}
		if t.SourceID != "" && t.SourceID != sourceID {
			return nil, nil, 0, ErrSource
		}
		for _, v := range all {
			if v.ID == sourceID && (v.Size != size.Int64 || v.ModifiedNS != modified.Int64) {
				return nil, nil, 0, ErrSource
			}
		}
	}
	if sourceID == "" && len(all) > 0 {
		sourceID = all[0].ID
	}
	for i := range all {
		v := &all[i]
		if v.ID == sourceID {
			if t.SourceVersion != "" && t.SourceVersion != v.Version {
				return nil, nil, 0, ErrSource
			}
			return all, v, generation, nil
		}
	}
	if sourceID != "" {
		return nil, nil, 0, ErrSource
	}
	return all, nil, generation, nil
}
func canRead(a Actor, scope, authority, account, profile string) bool {
	return scope == "library" || a.Authority == authority && a.AccountID == account && a.ProfileID == profile
}
func canWrite(a Actor, scope, authority, account, profile string) bool {
	if scope == "library" {
		return a.Owner
	}
	return a.Authority == authority && a.AccountID == account && a.ProfileID == profile
}
func readRevision(ctx context.Context, tx *sql.Tx, a Access, entity int64, source *Source, id string, rev int64, include bool) (Revision, error) {
	var out Revision
	var document, provenance, authority, account, profile string
	e := tx.QueryRowContext(ctx, `SELECT r.id,v.revision,r.scope,r.authority,r.account_id,r.profile_id,v.deleted,v.document,v.digest,v.language,v.offset_ms,v.provenance,v.created_at FROM lyric_resources r JOIN lyric_revisions v ON v.resource_id=r.id WHERE r.id=? AND r.item_id=? AND r.asset_id=? AND r.source_version=? AND v.revision=?`, id, entity, source.ID, source.Version, rev).Scan(&out.ID, &out.Revision, &out.Scope, &authority, &account, &profile, &out.Deleted, &document, &out.Digest, &out.Language, &out.OffsetMS, &provenance, &out.CreatedAt)
	if e != nil {
		return out, e
	}
	if !canRead(a.Actor, out.Scope, authority, account, profile) {
		return out, sql.ErrNoRows
	}
	out.CanManage = canWrite(a.Actor, out.Scope, authority, account, profile)
	var d Document
	if json.Unmarshal([]byte(document), &d) != nil || json.Unmarshal([]byte(provenance), &out.Provenance) != nil || digest(d) != out.Digest {
		return out, ErrUnavailable
	}
	out.Format = d.Format
	if include {
		out.Document = &d
	}
	return out, nil
}
func selection(ctx context.Context, tx *sql.Tx, a Access, entity int64, src *Source, session string) (Selection, error) {
	if src != nil {
		choice, configured, err := librarySelection(ctx, tx, a, entity, src)
		if err != nil {
			return Selection{}, err
		}
		if configured {
			return choice, nil
		}
	}
	out := Selection{}
	if session == "" || src == nil {
		return out, nil
	}
	var id sql.NullString
	var rev sql.NullInt64
	e := tx.QueryRowContext(ctx, `SELECT revision,resource_id,lyric_revision FROM lyric_selections WHERE session_id=?`, session).Scan(&out.Revision, &id, &rev)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if id.Valid {
		r, e := readRevision(ctx, tx, a, entity, src, id.String, rev.Int64, true)
		if e != nil {
			return out, e
		}
		out.Resource = &r
	}
	return out, nil
}
func (s *Service) View(ctx context.Context, a Access, t Target) (View, error) {
	if err := s.discoverLocal(ctx, a, t); err != nil {
		return View{}, err
	}
	out := View{Scope: Scope{ServerID: a.ServerID, LibraryID: a.LibraryID, ItemID: a.ItemID, ViewerFence: a.ViewerFence, SessionID: t.SessionID}, Sources: []Source{}, Resources: []Revision{}, CanShare: a.Actor.Owner}
	gated, entity, e := s.begin(ctx, a)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	out.Sources, out.Source, out.Scope.SessionGeneration, e = resolve(ctx, tx, a, entity, t)
	if e != nil {
		return out, e
	}
	out.Providers, e = s.librarySettings(ctx, tx, a.LibraryID)
	if e != nil {
		return out, e
	}
	if out.Source != nil {
		rows, e := tx.QueryContext(ctx, `SELECT id,revision FROM lyric_resources WHERE item_id=? AND asset_id=? AND source_version=? AND deleted=0 AND (scope='library' OR (authority=? AND account_id=? AND profile_id=?)) ORDER BY scope, id LIMIT 65`, entity, out.Source.ID, out.Source.Version, a.Actor.Authority, a.Actor.AccountID, a.Actor.ProfileID)
		if e != nil {
			return out, e
		}
		type head struct {
			id  string
			rev int64
		}
		heads := []head{}
		for rows.Next() {
			var h head
			if e = rows.Scan(&h.id, &h.rev); e != nil {
				rows.Close()
				return out, e
			}
			heads = append(heads, h)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if len(heads) > 64 {
			return out, ErrCapacity
		}
		for _, h := range heads {
			r, e := readRevision(ctx, tx, a, entity, out.Source, h.id, h.rev, false)
			if e != nil {
				return out, e
			}
			out.Resources = append(out.Resources, r)
		}
		choice, configured, err := librarySelection(ctx, tx, a, entity, out.Source)
		if err != nil {
			return out, err
		}
		if configured {
			out.LibrarySelection = &choice
		}
		// Choose a local/default library lyric only when the viewer has never
		// selected or explicitly cleared one for this session.
		if t.SessionID != "" && !configured {
			_, e = tx.ExecContext(ctx, `INSERT INTO lyric_selections(session_id,revision,resource_id,lyric_revision) SELECT ?,1,r.id,r.revision FROM lyric_resources r JOIN lyric_revisions v ON v.resource_id=r.id AND v.revision=r.revision WHERE r.item_id=? AND r.asset_id=? AND r.source_version=? AND r.scope='library' AND r.deleted=0 ORDER BY CASE WHEN json_extract(v.document,'$.format')='lrc' THEN 0 ELSE 1 END,r.id LIMIT 1 ON CONFLICT(session_id) DO NOTHING`, t.SessionID, entity, out.Source.ID, out.Source.Version)
			if e != nil {
				return out, e
			}
		}
		if t.SessionID != "" {
			out.Selection, e = selection(ctx, tx, a, entity, out.Source, t.SessionID)
			if e != nil {
				return out, e
			}
		}
	}
	return out, gated.Commit()
}
func (s *Service) Read(ctx context.Context, a Access, t Target, id string, rev int64) (Revision, error) {
	gated2, entity, e := s.begin(ctx, a)
	if e != nil {
		return Revision{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	_, src, _, e := resolve(ctx, tx, a, entity, t)
	if e != nil {
		return Revision{}, e
	}
	if src == nil {
		return Revision{}, ErrSource
	}
	// Non-head revisions are deliverable only through this viewer's active pin.
	var current int64
	var deleted bool
	e = tx.QueryRowContext(ctx, `SELECT revision,deleted FROM lyric_resources WHERE id=? AND item_id=?`, id, entity).Scan(&current, &deleted)
	if e != nil {
		return Revision{}, e
	}
	if current != rev || deleted {
		p, e := selection(ctx, tx, a, entity, src, t.SessionID)
		if e != nil {
			return Revision{}, e
		}
		if p.Resource == nil || p.Resource.ID != id || p.Resource.Revision != rev {
			return Revision{}, sql.ErrNoRows
		}
	}
	out, e := readRevision(ctx, tx, a, entity, src, id, rev, true)
	if e != nil {
		return out, e
	}
	return out, gated2.Commit()
}
func (s *Service) Choose(ctx context.Context, a Access, in Choose) (Selection, error) {
	if in.Scope == "library" {
		return s.chooseLibrary(ctx, a, in)
	}
	if in.Scope != "" {
		return Selection{}, ErrInput
	}
	if in.SessionID == "" || in.SourceVersion == "" || in.ExpectedSelectionRevision < 0 || in.ExpectedSelectionRevision >= 1<<52 {
		return Selection{}, ErrInput
	}
	if in.ResourceID == "" && in.Revision != 0 || in.ResourceID != "" && in.Revision < 1 {
		return Selection{}, ErrInput
	}
	gated3, entity, e := s.begin(ctx, a)
	if e != nil {
		return Selection{}, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	_, src, _, e := resolve(ctx, tx, a, entity, in.Target)
	if e != nil {
		return Selection{}, e
	}
	if src == nil {
		return Selection{}, ErrSource
	}
	if _, configured, err := librarySelection(ctx, tx, a, entity, src); err != nil {
		return Selection{}, err
	} else if configured {
		return Selection{}, ErrConflict
	}
	before, e := selection(ctx, tx, a, entity, src, in.SessionID)
	if e != nil {
		return before, e
	}
	if before.Revision != in.ExpectedSelectionRevision {
		return Selection{}, ErrConflict
	}
	if in.ResourceID != "" {
		var head int64
		var deleted bool
		e = tx.QueryRowContext(ctx, `SELECT revision,deleted FROM lyric_resources WHERE id=? AND item_id=?`, in.ResourceID, entity).Scan(&head, &deleted)
		if e != nil {
			return Selection{}, e
		}
		if head != in.Revision || deleted {
			return Selection{}, ErrConflict
		}
		if _, e = readRevision(ctx, tx, a, entity, src, in.ResourceID, in.Revision, true); e != nil {
			return Selection{}, e
		}
	}
	var id, revision any
	if in.ResourceID != "" {
		id, revision = in.ResourceID, in.Revision
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO lyric_selections(session_id,revision,resource_id,lyric_revision) VALUES(?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET revision=excluded.revision,resource_id=excluded.resource_id,lyric_revision=excluded.lyric_revision WHERE lyric_selections.revision=?`, in.SessionID, before.Revision+1, id, revision, before.Revision)
	if e != nil {
		return Selection{}, e
	}
	out, e := selection(ctx, tx, a, entity, src, in.SessionID)
	if e != nil {
		return out, e
	}
	return out, gated3.Commit()
}
func (s *Service) Publish(ctx context.Context, a Access, in Mutation, operation string) (Revision, error) {
	if in.SourceVersion == "" || in.ExpectedRevision < 0 || in.ExpectedRevision > 128 {
		return Revision{}, ErrInput
	}
	if in.ResourceID == "" && in.ExpectedRevision != 0 || in.ResourceID != "" && in.ExpectedRevision == 0 {
		return Revision{}, ErrInput
	}
	if operation != "replace" && operation != "offset" && operation != "delete" {
		return Revision{}, ErrInput
	}
	if operation != "replace" && (in.ResourceID == "" || in.CandidateID != "" || in.Text != "" || in.ContentBase64 != "" || in.Format != "" || in.Language != "" || in.Rights != "") {
		return Revision{}, ErrInput
	}
	if operation == "offset" && in.OffsetMS == nil {
		return Revision{}, ErrInput
	}
	var doc Document
	var provenance Provenance
	var e error
	language := ""
	if operation == "replace" && in.CandidateID == "" {
		raw := []byte(in.Text)
		if in.ContentBase64 != "" {
			if in.Text != "" || len(in.ContentBase64) > base64.StdEncoding.EncodedLen(MaxTextBytes) {
				return Revision{}, ErrInput
			}
			raw, e = base64.StdEncoding.Strict().DecodeString(in.ContentBase64)
			if e != nil {
				return Revision{}, ErrInput
			}
		}
		doc, e = Parse(raw, in.Format)
		if e != nil {
			return Revision{}, e
		}
		language, e = Language(in.Language)
		if e != nil {
			return Revision{}, e
		}
		if !boundedText(in.Rights, 1024) {
			return Revision{}, ErrInput
		}
		provenance = Provenance{Origin: "upload", Label: "Uploaded text", Rights: in.Rights}
	}
	gated4, entity, e := s.begin(ctx, a)
	if e != nil {
		return Revision{}, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	_, src, _, e := resolve(ctx, tx, a, entity, in.Target)
	if e != nil {
		return Revision{}, e
	}
	if src == nil {
		return Revision{}, ErrSource
	}
	scope := in.Scope
	id := in.ResourceID
	if id != "" {
		var authority, account, profile string
		var current int64
		var deleted bool
		e = tx.QueryRowContext(ctx, `SELECT scope,authority,account_id,profile_id,revision,deleted FROM lyric_resources WHERE id=? AND item_id=? AND asset_id=? AND source_version=?`, id, entity, src.ID, src.Version).Scan(&scope, &authority, &account, &profile, &current, &deleted)
		if e != nil {
			return Revision{}, e
		}
		if !canWrite(a.Actor, scope, authority, account, profile) {
			return Revision{}, identity.ErrUnauthorized
		}
		if current != in.ExpectedRevision || deleted {
			return Revision{}, ErrConflict
		}
		// Reserve the final revision for deletion so a full history remains removable.
		if current >= 128 || current >= 127 && operation != "delete" {
			return Revision{}, ErrCapacity
		}
		if operation != "replace" {
			before, e := readRevision(ctx, tx, a, entity, src, id, current, true)
			if e != nil {
				return Revision{}, e
			}
			doc = *before.Document
			language = before.Language
			provenance = before.Provenance
			if operation == "delete" {
				n := before.OffsetMS
				in.OffsetMS = &n
			}
		}
	} else {
		if scope != "private" && scope != "library" {
			return Revision{}, ErrInput
		}
		if scope == "library" && !a.Actor.Owner {
			return Revision{}, identity.ErrUnauthorized
		}
		var count int
		// Deleted resources count toward the bound; tombstones cannot be abused to
		// allocate unlimited immutable content. A server administrator can export
		// retained history before intentional database maintenance.
		e = tx.QueryRowContext(ctx, `SELECT count(*) FROM lyric_resources WHERE item_id=? AND scope=? AND (scope='library' OR (authority=? AND account_id=? AND profile_id=?))`, entity, scope, a.Actor.Authority, a.Actor.AccountID, a.Actor.ProfileID).Scan(&count)
		if e != nil {
			return Revision{}, e
		}
		if count >= 16 {
			return Revision{}, ErrCapacity
		}
		id = identity.Token()
	}
	if in.CandidateID != "" {
		if operation != "replace" || in.Text != "" || in.ContentBase64 != "" || in.Format != "" || in.Rights != "" {
			return Revision{}, ErrInput
		}
		var raw, prov, target, expires string
		var expected, providerRevision int64
		e = tx.QueryRowContext(ctx, `SELECT document,provenance,language,target_id,expected_revision,expires_at,provider_revision FROM lyric_candidates WHERE id=? AND item_id=? AND asset_id=? AND source_version=? AND actor=?`, in.CandidateID, entity, src.ID, src.Version, a.Actor.key()).Scan(&raw, &prov, &language, &target, &expected, &expires, &providerRevision)
		if e != nil {
			return Revision{}, e
		}
		if expires <= time.Now().UTC().Format(time.RFC3339) || target != in.ResourceID || expected != in.ExpectedRevision {
			return Revision{}, ErrConflict
		}
		if json.Unmarshal([]byte(raw), &doc) != nil || json.Unmarshal([]byte(prov), &provenance) != nil {
			return Revision{}, ErrUnavailable
		}
		if provenance.Origin == "lrclib" {
			settings, e := s.librarySettings(ctx, tx, a.LibraryID)
			if e != nil {
				return Revision{}, e
			}
			if !settings.LRCLIB || settings.Revision != providerRevision {
				return Revision{}, ErrConflict
			}
		}
		if in.Language != "" {
			language, e = Language(in.Language)
			if e != nil {
				return Revision{}, e
			}
		}
	}
	offset := doc.EmbeddedOffsetMS
	if in.OffsetMS != nil {
		offset = *in.OffsetMS
	}
	if e = validateTiming(doc, offset, src.SourceDuration); e != nil {
		return Revision{}, e
	}
	rev := in.ExpectedRevision + 1
	deleted := operation == "delete"
	if in.ResourceID == "" {
		_, e = tx.ExecContext(ctx, `INSERT INTO lyric_resources(id,item_id,asset_id,source_version,scope,authority,account_id,profile_id,revision,source_duration,source_start,source_end) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, entity, src.ID, src.Version, scope, a.Actor.Authority, a.Actor.AccountID, a.Actor.ProfileID, rev, src.SourceDuration, src.StartSeconds, src.StartSeconds+src.Duration)
	} else {
		var result sql.Result
		result, e = tx.ExecContext(ctx, `UPDATE lyric_resources SET revision=?,deleted=? WHERE id=? AND revision=? AND deleted=0`, rev, deleted, id, in.ExpectedRevision)
		if e == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				return Revision{}, ErrConflict
			}
		}
	}
	if e != nil {
		return Revision{}, e
	}

	raw, _ := json.Marshal(doc)
	prov, _ := json.Marshal(provenance)
	_, e = tx.ExecContext(ctx, `INSERT INTO lyric_revisions(resource_id,revision,document,digest,language,offset_ms,provenance,actor,created_at,deleted) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, rev, string(raw), digest(doc), language, offset, string(prov), a.Actor.key(), time.Now().UTC().Format(time.RFC3339Nano), deleted)
	if e != nil {
		return Revision{}, e
	}
	if deleted && scope == "library" {
		if _, e = tx.ExecContext(ctx, `UPDATE lyric_library_selections SET resource_id=NULL,lyric_revision=NULL,revision=revision+1,actor=? WHERE item_id=? AND resource_id=?`, a.Actor.key(), entity, id); e != nil {
			return Revision{}, e
		}
	}

	if in.CandidateID != "" {
		if _, e = tx.ExecContext(ctx, `DELETE FROM lyric_candidates WHERE id=?`, in.CandidateID); e != nil {
			return Revision{}, e
		}
	}
	out, e := readRevision(ctx, tx, a, entity, src, id, rev, true)
	if e != nil {
		return out, e
	}
	return out, gated4.Commit()
}
