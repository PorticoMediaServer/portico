package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

type BulkPreviewValue struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}
type BulkPreviewField struct {
	State         string             `json:"state"`
	DistinctCount int64              `json:"distinctCount,omitempty"`
	AtLeast       int64              `json:"atLeast,omitempty"`
	Value         *string            `json:"value,omitempty"`
	Values        []BulkPreviewValue `json:"values"`
	Entries       []BulkPreviewValue `json:"entries"`
	Locked        int64              `json:"locked"`
	Truncated     bool               `json:"truncated"`
}
type BulkPreviewTarget struct {
	Kind             string `json:"kind"`
	ID               string `json:"id"`
	LibraryID        string `json:"libraryId"`
	ExpectedRevision string `json:"expectedRevision"`
}
type BulkMetadataPreview struct {
	Tabs              []string                    `json:"tabs"`
	ServerID          string                      `json:"serverId"`
	ViewerFence       string                      `json:"viewerFence"`
	ContentKind       string                      `json:"contentKind"`
	Kind              string                      `json:"kind"`
	Total             int64                       `json:"total"`
	SelectionRevision string                      `json:"selectionRevision,omitempty"`
	Schema            []RepairFieldSpec           `json:"schema"`
	SingleItemFields  []RepairFieldSpec           `json:"singleItemFields"`
	ExcludedFields    []RepairFieldSpec           `json:"excludedFields"`
	Fields            map[string]BulkPreviewField `json:"fields"`
	Targets           []BulkPreviewTarget         `json:"targets"`
}

const bulkPreviewValues = 64

type bulkPreviewVector struct {
	Backbone    map[string]string `json:"backbone"`
	Details     map[string]string `json:"details"`
	Side        map[string]string `json:"side"`
	Locks       map[string]int    `json:"locks"`
	GenreLocked int               `json:"genreLocked"`
	Genres      []string          `json:"genres"`
}

func bulkPreviewGenres(values []string) []string {
	unique := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !unique[value] {
			unique[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

// BulkPreviewSource rebuilds the live selection and its catalogue/metadata
// fence inside each bounded snapshot. It must be repeatable at After, including
// at exhaustion, so the final validation reads current authority and revisions.
type BulkPreviewSource func(context.Context, *sql.Tx, int64) (catalog.BulkMetadataSelectionPage, error)
type bulkPreviewObservation struct {
	field, value string
	entries      []string
	locked       bool
	count        int64
}
type bulkPreviewPage struct {
	preview      BulkMetadataPreview
	observations []bulkPreviewObservation
}

func previewSchema(contentKind string) (BulkMetadataPreview, error) {
	out := BulkMetadataPreview{ContentKind: contentKind, Schema: []RepairFieldSpec{}, SingleItemFields: []RepairFieldSpec{}, ExcludedFields: []RepairFieldSpec{}, Fields: map[string]BulkPreviewField{}, Targets: []BulkPreviewTarget{}}
	kind, err := compactcatalog.ParseKind(contentKind)
	if err != nil {
		return out, err
	}
	out.Kind = compactcatalog.OwnerNamespace(kind)
	out.SingleItemFields = repairSchema(out.Kind, contentKind)
	for _, spec := range out.SingleItemFields {
		if spec.Bulk {
			out.Schema = append(out.Schema, spec)
		} else {
			out.ExcludedFields = append(out.ExcludedFields, spec)
		}
	}
	if out.Kind == "item" && (contentKind == "movie" || contentKind == "song") {
		out.Schema = append(out.Schema, listSpec("genres", "Genres"))
	}
	out.Tabs = repairSchemaTabs(out.Schema)
	if len(out.Schema) == 0 {
		return out, ErrRepairInput
	}
	return out, nil
}

// BulkPreview is the synchronous service adapter used by internal callers. The
// HTTP boundary runs the same incremental reducer in the shared readprep store.
// No preview path creates a database, TEMP table, or scratch file.
func (s *Service) BulkPreview(ctx context.Context, source BulkPreviewSource, authorize func(context.Context, *sql.Tx) error) (BulkMetadataPreview, error) {
	r, err := newBulkPreviewReduction(s, source, authorize, nil, "")
	if err != nil {
		return BulkMetadataPreview{}, err
	}
	for !r.done {
		if err = r.step(ctx); err != nil {
			return BulkMetadataPreview{}, err
		}
	}
	return r.out, nil
}

func (s *Service) bulkPreviewPageTx(ctx context.Context, tx *sql.Tx, selection catalog.BulkMetadataSelection) (page bulkPreviewPage, err error) {
	if tx == nil || strings.TrimSpace(selection.SQL) == "" || len(selection.Targets) > catalog.BulkMetadataPageSize {
		return page, ErrRepairInput
	}
	rows, err := tx.QueryContext(ctx, `SELECT k.name FROM (`+selection.SQL+`) selected JOIN catalog_entities i ON i.id=selected.id JOIN catalog_kinds k ON k.id=i.kind`, selection.Args...)
	if err != nil {
		return page, err
	}
	var contentKind string
	var total int64
	kinds := 0
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			break
		}
		if kinds == 0 {
			contentKind, kinds = name, 1
		} else if name != contentKind {
			kinds = 2
		}
		total++
		if kinds > 1 || total > catalog.BulkMetadataPageSize {
			err = ErrRepairInput
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return page, err
	}
	if kinds == 0 {
		return page, nil
	}
	if kinds != 1 || total > catalog.BulkMetadataPageSize {
		return page, ErrRepairInput
	}
	out, err := previewSchema(contentKind)
	if err != nil {
		return page, err
	}
	out.Total = total
	explicit := len(selection.Targets) > 0
	targets := selection.Targets
	if !explicit && readingRepairKind(out.ContentKind) {
		// Reading attribution is resolved from the current primary publication/path
		// and locked owner overlay. Reuse its editor reader, rather than fabricating
		// empty author/series values from the generic media vector.
		rows, e := tx.QueryContext(ctx, `SELECT pid(i.public_id),l.library_id FROM (`+selection.SQL+`) selected JOIN catalog_entities i ON i.id=selected.id JOIN catalog_libraries l ON l.id=i.library_id`, selection.Args...)
		if e != nil {
			return page, e
		}
		targets = []catalog.BulkMetadataTarget{}
		for rows.Next() {
			target := catalog.BulkMetadataTarget{Kind: out.Kind, ContentKind: out.ContentKind}
			if err = rows.Scan(&target.ID, &target.LibraryID); err != nil {
				break
			}
			targets = append(targets, target)
		}
		if err == nil {
			err = rows.Err()
		}
		closeErr = rows.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return page, err
		}
	}
	if len(targets) > 0 {
		for _, target := range targets {
			observations, result, e := bulkPreviewTargetTx(ctx, tx, target, out.ContentKind, out.Schema, explicit, nil)
			if e != nil {
				return page, e
			}
			if result != nil {
				out.Targets = append(out.Targets, *result)
			}
			page.observations = append(page.observations, observations...)
		}
	} else {
		vector := `json_object('backbone',json_object('metadata_language',i.metadata_language,'year',CAST(i.year AS TEXT)),
   'details',json_object('edition',COALESCE(d.edition,''),'content_rating',COALESCE(d.content_rating,''),'studio',COALESCE(d.studio,''),'network',COALESCE(d.network,''),'country',COALESCE(d.country,''),'tags',COALESCE(d.tags,''),'labels',COALESCE(d.labels,'')),
   'side',json(CASE k.name WHEN 'show' THEN json_object('content_rating',COALESCE(sh.content_rating,''),'studio',COALESCE(sh.studio,''),'network',COALESCE(sh.network,''),'country',COALESCE(sh.country,''),'tags',COALESCE(sh.tags,''),'labels',COALESCE(sh.labels,''))
   WHEN 'artist' THEN json_object('tags',COALESCE(ar.tags,'')) WHEN 'album' THEN json_object('label',COALESCE(al.label,''),'tags',COALESCE(al.tags,'')) WHEN 'book' THEN json_object('author',COALESCE(bk.author,''),'narrator',COALESCE(bk.narrator,''),'series',COALESCE(bk.series,''),'tags',COALESCE(bk.tags,''),'labels',COALESCE(bk.labels,'')) ELSE json_object() END),
   'locks',json(COALESCE((SELECT json_group_object(field,locked) FROM metadata_owner_fields owner WHERE owner.entity_id=i.id AND owner.kind=?),'{}')),
   'genreLocked',COALESCE((SELECT locked FROM metadata_relationship_decisions lock WHERE lock.entity_id=i.id AND lock.kind=? AND lock.relationship='genre'),0),
   'genres',json(COALESCE((SELECT json_group_array(COALESCE(ts.label_override,t.label)) FROM catalog_entity_terms et JOIN catalog_terms t ON t.id=et.term_id JOIN catalog_term_sources ts ON ts.entity_id=et.entity_id AND ts.term_id=et.term_id WHERE et.entity_id=i.id AND t.vocab=1),'[]')))`
		// SELECT-expression placeholders precede the bounded selection subquery.
		args := append([]any{out.Kind, out.Kind}, selection.Args...)
		vectors := `SELECT ` + vector + ` AS vector FROM (` + selection.SQL + `) selected JOIN catalog_entities i ON i.id=selected.id JOIN catalog_kinds k ON k.id=i.kind LEFT JOIN catalog_item_details d ON d.entity_id=i.id LEFT JOIN catalog_shows sh ON sh.entity_id=i.id LEFT JOIN catalog_artists ar ON ar.entity_id=i.id LEFT JOIN catalog_albums al ON al.entity_id=i.id LEFT JOIN catalog_books bk ON bk.entity_id=i.id`
		grouped, e := tx.QueryContext(ctx, `SELECT vector,1 FROM (`+vectors+`)`, args...)
		if e != nil {
			return page, e
		}
		for grouped.Next() {
			var raw string
			var count int64
			if err = grouped.Scan(&raw, &count); err != nil {
				break
			}
			var value bulkPreviewVector
			if err = json.Unmarshal([]byte(raw), &value); err != nil {
				break
			}
			for _, spec := range out.Schema {
				observation := bulkPreviewObservation{field: spec.Field, count: count}
				if spec.Field == "genres" {
					observation.entries = bulkPreviewGenres(value.Genres)
					observation.value = encodeList(observation.entries)
					observation.locked = value.GenreLocked != 0
				} else {
					observation.value = repairFieldValue(out.Kind, out.ContentKind, spec.Field, value.Backbone, value.Details, value.Side, sql.NullInt64{})
					if spec.Type == "list" {
						observation.entries = decodeList(observation.value)
						observation.value = encodeList(observation.entries)
					}
					observation.locked = value.Locks[spec.Field] != 0
				}
				page.observations = append(page.observations, observation)
			}
		}
		if err == nil {
			err = grouped.Err()
		}
		closeErr = grouped.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return page, err
		}
	}
	page.preview = out
	return page, nil
}
