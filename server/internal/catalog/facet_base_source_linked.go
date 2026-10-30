package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// LinkedDecadeFacetBaseSource preserves the published-class predicate separately
// from live SIMPLE decade. It builds a NEW disclosed exact accepted content
// basis; it does not mislabel mutable/trim-keyed legacy class rows as COMPLETE.
const LinkedDecadeFacetBaseSource = "simple-restricted-linked-decade-v1"

type linkedDecadeCursor struct {
	HasAfter                  bool `json:"hasAfter"`
	Entity, Item              int64
	Source                    string
	HasGroup                  bool
	Group                     int64
	Value                     string
	Eligible, Linked, Visible bool
	Pending                   bool
	Policy                    FacetBaseItemPolicyCursor
}

func CaptureLinkedDecadeFacetBaseWitness(ctx context.Context, q compactcatalog.ReadQuery, scope FacetBaseScope) (string, error) {
	if !scope.valid() || scope.Field != "decade" || scope.Source != LinkedDecadeFacetBaseSource {
		return "", ErrFacetBaseInput
	}
	r, err := parseFacetBasePolicy(scope.Policy)
	if err != nil {
		return "", err
	}
	if !r.Active() {
		return "", ErrFacetBaseUnavailable
	}
	var kind string
	if err = q.QueryRowContext(ctx, `SELECT l.kind FROM catalog_libraries c JOIN libraries l ON l.id=c.library_id WHERE c.id=? AND c.library_id=? AND c.retired=0`, scope.LibraryID, scope.Library).Scan(&kind); err != nil {
		return "", err
	}
	if isReadingLibrary(kind) {
		return "", ErrFacetBaseUnavailable
	}
	// Require the published dispatch branch to exist. The legacy key is ONLY a
	// branch selector: neither its trimmed policy nor its rows decide membership.
	if err = rejectPublishedFacetDecade(ctx, q, scope, r); !errors.Is(err, ErrFacetBaseUnavailable) {
		if err == nil {
			err = ErrFacetBaseUnavailable
		}
		return "", err
	}
	fence, err := compactcatalog.CaptureFacetProducerFence(ctx, q)
	if err != nil {
		return "", err
	}
	if err = linkedFacetClassSourceReady(ctx, q, scope, r, fence.VisibilitySequence); err != nil {
		return "", err
	}
	raw, err := json.Marshal(restrictedFacetWitness{Version: 1, Kind: kind, Fence: fence})
	return string(raw), err
}

type LinkedDecadeFacetBaseProducer struct{}

func (p LinkedDecadeFacetBaseProducer) validate(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot) (restrictedFacetWitness, error) {
	var witness restrictedFacetWitness
	if s.Scope.Source != LinkedDecadeFacetBaseSource || s.Scope.Field != "decade" || json.Unmarshal([]byte(s.Witness), &witness) != nil || witness.Version != 1 {
		return witness, ErrFacetBaseInput
	}
	r, err := parseFacetBasePolicy(s.Scope.Policy)
	if err != nil {
		return witness, err
	}
	if !r.Active() {
		return witness, ErrFacetBaseUnavailable
	}
	if err = compactcatalog.ValidateFacetProducerFence(ctx, tx, witness.Fence); err != nil {
		return witness, err
	}
	if err = rejectPublishedFacetDecade(ctx, tx, s.Scope, r); errors.Is(err, ErrFacetBaseUnavailable) {
		return witness, linkedFacetClassSourceReady(ctx, tx, s.Scope, r, witness.Fence.VisibilitySequence)
	}
	if err == nil {
		err = ErrFacetBaseUnavailable
	}
	return witness, err
}
func (p LinkedDecadeFacetBaseProducer) ValidatePublication(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot) error {
	_, err := p.validate(ctx, tx, s)
	return err
}
func (p LinkedDecadeFacetBaseProducer) Next(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot, cursor string) (FacetBaseBatch, error) {
	w, err := p.validate(ctx, tx, s)
	if err != nil {
		return FacetBaseBatch{}, err
	}
	r, err := parseFacetBasePolicy(s.Scope.Policy)
	if err != nil {
		return FacetBaseBatch{}, err
	}
	var c linkedDecadeCursor
	if cursor != "" && json.Unmarshal([]byte(cursor), &c) != nil {
		return FacetBaseBatch{}, ErrFacetBaseInput
	}
	out := FacetBaseBatch{}
	if c.Pending {
		done, visible, err := StepFacetBaseItemPolicy(ctx, tx, r, &c.Policy)
		if err != nil {
			return out, err
		}
		if done {
			c.Pending = false
			c.Visible = c.Visible || visible
		}
	} else {
		q := `SELECT entity_id,item_id,source FROM catalog_browse_memberships WHERE entity_id>=-9223372036854775808`
		args := []any{}
		if c.HasAfter {
			q += ` AND (entity_id,item_id,source)>(?,?,?)`
			args = append(args, c.Entity, c.Item, c.Source)
		}
		q += ` ORDER BY entity_id,item_id,source LIMIT 1`
		var entity, item int64
		var source string
		err = tx.QueryRowContext(ctx, q, args...).Scan(&entity, &item, &source)
		end := errors.Is(err, sql.ErrNoRows)
		if err != nil && !end {
			return out, err
		}
		if c.HasGroup && (end || entity != c.Group) {
			if c.Eligible && c.Visible {
				f, err := incrementFacetBaseFact(ctx, tx, s, c.Value, 1)
				if err != nil {
					return out, err
				}
				out.Facts = []FacetBaseFact{f}
			}
			c.HasGroup = false
			c.Visible = false
			c.Eligible = false
			if end {
				out.Done = true
			}
			// The next entity's raw membership was not consumed; no hidden DISTINCT.
		} else if end {
			out.Done = true
		} else {
			if !c.HasGroup {
				c.HasGroup = true
				c.Group = entity
				var library int64
				var kind int
				var year sql.NullInt64
				var retired bool
				err = tx.QueryRowContext(ctx, `SELECT e.library_id,e.kind,e.year,ce.retired,
 EXISTS(SELECT 1 FROM catalog_shows WHERE entity_id=e.entity_id) OR EXISTS(SELECT 1 FROM catalog_seasons WHERE entity_id=e.entity_id)
 FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.entity_id=?`, entity).Scan(&library, &kind, &year, &retired, &c.Linked)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return out, err
				}
				allowedKind := kind == int(compactcatalog.Movie) || kind == int(compactcatalog.Episode) || kind == int(compactcatalog.Track) || kind == int(compactcatalog.Part) || kind == int(compactcatalog.Extra)
				if w.Kind == "tv" || w.Kind == "anime" {
					allowedKind = kind == int(compactcatalog.Show)
				}
				c.Eligible = err == nil && library == s.Scope.LibraryID && allowedKind && !retired && year.Valid && year.Int64 >= 1800 && year.Int64 <= 2199
				if c.Eligible {
					c.Value = strconv.FormatInt(year.Int64/10*10, 10)
				}
			}
			c.HasAfter = true
			c.Entity, c.Item, c.Source = entity, item, source
			if c.Eligible && !c.Visible {
				linked := true
				if c.Linked {
					err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_asset_links WHERE entity_id=?)`, item).Scan(&linked)
					if err != nil {
						return out, err
					}
				}
				if linked {
					c.Pending = true
					c.Policy = FacetBaseItemPolicyCursor{Item: item}
				}
			}
		}
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		return out, err
	}
	out.Cursor = string(encoded)
	return out, nil
}

// The published class selects this branch. Its captured source watermark must
// cover the producer's accepted frontier, even though membership is reduced
// directly from raw facts rather than read from legacy class rows.
func linkedFacetClassSourceReady(ctx context.Context, q compactcatalog.ReadQuery, scope FacetBaseScope, r identity.ContentRestrictions, target int64) error {
	key, _ := visibilityClassKey(scope.Library, r)
	var ready bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM compact_visibility_classes c WHERE c.class_key=? AND c.library_id=? AND c.retired=0 AND c.active_generation>0 AND c.captured_sequence>=?)`, key, scope.LibraryID, target).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return compactcatalog.ErrBuilding
	}
	return nil
}
