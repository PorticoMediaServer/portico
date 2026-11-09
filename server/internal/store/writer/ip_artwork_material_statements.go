package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP-19's finite material custody family. Group E is not embedded yet, so these
// definitions are qualified only against the draft fixture in
// testdata/ip-artwork-material-draft.sql; they make no Capture or adoption
// claim. Every read is an exact owner/job/intent primary key or a raw
// (intent_id,slot) keyset; the native TEXT copies are point octet-preflighted
// first. The receipt CASE maps the frozen E labels: 223 stores "job_claim" and
// 224 stores "material_publish" (IP-20's publish reads its own receipt
// statement over the same closed table).
const (
	ipAMOwner        stmt.ID = "ip.art.material.owner"
	ipAMReceiptOctet stmt.ID = "ip.art.material.receipt_octets"
	ipAMReceipt      stmt.ID = "ip.art.material.receipt"
	ipAMFreshOwner   stmt.ID = "ip.art.material.fresh_owner"
	ipAMEntity       stmt.ID = "ip.art.material.entity"
	ipAMPerson       stmt.ID = "ip.art.material.person"
	ipAMPolicy       stmt.ID = "ip.art.material.policy"
	ipAMJobDue       stmt.ID = "ip.art.material.job_due"
	ipAMClaim        stmt.ID = "ip.art.material.job_claim"
	ipAMJobLive      stmt.ID = "ip.art.material.job_live"
	ipAMComplete     stmt.ID = "ip.art.material.job_complete"
	ipAMIntentCreate stmt.ID = "ip.art.material.intent_create"
	ipAMIntent       stmt.ID = "ip.art.material.intent"
	ipAMSlotCreate   stmt.ID = "ip.art.material.slot_create"
	ipAMSlotStage    stmt.ID = "ip.art.material.slot_stage"
	ipAMSlotKeys     stmt.ID = "ip.art.material.slot_keys"
	ipAMSlotOctets   stmt.ID = "ip.art.material.slot_octets"
	ipAMSlotExact    stmt.ID = "ip.art.material.slot_exact"
	ipAMOwnerCAS     stmt.ID = "ip.art.material.owner_cas"
	ipAMSave         stmt.ID = "ip.art.material.save"
)

func ipArtworkMaterialDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			ID: ipAMOwner, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM artwork_subject_jobs WHERE id=?`,
			Params: []stmt.Param{i("owner")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901)}},
				{Name: "missing", Bindings: []any{int64(1 << 40)}},
			},
		},
		{
			// Point octet preflight before copying the pinned intent id.
			ID: ipAMReceiptOctet, SQL: `SELECT octet_length(coalesce(material_intent_id,'')),coalesce(octet_length(retirement_token),0) FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			ID: ipAMReceipt, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'job_claim' THEN 223 WHEN 'material_publish' THEN 224 ELSE 0 END,accepted_revision,applied_ms,result_count,result_bytes,lease_generation,lease_until_ms,coalesce(material_intent_id,''),retirement_token FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			// The retained owner is exact and still in its publish phase under a
			// live lease; it is never inferred from a job or image row.
			ID: ipAMFreshOwner, SQL: `SELECT 1 FROM artwork_subject_jobs WHERE id=? AND command_incarnation=? AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=? AND source_incarnation=? AND source_revision=? AND job_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>? AND phase='publish'`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("entity"), i("person"), b("source_incarnation"), i("source_revision"), i("revision"), i("lease_owner"), i("lease_generation"), i("lease_until"), i("now")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(778), int64(0), make([]byte, 16), int64(1), int64(1), int64(7), int64(1), int64(1), int64(1)}},
				{Name: "missing", Bindings: []any{int64(1 << 40), make([]byte, 16), int64(0), int64(0), make([]byte, 16), int64(1), int64(1), int64(7), int64(1), int64(1), int64(1)}},
			},
		},
		{
			ID: ipAMEntity, SQL: `SELECT e.artwork_incarnation,e.artwork_revision,e.retired,(CASE s.artwork_language WHEN 'en' THEN 'en' WHEN 'textless' THEN '' ELSE coalesce(nullif(e.metadata_language,''),s.default_language) END)=? FROM catalog_entities AS e JOIN metadata_runtime_settings AS s ON s.singleton=1 JOIN catalog_libraries AS l ON l.id=e.library_id AND l.retired=0 WHERE e.id=?`,
			Params: []stmt.Param{t("language"), i("entity")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "s", "l"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"en", int64(778)}},
				{Name: "missing", Bindings: []any{"en", int64(1 << 40)}},
			},
		},
		{
			ID: ipAMPerson, SQL: `SELECT p.artwork_incarnation,p.artwork_revision,(CASE s.artwork_language WHEN 'en' THEN 'en' WHEN 'textless' THEN '' ELSE s.default_language END)=? FROM catalog_people AS p JOIN metadata_runtime_settings AS s ON s.singleton=1 WHERE p.id=?`,
			Params: []stmt.Param{t("language"), i("person")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"p", "s"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"en", int64(778)}},
				{Name: "missing", Bindings: []any{"en", int64(1 << 40)}},
			},
		},
		{
			ID: ipAMPolicy, SQL: `SELECT artwork_policy_revision FROM metadata_runtime_settings WHERE singleton=1`,
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_runtime_settings"}},
			Cohorts: []stmt.Cohort{{Name: "singleton"}},
		},
		{
			// The exact native job must be due, unleased or expired, and still
			// open; its ID is the intent ID, and it belongs to this subject job.
			ID: ipAMJobDue, SQL: `SELECT 1 FROM artwork_jobs WHERE id=? AND subject_job_id=? AND kind=? AND entity_id=? AND role=? AND choice=? AND image_id=? AND provider=? AND selection_revision=? AND job_revision=? AND job_lease_generation=? AND job_due_ms=? AND job_due_ms<=? AND status IN('pending','retry','running') AND job_attempts<4294967295`,
			Params: []stmt.Param{t("job"), i("subject_job"), t("kind"), i("entity"), t("role"), t("choice"), t("image_id"), t("provider"), i("selection_revision"), i("revision"), i("lease_generation"), i("due"), i("now")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "due", Bindings: []any{"job-1", int64(901), "entity", int64(778), "poster", "matched", "image-1", "tmdb", int64(1), int64(1), int64(0), int64(1), int64(2)}},
				{Name: "missing", Bindings: []any{"job-x", int64(901), "entity", int64(778), "poster", "matched", "image-1", "tmdb", int64(1), int64(1), int64(0), int64(1), int64(2)}},
			},
		},
		{
			ID: ipAMClaim, SQL: `UPDATE artwork_jobs SET status='running',job_revision=job_revision+1,job_lease_owner=?,job_lease_generation=job_lease_generation+1,job_lease_expires_ms=?,job_attempts=job_attempts+1 WHERE id=? AND subject_job_id=? AND kind=? AND entity_id=? AND job_revision=? AND job_lease_generation=? AND job_due_ms IS NOT NULL AND job_due_ms<=? AND status IN('pending','retry','running') AND job_attempts<4294967295`,
			Params:  []stmt.Param{i("lease_owner"), i("lease_until"), t("job"), i("subject_job"), t("kind"), i("entity"), i("revision"), i("lease_generation"), i("now")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_jobs"}},
			Cohorts: []stmt.Cohort{{Name: "claimed", Bindings: []any{int64(7), int64(1 << 50), "job-1", int64(901), "entity", int64(778), int64(1), int64(0), int64(2)}}},
		},
		{
			// The claimed job is still live under the same owner lease and the
			// provider the intent was declared for.
			ID: ipAMJobLive, SQL: `SELECT 1 FROM artwork_jobs WHERE id=? AND subject_job_id=? AND kind=? AND entity_id=? AND status='running' AND job_revision=? AND provider=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>?`,
			Params: []stmt.Param{t("job"), i("subject_job"), t("kind"), i("entity"), i("revision"), t("provider"), i("lease_owner"), i("lease_generation"), i("lease_until"), i("now")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "live", Bindings: []any{"job-1", int64(901), "entity", int64(778), int64(2), "tmdb", int64(7), int64(1), int64(1 << 50), int64(2)}},
				{Name: "missing", Bindings: []any{"job-x", int64(901), "entity", int64(778), int64(2), "tmdb", int64(7), int64(1), int64(1 << 50), int64(2)}},
			},
		},
		{
			ID: ipAMComplete, SQL: `UPDATE artwork_jobs SET status='complete',job_revision=job_revision+1,job_lease_expires_ms=0,job_next_attempt_ms=NULL WHERE id=? AND subject_job_id=? AND status='running' AND job_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>?`,
			Params:  []stmt.Param{t("job"), i("subject_job"), i("revision"), i("lease_owner"), i("lease_generation"), i("lease_until"), i("now")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_jobs"}},
			Cohorts: []stmt.Cohort{{Name: "complete", Bindings: []any{"job-1", int64(901), int64(2), int64(7), int64(1), int64(1 << 50), int64(2)}}},
		},
		{
			// The intent is durable before any file exists: prepared, no slots,
			// and its original generation fixed by the claimed job.
			ID: ipAMIntentCreate, SQL: `INSERT INTO artwork_material_intents(id,owner_id,owner_incarnation,generation,revision,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,state,expected_slots,staged_slots) VALUES(?,?,?,?,1,?,?,?,?,?,?,'prepared',?,0)`,
			Params:  []stmt.Param{t("intent"), i("owner"), b("owner_incarnation"), i("generation"), t("source_digest"), t("mime"), i("width"), i("height"), i("large_edge"), i("encoder_revision"), i("expected_slots")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_intents"}},
			Cohorts: []stmt.Cohort{{Name: "created", Bindings: []any{"job-1", int64(901), make([]byte, 16), int64(1), "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "image/jpeg", int64(3000), int64(2000), int64(1920), int64(3), int64(4)}}},
		},
		{
			ID: ipAMIntent, SQL: `SELECT 1 FROM artwork_material_intents WHERE id=? AND owner_id=? AND owner_incarnation=? AND generation=? AND revision=? AND source_digest=? AND accepted_mime=? AND accepted_width=? AND accepted_height=? AND large_edge=? AND encoder_revision=? AND state=? AND expected_slots=? AND staged_slots=?`,
			Params: []stmt.Param{t("intent"), i("owner"), b("owner_incarnation"), i("generation"), i("revision"), t("source_digest"), t("mime"), i("width"), i("height"), i("large_edge"), i("encoder_revision"), t("state"), i("expected_slots"), i("staged_slots")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_intents"}},
			Cohorts: []stmt.Cohort{
				{Name: "prepared", Bindings: []any{"job-1", int64(901), make([]byte, 16), int64(1), int64(1), "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "image/jpeg", int64(3000), int64(2000), int64(1920), int64(3), "prepared", int64(4), int64(0)}},
				{Name: "missing", Bindings: []any{"job-x", int64(901), make([]byte, 16), int64(1), int64(1), "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "image/jpeg", int64(3000), int64(2000), int64(1920), int64(3), "prepared", int64(4), int64(0)}},
			},
		},
		{
			ID: ipAMSlotCreate, SQL: `INSERT INTO artwork_material_slots(intent_id,slot,digest,mime,width,height,bytes,outlier) VALUES(?,?,?,?,?,?,?,?)`,
			Params:  []stmt.Param{t("intent"), i("slot"), t("digest"), t("mime"), i("width"), i("height"), i("bytes"), i("outlier")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_slots"}},
			Cohorts: []stmt.Cohort{{Name: "created", Bindings: []any{"job-1", int64(0), "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "image/jpeg", int64(500), int64(750), int64(4096), int64(0)}}},
		},
		{
			// Installing records the complete manifest and advances the intent in
			// one CAS: prepared+0 staged and the exact source facts.
			ID: ipAMSlotStage, SQL: `UPDATE artwork_material_intents SET state='installed',staged_slots=?,revision=revision+1 WHERE id=? AND owner_id=? AND owner_incarnation=? AND generation=? AND revision=? AND state='prepared' AND staged_slots=0 AND expected_slots=? AND source_digest=?`,
			Params:  []stmt.Param{i("slots"), t("intent"), i("owner"), b("owner_incarnation"), i("generation"), i("revision"), i("expected_slots"), t("source_digest")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_intents"}},
			Cohorts: []stmt.Cohort{{Name: "installed", Bindings: []any{int64(4), "job-1", int64(901), make([]byte, 16), int64(1), int64(1), int64(4), "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}},
		},
		{
			// Raw slot keyset only. LIMIT applies to slots, and the (intent_id,slot)
			// primary key supplies the order without a temp b-tree.
			ID: ipAMSlotKeys, SQL: `SELECT slot FROM artwork_material_slots WHERE intent_id=? AND slot>? ORDER BY slot LIMIT ?`,
			Params: []stmt.Param{t("intent"), i("after"), i("limit")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_slots"}, Seeks: []string{"intent_id=? AND slot>?"}},
			Cohorts: []stmt.Cohort{
				{Name: "all", Bindings: []any{"job-1", int64(-1), int64(5)}},
				{Name: "after", Bindings: []any{"job-1", int64(0), int64(1)}},
			},
		},
		{
			ID: ipAMSlotOctets, SQL: `SELECT octet_length(digest),octet_length(mime) FROM artwork_material_slots WHERE intent_id=? AND slot=?`,
			Params: []stmt.Param{t("intent"), i("slot")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_slots"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"job-1", int64(0)}},
				{Name: "missing", Bindings: []any{"job-1", int64(1 << 40)}},
			},
		},
		{
			ID: ipAMSlotExact, SQL: `SELECT digest,mime,width,height,bytes,outlier FROM artwork_material_slots WHERE intent_id=? AND slot=?`,
			Params: []stmt.Param{t("intent"), i("slot")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_slots"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"job-1", int64(0)}},
				{Name: "missing", Bindings: []any{"job-1", int64(1 << 40)}},
			},
		},
		{
			// The owner CAS is the command allowance and the receipt is saved in
			// the same transaction. ACK alone does not retire the pinned intent.
			ID: ipAMOwnerCAS, SQL: `UPDATE artwork_subject_jobs SET last_command_sequence=?,job_revision=job_revision+1 WHERE id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=? AND source_incarnation=? AND source_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>? AND phase='publish'`,
			Params:  []stmt.Param{i("sequence"), i("owner"), b("incarnation"), i("revision"), i("last"), i("acknowledged"), i("entity"), i("person"), b("source_incarnation"), i("source_revision"), i("lease_owner"), i("lease_generation"), i("lease_until"), i("now")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}},
			Cohorts: []stmt.Cohort{{Name: "cas", Bindings: []any{int64(1), int64(901), make([]byte, 16), int64(1), int64(0), int64(0), int64(778), int64(0), make([]byte, 16), int64(1), int64(7), int64(1), int64(1 << 50), int64(2)}}},
		},
		{
			ID: ipAMSave, SQL: `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,lease_generation,lease_until_ms,material_intent_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params:  []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), t("command_kind"), i("accepted_revision"), i("result_count"), i("result_bytes"), i("lease_generation"), i("lease_until"), t("intent")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(901), make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), "job_claim", int64(1), int64(4), int64(0), int64(1), int64(1 << 50), "job-1"}}},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipAMClaim, ipAMComplete, ipAMIntentCreate, ipAMSlotCreate, ipAMSlotStage, ipAMOwnerCAS, ipAMSave:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP artwork material"
		defs[n].CallSites = []string{"store/writer/ip_artwork_material_handoff.go", "store/writer/ip_artwork_job.go"}
	}
	return defs
}
