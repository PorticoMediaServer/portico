package writer

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"time"
)

const (
	ipAMOwner            stmt.ID = "ip.artwork_material.owner"
	ipAMFresh            stmt.ID = "ip.artwork_material.fresh"
	ipAMMaintenance      stmt.ID = "ip.artwork_material.maintenance"
	ipAMOwnerCAS         stmt.ID = "ip.artwork_material.ownercas"
	ipAMEntity           stmt.ID = "ip.artwork_material.entity"
	ipAMPerson           stmt.ID = "ip.artwork_material.person"
	ipAMPolicy           stmt.ID = "ip.artwork_material.policy"
	ipAMProvider         stmt.ID = "ip.artwork_material.provider"
	ipAMReceiptOctets    stmt.ID = "ip.artwork_material.receiptoctets"
	ipAMReceipt          stmt.ID = "ip.artwork_material.receipt"
	ipAMSave             stmt.ID = "ip.artwork_material.save"
	ipAMJob              stmt.ID = "ip.artwork_material.job"
	ipAMClaim            stmt.ID = "ip.artwork_material.claim"
	ipAMRenew            stmt.ID = "ip.artwork_material.renew"
	ipAMCompleteJob      stmt.ID = "ip.artwork_material.completejob"
	ipAMIntentCreate     stmt.ID = "ip.artwork_material.intentcreate"
	ipAMSlotCreate       stmt.ID = "ip.artwork_material.slotcreate"
	ipAMStageSlots       stmt.ID = "ip.artwork_material.stageslots"
	ipAMIntent           stmt.ID = "ip.artwork_material.intent"
	ipAMSlotKeys         stmt.ID = "ip.artwork_material.slotkeys"
	ipAMSlotExact        stmt.ID = "ip.artwork_material.slotexact"
	ipAMInstall          stmt.ID = "ip.artwork_material.install"
	ipAMPublishIntent    stmt.ID = "ip.artwork_material.publishintent"
	ipAMBlob             stmt.ID = "ip.artwork_material.blob"
	ipAMBlobAbsent       stmt.ID = "ip.artwork_material.blobabsent"
	ipAMBlobCreate       stmt.ID = "ip.artwork_material.blobcreate"
	ipAMSource           stmt.ID = "ip.artwork_material.source"
	ipAMSourceEquivalent stmt.ID = "ip.artwork_material.sourceequivalent"
	ipAMOriginalHeld     stmt.ID = "ip.artwork_material.originalheld"
	ipAMSourceAbsent     stmt.ID = "ip.artwork_material.sourceabsent"
	ipAMSourceReady      stmt.ID = "ip.artwork_material.sourceready"
	ipAMSourceCreate     stmt.ID = "ip.artwork_material.sourcecreate"
	ipAMReencode         stmt.ID = "ip.artwork_material.reencode"
	ipAMSourceSelection  stmt.ID = "ip.artwork_material.sourceselection"
	ipAMSourceHistory    stmt.ID = "ip.artwork_material.sourcehistory"
	ipAMSourceReceipt    stmt.ID = "ip.artwork_material.sourcereceipt"
	ipAMSourcePrevious   stmt.ID = "ip.artwork_material.sourceprevious"
	ipAMSourceIntent     stmt.ID = "ip.artwork_material.sourceintent"
	ipAMBlobSmall        stmt.ID = "ip.artwork_material.blobsmall"
	ipAMBlobLarge        stmt.ID = "ip.artwork_material.bloblarge"
	ipAMBlobAmbient      stmt.ID = "ip.artwork_material.blobambient"
	ipAMBlobSlot         stmt.ID = "ip.artwork_material.blobslot"
	ipAMBackup           stmt.ID = "ip.artwork_material.backup"
	ipAMIntentReceipt    stmt.ID = "ip.artwork_material.intentreceipt"
	ipAMSourceMark       stmt.ID = "ip.artwork_material.sourcemark"
	ipAMSourceDelete     stmt.ID = "ip.artwork_material.sourcedelete"
	ipAMBlobMark         stmt.ID = "ip.artwork_material.blobmark"
	ipAMBlobDelete       stmt.ID = "ip.artwork_material.blobdelete"
	ipAMIntentMark       stmt.ID = "ip.artwork_material.intentmark"
	ipAMIntentRetiring   stmt.ID = "ip.artwork_material.intentretiring"
	ipAMSlotDelete       stmt.ID = "ip.artwork_material.slotdelete"
	ipAMIntentFinish     stmt.ID = "ip.artwork_material.intentfinish"
	ipAMCandidate        stmt.ID = "ip.artwork_material.candidate"
	ipAMSelectionAbsent  stmt.ID = "ip.artwork_material.selectionabsent"
	ipAMSelectionExact   stmt.ID = "ip.artwork_material.selectionexact"
	ipAMSelectionCreate  stmt.ID = "ip.artwork_material.selectioncreate"
	ipAMSelectionUpdate  stmt.ID = "ip.artwork_material.selectionupdate"
	ipAMSelectionDelete  stmt.ID = "ip.artwork_material.selectiondelete"
	ipAMEntityBump       stmt.ID = "ip.artwork_material.entitybump"
	ipAMPersonBump       stmt.ID = "ip.artwork_material.personbump"
	ipAMHistory          stmt.ID = "ip.artwork_material.history"
	ipAMHistoryPin       stmt.ID = "ip.artwork_material.historypin"
)

// Inert static catalogue only; no registry, Capture or budget admission claim.
func ipArtworkMaterialDefinitions() []stmt.Definition {
	d := func(id stmt.ID, k stmt.Kind, sql, types string, keys, indexes []string) stmt.Definition {
		v := stmt.Definition{ID: id, Kind: k, SQL: sql, Owner: "IP artwork material", CallSites: []string{"store/writer/ip_artwork_material_native.go", "store/writer/ip_artwork_selection_native.go", "store/writer/ip_artwork_gc_native.go"}}
		var bindings []any
		for n, c := range types {
			p := stmt.Param{Name: fmt.Sprintf("p%d", n+1)}
			if c >= 'A' && c <= 'Z' {
				p.Nullable = true
				c += 32
			}
			switch c {
			case 'i':
				p.Type = stmt.Integer
				bindings = append(bindings, int64(1))
			case 's':
				p.Type = stmt.Text
				bindings = append(bindings, "fixture")
			case 'b':
				p.Type = stmt.Blob
				bindings = append(bindings, make([]byte, 16))
			}
			v.Params = append(v.Params, p)
		}
		v.Cohorts = []stmt.Cohort{{Name: "draft_exact_point_or_bounded_keyset", Bindings: bindings}}
		if len(keys)+len(indexes) > 0 {
			v.Plan = stmt.Plan{Hot: true, PrimaryKeys: keys, Indexes: indexes}
		}
		return v
	}
	return []stmt.Definition{
		d(ipAMOwner, stmt.Read, `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM artwork_subject_jobs WHERE id=?`, "i", []string{"artwork_subject_jobs"}, nil),
		d(ipAMFresh, stmt.Read, `SELECT 1 FROM artwork_subject_jobs WHERE id=? AND command_incarnation=? AND job_revision=?
AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=? AND source_incarnation=? AND source_revision=?
AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>? AND phase<>'retire'`, "ibiiibiiiii", []string{"artwork_subject_jobs"}, nil),
		d(ipAMMaintenance, stmt.Read, `SELECT 1 FROM artwork_subject_jobs WHERE id=? AND command_incarnation=? AND job_revision=? AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=?`, "ibiii", []string{"artwork_subject_jobs"}, nil),
		d(ipAMOwnerCAS, stmt.Write, `UPDATE artwork_subject_jobs SET last_command_sequence=?,job_revision=job_revision+1,source_revision=source_revision+? WHERE id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=?`, "iiibiiiii", []string{"artwork_subject_jobs"}, nil),
		d(ipAMEntity, stmt.Read, `SELECT e.artwork_incarnation,e.artwork_revision,e.retired,
(CASE s.artwork_language WHEN 'en' THEN 'en' WHEN 'textless' THEN '' ELSE coalesce(nullif(e.metadata_language,''),s.default_language) END)=?
FROM catalog_entities e JOIN metadata_runtime_settings s ON s.singleton=1 JOIN catalog_libraries l ON l.id=e.library_id AND l.retired=0 WHERE e.id=?`, "si", []string{"catalog_entities", "metadata_runtime_settings", "catalog_libraries"}, nil),
		d(ipAMPerson, stmt.Read, `SELECT p.artwork_incarnation,p.artwork_revision,0,
(CASE s.artwork_language WHEN 'en' THEN 'en' WHEN 'textless' THEN '' ELSE s.default_language END)=?
FROM catalog_people p JOIN metadata_runtime_settings s ON s.singleton=1 WHERE p.id=?`, "si", []string{"catalog_people", "metadata_runtime_settings"}, nil),
		d(ipAMPolicy, stmt.Read, `SELECT artwork_policy_revision,online_lookups FROM metadata_runtime_settings WHERE singleton=1`, "", []string{"metadata_runtime_settings"}, nil),
		d(ipAMProvider, stmt.Read, `SELECT enabled FROM metadata_providers WHERE code=?`, "s", []string{"metadata_providers"}, nil),
		d(ipAMReceiptOctets, stmt.Read, `SELECT coalesce(octet_length(material_intent_id),0),coalesce(octet_length(role),0),coalesce(octet_length(choice),0),
coalesce(octet_length(selected_digest),0),coalesce(octet_length(previous_digest),0),coalesce(octet_length(blob_digest),0),
octet_length(operation_id),octet_length(request_digest),octet_length(result_digest),coalesce(octet_length(retirement_token),0)
FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "ibi", []string{"artwork_command_receipts"}, nil),
		d(ipAMReceipt, stmt.Read, `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'job_claim' THEN 223 WHEN 'job_renew' THEN 224 WHEN 'material_publish' THEN 225 WHEN 'select' THEN 226 WHEN 'clear' THEN 227 WHEN 'upload_accept' THEN 228 WHEN 'gc_mark' THEN 229 WHEN 'gc_finish' THEN 230 WHEN 'reencode' THEN 231 ELSE 0 END,
accepted_revision,applied_ms,result_count,after_ordinal,lease_generation,lease_until_ms,
coalesce(material_intent_id,''),coalesce(role,''),coalesce(choice,''),coalesce(selected_digest,''),coalesce(previous_digest,''),coalesce(blob_digest,''),
coalesce(selection_revision,0),coalesce(storage_revision,0),coalesce(history_id,0),retirement_token
FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "ibi", []string{"artwork_command_receipts"}, nil),
		d(ipAMSave, stmt.Write, `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,result_digest,command_kind,
accepted_revision,applied_ms,result_count,after_ordinal,lease_generation,lease_until_ms,material_intent_id,role,choice,selected_digest,previous_digest,blob_digest,selection_revision,storage_revision,history_id,retirement_token)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "ibibbbsiiiiiiSSSSSSIIIB", nil, nil),
		d(ipAMJob, stmt.Read, `SELECT 1 FROM artwork_jobs WHERE id=? AND subject_job_id=? AND kind=? AND entity_id=? AND role=? AND choice=? AND image_id=? AND origin=? AND attribution=?
AND locale=? AND effective_language=? AND provider=? AND source_fence=? AND actor=? AND source_object_id IS ? AND source_object_revision IS ?
AND selection_revision=? AND job_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=?
AND CASE status WHEN 'pending' THEN 1 WHEN 'running' THEN 2 WHEN 'retry' THEN 3 WHEN 'complete' THEN 4 ELSE 0 END=? AND job_next_attempt_ms IS ?`, "sisissssssssssSSiiiiiiI", []string{"artwork_jobs"}, nil),
		d(ipAMClaim, stmt.Write, `UPDATE artwork_jobs SET status='running',job_revision=job_revision+1,job_lease_owner=?,job_lease_generation=job_lease_generation+1,job_lease_expires_ms=?,job_attempts=job_attempts+1 WHERE id=? AND job_revision=? AND job_lease_generation=? AND job_due_ms IS NOT NULL AND job_due_ms<=? AND status IN('pending','retry','running') AND job_attempts<4294967295`, "iisiii", []string{"artwork_jobs"}, nil),
		d(ipAMRenew, stmt.Write, `UPDATE artwork_jobs SET job_revision=job_revision+1,job_lease_expires_ms=? WHERE id=? AND job_revision=? AND status='running' AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>?`, "isiiiii", []string{"artwork_jobs"}, nil),
		d(ipAMCompleteJob, stmt.Write, `UPDATE artwork_jobs SET status='complete',job_revision=job_revision+1,job_lease_expires_ms=0,job_next_attempt_ms=NULL WHERE id=? AND job_revision=? AND status='running' AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>?`, "siiiii", []string{"artwork_jobs"}, nil),
		d(ipAMIntentCreate, stmt.Write, `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots)
VALUES(?,?,?,?,1,?,?,?,?,?,?,'prepared',?,0)`, "sibissiiiii", nil, nil),
		d(ipAMSlotCreate, stmt.Write, `INSERT INTO artwork_material_slots(intent_id,slot,digest,mime,width,height,bytes,outlier) VALUES(?,?,?,?,?,?,?,?)`, "sissiiii", nil, nil),
		d(ipAMStageSlots, stmt.Write, `UPDATE artwork_material_intents SET staged_slots=? WHERE id=? AND revision=1 AND state='prepared' AND staged_slots=0 AND expected_slots=?`, "isi", []string{"artwork_material_intents"}, nil),
		d(ipAMIntent, stmt.Read, `SELECT CASE state WHEN 'prepared' THEN 1 WHEN 'installed' THEN 2 WHEN 'published' THEN 3 WHEN 'retiring' THEN 4 ELSE 5 END
FROM artwork_material_intents WHERE id=? AND owner_id=? AND owner_incarnation=? AND generation=? AND revision=? AND source_digest=? AND accepted_mime=?
AND accepted_width=? AND accepted_height=? AND large_edge=? AND encoder_revision=? AND expected_slots=? AND staged_slots=?`, "sibiissiiiiii", []string{"artwork_material_intents"}, nil),
		d(ipAMSlotKeys, stmt.Read, `SELECT slot FROM artwork_material_slots WHERE intent_id=? AND slot>? ORDER BY slot LIMIT ?`, "sii", []string{"artwork_material_slots"}, nil),
		d(ipAMSlotExact, stmt.Read, `SELECT 1 FROM artwork_material_slots WHERE intent_id=? AND slot=? AND digest=? AND mime=? AND width=? AND height=? AND bytes=? AND outlier=?`, "sissiiii", []string{"artwork_material_slots"}, nil),
		d(ipAMInstall, stmt.Write, `UPDATE artwork_material_intents SET state='installed',revision=revision+1 WHERE id=? AND generation=? AND revision=? AND state='prepared' AND staged_slots=expected_slots`, "sii", []string{"artwork_material_intents"}, nil),
		d(ipAMPublishIntent, stmt.Write, `UPDATE artwork_material_intents SET state='published',revision=revision+1 WHERE id=? AND generation=? AND revision=? AND state='installed' AND staged_slots=expected_slots`, "sii", []string{"artwork_material_intents"}, nil),
		d(ipAMBlob, stmt.Read, `SELECT 1 FROM artwork_blobs WHERE digest=? AND mime=? AND width=? AND height=? AND bytes=? AND status='ready' AND deletion_token IS NULL`, "ssiii", []string{"artwork_blobs"}, nil),
		d(ipAMBlobAbsent, stmt.Read, `SELECT 1 FROM artwork_blobs WHERE digest=?`, "s", []string{"artwork_blobs"}, nil),
		d(ipAMBlobCreate, stmt.Write, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,?,?,?,?,?)`, "ssiiis", nil, nil),
		d(ipAMSource, stmt.Read, `SELECT 1 FROM artwork_sources WHERE digest=? AND accepted_mime=? AND accepted_width=? AND accepted_height=? AND small_blob=? AND large_blob=? AND ambient_blob=?
AND large_edge=? AND small_outlier=? AND large_outlier=? AND encoder_revision=? AND original_bytes=? AND revision=? AND retiring=0 AND retirement_token IS NULL`, "ssiisssiiiiii", []string{"artwork_sources"}, nil),
		d(ipAMSourceEquivalent, stmt.Read, `SELECT revision FROM artwork_sources WHERE digest=? AND accepted_mime=? AND accepted_width=? AND accepted_height=? AND small_blob=? AND large_blob=? AND ambient_blob=?
AND large_edge=? AND small_outlier=? AND large_outlier=? AND encoder_revision=? AND original_bytes=? AND retiring=0 AND retirement_token IS NULL`, "ssiisssiiiii", []string{"artwork_sources"}, nil),
		d(ipAMOriginalHeld, stmt.Read, `SELECT 1 FROM artwork_sources WHERE digest=? AND original_bytes=? AND retiring=0 AND retirement_token IS NULL`, "si", []string{"artwork_sources"}, nil),
		d(ipAMSourceAbsent, stmt.Read, `SELECT 1 FROM artwork_sources WHERE digest=?`, "s", []string{"artwork_sources"}, nil),
		d(ipAMSourceReady, stmt.Read, `SELECT 1 FROM artwork_sources s JOIN artwork_blobs b ON b.digest=s.small_blob AND b.status='ready' AND b.deletion_token IS NULL
JOIN artwork_blobs l ON l.digest=s.large_blob AND l.status='ready' AND l.deletion_token IS NULL JOIN artwork_blobs a ON a.digest=s.ambient_blob AND a.status='ready' AND a.deletion_token IS NULL
WHERE s.digest=? AND s.retiring=0 AND s.retirement_token IS NULL`, "s", []string{"artwork_sources", "artwork_blobs"}, nil),
		d(ipAMSourceCreate, stmt.Write, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,original_bytes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "ssiisssiiiiis", nil, nil),
		d(ipAMReencode, stmt.Write, `UPDATE artwork_sources SET small_blob=?,large_blob=?,ambient_blob=?,large_edge=?,small_outlier=?,large_outlier=?,encoder_revision=?,revision=revision+1
WHERE digest=? AND revision=? AND retiring=0 AND retirement_token IS NULL AND small_blob=? AND large_blob=? AND ambient_blob=? AND large_edge=? AND small_outlier=? AND large_outlier=? AND encoder_revision=? AND original_bytes=?`, "sssiiiisisssiiiii", []string{"artwork_sources"}, nil),
		d(ipAMSourceSelection, stmt.Read, `SELECT 1 FROM artwork_selections INDEXED BY artwork_selections_digest WHERE digest=? LIMIT 1`, "s", nil, []string{"artwork_selections_digest"}),
		d(ipAMSourceHistory, stmt.Read, `SELECT 1 FROM metadata_owner_history_artwork INDEXED BY metadata_owner_history_artwork_digest WHERE digest=? LIMIT 1`, "s", nil, []string{"metadata_owner_history_artwork_digest"}),
		d(ipAMSourceReceipt, stmt.Read, `SELECT 1 FROM artwork_command_receipts INDEXED BY artwork_receipt_selected_digest WHERE selected_digest=? LIMIT 1`, "s", nil, []string{"artwork_receipt_selected_digest"}),
		d(ipAMSourcePrevious, stmt.Read, `SELECT 1 FROM artwork_command_receipts INDEXED BY artwork_receipt_previous_digest WHERE previous_digest=? LIMIT 1`, "s", nil, []string{"artwork_receipt_previous_digest"}),
		d(ipAMSourceIntent, stmt.Read, `SELECT 1 FROM artwork_material_intents INDEXED BY artwork_material_intent_source WHERE source_digest=? AND state IN('prepared','installed','published','retiring') LIMIT 1`, "s", nil, []string{"artwork_material_intent_source"}),
		d(ipAMBlobSmall, stmt.Read, `SELECT 1 FROM artwork_sources INDEXED BY artwork_sources_small WHERE small_blob=? LIMIT 1`, "s", nil, []string{"artwork_sources_small"}),
		d(ipAMBlobLarge, stmt.Read, `SELECT 1 FROM artwork_sources INDEXED BY artwork_sources_large WHERE large_blob=? LIMIT 1`, "s", nil, []string{"artwork_sources_large"}),
		d(ipAMBlobAmbient, stmt.Read, `SELECT 1 FROM artwork_sources INDEXED BY artwork_sources_ambient WHERE ambient_blob=? LIMIT 1`, "s", nil, []string{"artwork_sources_ambient"}),
		d(ipAMBlobSlot, stmt.Read, `SELECT 1 FROM artwork_material_slots INDEXED BY artwork_material_slot_digest WHERE digest=? LIMIT 1`, "s", nil, []string{"artwork_material_slot_digest"}),
		d(ipAMBackup, stmt.Read, `SELECT 1 FROM backup_blob_holds WHERE singleton=1`, "", []string{"backup_blob_holds"}, nil),
		d(ipAMIntentReceipt, stmt.Read, `SELECT 1 FROM artwork_command_receipts INDEXED BY artwork_receipt_material_intent_id WHERE material_intent_id=? LIMIT 1`, "s", nil, []string{"artwork_receipt_material_intent_id"}),
		d(ipAMSourceMark, stmt.Write, `UPDATE artwork_sources SET retiring=1,revision=revision+1,retirement_token=? WHERE digest=? AND revision=? AND retiring=0 AND retirement_token IS NULL AND original_bytes=?`, "bsii", []string{"artwork_sources"}, nil),
		d(ipAMSourceDelete, stmt.Write, `DELETE FROM artwork_sources WHERE digest=? AND revision=? AND retiring=1 AND retirement_token=? AND original_bytes=?`, "sibi", []string{"artwork_sources"}, nil),
		d(ipAMBlobMark, stmt.Write, `UPDATE artwork_blobs SET status='deleting',storage_revision=storage_revision+1,deletion_token=? WHERE digest=? AND storage_revision=? AND status='ready' AND deletion_token IS NULL`, "bsi", []string{"artwork_blobs"}, nil),
		d(ipAMBlobDelete, stmt.Write, `DELETE FROM artwork_blobs WHERE digest=? AND storage_revision=? AND status='deleting' AND deletion_token=?`, "sib", []string{"artwork_blobs"}, nil),
		d(ipAMIntentMark, stmt.Write, `UPDATE artwork_material_intents SET state='retiring',revision=revision+1,retirement_token=?,reclaim_due_ms=? WHERE id=? AND owner_id=? AND owner_incarnation=? AND generation=? AND revision=? AND state='published' AND retirement_token IS NULL`, "bisibii", []string{"artwork_material_intents"}, nil),
		d(ipAMIntentRetiring, stmt.Read, `SELECT staged_slots FROM artwork_material_intents WHERE id=? AND owner_id=? AND owner_incarnation=? AND generation=? AND revision=? AND state='retiring' AND retirement_token=?`, "sibiib", []string{"artwork_material_intents"}, nil),
		d(ipAMSlotDelete, stmt.Write, `DELETE FROM artwork_material_slots WHERE intent_id=? AND slot=?`, "si", []string{"artwork_material_slots"}, nil),
		d(ipAMIntentFinish, stmt.Write, `UPDATE artwork_material_intents SET state='done',staged_slots=0,revision=revision+1,retirement_token=NULL,reclaim_due_ms=NULL,reclaim_after_slot=? WHERE id=? AND generation=? AND revision=? AND state='retiring' AND retirement_token=? AND staged_slots=?`, "isiibi", []string{"artwork_material_intents"}, nil),
		d(ipAMCandidate, stmt.Read, `SELECT 1 FROM artwork_discovery_stages s JOIN artwork_discovery_candidates c ON c.stage_id=s.id AND c.ordinal=?
WHERE s.id=? AND s.owner_id=? AND s.owner_incarnation=? AND s.provider=? AND s.source_incarnation=? AND s.source_revision=?
AND s.policy_revision=? AND s.effective_language=? AND s.state='ready' AND s.staged_count=s.expected_count AND s.staged_bytes=s.expected_bytes
AND c.role=? AND c.image_id=? AND c.origin=? AND c.locale=? AND c.attribution=?`, "iiibsbiissssss", []string{"artwork_discovery_stages", "artwork_discovery_candidates"}, nil),
		d(ipAMSelectionAbsent, stmt.Read, `SELECT 1 FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND choice=?`, "siss", []string{"artwork_selections"}, nil),
		d(ipAMSelectionExact, stmt.Read, `SELECT 1 FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND choice=? AND revision=? AND digest=? AND provider=? AND image_id=? AND origin=?
AND attribution=? AND locale=? AND effective_language=? AND source_fence=? AND actor=? AND source_object_id IS ? AND source_object_revision IS ?`, "sississsssssssSS", []string{"artwork_selections"}, nil),
		d(ipAMSelectionCreate, stmt.Write, `INSERT INTO artwork_selections(kind,entity_id,role,choice,revision,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,actor,source_object_id,source_object_revision,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "sississsssssssSSs", nil, nil),
		d(ipAMSelectionUpdate, stmt.Write, `UPDATE artwork_selections SET revision=?,digest=?,provider=?,image_id=?,origin=?,attribution=?,locale=?,effective_language=?,source_fence=?,actor=?,source_object_id=?,source_object_revision=?,observed_at=?
WHERE kind=? AND entity_id=? AND role=? AND choice=? AND revision=? AND digest=?`, "isssssssssSSssissis", []string{"artwork_selections"}, nil),
		d(ipAMSelectionDelete, stmt.Write, `DELETE FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND choice=? AND revision=? AND digest=?`, "sissis", []string{"artwork_selections"}, nil),
		d(ipAMEntityBump, stmt.Write, `UPDATE catalog_entities SET artwork_revision=artwork_revision+1 WHERE id=? AND artwork_incarnation=? AND artwork_revision=? AND retired=0`, "ibi", []string{"catalog_entities"}, nil),
		d(ipAMPersonBump, stmt.Write, `UPDATE catalog_people SET artwork_revision=artwork_revision+1 WHERE id=? AND artwork_incarnation=? AND artwork_revision=?`, "ibi", []string{"catalog_people"}, nil),
		d(ipAMHistory, stmt.Write, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES(?,?,?,?,?,?,?,?,?) RETURNING id`, "sisssssss", nil, nil),
		d(ipAMHistoryPin, stmt.Write, `INSERT INTO metadata_owner_history_artwork(history_id,role,choice,digest) VALUES(?,?,?,?)`, "isss", nil, nil),
	}
}
func ipArtworkMaterialWork(id stmt.ID) Work {
	rows := uint64(8)
	if id == ipAMSlotKeys {
		rows = 5
	}
	if id == ipAMHistory {
		return Work{Rows: 8, VMOps: 65536, Pages: 256, WALBytes: 1 << 20, Hold: 100 * time.Microsecond}
	}
	return Work{Rows: rows, VMOps: 4096, Pages: 32, WALBytes: 65536, Hold: 100 * time.Microsecond}
}
func ipArtworkMaterialSteps() []statementStep {
	var steps []statementStep
	for _, d := range ipArtworkMaterialDefinitions() {
		steps = append(steps, statementStep{d.ID, ipArtworkMaterialWork(d.ID)})
	}
	return steps
}
