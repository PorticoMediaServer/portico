package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP-18's finite discovery family. Group E is not embedded yet, so these
// definitions are qualified only against the draft fixture in
// testdata/ip-artwork-discovery-draft.sql; they make no Capture or adoption
// claim. Owner/receipt statements ride the exact subject-job incarnation and
// its last/acknowledged command floors; the candidate walk is the RAW
// (stage_id,ordinal) primary key, never a due sort or OFFSET.
const (
	ipADOwner      stmt.ID = "ip.art.discovery.owner"
	ipADReceipt    stmt.ID = "ip.art.discovery.receipt"
	ipADFreshOwner stmt.ID = "ip.art.discovery.fresh_owner"
	ipADEntity     stmt.ID = "ip.art.discovery.entity"
	ipADPerson     stmt.ID = "ip.art.discovery.person"
	ipADPolicy     stmt.ID = "ip.art.discovery.policy"
	ipADConsent    stmt.ID = "ip.art.discovery.consent"
	ipADStage      stmt.ID = "ip.art.discovery.stage"
	ipADBegin      stmt.ID = "ip.art.discovery.begin"
	ipADAppend     stmt.ID = "ip.art.discovery.append"
	ipADAdvance    stmt.ID = "ip.art.discovery.advance"
	ipADKeys       stmt.ID = "ip.art.discovery.keys"
	ipADOctets     stmt.ID = "ip.art.discovery.octets"
	ipADCandidate  stmt.ID = "ip.art.discovery.candidate"
	ipADFinish     stmt.ID = "ip.art.discovery.finish"
	ipADOwnerCAS   stmt.ID = "ip.art.discovery.owner_cas"
	ipADSave       stmt.ID = "ip.art.discovery.save"
)

func ipArtworkDiscoveryDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	r := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real} }
	defs := []stmt.Definition{
		{
			ID: ipADOwner, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM artwork_subject_jobs WHERE id=?`,
			Params: []stmt.Param{i("owner")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901)}},
				{Name: "missing", Bindings: []any{int64(1 << 40)}},
			},
		},
		{
			ID: ipADReceipt, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'discovery_claim' THEN 220 WHEN 'discovery_page' THEN 221 WHEN 'discovery_finish' THEN 222 ELSE 0 END,stage_id,accepted_revision,result_count,after_ordinal FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			// The claimed job is fenced by its own incarnation, accepted artwork
			// source and a still-live lease; the phase must still be providers.
			ID: ipADFreshOwner, SQL: `SELECT 1 FROM artwork_subject_jobs WHERE id=? AND command_incarnation=? AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=? AND source_incarnation=? AND source_revision=? AND job_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>? AND phase='providers'`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("entity"), i("person"), b("source_incarnation"), i("source_revision"), i("revision"), i("lease_owner"), i("lease_generation"), i("lease_until"), i("now")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(778), int64(0), make([]byte, 16), int64(1), int64(1), int64(7), int64(1), int64(1), int64(1)}},
				{Name: "missing", Bindings: []any{int64(1 << 40), make([]byte, 16), int64(0), int64(0), make([]byte, 16), int64(1), int64(1), int64(7), int64(1), int64(1), int64(1)}},
			},
		},
		{
			// The resolved artwork language follows the current runtime setting
			// and the entity/season language, independent of provider truth.
			ID: ipADEntity, SQL: `SELECT e.artwork_incarnation,e.artwork_revision,e.retired,(CASE s.artwork_language WHEN 'en' THEN 'en' WHEN 'textless' THEN '' ELSE coalesce(nullif(e.metadata_language,''),s.default_language) END)=? FROM catalog_entities AS e JOIN metadata_runtime_settings AS s ON s.singleton=1 JOIN catalog_libraries AS l ON l.id=e.library_id AND l.retired=0 WHERE e.id=?`,
			Params: []stmt.Param{t("language"), i("entity")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "s", "l"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"en", int64(778)}},
				{Name: "missing", Bindings: []any{"en", int64(1 << 40)}},
			},
		},
		{
			ID: ipADPerson, SQL: `SELECT p.artwork_incarnation,p.artwork_revision,(CASE s.artwork_language WHEN 'en' THEN 'en' WHEN 'textless' THEN '' ELSE s.default_language END)=? FROM catalog_people AS p JOIN metadata_runtime_settings AS s ON s.singleton=1 WHERE p.id=?`,
			Params: []stmt.Param{t("language"), i("person")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"p", "s"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"en", int64(778)}},
				{Name: "missing", Bindings: []any{"en", int64(1 << 40)}},
			},
		},
		{
			ID: ipADPolicy, SQL: `SELECT s.artwork_policy_revision,v.enabled FROM metadata_runtime_settings AS s JOIN metadata_providers AS v ON v.code=? WHERE s.singleton=1`,
			Params: []stmt.Param{t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "metadata_providers"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb"}},
				{Name: "missing", Bindings: []any{"tvdb"}},
			},
		},
		{
			ID: ipADConsent, SQL: `SELECT 1 FROM screen_metadata_consent WHERE singleton=1 AND confirmed=1 AND revision=?`,
			Params: []stmt.Param{i("revision")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"screen_metadata_consent"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1)}},
				{Name: "missing", Bindings: []any{int64(2)}},
			},
		},
		{
			// The stored stage is bound to every declared spec field and its
			// manifest; a page/finish built against another spec cannot even read it.
			ID: ipADStage, SQL: `SELECT revision,state,staged_count,staged_bytes,reclaim_after_ordinal FROM artwork_discovery_stages WHERE id=? AND owner_id=? AND owner_incarnation=? AND provider=? AND source_incarnation=? AND source_revision=? AND policy_revision=? AND effective_language=? AND generation=? AND expected_count=? AND expected_bytes=? AND manifest_algorithm=? AND manifest_digest=?`,
			Params: []stmt.Param{i("stage"), i("owner"), b("owner_incarnation"), t("provider"), b("source_incarnation"), i("source_revision"), i("policy_revision"), t("language"), i("generation"), i("expected_count"), i("expected_bytes"), t("algorithm"), b("manifest")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_stages"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1), int64(901), make([]byte, 16), "tmdb", make([]byte, 16), int64(1), int64(1), "", int64(1), int64(0), int64(0), ipArtworkDiscoveryManifest, make([]byte, 32)}},
				{Name: "missing", Bindings: []any{int64(1 << 40), int64(901), make([]byte, 16), "tmdb", make([]byte, 16), int64(1), int64(1), "", int64(1), int64(0), int64(0), ipArtworkDiscoveryManifest, make([]byte, 32)}},
			},
		},
		{
			ID: ipADBegin, SQL: `INSERT INTO artwork_discovery_stages(owner_id,owner_incarnation,provider,source_incarnation,source_revision,policy_revision,effective_language,generation,revision,state,expected_count,expected_bytes,manifest_algorithm,manifest_digest) VALUES(?,?,?,?,?,?,?,?,1,'building',?,?,'` + ipArtworkDiscoveryManifest + `',?) RETURNING id`,
			Params:  []stmt.Param{i("owner"), b("owner_incarnation"), t("provider"), b("source_incarnation"), i("source_revision"), i("policy_revision"), t("language"), i("generation"), i("expected_count"), i("expected_bytes"), b("manifest")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_stages"}},
			Cohorts: []stmt.Cohort{{Name: "created", Bindings: []any{int64(901), make([]byte, 16), "tmdb", make([]byte, 16), int64(1), int64(1), "", int64(1), int64(0), int64(0), make([]byte, 32)}}},
		},
		{
			ID: ipADAppend, SQL: `INSERT INTO artwork_discovery_candidates(stage_id,ordinal,role,source_ordinal,image_id,origin,locale,attribution,score) VALUES(?,?,?,?,?,?,?,?,?)`,
			Params:  []stmt.Param{i("stage"), i("ordinal"), t("role"), i("source_ordinal"), t("image_id"), t("origin"), t("locale"), t("attribution"), r("score")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_candidates"}},
			Cohorts: []stmt.Cohort{{Name: "appended", Bindings: []any{int64(1), int64(0), "poster", int64(0), "image-000", "https://images.example.test/000.jpg", "en", "Example", float64(100)}}},
		},
		{
			ID: ipADAdvance, SQL: `UPDATE artwork_discovery_stages SET staged_count=?,staged_bytes=?,revision=revision+1 WHERE id=? AND revision=? AND state='building' AND staged_count=? AND staged_bytes=? AND expected_count=? AND expected_bytes=?`,
			Params:  []stmt.Param{i("count"), i("bytes"), i("stage"), i("revision"), i("old_count"), i("old_bytes"), i("expected_count"), i("expected_bytes")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_stages"}},
			Cohorts: []stmt.Cohort{{Name: "advanced", Bindings: []any{int64(1), int64(128), int64(1), int64(1), int64(0), int64(0), int64(1), int64(128)}}},
		},
		{
			// Raw keyset only: LIMIT applies to staged ordinals, and the
			// (stage_id,ordinal) primary key supplies the order without a temp b-tree.
			ID: ipADKeys, SQL: `SELECT ordinal FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal>? ORDER BY ordinal LIMIT ?`,
			Params: []stmt.Param{i("stage"), i("after"), i("limit")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_candidates"}, Seeks: []string{"stage_id=? AND ordinal>?"}},
			Cohorts: []stmt.Cohort{
				{Name: "first-page", Bindings: []any{int64(1), int64(-1), int64(1)}},
				{Name: "after-page", Bindings: []any{int64(1), int64(16), int64(1)}},
			},
		},
		{
			// Point octet preflight before any TEXT decode: five stored lengths only.
			ID: ipADOctets, SQL: `SELECT octet_length(role),octet_length(image_id),octet_length(origin),octet_length(locale),octet_length(attribution) FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal=?`,
			Params: []stmt.Param{i("stage"), i("ordinal")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_candidates"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1), int64(0)}},
				{Name: "missing", Bindings: []any{int64(1), int64(1 << 40)}},
			},
		},
		{
			ID: ipADCandidate, SQL: `SELECT ordinal,source_ordinal,role,image_id,origin,locale,attribution,score FROM artwork_discovery_candidates WHERE stage_id=? AND ordinal=?`,
			Params: []stmt.Param{i("stage"), i("ordinal")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_candidates"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1), int64(0)}},
				{Name: "missing", Bindings: []any{int64(1), int64(1 << 40)}},
			},
		},
		{
			ID: ipADFinish, SQL: `UPDATE artwork_discovery_stages SET state='ready',revision=revision+1 WHERE id=? AND revision=? AND state='building' AND staged_count=? AND staged_bytes=? AND expected_count=? AND expected_bytes=?`,
			Params:  []stmt.Param{i("stage"), i("revision"), i("count"), i("bytes"), i("expected_count"), i("expected_bytes")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_discovery_stages"}},
			Cohorts: []stmt.Cohort{{Name: "finished", Bindings: []any{int64(1), int64(1), int64(1), int64(128), int64(1), int64(128)}}},
		},
		{
			// The owner CAS carries the same live lease and exact last/ack floors;
			// it is the page/finish allowance, not permission to run provider IO.
			ID: ipADOwnerCAS, SQL: `UPDATE artwork_subject_jobs SET last_command_sequence=?,job_revision=job_revision+1 WHERE id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND job_revision=? AND source_incarnation=? AND source_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>? AND phase='providers'`,
			Params:  []stmt.Param{i("sequence"), i("owner"), b("incarnation"), i("last"), i("acknowledged"), i("revision"), b("source_incarnation"), i("source_revision"), i("lease_owner"), i("lease_generation"), i("lease_until"), i("now")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}},
			Cohorts: []stmt.Cohort{{Name: "cas", Bindings: []any{int64(1), int64(901), make([]byte, 16), int64(0), int64(0), int64(1), make([]byte, 16), int64(1), int64(7), int64(1), int64(1), int64(1)}}},
		},
		{
			ID: ipADSave, SQL: `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,after_ordinal,stage_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params:  []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), t("command_kind"), i("accepted_revision"), i("result_count"), i("after_ordinal"), i("stage")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(901), make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), "discovery_claim", int64(1), int64(0), int64(0), int64(1)}}},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipADBegin, ipADAppend, ipADAdvance, ipADFinish, ipADOwnerCAS, ipADSave:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP artwork discovery"
		defs[n].CallSites = []string{"store/writer/ip_artwork_discovery_native.go"}
	}
	return defs
}
