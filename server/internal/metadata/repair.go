package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

var ErrRepairConflict = errors.New("metadata changed; reload and review your decision")
var ErrRepairInput = requestinput.New("invalid metadata repair")

type RepairTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type RepairField struct {
	Value     string   `json:"value"`
	Automatic string   `json:"automaticValue"`
	Locked    bool     `json:"locked"`
	Source    string   `json:"source"`
	Observed  string   `json:"observedAt,omitempty"`
	Values    []string `json:"values,omitempty"`
}
type RepairRelationship struct {
	SourceRowID  string `json:"sourceRowId,omitempty"`
	OwnerEntryID string `json:"ownerEntryId,omitempty"`
	SourceStatus string `json:"sourceStatus,omitempty"`
	RecordID     string `json:"recordId,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Department   string `json:"department,omitempty"`
	Kind         string `json:"kind"`
	TargetKind   string `json:"targetKind"`
	TargetID     string `json:"targetId"`
	Label        string `json:"label"`
	Role         string `json:"role,omitempty"`
	Ordinal      int    `json:"ordinal"`
	Source       string `json:"source"`
	Locked       bool   `json:"locked"`
	Observed     string `json:"observedAt,omitempty"`
}
type RepairIdentity struct {
	Engine   string `json:"engine,omitempty"`
	Type     string `json:"type,omitempty"`
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Order    string `json:"order,omitempty"`
	Revision int64  `json:"revision"`
	Status   string `json:"status"`
	Locked   bool   `json:"locked"`
}
type RepairCandidate struct {
	IdentityType string                         `json:"identityType,omitempty"`
	IdentityID   string                         `json:"identityId,omitempty"`
	Orders       []metadataprovider.ScreenOrder `json:"orders,omitempty"`
	ID           string                         `json:"id"`
	Provider     string                         `json:"provider"`
	Title        string                         `json:"title"`
	Subtitle     string                         `json:"subtitle"`
	Observed     string                         `json:"observedAt"`
	PreviewURL   string                         `json:"previewUrl,omitempty"`
	Year         int                            `json:"year,omitempty"`
}
type RepairHistory struct {
	ID       int64  `json:"id"`
	Trigger  string `json:"trigger"`
	Observed string `json:"observedAt"`
}
type RepairSnapshot struct {
	CanEditProviderCredits bool                   `json:"canEditProviderCredits,omitempty"`
	RelationshipLocks      map[string]bool        `json:"relationshipLocks"`
	Fields                 map[string]RepairField `json:"fields"`
	Identity               *RepairIdentity        `json:"identity,omitempty"`
	Relationships          []RepairRelationship   `json:"relationships"`
	Artwork                []ArtworkChoice        `json:"artwork"`
}
type RepairState struct {
	OnlineLookups           *bool             `json:"onlineLookups,omitempty"`
	ArtworkFallbackURL      string            `json:"artworkFallbackUrl,omitempty"`
	CanMakeEpisodeThumbnail bool              `json:"canMakeEpisodeThumbnail,omitempty"`
	TargetItemKind          string            `json:"-"`
	Tabs                    []string          `json:"tabs"`
	Target                  RepairTarget      `json:"target"`
	Library                 string            `json:"libraryId"`
	Server                  string            `json:"serverId"`
	ViewerFence             string            `json:"viewerFence"`
	Revision                string            `json:"revision"`
	Snapshot                RepairSnapshot    `json:"snapshot"`
	Candidates              []RepairCandidate `json:"candidates"`
	History                 []RepairHistory   `json:"history"`
	Artwork                 ArtworkState      `json:"artwork"`
	Cascades                []RepairCascade   `json:"cascades"`
	Schema                  []RepairFieldSpec `json:"schema"`
	ArtworkRoles            []string          `json:"artworkRoles"`
}
type RepairFieldEdit struct {
	Value     *string   `json:"value,omitempty"`
	Values    *[]string `json:"values,omitempty"`
	Locked    *bool     `json:"locked,omitempty"`
	Automatic bool      `json:"useAutomatic,omitempty"`
}
type RepairCommand struct {
	EffectiveCredits bool                       `json:"effectiveCredits,omitempty"`
	Query            string                     `json:"query,omitempty"`
	Year             int                        `json:"year,omitempty"`
	Relationships    []RepairRelationship       `json:"relationships,omitempty"`
	ExpectedRevision string                     `json:"expectedRevision"`
	Action           string                     `json:"action"`
	Fields           map[string]RepairFieldEdit `json:"fields,omitempty"`
	CandidateID      string                     `json:"candidateId,omitempty"`
	Provider         string                     `json:"provider,omitempty"`
	Order            string                     `json:"order,omitempty"`
	HistoryID        int64                      `json:"historyId,omitempty"`
	Confirm          bool                       `json:"confirm,omitempty"`
	Role             string                     `json:"role,omitempty"`
	Subject          string                     `json:"subject,omitempty"`
	Locked           *bool                      `json:"locked,omitempty"`
	Intent           string                     `json:"intent,omitempty"`
	OperationID      string                     `json:"operationId,omitempty"`
}
type repairEntity struct {
	library  string
	itemKind string
	fields   map[string]string
	schema   []RepairFieldSpec
	stamp    string
	// readingAutomatic is the genuine same-transaction automatic attribution
	// for an ebook/comic; it is what "use automatic" restores and what a clear
	// falls back to.
	readingAutomatic map[string]string
}

func readRepairSnapshot(ctx context.Context, tx *sql.Tx, t RepairTarget) (RepairSnapshot, repairEntity, error) {
	out := RepairSnapshot{Fields: map[string]RepairField{}, Relationships: []RepairRelationship{}, Artwork: []ArtworkChoice{}}
	ent, err := readRepairEntity(ctx, tx, t)
	if err != nil {
		return out, ent, err
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return out, ent, err
	}
	automatic, err := automaticFieldSource(ctx, tx, t, ent)
	if err != nil {
		return out, ent, err
	}
	for k, v := range ent.fields {
		source := automatic
		if v == "" {
			source = "automatic"
		}
		out.Fields[k] = RepairField{Value: v, Automatic: v, Source: source}
	}
	rows, err := tx.QueryContext(ctx, `SELECT field,value,automatic_value,locked,observed_at FROM metadata_owner_fields WHERE kind=? AND entity_id=? ORDER BY field`, t.Kind, entity)
	if err != nil {
		return out, ent, err
	}
	for rows.Next() {
		var k, v, a, observed string
		var locked bool
		if err = rows.Scan(&k, &v, &a, &locked, &observed); err != nil {
			rows.Close()
			return out, ent, err
		}
		f, ok := out.Fields[k]
		if ok {
			f.Locked = locked
			f.Automatic = a
			f.Observed = observed
			if f.Value == v {
				f.Source = "manual"
			}
			if readingRepairKind(ent.itemKind) && readingAttributionField(k) {
				// The canonical map is the whole automatic value: an omitted
				// field is genuine absence, never the recorded column.
				genuine := ent.readingAutomatic[k]
				f.Automatic = genuine
				if locked && strings.TrimSpace(v) != "" {
					f.Value, f.Source = v, "owner"
				} else {
					f.Value, f.Source = genuine, "automatic"
				}
			}
			out.Fields[k] = f
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, ent, err
	}
	// item_manual_metadata is gone: the manual title/overview live in the
	// title/overview owner fields read above.
	for _, spec := range ent.schema {
		if spec.Type != "list" {
			continue
		}
		f := out.Fields[spec.Field]
		f.Values = decodeList(f.Value)
		out.Fields[spec.Field] = f
	}
	ident := &RepairIdentity{}
	switch t.Kind {
	case "show":
		ident.Provider = "tvdb"
		err = tx.QueryRowContext(ctx, `SELECT CAST(provider_id AS TEXT),episode_order,revision,status FROM tvdb_jobs WHERE show_id=?`, entity).Scan(&ident.ID, &ident.Order, &ident.Revision, &ident.Status)
	case "album":
		ident.Provider = "musicbrainz"
		err = tx.QueryRowContext(ctx, `SELECT selected_id,revision,status,manual FROM mb_jobs WHERE kind='album' AND entity_id=?`, entity).Scan(&ident.ID, &ident.Revision, &ident.Status, &ident.Locked)
	case "item":
		var kind int
		err = tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, entity).Scan(&kind)
		if err != nil {
			return out, ent, err
		}
		if kind == 7 {
			ident.Provider = "musicbrainz"
			err = tx.QueryRowContext(ctx, `SELECT selected_id,revision,status,manual FROM mb_jobs WHERE kind='song' AND entity_id=?`, entity).Scan(&ident.ID, &ident.Revision, &ident.Status, &ident.Locked)
		} else if kind == 1 {
			ident.Provider = "tmdb"
			err = tx.QueryRowContext(ctx, `SELECT CAST(provider_id AS TEXT),revision,locked,status FROM metadata_movie_selections WHERE item_id=?`, entity).Scan(&ident.ID, &ident.Revision, &ident.Locked, &ident.Status)
			if errors.Is(err, sql.ErrNoRows) {
				err = tx.QueryRowContext(ctx, `SELECT CAST(provider_id AS TEXT) FROM provider_evidence WHERE item_id=? AND provider='tmdb'`, entity).Scan(&ident.ID)
				ident.Status = "accepted"
				if errors.Is(err, sql.ErrNoRows) {
					err = nil
					ident.ID = ""
					ident.Status = "unresolved"
				}
			}
		} else {
			err = tx.QueryRowContext(ctx, `SELECT provider,CAST(provider_id AS TEXT) FROM provider_evidence WHERE item_id=? ORDER BY provider LIMIT 1`, entity).Scan(&ident.Provider, &ident.ID)
			ident.Status = "accepted"
		}
	default:
		err = sql.ErrNoRows
	}
	if err == nil {
		out.Identity = ident
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, ent, err
	}
	if screen, e := repairScreenIdentity(ctx, tx, t); e != nil {
		return out, ent, e
	} else if screen != nil {
		out.Identity = screen
	}
	out.RelationshipLocks, err = relationshipLocks(ctx, tx, t)
	if err != nil {
		return out, ent, err
	}
	if out.Identity != nil {
		if locked, ok := out.RelationshipLocks["identity"]; ok {
			out.Identity.Locked = out.Identity.Locked || locked
		}
	}
	if t.Kind == "item" {
		out.CanEditProviderCredits, err = creditOverridesAvailable(ctx, tx, entity)
		if err != nil {
			return out, ent, err
		}
	}
	out.Relationships, err = readRepairRelationships(ctx, tx, t)
	if err != nil {
		return out, ent, err
	}
	// A reading item's cover lives in the same artwork store as every other
	// item's: the publication's own page is adopted there as the automatic
	// choice, and an owner upload replaces it. The selected choices are read the
	// same way for every kind.
	out.Artwork, err = readArtworkChoices(ctx, tx, t)
	return out, ent, err
}
func repairRevision(ctx context.Context, tx *sql.Tx, t RepairTarget, snap RepairSnapshot, ent repairEntity) (string, error) {
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return "", err
	}
	var last int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM metadata_owner_history WHERE kind=? AND entity_id=?`, t.Kind, entity).Scan(&last)
	if err != nil {
		return "", err
	}
	var creditFence []any
	if t.Kind == "item" {
		creditFence, err = creditRepairFence(ctx, tx, entity)
		if err != nil {
			return "", err
		}
	}
	return publicationDigest([]any{t, ent.library, ent.stamp, snap, last, creditFence}), nil
}
func (s *Service) RepairState(ctx context.Context, t RepairTarget, authorize func(*sql.Tx) error) (RepairState, error) {
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return RepairState{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize == nil {
		return RepairState{}, ErrRepairInput
	}
	if err = authorize(tx); err != nil {
		return RepairState{}, err
	}
	out, err := s.readRepairState(ctx, tx, t)
	if err != nil {
		return out, err
	}
	return out, gated.Commit()
}

// readRepairState adds the current explicit thumbnail capability without changing
// automatic-generation policy or granting authority to invoke the command.
func (s *Service) readRepairState(ctx context.Context, tx *sql.Tx, t RepairTarget) (RepairState, error) {
	out, err := readRepairState(ctx, tx, t)
	if err != nil {
		return out, err
	}
	online, _, err := onlineLookupPolicyTx(ctx, tx)
	if err != nil {
		return out, err
	}
	out.OnlineLookups = &online
	out.CanMakeEpisodeThumbnail, err = s.CanMakeEpisodeThumbnailTx(ctx, tx, t)
	if err == nil && out.TargetItemKind == "episode" {
		var show, digest string
		var d artworkref.Descriptor
		e := tx.QueryRowContext(ctx, `SELECT pid(parent.public_id),src.digest,`+artworkref.Columns+` FROM catalog_entities child JOIN catalog_episodes ep ON ep.entity_id=child.id JOIN catalog_shows sh ON sh.entity_id=ep.show_id JOIN catalog_entities parent ON parent.id=sh.entity_id AND parent.library_id=child.library_id JOIN artwork_selections a ON a.kind='show' AND a.entity_id=parent.id AND a.role='backdrop' AND a.subject='' JOIN artwork_sources src ON src.digest=a.digest JOIN artwork_blobs sb ON sb.digest=src.small_blob JOIN artwork_blobs lb ON lb.digest=src.large_blob WHERE child.public_id=pid_blob(?) AND NOT EXISTS(SELECT 1 FROM artwork_selections own JOIN artwork_sources bytes ON bytes.digest=own.digest WHERE own.kind='item' AND own.entity_id=child.id AND own.role IN('still','thumbnail') AND own.subject='')`, t.ID).Scan(append([]any{&show, &digest}, d.ScanTargets()...)...)
		if e == nil {
			out.ArtworkFallbackURL = describedArtworkPath(RepairTarget{Kind: "show", ID: show}, "backdrop", "", digest, d)
		} else if !errors.Is(e, sql.ErrNoRows) {
			err = e
		}
	}

	return out, err
}

func readRepairState(ctx context.Context, tx *sql.Tx, t RepairTarget) (RepairState, error) {
	out := RepairState{Target: t, History: []RepairHistory{}, Candidates: []RepairCandidate{}, Cascades: []RepairCascade{}}
	snap, ent, err := readRepairSnapshot(ctx, tx, t)
	if err != nil {
		return out, err
	}
	out.Snapshot = snap
	out.TargetItemKind = ent.itemKind
	out.Library = ent.library
	out.Schema = ent.schema
	out.Tabs = repairSchemaTabs(ent.schema)
	out.ArtworkRoles = artworkRolesFor(t.Kind, ent.itemKind)
	if out.Revision, err = repairRevision(ctx, tx, t, snap, ent); err != nil {
		return out, err
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,trigger,observed_at FROM metadata_owner_history WHERE kind=? AND entity_id=? ORDER BY id DESC LIMIT 30`, t.Kind, entity)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var h RepairHistory
		if err = rows.Scan(&h.ID, &h.Trigger, &h.Observed); err != nil {
			rows.Close()
			return out, err
		}
		out.History = append(out.History, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if snap.Identity != nil && snap.Identity.Engine == "screen" {
		out.Candidates, err = repairScreenCandidates(ctx, tx, t)
		if err != nil {
			return out, err
		}
	} else if snap.Identity != nil {
		q := `SELECT provider_id,title,artist,observed_at FROM mb_candidates WHERE kind=? AND entity_id=? ORDER BY provider_id LIMIT 25`
		kind := t.Kind
		if kind == "item" {
			kind = "song"
		}
		args := []any{kind, entity}
		if snap.Identity.Provider == "tvdb" && t.Kind == "show" {
			q = `SELECT CAST(provider_id AS TEXT),name,CAST(year AS TEXT),observed_at FROM tvdb_series_candidates WHERE show_id=? ORDER BY provider_id LIMIT 25`
			args = []any{entity}
		}
		if snap.Identity.Provider == "tmdb" {
			q = `SELECT CAST(provider_id AS TEXT),title,CAST(year AS TEXT),observed_at FROM metadata_movie_candidates WHERE item_id=? ORDER BY provider_id LIMIT 25`
			args = []any{entity}
		}
		if snap.Identity.Provider == "musicbrainz" || t.Kind == "show" || snap.Identity.Provider == "tmdb" {
			rows, err = tx.QueryContext(ctx, q, args...)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				c := RepairCandidate{Provider: snap.Identity.Provider}
				if err = rows.Scan(&c.ID, &c.Title, &c.Subtitle, &c.Observed); err != nil {
					rows.Close()
					return out, err
				}
				if n, e := strconv.Atoi(c.Subtitle); e == nil && n > 0 && n <= 9999 {
					c.Year = n
				}
				out.Candidates = append(out.Candidates, c)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
		}
	}
	out.Artwork, err = readArtworkState(ctx, tx, t)
	if err != nil {
		return out, err
	}
	if readingRepairKind(ent.itemKind) {
		// A reading item has no provider hierarchy to cascade into and no
		// automatic acquisition work: its cover is the publication's own page or
		// the owner's upload. The editor therefore shows the selected cover and
		// the candidates it can choose between, and no pending work.
		out.Artwork.Jobs = []ArtworkJob{}
		return out, nil
	}
	out.Cascades, err = readRepairCascades(ctx, tx, t)
	return out, err
}
func recordRepair(ctx context.Context, tx *sql.Tx, t RepairTarget, before RepairSnapshot, base, trigger, actor string) error {
	after, ent, err := readRepairSnapshot(ctx, tx, t)
	if err != nil {
		return err
	}
	revision, err := repairRevision(ctx, tx, t, after, ent)
	if err != nil {
		return err
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	if t.Kind == "item" {
		if err = attachCreditHistorySources(ctx, tx, entity, &before); err != nil {
			return err
		}
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if len(a) > 1<<20 || len(b) > 1<<20 {
		return ErrRepairInput
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES(?,?,?,?,?,?,?,?,?)`, t.Kind, entity, base, revision, trigger, actor, time.Now().UTC().Format(time.RFC3339Nano), string(a), string(b)); err != nil {
		return err
	}
	revision, err = repairRevision(ctx, tx, t, after, ent)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE metadata_owner_history SET revision=? WHERE id=(SELECT MAX(id) FROM metadata_owner_history WHERE kind=? AND entity_id=?)`, revision, t.Kind, entity); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM metadata_owner_history WHERE kind=? AND entity_id=? AND id NOT IN(SELECT id FROM metadata_owner_history WHERE kind=? AND entity_id=? ORDER BY id DESC LIMIT 30)`, t.Kind, entity, t.Kind, entity)
	return err
}
func (s *Service) Repair(ctx context.Context, t RepairTarget, m RepairCommand, actor MBActor, authorize func(*sql.Tx) error) (RepairState, error) {
	if m.EffectiveCredits && (m.Action != "edit_relationships" || m.Role != "credit") {
		return RepairState{}, ErrRepairInput
	}
	if len(m.ExpectedRevision) != 64 || actor.AccountID == "" || authorize == nil {
		return RepairState{}, ErrRepairInput
	}
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return RepairState{}, err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if err = authorize(tx); err != nil {
		return RepairState{}, err
	}
	before, ent, err := readRepairSnapshot(ctx, tx, t)
	if err != nil {
		return RepairState{}, err
	}
	base, err := repairRevision(ctx, tx, t, before, ent)
	if err != nil {
		return RepairState{}, err
	}
	if base != m.ExpectedRevision {
		return RepairState{}, ErrRepairConflict
	}
	if readingRepairKind(ent.itemKind) && m.Action != "edit" && m.Action != "lock_all" && m.Action != "unlock_all" && m.Action != "undo" && m.Action != "select_artwork" && m.Action != "artwork_lock" {
		return RepairState{}, ErrRepairInput
	}
	who := actor.Authority + ":" + actor.AccountID + ":" + actor.ProfileID
	now := s.publicationTime().Format(time.RFC3339Nano)
	switch m.Action {
	case "edit", "lock_all", "unlock_all":
		if m.Action != "edit" {
			if !m.Confirm {
				return RepairState{}, ErrRepairInput
			}
			m.Fields = map[string]RepairFieldEdit{}
			locked := m.Action == "lock_all"
			for k := range before.Fields {
				m.Fields[k] = RepairFieldEdit{Locked: &locked}
			}
		}
		specs := schemaIndex(ent.schema)
		if (len(m.Fields) == 0 && m.Action == "edit") || len(m.Fields) > len(specs) {
			return RepairState{}, ErrRepairInput
		}
		for k, v := range m.Fields {
			f, ok := before.Fields[k]
			spec, known := specs[k]
			if !ok || !known || v.Value == nil && v.Values == nil && v.Locked == nil && !v.Automatic {
				return RepairState{}, ErrRepairInput
			}
			if err = applyRepairField(ctx, tx, t, k, spec, f, v, who, now); err != nil {
				return RepairState{}, err
			}
		}
		if err = syncCatalogAttributes(ctx, tx, t); err != nil {
			return RepairState{}, err
		}
		if m.Action != "edit" {
			locked := m.Action == "lock_all"
			for _, class := range []string{"credit", "genre", "identity"} {
				if err = setRelationshipLock(ctx, tx, t, class, locked, who, now); err != nil {
					return RepairState{}, err
				}
			}
			lockEntity, err := resolveEntity(ctx, tx, t.ID)
			if err != nil {
				return RepairState{}, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE artwork_selections SET locked=?,revision=revision+1 WHERE kind=? AND entity_id=?`, locked, t.Kind, lockEntity); err != nil {
				return RepairState{}, err
			}
		}
	case "relationship_lock":
		if !m.Confirm || m.Locked == nil {
			return RepairState{}, ErrRepairInput
		}
		err = setRelationshipLock(ctx, tx, t, m.Role, *m.Locked, who, now)
	case "edit_relationships":
		if !m.Confirm {
			return RepairState{}, ErrRepairInput
		}
		err = applyRelationshipSetMode(ctx, tx, t, m.Role, m.Relationships, true, who, now, false, m.EffectiveCredits)
	case "search", "retry":
		if before.Identity != nil && before.Identity.Engine == "screen" {
			err = queueScreenRepair(ctx, tx, t, m)
		} else {
			// Only the screen engine takes an owner-supplied title, year and
			// provider. Refuse them elsewhere rather than accepting and ignoring
			// them, which would look like a search that found nothing.
			if m.Query != "" || m.Year != 0 || before.Identity != nil && m.Provider != "" && m.Provider != before.Identity.Provider {
				return RepairState{}, ErrRepairInput
			}
			err = retryRepairProvider(ctx, tx, t, before.Identity, m.Action == "search")
		}
	case "identify":
		if !m.Confirm || before.Identity == nil {
			return RepairState{}, ErrRepairInput
		}
		if err = s.repairIdentifyTx(ctx, tx, t, before.Identity, m, actor); err != nil {
			return RepairState{}, err
		}
	case "undo":
		if !m.Confirm || m.HistoryID <= 0 {
			return RepairState{}, ErrRepairInput
		}
		var raw string
		var undoEntity int64
		if undoEntity, err = resolveEntity(ctx, tx, t.ID); err != nil {
			return RepairState{}, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT before_json FROM metadata_owner_history WHERE id=? AND kind=? AND entity_id=?`, m.HistoryID, t.Kind, undoEntity).Scan(&raw); err != nil {
			return RepairState{}, err
		}
		var saved RepairSnapshot
		if json.Unmarshal([]byte(raw), &saved) != nil {
			return RepairState{}, ErrRepairInput
		}
		specs := schemaIndex(ent.schema)
		for k, v := range saved.Fields {
			f, ok := before.Fields[k]
			spec, known := specs[k]
			if !ok || !known {
				continue
			}
			f.Automatic = v.Automatic
			if err = applyRepairField(ctx, tx, t, k, spec, f, RepairFieldEdit{Value: &v.Value, Locked: &v.Locked}, who, now); err != nil {
				return RepairState{}, err
			}
		}
		if err = syncCatalogAttributes(ctx, tx, t); err != nil {
			return RepairState{}, err
		}
		if saved.Identity != nil && before.Identity != nil && (saved.Identity.ID != before.Identity.ID || saved.Identity.Order != before.Identity.Order || saved.Identity.Provider != before.Identity.Provider || saved.Identity.Type != before.Identity.Type) {
			command := RepairCommand{Provider: saved.Identity.Provider, CandidateID: saved.Identity.ID, Order: saved.Identity.Order, Confirm: true}
			// A retained committed owner snapshot is identity evidence, not a name search.
			if before.Identity.Engine == "screen" {
				if err = restoreScreenIdentity(ctx, tx, t, saved.Identity, actor); err != nil {
					return RepairState{}, err
				}
			} else if saved.Identity.ID == "" || saved.Identity.ID == "0" {
				if err = clearRepairIdentity(ctx, tx, t, saved.Identity); err != nil {
					return RepairState{}, err
				}
			} else {
				if err = restoreRepairCandidate(ctx, tx, t, saved.Identity, now); err != nil {
					return RepairState{}, err
				}
				if err = s.repairIdentifyTx(ctx, tx, t, before.Identity, command, actor); err != nil {
					return RepairState{}, err
				}
			}
		}
		if t.Kind == "item" {
			for _, class := range []string{"credit", "genre"} {
				values := []RepairRelationship{}
				for _, v := range saved.Relationships {
					if v.Kind == class {
						values = append(values, v)
					}
				}
				if err = applyRelationshipSet(ctx, tx, t, class, values, saved.RelationshipLocks[class], who, now, true); err != nil {
					return RepairState{}, err
				}
			}
		}
		identityLocked := saved.RelationshipLocks["identity"]
		if saved.Identity != nil {
			identityLocked = identityLocked || saved.Identity.Locked
		}
		if err = setRelationshipLock(ctx, tx, t, "identity", identityLocked, who, now); err != nil {
			return RepairState{}, err
		}
		if readingRepairKind(ent.itemKind) {
			if len(saved.Artwork) != 0 {
				return RepairState{}, ErrRepairInput
			}
		} else {
			if err = s.verifyRestorableArtwork(ctx, saved.Artwork); err != nil {
				return RepairState{}, err
			}
			if err = restoreArtworkChoices(ctx, tx, t, saved.Artwork, who, now); err != nil {
				return RepairState{}, err
			}
		}
	case "preview_artwork":
		err = queueArtworkPreview(ctx, tx, t, m.CandidateID, who, now)
	case "select_artwork", "repair_assets", "artwork_lock":
		err = s.repairArtworkTx(ctx, tx, t, m, who, now)
	case "cascade":
		if !m.Confirm {
			return RepairState{}, ErrRepairInput
		}
		err = queueRepairCascade(ctx, tx, t, ent.library, base, who, m.Intent, now)
	case "cancel_cascade":
		cancelEntity, err := resolveEntity(ctx, tx, t.ID)
		if err != nil {
			return RepairState{}, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE metadata_repair_cascades SET status='cancelled' WHERE id=? AND kind=? AND entity_id=? AND status='pending'`, m.OperationID, t.Kind, cancelEntity)
	case "discover_artwork":
		discoverEntity, err := resolveEntity(ctx, tx, t.ID)
		if err != nil {
			return RepairState{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO artwork_dirty VALUES(?,?)`, t.Kind, discoverEntity)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE artwork_discovery SET status='pending',attempts=0,next_attempt='',lease='',lease_until='' WHERE kind=? AND entity_id=?`, t.Kind, discoverEntity)
		}
	default:
		return RepairState{}, ErrRepairInput
	}
	if err != nil {
		return RepairState{}, err
	}
	if m.Action != "preview_artwork" && m.Action != "discover_artwork" {
		if err = recordRepair(ctx, tx, t, before, base, m.Action, who); err != nil {
			return RepairState{}, err
		}
	}
	out, err := s.readRepairState(ctx, tx, t)
	if err != nil {
		return out, err
	}
	if err = authorize(tx); err != nil {
		return out, err
	}
	return out, gated2.Commit()
}
func (s *Service) repairIdentifyTx(ctx context.Context, tx *sql.Tx, t RepairTarget, current *RepairIdentity, m RepairCommand, actor MBActor) error {
	if current.Engine == "screen" {
		identifyEntity, err := resolveEntity(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		var provider string
		if err := tx.QueryRowContext(ctx, `SELECT provider FROM screen_metadata_candidates WHERE candidate_key=? AND target_kind=? AND target_id=?`, m.CandidateID, t.Kind, identifyEntity).Scan(&provider); err != nil {
			return ErrRepairInput
		}
		if provider != m.Provider {
			return ErrRepairInput
		}
		return s.selectScreenTx(ctx, tx, t.Kind, t.ID, ScreenSelection{ExpectedRevision: current.Revision, CandidateKey: m.CandidateID, Order: m.Order, Actor: actor})
	}
	if current.Provider != m.Provider {
		return ErrRepairInput
	}
	if m.Provider == "musicbrainz" && (t.Kind == "album" || t.Kind == "item") {
		kind := t.Kind
		if kind == "item" {
			kind = "song"
		}
		return s.selectMusicBrainzTx(tx, kind, t.ID, MBSelection{ExpectedRevision: current.Revision, ProviderID: m.CandidateID, Actor: actor})
	}
	if m.Provider == "tvdb" && t.Kind == "show" {
		n, err := strconv.ParseInt(m.CandidateID, 10, 64)
		if err != nil {
			return ErrRepairInput
		}
		return s.selectTVDBTx(tx, t.ID, TVDBSelection{ExpectedRevision: current.Revision, ProviderID: n, Order: m.Order, Actor: actor})
	}
	// Film identity is chosen through the screen engine above; the legacy TMDB
	// selection queue has no worker (B84).
	return ErrRepairInput // Local books and artists do not imply a remote matcher.
}
func restoreRepairCandidate(ctx context.Context, tx *sql.Tx, t RepairTarget, i *RepairIdentity, now string) error {
	if i.ID == "" || i.ID == "0" {
		return ErrRepairInput
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	if i.Provider == "tmdb" && t.Kind == "item" {
		provider, err := strconv.ParseInt(i.ID, 10, 64)
		if err != nil {
			return ErrRepairInput
		}
		fence, err := artworkFence(ctx, tx, t)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO metadata_movie_candidates VALUES(?,?,'Previously selected movie',0,'{}',?,?) ON CONFLICT(item_id,provider_id) DO UPDATE SET source_fence=excluded.source_fence`, entity, provider, fence, now)
		return err
	}
	if i.Provider == "tvdb" && t.Kind == "show" {
		provider, err := strconv.ParseInt(i.ID, 10, 64)
		if err != nil {
			return ErrRepairInput
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO tvdb_series_candidates(show_id,provider_id,name,year,overview,payload,observed_at) VALUES(?,?,?,0,'','{}',?)`, entity, provider, "Previously selected show", now)
		return err
	}
	if i.Provider == "musicbrainz" {
		kind := t.Kind
		if kind == "item" {
			kind = "song"
		}
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mb_candidates(kind,entity_id,provider_id,entity_type,title,artist,edition,observed_at,query_digest,confidence,decision,payload) VALUES(?,?,?,?,'Previously selected identity','','',?, ?,1,'candidate','{}')`, kind, entity, i.ID, map[string]string{"album": "release", "song": "recording"}[kind], now, publicationDigest(i))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO mb_candidate_reasons(kind,entity_id,provider_id,ordinal,reason) VALUES(?,?,?,0,'owner_restored')`, kind, entity, i.ID)
		return err
	}
	return ErrRepairInput
}
