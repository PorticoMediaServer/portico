package writer

import (
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	ipADOwner            stmt.ID = "ip.artwork_discovery.owner"
	ipADReceipt          stmt.ID = "ip.artwork_discovery.receipt"
	ipADFreshOwner       stmt.ID = "ip.artwork_discovery.fresh_owner"
	ipADEntity           stmt.ID = "ip.artwork_discovery.entity"
	ipADPerson           stmt.ID = "ip.artwork_discovery.person"
	ipADPolicy           stmt.ID = "ip.artwork_discovery.policy"
	ipADStage            stmt.ID = "ip.artwork_discovery.stage"
	ipADBegin            stmt.ID = "ip.artwork_discovery.begin"
	ipADAppend           stmt.ID = "ip.artwork_discovery.append"
	ipADAdvance          stmt.ID = "ip.artwork_discovery.advance"
	ipADKeys             stmt.ID = "ip.artwork_discovery.keys"
	ipADOctets           stmt.ID = "ip.artwork_discovery.octets"
	ipADCandidate        stmt.ID = "ip.artwork_discovery.candidate"
	ipADFinish           stmt.ID = "ip.artwork_discovery.finish"
	ipADOwnerCAS         stmt.ID = "ip.artwork_discovery.owner_cas"
	ipADSave             stmt.ID = "ip.artwork_discovery.save"
	ipADPin              stmt.ID = "ip.artwork_discovery.pin"
	ipADDelete           stmt.ID = "ip.artwork_discovery.delete"
	ipADMaintenanceOwner stmt.ID = "ip.artwork_discovery.maintenance_owner"
	ipADMaintenanceCAS   stmt.ID = "ip.artwork_discovery.maintenance_cas"
	ipADReclaim          stmt.ID = "ip.artwork_discovery.reclaim"
)

// Inert finite catalogue. W composes stmt.New with HN Capture receipts and its
// complete purpose recipe; no production registry or caller SQL is accepted.
func ipArtworkDiscoveryDefinitions() []stmt.Definition {
	d := func(id stmt.ID, k stmt.Kind, sql, types string) stmt.Definition {
		v := stmt.Definition{ID: id, Kind: k, SQL: sql, Owner: "IP artwork discovery", CallSites: []string{"store/writer/ip_artwork_discovery_native.go"}}
		var bindings []any
		for n, c := range types {
			p := stmt.Param{Name: fmt.Sprintf("p%d", n+1)}
			switch c {
			case 'i':
				p.Type = stmt.Integer
				bindings = append(bindings, int64(1))
			case 's':
				p.Type = stmt.Text
				bindings = append(bindings, "en")
			case 'b':
				p.Type = stmt.Blob
				bindings = append(bindings, make([]byte, 16))
			case 'r':
				p.Type = stmt.Real
				bindings = append(bindings, float64(1))
			}
			v.Params = append(v.Params, p)
		}
		v.Cohorts = []stmt.Cohort{{Name: "draft_point_or_first_keyset", Bindings: bindings}}
		return v
	}
	defs := []stmt.Definition{
		d(ipADOwner, stmt.Read, `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM artwork_subject_jobs WHERE id=?`, "i"),
		d(ipADReceipt, stmt.Read, `SELECT operation_id,request_digest,result_digest,
CASE command_kind WHEN 'discovery_claim' THEN 220 WHEN 'discovery_page' THEN 221 WHEN 'discovery_finish' THEN 222 ELSE 0 END,
stage_id,accepted_revision,result_count,after_ordinal FROM artwork_command_receipts
WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "ibi"),
		d(ipADFreshOwner, stmt.Read, `SELECT 1 FROM artwork_subject_jobs WHERE id=? AND command_incarnation=?
AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=? AND source_incarnation=? AND source_revision=?
AND job_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=?
AND job_lease_expires_ms>? AND phase='providers'`, "ibiibiiiiii"),
		d(ipADEntity, stmt.Read, `SELECT e.artwork_incarnation,e.artwork_revision,e.retired,
(CASE s.artwork_language WHEN 'en' THEN 'en' WHEN 'textless' THEN '' ELSE coalesce(nullif(e.metadata_language,''),s.default_language) END)=?
FROM catalog_entities AS e JOIN metadata_runtime_settings AS s ON s.singleton=1
JOIN catalog_libraries AS l ON l.id=e.library_id AND l.retired=0 WHERE e.id=?`, "si"),
		d(ipADPerson, stmt.Read, `SELECT p.artwork_incarnation,p.artwork_revision,
(CASE s.artwork_language WHEN 'en' THEN 'en' WHEN 'textless' THEN '' ELSE s.default_language END)=?
FROM catalog_people AS p JOIN metadata_runtime_settings AS s ON s.singleton=1 WHERE p.id=?`, "si"),
		d(ipADPolicy, stmt.Read, `SELECT s.artwork_policy_revision,s.online_lookups,p.enabled FROM metadata_runtime_settings AS s
JOIN metadata_providers AS p ON p.code=? WHERE s.singleton=1`, "s"),
		d(ipADStage, stmt.Read, `SELECT revision,CASE state WHEN 'building' THEN 1 WHEN 'ready' THEN 2 WHEN 'retiring' THEN 3 ELSE 4 END,
staged_count,staged_bytes,reclaim_after_ordinal FROM artwork_discovery_stages
WHERE id=? AND owner_id=? AND owner_incarnation=? AND provider=? AND source_incarnation=? AND source_revision=?
AND policy_revision=? AND effective_language=? AND generation=? AND expected_count=? AND expected_bytes=?
AND manifest_algorithm='portico-ip-artwork-discovery-sha256-v1' AND manifest_digest=?`, "iibsbiisiiib"),
		d(ipADBegin, stmt.Write, `INSERT INTO artwork_discovery_stages(owner_id,owner_incarnation,provider,source_incarnation,
source_revision,policy_revision,effective_language,generation,revision,state,expected_count,expected_bytes,manifest_algorithm,manifest_digest)
VALUES(?,?,?,?,?,?,?,?,1,'building',?,?,'portico-ip-artwork-discovery-sha256-v1',?) RETURNING id`, "ibsbiisiiib"),
		d(ipADAppend, stmt.Write, `INSERT INTO artwork_discovery_candidates(stage_id,ordinal,role,source_ordinal,image_id,origin,locale,attribution,score)
VALUES(?,?,?,?,?,?,?,?,?)`, "iisissssr"),
		d(ipADAdvance, stmt.Write, `UPDATE artwork_discovery_stages SET staged_count=?,staged_bytes=?,revision=revision+1
WHERE id=? AND revision=? AND state='building' AND staged_count=? AND staged_bytes=? AND expected_count=? AND expected_bytes=?`, "iiiiiiii"),
		d(ipADKeys, stmt.Read, `SELECT ordinal FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal>? ORDER BY ordinal LIMIT ?`, "iii"),
		d(ipADOctets, stmt.Read, `SELECT octet_length(role),octet_length(image_id),octet_length(origin),octet_length(locale),octet_length(attribution)
FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal=?`, "ii"),
		d(ipADCandidate, stmt.Read, `SELECT ordinal,source_ordinal,role,image_id,origin,locale,attribution,score
FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal=?`, "ii"),
		d(ipADFinish, stmt.Write, `UPDATE artwork_discovery_stages SET state='ready',revision=revision+1
WHERE id=? AND revision=? AND state='building' AND staged_count=? AND staged_bytes=? AND expected_count=? AND expected_bytes=?`, "iiiiii"),
		d(ipADOwnerCAS, stmt.Write, `UPDATE artwork_subject_jobs SET last_command_sequence=?,job_revision=job_revision+1
WHERE id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND job_revision=? AND source_incarnation=? AND source_revision=? AND job_lease_owner=? AND job_lease_generation=?
AND job_lease_expires_ms=? AND job_lease_expires_ms>? AND phase='providers'`, "iibiiibiiiii"),
		d(ipADSave, stmt.Write, `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,
applied_ms,result_digest,command_kind,accepted_revision,result_count,after_ordinal,stage_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "ibibbibsiiii"),
		d(ipADPin, stmt.Read, `SELECT command_sequence FROM artwork_command_receipts INDEXED BY artwork_receipt_stage_id
WHERE stage_id=? ORDER BY owner_id LIMIT 1`, "i"),
		d(ipADDelete, stmt.Write, `DELETE FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal=?`, "ii"),
		d(ipADMaintenanceOwner, stmt.Read, `SELECT 1 FROM artwork_subject_jobs WHERE id=? AND command_incarnation=? AND job_revision=?
AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=?`, "ibiii"),
		d(ipADMaintenanceCAS, stmt.Write, `UPDATE artwork_subject_jobs SET job_revision=job_revision+1
WHERE id=? AND command_incarnation=? AND job_revision=? AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=?`, "ibiii"),
		d(ipADReclaim, stmt.Write, `UPDATE artwork_discovery_stages SET staged_count=staged_count-?,staged_bytes=staged_bytes-?,
reclaim_after_ordinal=?,revision=revision+1,state=CASE WHEN ?=1 THEN 'done' ELSE 'retiring' END,
reclaim_due_ms=CASE WHEN ?=1 THEN NULL ELSE reclaim_due_ms END
WHERE id=? AND revision=? AND state='retiring' AND staged_count=? AND staged_bytes=? AND reclaim_after_ordinal=?
AND reclaim_due_ms IS NOT NULL AND reclaim_due_ms<=?`, "iiiiiiiiiii"),
	}
	for n := range defs {
		v := &defs[n]
		switch v.ID {
		case ipADOwner, ipADFreshOwner, ipADOwnerCAS, ipADMaintenanceOwner, ipADMaintenanceCAS:
			v.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}}
		case ipADReceipt:
			v.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}}
		case ipADEntity:
			v.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities", "metadata_runtime_settings", "catalog_libraries"}}
		case ipADPerson:
			v.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people", "metadata_runtime_settings"}}
		case ipADPolicy:
			v.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_runtime_settings", "metadata_providers"}}
		case ipADStage, ipADAdvance, ipADFinish, ipADReclaim:
			v.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_stages"}}
		case ipADKeys, ipADOctets, ipADCandidate, ipADDelete:
			v.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_candidates"}}
		case ipADPin:
			v.Plan = stmt.Plan{Hot: true, Indexes: []string{"artwork_receipt_stage_id"}}
		}
	}
	return defs
}

// Draft bookkeeping forecasts, NOT measured budgets or issuer admission. The
// parent must fund the full repeated recipe, bytes and terminal cost together.
func ipArtworkDiscoverySteps() []statementStep {
	var out []statementStep
	for _, d := range ipArtworkDiscoveryDefinitions() {
		out = append(out, statementStep{d.ID, ipArtworkDiscoveryWork(d.ID)})
	}
	return out
}

func ipArtworkDiscoveryWork(id stmt.ID) Work {
	rows := uint64(1)
	if id == ipADKeys {
		rows = ipArtworkDiscoveryMaxCandidates + 1
	}
	return Work{Rows: rows, VMOps: 4096, Pages: 8, WALBytes: 4096, Hold: 100 * time.Microsecond}
}
