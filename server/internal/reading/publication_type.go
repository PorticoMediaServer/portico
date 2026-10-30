package reading

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Root20: only recognized explicit fields classify a publication. Generic serial,
// cadence, dates, folder/title words, MIME and subjects are not type evidence.
func explicitPublicationType(values []string) string {
	accepted := ""
	for _, raw := range values {
		value := strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return r + ('a' - 'A')
			}
			return r
		}, strings.TrimSpace(raw))
		if !compactcatalog.ValidReadingPublicationType(value) {
			continue
		}
		if accepted != "" && accepted != value {
			return ""
		}
		accepted = value
	}
	return accepted
}

// File facts are independent of owner rows. Adoption checks the original current
// source and principal again in the write transaction; unknown never becomes Book.
func boundedTypeEvidence(values []string) ([]string, bool) {
	if len(values) > 64 {
		return []string{}, false
	}
	out := make([]string, len(values))
	for i, value := range values {
		if len(value) > 4096 {
			return []string{}, false
		}
		out[i] = value
	}
	return out, true
}

func (s *Service) adoptPublicationType(ctx context.Context, p identity.Principal, selected Selection, facts Metadata) error {
	if selected.Format != "epub" && selected.Format != "cbz" {
		return nil
	}
	value := facts.ExplicitPublicationType
	if value != "" && !compactcatalog.ValidReadingPublicationType(value) {
		return ErrInvalid
	}
	gate, err := dbwork.Begin(ctx, s.DB, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gate.Rollback()
	current, err := s.selectTx(ctx, gate.Tx(), p, selected.ItemID, selected.SourceID)
	if err != nil {
		return err
	}
	if current.Revision != selected.Revision || current.object != selected.object || current.inventory != selected.inventory || current.inventoryRow != selected.inventoryRow {
		return ErrChanged
	}
	raw, complete := boundedTypeEvidence(facts.PublicationTypeRaw)
	complete = complete && facts.PublicationTypeComplete
	if !complete {
		raw = []string{}
		value = ""
	}
	evidence, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	// JSON escaping can exceed the raw scalar bounds. Withhold classification
	// rather than accept evidence whose complete original value cannot be stored.
	if len(evidence) > 262144 {
		evidence = []byte("[]")
		complete = false
		value = ""
	}
	field := facts.PublicationTypeField
	if field == "" {
		if selected.Format == "epub" {
			field = "{http://purl.org/dc/elements/1.1/}type"
		} else {
			field = "ComicInfo/Format"
		}
	}
	var normalized any
	if value != "" {
		normalized = value
	}
	_, err = gate.Tx().ExecContext(ctx, `INSERT INTO reading_publication_types(item_id,asset_id,publication_type,source_format,source_field,source_raw_json,evidence_complete,inventory_object_id,source_revision,classifier_version)
      VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(item_id,asset_id) DO UPDATE SET publication_type=excluded.publication_type,source_format=excluded.source_format,source_field=excluded.source_field,source_raw_json=excluded.source_raw_json,evidence_complete=excluded.evidence_complete,inventory_object_id=excluded.inventory_object_id,source_revision=excluded.source_revision,classifier_version=excluded.classifier_version
      WHERE reading_publication_types.publication_type IS NOT excluded.publication_type OR reading_publication_types.source_format<>excluded.source_format OR reading_publication_types.source_field<>excluded.source_field OR reading_publication_types.source_raw_json<>excluded.source_raw_json OR reading_publication_types.evidence_complete<>excluded.evidence_complete OR reading_publication_types.inventory_object_id<>excluded.inventory_object_id OR reading_publication_types.source_revision<>excluded.source_revision OR reading_publication_types.classifier_version<>excluded.classifier_version`, current.Entity, current.Asset, normalized, current.Format, field, string(evidence), complete, current.inventoryRow, current.inventory, compactcatalog.ReadingPublicationTypeRuleVersion)

	if err != nil {
		return err
	}
	return gate.Commit()
}
