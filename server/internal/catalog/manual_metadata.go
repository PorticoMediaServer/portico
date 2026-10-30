package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrManualMetadataConflict = errors.New("These values changed. Load the latest values before saving again.")
var ErrManualMetadataProjection = errors.New("Editable metadata exceeds the supported read limits.")

var ErrManualMetadataInput = requestinput.New("Check the metadata fields and try again.")

type MetadataEditValue struct {
	Present bool
	Value   *string
}

func (v *MetadataEditValue) UnmarshalJSON(raw []byte) error {
	v.Present = true
	return json.Unmarshal(raw, &v.Value)
}

type ManualMetadataMutation struct {
	ExpectedRevision string            `json:"expectedRevision"`
	Title            MetadataEditValue `json:"title"`
	Description      MetadataEditValue `json:"description"`
}
type ManualMetadataField struct {
	Value          string `json:"value"`
	AutomaticValue string `json:"automaticValue"`
	Manual         bool   `json:"manual"`
}
type ManualMetadata struct {
	Scope       DetailScope         `json:"scope"`
	Revision    string              `json:"revision"`
	Kind        string              `json:"kind"`
	Title       ManualMetadataField `json:"title"`
	Description ManualMetadataField `json:"description"`
	UpdatedAt   string              `json:"updatedAt,omitempty"`
}

func editableMetadataKind(kind string) bool {
	return kind == "movie" || kind == "episode" || kind == "song" || kind == "audiobook_file"
}

// The manual title and description are owner fields ('title', 'overview'): a
// locked metadata_owner_fields row keeps the owner's value and records what the
// scanner or provider would say as its automatic value (compactcatalog applies
// the lock to every automatic write).
const manualMetadataQuery = `SELECT cl.library_id,k.name,e.title,COALESCE(d.overview,''),
 COALESCE((SELECT automatic_value FROM metadata_owner_fields WHERE kind='item' AND entity_id=e.id AND field='title' AND locked=1),e.title),
 COALESCE((SELECT automatic_value FROM metadata_owner_fields WHERE kind='item' AND entity_id=e.id AND field='overview' AND locked=1),COALESCE(d.overview,'')),
 (SELECT value FROM metadata_owner_fields WHERE kind='item' AND entity_id=e.id AND field='title' AND locked=1),
 (SELECT value FROM metadata_owner_fields WHERE kind='item' AND entity_id=e.id AND field='overview' AND locked=1),
 COALESCE((SELECT max(observed_at) FROM metadata_owner_fields WHERE kind='item' AND entity_id=e.id AND field IN('title','overview')),'')
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind
 LEFT JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.public_id=pid_blob(?)`

func readManualMetadata(ctx context.Context, tx *sql.Tx, server, fence, item string) (ManualMetadata, error) {
	out := ManualMetadata{Scope: DetailScope{ServerID: server, ItemID: item, ViewerFence: fence}}
	if _, ok := entityid.Decode(item); !ok {
		return out, sql.ErrNoRows
	}
	var title, description sql.NullString
	err := tx.QueryRowContext(ctx, manualMetadataQuery, item).Scan(&out.Scope.LibraryID, &out.Kind, &out.Title.Value, &out.Description.Value, &out.Title.AutomaticValue, &out.Description.AutomaticValue, &title, &description, &out.UpdatedAt)
	if err != nil {
		return out, err
	}
	if !editableMetadataKind(out.Kind) {
		return out, ErrManualMetadataInput
	}
	if !validMetadataSource(out.Title.Value, true) || !validMetadataSource(out.Title.AutomaticValue, true) || !validMetadataSource(out.Description.Value, false) || !validMetadataSource(out.Description.AutomaticValue, false) {
		return out, ErrManualMetadataProjection
	}
	out.Title.Manual = title.Valid
	out.Description.Manual = description.Valid
	raw, _ := json.Marshal([]any{item, out.Scope.LibraryID, out.Kind, out.Title, out.Description, out.UpdatedAt})
	out.Revision = fmt.Sprintf("%x", sha256.Sum256(raw))
	return out, nil
}
func (s *Service) ManualMetadata(ctx context.Context, server, fence, item string, authorize func(*sql.Tx) error) (ManualMetadata, error) {
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return ManualMetadata{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize == nil {
		return ManualMetadata{}, ErrManualMetadataInput
	}
	if err = authorize(tx); err != nil {
		return ManualMetadata{}, err
	}
	out, err := readManualMetadata(ctx, tx, server, fence, item)
	if err != nil {
		return out, err
	}
	return out, gated.Commit()
}

// Read limits follow the existing Detail DTO's UTF-16 string bounds, independently
// of the smaller manual mutation limits. Source values are never truncated.
func validMetadataSource(value string, title bool) bool {
	if !utf8.ValidString(value) {
		return false
	}
	limit := 65536
	if title {
		limit = 2048
	}
	units := 0
	for _, r := range value {
		units++
		if r > 0xffff {
			units++
		}
		if units > limit || r == 0x7f || r < 0x20 && !(!title && (r == '\n' || r == '\r' || r == '\t')) {
			return false
		}
	}
	return true
}
func validManualText(v MetadataEditValue, title bool) bool {
	if !v.Present || v.Value == nil {
		return true
	}
	text := *v.Value
	limit := 20000
	if title {
		limit = 300
		if strings.TrimSpace(text) == "" {
			return false
		}
	}
	return utf8.ValidString(text) && utf8.RuneCountInString(text) <= limit && strings.IndexFunc(text, func(r rune) bool { return unicode.IsControl(r) && !(!title && (r == '\n' || r == '\r' || r == '\t')) }) < 0
}
func (s *Service) SaveManualMetadata(ctx context.Context, server, fence, item, actor string, m ManualMetadataMutation, authorize func(*sql.Tx) error) (ManualMetadata, error) {
	if actor == "" || len(m.ExpectedRevision) != 64 || !m.Title.Present && !m.Description.Present || !validManualText(m.Title, true) || !validManualText(m.Description, false) {
		return ManualMetadata{}, ErrManualMetadataInput
	}
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return ManualMetadata{}, err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if authorize == nil {
		return ManualMetadata{}, ErrManualMetadataInput
	}
	if err = authorize(tx); err != nil {
		return ManualMetadata{}, err
	}
	before, err := readManualMetadata(ctx, tx, server, fence, item)
	if err != nil {
		return before, err
	}
	if before.Revision != m.ExpectedRevision {
		return ManualMetadata{}, ErrManualMetadataConflict
	}
	var finish func() error
	if s.OwnerMetadataRevision != nil {
		finish, err = s.OwnerMetadataRevision(ctx, tx, item, actor)
		if err != nil {
			return ManualMetadata{}, err
		}
	}
	entity, err := entityid.Resolve(ctx, tx, item)
	if err != nil {
		return ManualMetadata{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, f := range []struct {
		field     string
		edit      MetadataEditValue
		automatic string
	}{{"title", m.Title, before.Title.AutomaticValue}, {"overview", m.Description, before.Description.AutomaticValue}} {
		if !f.edit.Present {
			continue
		}
		if f.edit.Value == nil {
			// Clearing the override hands the field back to its automatic value.
			if _, err = tx.ExecContext(ctx, `DELETE FROM metadata_owner_fields WHERE kind='item' AND entity_id=? AND field=?`, entity, f.field); err != nil {
				return ManualMetadata{}, err
			}
			if err = compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Automatic, map[string]any{f.field: f.automatic}); err != nil {
				return ManualMetadata{}, err
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,?,?,?,1,?,?)
 ON CONFLICT(kind,entity_id,field) DO UPDATE SET value=excluded.value,locked=1,actor=excluded.actor,observed_at=excluded.observed_at`, entity, f.field, *f.edit.Value, f.automatic, actor, now); err != nil {
			return ManualMetadata{}, err
		}
		if err = compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Owner, map[string]any{f.field: *f.edit.Value}); err != nil {
			return ManualMetadata{}, err
		}
	}
	if finish != nil {
		if err = finish(); err != nil {
			return ManualMetadata{}, err
		}
	}
	if err = authorize(tx); err != nil {
		return ManualMetadata{}, err
	}
	out, err := readManualMetadata(ctx, tx, server, fence, item)
	if err != nil {
		return out, err
	}
	return out, gated2.Commit()
}
