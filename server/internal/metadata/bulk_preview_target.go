package metadata

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// bulkPreviewTargetTx shares the editor snapshot/observation reader. An optional
// revision reader can preflight the snapshot and CAS inputs before serialization.
func bulkPreviewTargetTx(ctx context.Context, tx *sql.Tx, target catalog.BulkMetadataTarget, contentKind string, schema []RepairFieldSpec, explicit bool,
	revisionReader func(context.Context, *sql.Tx, RepairTarget, RepairSnapshot, repairEntity) (string, error),
) (observations []bulkPreviewObservation, result *BulkPreviewTarget, err error) {
	repair := RepairTarget{Kind: target.Kind, ID: target.ID}
	snapshot, entity, e := readRepairSnapshot(ctx, tx, repair)
	if e != nil {
		return nil, nil, e
	}

	if entity.library != target.LibraryID || entity.itemKind != contentKind {
		return nil, nil, ErrRepairInput
	}
	if explicit {
		if revisionReader == nil {
			revisionReader = repairRevision
		}
		revision, e := revisionReader(ctx, tx, repair, snapshot, entity)
		if e != nil {
			return nil, nil, e
		}
		result = &BulkPreviewTarget{target.Kind, target.ID, target.LibraryID, revision}
	}
	for _, spec := range schema {
		observation := bulkPreviewObservation{field: spec.Field, count: 1}
		if spec.Field == "genres" {
			genres := []string{}
			for _, relationship := range snapshot.Relationships {
				if relationship.Kind == "genre" {
					genres = append(genres, relationship.Label)
				}
			}
			observation.entries = bulkPreviewGenres(genres)
			observation.value = encodeList(observation.entries)
			observation.locked = snapshot.RelationshipLocks["genre"]
		} else {
			field, ok := snapshot.Fields[spec.Field]
			if !ok {
				return nil, nil, ErrRepairInput
			}
			observation.value, observation.entries, observation.locked = field.Value, field.Values, field.Locked
		}
		observations = append(observations, observation)
	}
	return observations, result, nil
}
