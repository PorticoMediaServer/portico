package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP provider source statements. Every statement is a closed point or a bounded
// PK/unique-key range over the embedded D/E relations; there is no caller SQL,
// writer handle, provider fetch or unbounded child body. The accepted head is the
// complete composite: selected subject slot, accepted publication, IP-sealed
// evidence and its applied operation, plus the IP evidence-seal receipt. A
// pending or later evidence row can never substitute for that tuple.
const ipPolicySQL = `SELECT e.id,e.library_id,e.kind,e.retired,e.metadata_incarnation,e.metadata_revision,e.source_revision,e.identity_revision,pol.enabled,pol.revision,pol.incarnation,mp.enabled,sc.confirmed,sc.revision,rs.online_lookups,rs.lookup_epoch,e.admission_own_age
FROM catalog_entities AS e
CROSS JOIN metadata_provider_policy AS pol INDEXED BY metadata_policy_command_owner ON pol.library_id=e.library_id AND pol.provider=?
CROSS JOIN metadata_providers AS mp ON mp.code=pol.provider
CROSS JOIN screen_metadata_consent AS sc ON sc.singleton=?
CROSS JOIN metadata_runtime_settings AS rs ON rs.singleton=?
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

const ipPolicyTextSQL = `SELECT pol.algorithm,pol.refresh_mode,pol.region
FROM metadata_provider_policy AS pol
WHERE pol.library_id=? AND pol.provider=?`

const ipPolicyTextOctetsSQL = `SELECT octet_length(pol.algorithm),octet_length(pol.refresh_mode),octet_length(pol.region)
FROM metadata_provider_policy AS pol
WHERE pol.library_id=? AND pol.provider=?`

// The head joins every accepted-composite key by its exact native key. The
// operation must be applied with the publication's frozen generation and
// incarnation; the evidence must be sealed by the same operation, and that
// seal's IP receipt must exist. Missing pieces yield no row (honest absence),
// never a pending/latest fallback.
const ipHeadSQL = `SELECT e.id,e.public_id,e.title,e.revision,e.retired,e.library_id,e.kind,sp.provider,sp.role,sp.subject_kind,sp.provider_id,sp.requested_id,sp.revision,sp.observed_at,p.id,p.decision,p.selection_revision,p.generation,p.observed_at,p.operation_id,ev.id,ev.parser_version,ev.digest,ev.observed_at,ev.sealed,ev.ip_seal_owner,ev.ip_seal_sequence,ev.ip_seal_incarnation,ev.release_group_id,ev.primary_type,op.command_incarnation,op.status,op.generation,op.policy_incarnation,op.policy_revision,op.consent_revision,op.algorithm,op.refresh_mode,rc.command_kind,rc.accepted_revision,rc.applied_ms,rc.evidence_subject_id,rc.evidence_subject_kind,rc.evidence_provider_id
FROM catalog_entities AS e
CROSS JOIN metadata_subject_providers AS sp ON sp.entity_id=e.id AND sp.provider=? AND sp.role=?
CROSS JOIN metadata_publications AS p INDEXED BY sqlite_autoindex_metadata_publications_2 ON p.entity_id=e.id AND p.provider=sp.provider AND p.subject_kind=sp.subject_kind AND p.provider_id=sp.provider_id AND p.id=sp.accepted_publication_id AND p.selection_revision=sp.revision AND p.evidence_id=sp.accepted_evidence_id
CROSS JOIN metadata_evidence AS ev INDEXED BY sqlite_autoindex_metadata_evidence_3 ON ev.id=p.evidence_id AND ev.provider=p.provider AND ev.subject_kind=p.subject_kind AND ev.provider_id=p.provider_id AND ev.ip_seal_owner=p.operation_id
CROSS JOIN metadata_provider_operations AS op INDEXED BY sqlite_autoindex_metadata_provider_operations_2 ON op.id=p.operation_id AND op.entity_id=e.id AND op.provider=p.provider AND op.generation=p.generation AND op.command_incarnation=p.ip_proof_incarnation
CROSS JOIN metadata_ip_receipts AS rc INDEXED BY sqlite_autoindex_metadata_ip_receipts_3 ON rc.owner_id=ev.ip_seal_owner AND rc.command_incarnation=ev.ip_seal_incarnation AND rc.command_sequence=ev.ip_seal_sequence AND rc.evidence_id=ev.id AND rc.evidence_subject_id=ev.subject_id AND rc.parser_version=ev.parser_version
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND sp.status='selected' AND p.decision='accepted' AND ev.sealed=1 AND op.status='applied' AND rc.command_kind='evidence_seal' AND p.ip_proof_owner=p.operation_id`

const ipHeadOctetsSQL = `SELECT octet_length(e.public_id),octet_length(e.title),octet_length(sp.provider),octet_length(sp.role),octet_length(sp.subject_kind),octet_length(sp.provider_id),octet_length(sp.requested_id),octet_length(sp.observed_at),octet_length(p.id),octet_length(p.decision),octet_length(p.observed_at),octet_length(p.operation_id),octet_length(ev.id),octet_length(ev.parser_version),octet_length(ev.digest),octet_length(ev.observed_at),octet_length(ev.ip_seal_owner),octet_length(ev.ip_seal_incarnation),octet_length(ev.release_group_id),octet_length(ev.primary_type),octet_length(op.command_incarnation),octet_length(op.status),octet_length(op.policy_incarnation),octet_length(op.algorithm),octet_length(op.refresh_mode),octet_length(rc.command_kind),octet_length(rc.evidence_subject_kind),octet_length(rc.evidence_provider_id)
FROM catalog_entities AS e
CROSS JOIN metadata_subject_providers AS sp ON sp.entity_id=e.id AND sp.provider=? AND sp.role=?
CROSS JOIN metadata_publications AS p INDEXED BY sqlite_autoindex_metadata_publications_2 ON p.entity_id=e.id AND p.provider=sp.provider AND p.subject_kind=sp.subject_kind AND p.provider_id=sp.provider_id AND p.id=sp.accepted_publication_id AND p.selection_revision=sp.revision AND p.evidence_id=sp.accepted_evidence_id
CROSS JOIN metadata_evidence AS ev INDEXED BY sqlite_autoindex_metadata_evidence_3 ON ev.id=p.evidence_id AND ev.provider=p.provider AND ev.subject_kind=p.subject_kind AND ev.provider_id=p.provider_id AND ev.ip_seal_owner=p.operation_id
CROSS JOIN metadata_provider_operations AS op INDEXED BY sqlite_autoindex_metadata_provider_operations_2 ON op.id=p.operation_id AND op.entity_id=e.id AND op.provider=p.provider AND op.generation=p.generation AND op.command_incarnation=p.ip_proof_incarnation
CROSS JOIN metadata_ip_receipts AS rc INDEXED BY sqlite_autoindex_metadata_ip_receipts_3 ON rc.owner_id=ev.ip_seal_owner AND rc.command_incarnation=ev.ip_seal_incarnation AND rc.command_sequence=ev.ip_seal_sequence AND rc.evidence_id=ev.id AND rc.evidence_subject_id=ev.subject_id AND rc.parser_version=ev.parser_version
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND sp.status='selected' AND p.decision='accepted' AND ev.sealed=1 AND op.status='applied' AND rc.command_kind='evidence_seal' AND p.ip_proof_owner=p.operation_id`

// ipSourceHeadVariable is the exact variable payload of ipHeadSQL. Every listed
// column participates in the same-snapshot octet preflight; no column maximum is
// used as a length.
var ipSourceHeadVariable = []int{1, 2, 7, 8, 9, 10, 11, 13, 14, 15, 18, 19, 20, 21, 22, 23, 25, 27, 28, 29, 30, 31, 33, 36, 37, 38, 42, 43}

const ipBuildSQL = `SELECT b.id,b.state,b.generation,b.content_revision,b.owner_operation,b.owner_generation,b.expected_rows,b.materialized_rows,b.materialized_charge
FROM metadata_evidence_builds AS b INDEXED BY sqlite_autoindex_metadata_evidence_builds_2
WHERE b.evidence_id=?`

const ipBuildOctetsSQL = `SELECT octet_length(b.id),octet_length(b.state),octet_length(b.owner_operation)
FROM metadata_evidence_builds AS b INDEXED BY sqlite_autoindex_metadata_evidence_builds_2
WHERE b.evidence_id=?`

var ipSourceBuildVariable = []int{0, 1, 4}

// The material point is a private source fact for one entity/role. The locked
// choice is read first so an owner lock always wins over the matched choice. A
// locked choice with a NULL digest is explicit absence and stops fallback; it is
// not pending work. Retiring sources are never disclosed as current.
const ipMaterialSQL = `SELECT s.kind,s.entity_id,s.role,s.choice,s.digest,s.revision,s.provider,s.image_id,s.origin,s.attribution,s.locale,s.effective_language,s.source_fence,s.source_object_id,s.source_object_revision,s.actor,s.observed_at,src.accepted_mime,src.accepted_width,src.accepted_height,src.large_edge,src.small_outlier,src.large_outlier,src.encoder_revision,src.revision,src.retiring,sb.digest,sb.mime,sb.width,sb.height,sb.bytes,sb.status,lb.digest,lb.mime,lb.width,lb.height,lb.bytes,lb.status
FROM artwork_selections AS s
CROSS JOIN catalog_entities AS e ON e.id=s.entity_id AND e.library_id=? AND e.kind=? AND e.retired=0
LEFT JOIN artwork_sources AS src INDEXED BY sqlite_autoindex_artwork_sources_1 ON src.digest=s.digest
LEFT JOIN artwork_blobs AS sb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON sb.digest=src.small_blob
LEFT JOIN artwork_blobs AS lb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON lb.digest=src.large_blob
WHERE s.kind='entity' AND s.entity_id=? AND s.role=? AND s.choice=?`

const ipMaterialOctetsSQL = `SELECT octet_length(s.kind),octet_length(s.role),octet_length(s.choice),octet_length(s.digest),octet_length(s.provider),octet_length(s.image_id),octet_length(s.origin),octet_length(s.attribution),octet_length(s.locale),octet_length(s.effective_language),octet_length(s.source_fence),octet_length(s.source_object_id),octet_length(s.source_object_revision),octet_length(s.actor),octet_length(s.observed_at),octet_length(src.accepted_mime),octet_length(sb.digest),octet_length(sb.mime),octet_length(sb.status),octet_length(lb.digest),octet_length(lb.mime),octet_length(lb.status)
FROM artwork_selections AS s
CROSS JOIN catalog_entities AS e ON e.id=s.entity_id AND e.library_id=? AND e.kind=? AND e.retired=0
LEFT JOIN artwork_sources AS src INDEXED BY sqlite_autoindex_artwork_sources_1 ON src.digest=s.digest
LEFT JOIN artwork_blobs AS sb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON sb.digest=src.small_blob
LEFT JOIN artwork_blobs AS lb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON lb.digest=src.large_blob
WHERE s.kind='entity' AND s.entity_id=? AND s.role=? AND s.choice=?`

var ipMaterialVariable = []int{0, 2, 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 26, 27, 31, 32, 33, 37}

func ipAutoKey(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
	b := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
	for _, c := range columns {
		b.Columns = append(b.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
	}
	return b
}

// ipMaterialScopeSQL proves the subject is one visible, live entity in the
// admitted library before any selection point is read. It projects only fixed
// primitives; the payload point does its own octet preflight.
const ipMaterialScopeSQL = `SELECT e.id,e.library_id,e.kind,e.retired,e.metadata_incarnation,e.metadata_revision,e.artwork_revision,e.admission_own_age
FROM catalog_entities AS e
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

// IPSourceHarnessDefinitions is inert harness composition for the private
// provider/art source reads. Capture these exact definitions on the real reader
// before registration; nothing here is production wiring.
func IPSourceHarnessDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	pubKey := ipAutoKey("metadata_publications", "sqlite_autoindex_metadata_publications_2", "u", "entity_id", "provider", "subject_kind", "provider_id", "id")
	buildKey := ipAutoKey("metadata_evidence_builds", "sqlite_autoindex_metadata_evidence_builds_2", "u", "evidence_id")
	evidenceKey := ipAutoKey("metadata_evidence", "sqlite_autoindex_metadata_evidence_3", "u", "id", "provider", "subject_kind", "provider_id")
	operationKey := ipAutoKey("metadata_provider_operations", "sqlite_autoindex_metadata_provider_operations_2", "u", "provider", "id")
	receiptKey := ipAutoKey("metadata_ip_receipts", "sqlite_autoindex_metadata_ip_receipts_3", "u", "owner_id", "command_incarnation", "command_sequence", "command_kind", "evidence_id", "evidence_subject_id", "parser_version")
	sourceKey := ipAutoKey("artwork_sources", "sqlite_autoindex_artwork_sources_1", "pk", "digest")
	blobKey := ipAutoKey("artwork_blobs", "sqlite_autoindex_artwork_blobs_1", "pk", "digest")

	policyParams := []stmt.Param{s("provider"), i("screen_singleton"), i("settings_singleton"), i("entity"), i("library"), i("kind")}
	policyCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"tmdb", int64(1), int64(1), int64(2001), int64(1), int64(2)}}, {Name: "empty", Bindings: []any{"tmdb", int64(1), int64(1), int64(999999), int64(1), int64(2)}}}
	headParams := []stmt.Param{s("provider"), s("role"), i("entity"), i("library"), i("kind")}
	headCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"tmdb", "primary", int64(2001), int64(1), int64(2)}}, {Name: "empty", Bindings: []any{"tmdb", "primary", int64(999999), int64(1), int64(2)}}}
	buildParams := []stmt.Param{s("evidence")}
	buildCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"ev-1"}}, {Name: "empty", Bindings: []any{"missing"}}}
	materialParams := []stmt.Param{i("library"), i("kind"), i("entity"), s("role"), s("choice")}
	materialCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(1), "poster", "locked"}}, {Name: "empty", Bindings: []any{int64(1), int64(1), int64(999999), "poster", "locked"}}}

	return []stmt.Definition{
		{ID: "read.ip.policy", SQL: ipPolicySQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPProviderSource"}, Params: policyParams, Cohorts: policyCohorts,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "mp"}, Indexes: []string{"metadata_policy_command_owner"}, Seeks: []string{"rowid=?", "library_id=? AND provider=?", "code=?"},
				Waivers: []stmt.Waiver{
					{StatementID: "read.ip.policy", Stage: "eqp", Form: "SCAN", Match: "SCAN sc", Reason: "screen consent is a CHECK(singleton=1) one-row table; a tiny rowid scan is the only plan SQLite chooses after ANALYZE and reads at most one row"},
					{StatementID: "read.ip.policy", Stage: "eqp", Form: "SCAN", Match: "SCAN rs", Reason: "runtime settings is a CHECK(singleton=1) one-row table; a tiny rowid scan is the only plan SQLite chooses after ANALYZE and reads at most one row"},
				}}},
		{ID: "read.ip.policy_text", SQL: ipPolicyTextSQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPProviderSource"}, Params: []stmt.Param{i("library"), s("provider")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), "tmdb"}}, {Name: "empty", Bindings: []any{int64(1), "missing"}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"pol"}, Seeks: []string{"library_id=? AND provider=?"}}},
		{ID: "read.ip.policy_text_bytes", SQL: ipPolicyTextOctetsSQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPProviderSource"}, Params: []stmt.Param{i("library"), s("provider")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), "tmdb"}}, {Name: "empty", Bindings: []any{int64(1), "missing"}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"pol"}, Seeks: []string{"library_id=? AND provider=?"}}},
		{ID: "read.ip.head", SQL: ipHeadSQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPProviderSource"}, Params: headParams, Cohorts: headCohorts,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "sp"}, Indexes: []string{pubKey.Name, evidenceKey.Name, operationKey.Name, receiptKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{pubKey, evidenceKey, operationKey, receiptKey}, Seeks: []string{"rowid=?", "entity_id=? AND provider=? AND role=?", "entity_id=? AND provider=? AND subject_kind=? AND provider_id=? AND id=?", "id=? AND provider=? AND subject_kind=? AND provider_id=?", "provider=? AND id=?", "owner_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=? AND evidence_id=?"}}},
		{ID: "read.ip.head_bytes", SQL: ipHeadOctetsSQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPProviderSource"}, Params: headParams, Cohorts: headCohorts,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "sp"}, Indexes: []string{pubKey.Name, evidenceKey.Name, operationKey.Name, receiptKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{pubKey, evidenceKey, operationKey, receiptKey}, Seeks: []string{"rowid=?", "entity_id=? AND provider=? AND role=?", "entity_id=? AND provider=? AND subject_kind=? AND provider_id=? AND id=?", "id=? AND provider=? AND subject_kind=? AND provider_id=?", "provider=? AND id=?", "owner_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=? AND evidence_id=?"}}},
		{ID: "read.ip.build", SQL: ipBuildSQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPProviderSource"}, Params: buildParams, Cohorts: buildCohorts,
			Plan: stmt.Plan{Hot: true, Indexes: []string{buildKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{buildKey}, Seeks: []string{"evidence_id=?"}}},
		{ID: "read.ip.build_bytes", SQL: ipBuildOctetsSQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPProviderSource"}, Params: buildParams, Cohorts: buildCohorts,
			Plan: stmt.Plan{Hot: true, Indexes: []string{buildKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{buildKey}, Seeks: []string{"evidence_id=?"}}},
		{ID: "read.ip.material_scope", SQL: ipMaterialScopeSQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPArtworkMaterial"}, Params: []stmt.Param{i("entity"), i("library"), i("kind")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(1)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Seeks: []string{"rowid=?"}}},
		{ID: "read.ip.material", SQL: ipMaterialSQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPArtworkMaterial"}, Params: materialParams, Cohorts: materialCohorts,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "e"}, Indexes: []string{sourceKey.Name, blobKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{sourceKey, blobKey}, Seeks: []string{"kind=? AND entity_id=? AND role=? AND choice=?", "rowid=?", "digest=?"}}},
		{ID: "read.ip.material_bytes", SQL: ipMaterialOctetsSQL, Kind: stmt.Read, Owner: "store/read IP source", CallSites: []string{"store/read/ip_sources.go:ForegroundReader.IPArtworkMaterial"}, Params: materialParams, Cohorts: materialCohorts,
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "e"}, Indexes: []string{sourceKey.Name, blobKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{sourceKey, blobKey}, Seeks: []string{"kind=? AND entity_id=? AND role=? AND choice=?", "rowid=?", "digest=?"}}},
	}
}
