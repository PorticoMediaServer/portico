package plan

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const rsAlbumHeaderOctetsSQL = `SELECT octet_length(p.provider_id),octet_length(p.requested_id),octet_length(ev.release_group_id),octet_length(ev.observed_at),octet_length(ev.primary_type),octet_length(ev.id)
FROM catalog_entities AS e NOT INDEXED
CROSS JOIN metadata_subject_providers AS p INDEXED BY sqlite_autoindex_metadata_subject_providers_1 ON p.entity_id=e.id
CROSS JOIN metadata_publications AS pub INDEXED BY sqlite_autoindex_metadata_publications_1 ON pub.id=p.accepted_publication_id
CROSS JOIN metadata_evidence AS ev INDEXED BY sqlite_autoindex_metadata_evidence_1 ON ev.id=p.accepted_evidence_id
CROSS JOIN metadata_provider_operations AS op INDEXED BY sqlite_autoindex_metadata_provider_operations_1 ON op.id=pub.operation_id
CROSS JOIN metadata_provider_policy AS pol INDEXED BY sqlite_autoindex_metadata_provider_policy_1 ON pol.library_id=e.library_id AND pol.provider=p.provider
CROSS JOIN metadata_providers AS pr ON pr.code=p.provider
CROSS JOIN metadata_provider_subjects AS subj INDEXED BY sqlite_autoindex_metadata_provider_subjects_1 ON subj.provider=ev.provider
CROSS JOIN enrichment_command_receipts AS proof INDEXED BY enrichment_seal_subject_namespace ON proof.provider_operation_id=ev.seal_proof_owner AND proof.command_incarnation=ev.seal_proof_incarnation AND proof.command_sequence=ev.seal_proof_sequence
WHERE e.id=? AND e.library_id=? AND e.kind=6 AND e.retired=0 AND p.provider='musicbrainz' AND p.role='primary' AND p.status='selected' AND p.subject_kind='release' AND p.accepted_publication_id IS NOT NULL AND p.accepted_evidence_id IS NOT NULL
AND pub.decision='accepted' AND pub.entity_id=e.id AND pub.provider=p.provider AND pub.subject_kind=p.subject_kind AND pub.provider_id=p.provider_id AND pub.evidence_id=ev.id AND pub.selection_revision=p.revision
AND ev.sealed=1 AND ev.provider=p.provider AND ev.subject_kind=p.subject_kind AND ev.provider_id=p.provider_id AND ev.parser_version='musicbrainz.release.header-complete-children.v1' AND ev.request_shape='musicbrainz.release.detail+artist-credits+recordings+release-groups.v1'
AND pub.observed_at=ev.observed_at AND p.observed_at=ev.observed_at
AND op.provider=p.provider AND op.entity_id=e.id AND op.status='applied' AND pub.generation=op.generation
AND op.entity_incarnation=e.metadata_incarnation AND op.source_revision=e.source_revision AND op.identity_revision=e.identity_revision
AND pr.enabled=1 AND pol.enabled=1 AND op.policy_incarnation=pol.incarnation AND op.policy_revision=pol.revision AND op.algorithm=pol.algorithm AND op.refresh_mode=pol.refresh_mode
AND coalesce(subj.id,0)=ev.subject_id AND subj.provider=ev.provider AND subj.subject_kind=ev.subject_kind AND subj.provider_id=ev.provider_id AND subj.language=ev.language AND subj.region=ev.region AND subj.request_shape=ev.request_shape AND ev.region=pol.region
AND op.source_evidence_id=ev.id AND op.source_subject_id=ev.subject_id AND op.source_parser_version=ev.parser_version AND op.source_evidence_owner=ev.seal_proof_owner AND op.source_evidence_incarnation=ev.seal_proof_incarnation AND op.source_evidence_sequence=ev.seal_proof_sequence
AND proof.command_kind='evidence_seal' AND proof.seal_version=1 AND proof.seal_evidence_id=ev.id AND proof.seal_subject_id=ev.subject_id AND proof.seal_parser_version=ev.parser_version AND proof.seal_provider=ev.provider AND proof.seal_subject_kind=ev.subject_kind AND proof.seal_provider_id=ev.provider_id AND proof.seal_language=ev.language AND proof.seal_region=ev.region AND proof.seal_request_shape=ev.request_shape AND proof.seal_observation_digest=ev.digest
AND proof.seal_algorithm='portico.normalized-evidence.sha256.v1' AND op.source_algorithm=proof.seal_algorithm AND op.source_version=proof.seal_version AND op.source_expected_commitment=proof.seal_commitment AND op.source_expected_members=proof.seal_verified_rows AND op.source_expected_bytes=proof.seal_canonical_bytes AND op.source_content_revision=proof.seal_content_revision AND op.source_owner_generation=proof.seal_owner_generation`

const rsAlbumTypeOctetsSQL = `SELECT octet_length(types.value) FROM metadata_evidence_terms AS types WHERE types.evidence_id=? AND types.vocabulary='release_group_secondary_type' AND types.ordinal=?`

func init() {
	for _, v := range []struct {
		id, sql string
		term    bool
	}{{"read.browse.rs.album.provider.header_octets", rsAlbumHeaderOctetsSQL, false}, {"read.browse.rs.album.provider.type_octets", rsAlbumTypeOctetsSQL, true}} {
		shape := v
		pointOctets.add(octetShape{stmt.ID(shape.id), shape.sql, "RS album requires an exact selected source or sealed evidence point", func(d stmt.Definition) bool {
			if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 2 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
				return false
			}
			if shape.term {
				return d.Params[0].Type == stmt.Text && d.Params[1].Type == stmt.Integer && !d.Params[0].Nullable && !d.Params[1].Nullable && len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == "types" && len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0
			}
			if len(d.Plan.PrimaryKeys) != 2 || len(d.Plan.Indexes) != 7 || len(d.Plan.AutoIndexes) != 6 {
				return false
			}
			for _, p := range d.Params {
				if p.Type != stmt.Integer || p.Nullable {
					return false
				}
			}
			return true
		}, auditRSDetailOctetPoint, func(rows Rows) error {
			if shape.term {
				if len(rows.Values) != 1 || len(rows.Values[0]) != 4 || rows.Values[0][3] != "SEARCH types USING PRIMARY KEY (evidence_id=? AND vocabulary=? AND ordinal=?)" {
					return fmt.Errorf("RS album: exact sealed type point required")
				}
				return nil
			}
			if len(rows.Values) != 9 {
				return fmt.Errorf("RS album: exact source point count required")
			}
			for _, row := range rows.Values {
				if len(row) != 4 {
					return fmt.Errorf("RS album: invalid point EQP")
				}
				text, ok := row[3].(string)
				if !ok || !strings.HasPrefix(text, "SEARCH ") {
					return fmt.Errorf("RS album: source must remain SEARCH-only")
				}
			}
			if rows.Values[0][3] != "SEARCH e USING INTEGER PRIMARY KEY (rowid=?)" {
				return fmt.Errorf("RS album: exact album root required")
			}
			return nil
		}})
	}
}
