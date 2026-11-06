package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Finite domain-owned declaration only, never caller SQL, a registration
// service or a capability. HN Capture still verifies every native key/plan.
func enrichmentStagePlanHints(defs []stmt.Definition) []stmt.Definition {
	for i := range defs {
		d := &defs[i]
		if strings.HasPrefix(d.SQL, "INSERT ") {
			d.Plan = stmt.Plan{}
			continue
		}
		table, alias := "", ""
		switch d.ID {
		case enrichmentEvidencePinBuild, enrichmentSourceAdvance, enrichmentSourceSealAdvance, sourceStageAllocate, sealCandidateAdvance:
			table = "metadata_provider_operations"
		case sealObservedOwner, sourceStagePrefixRead:
			table = "metadata_provider_operations"
			alias = "o"
		case stageBuildFacts:
			table = "metadata_evidence_builds"
			alias = "b"
		case evidenceStagePrefixRead, evidenceStageAdvance, enrichmentEvidenceSealAdvance, evidenceSealBuildFinalize:
			table = "metadata_evidence_builds"
		case stageEvidenceProofFacts, evidenceSealBodyAttach:
			table = "metadata_evidence"
		case sourceSealBodyAttach:
			table = "credit_source_publications"
		case evidenceStageAccountingDelta:
			table = "metadata_evidence_accounting"
		case sealCandidateParent:
			table = "metadata_candidates"
		case evidenceStageTotalsDelta:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_totals"}}
			continue
		case sourceSealUpstreamPin:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}
			continue
		case sealCandidateReasons, sealCandidateReasonRead:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_candidate_reasons"}}
			continue
		case sealCandidateAliases, sealCandidateAliasRead:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_candidate_aliases"}}
			continue
		case sealCandidateReady:
			d.Plan = stmt.Plan{Hot: true, Indexes: []string{"metadata_candidate_operation"}}
			continue
		default:
			continue
		}
		key := "id"
		if table == "metadata_evidence_accounting" {
			key = "evidence_id"
		}
		if table == "credit_source_publications" {
			key = "publication_id"
		}
		index := "sqlite_autoindex_" + table + "_1"
		d.Plan = stmt.Plan{Hot: true, Indexes: []string{index}, AutoIndexes: []stmt.AutoIndexBinding{{Table: table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: key, Collation: "BINARY"}}}}}
		needle := "FROM " + table
		if strings.HasPrefix(d.SQL, "UPDATE ") {
			needle = "UPDATE " + table
		} else if strings.HasPrefix(d.SQL, "DELETE ") {
			needle = "FROM " + table
		}
		if alias != "" {
			needle += " AS " + alias
		}
		d.SQL = strings.Replace(d.SQL, needle, needle+" INDEXED BY "+index, 1)
	}
	return defs
}
