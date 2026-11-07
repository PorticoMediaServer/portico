package writer

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	ipPPOwner          stmt.ID = "ip.person.owner"
	ipPPReceiptOctets  stmt.ID = "ip.person.receiptoctets"
	ipPPReceipt        stmt.ID = "ip.person.receipt"
	ipPPFresh          stmt.ID = "ip.person.fresh"
	ipPPSource         stmt.ID = "ip.person.source"
	ipPPBuild          stmt.ID = "ip.person.build"
	ipPPHeaderOctets   stmt.ID = "ip.person.headeroctets"
	ipPPHeader         stmt.ID = "ip.person.header"
	ipPPOtherChildren  stmt.ID = "ip.person.otherchildren"
	ipPPAliasKeys      stmt.ID = "ip.person.aliaskeys"
	ipPPAliasOctets    stmt.ID = "ip.person.aliasoctets"
	ipPPAlias          stmt.ID = "ip.person.alias"
	ipPPExternalOctets stmt.ID = "ip.person.externaloctets"
	ipPPExternals      stmt.ID = "ip.person.externals"
	ipPPEvidenceInsert stmt.ID = "ip.person.evidenceinsert"
	ipPPBuildInsert    stmt.ID = "ip.person.buildinsert"
	ipPPAliasInsert    stmt.ID = "ip.person.aliasinsert"
	ipPPExternalInsert stmt.ID = "ip.person.externalinsert"
	ipPPBuildAdvance   stmt.ID = "ip.person.buildadvance"
	ipPPEvidenceSeal   stmt.ID = "ip.person.evidenceseal"
	ipPPBuildSeal      stmt.ID = "ip.person.buildseal"
	ipPPOwnerAdvance   stmt.ID = "ip.person.owneradvance"
	ipPPSave           stmt.ID = "ip.person.save"
	ipPPLease          stmt.ID = "ip.person.lease"
	ipPPRetry          stmt.ID = "ip.person.retry"
	ipPPPublication    stmt.ID = "ip.person.publication"
	ipPPProof          stmt.ID = "ip.person.proof"
	ipPPSourceInsert   stmt.ID = "ip.person.sourceinsert"
	ipPPSourceUpdate   stmt.ID = "ip.person.sourceupdate"
	ipPPFields         stmt.ID = "ip.person.fields"
	ipPPFieldOctets    stmt.ID = "ip.person.fieldoctets"
	ipPPFieldValues    stmt.ID = "ip.person.fieldvalues"
	ipPPFieldUpdate    stmt.ID = "ip.person.fieldupdate"
	ipPPPersonUpdate   stmt.ID = "ip.person.personupdate"
	ipPPOrigin         stmt.ID = "ip.person.origin"
	ipPPPersonOctets   stmt.ID = "ip.person.personoctets"
	ipPPPersonValues   stmt.ID = "ip.person.personvalues"
	ipPPPolicyMode     stmt.ID = "ip.person.policymode"
)

// Literal definitions only; no accepting registry without W Capture receipts.
func ipPersonDefinitions() []stmt.Definition {
	return append([]stmt.Definition{
		ipPersonDefinition(ipPPOwner, stmt.Read, `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM metadata_person_operations WHERE id=?`, "t"),
		ipPersonDefinition(ipPPReceiptOctets, stmt.Read, `SELECT coalesce(octet_length(evidence_id),0)+coalesce(octet_length(build_id),0)+coalesce(octet_length(publication_id),0)+coalesce(octet_length(seal_algorithm),0) FROM metadata_person_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "tbi"),
		ipPersonDefinition(ipPPReceipt, stmt.Read, `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'begin' THEN CASE WHEN evidence_id IS NULL THEN 203 ELSE 210 END WHEN 'append' THEN 211 WHEN 'evidence_seal' THEN 212 WHEN 'accept' THEN 216 WHEN 'delta' THEN 217 WHEN 'renew' THEN 204 WHEN 'retry' THEN 205 ELSE 0 END,accepted_revision,after_ordinal,result_count,applied_ms,evidence_id,build_id,publication_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes FROM metadata_person_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "tbi"),
		ipPersonDefinition(ipPPFresh, stmt.Read, `SELECT 1 FROM metadata_person_operations o
 JOIN catalog_people p ON p.id=o.person_id
 JOIN catalog_libraries cl ON cl.id=o.library_id AND cl.retired=0
 JOIN libraries l ON l.id=cl.library_id
 JOIN metadata_provider_subjects s ON s.id=o.subject_id
 JOIN metadata_provider_policy f ON f.library_id=o.library_id AND f.provider=o.provider
 JOIN metadata_providers v ON v.code=o.provider
 JOIN screen_metadata_consent c ON c.singleton=1
 JOIN metadata_runtime_settings m ON m.singleton=1
 WHERE o.id=? AND o.command_incarnation=? AND o.revision=? AND o.generation=?
 AND o.person_id=? AND o.captured_person_token=? AND o.captured_person_revision=? AND p.token=o.captured_person_token AND p.revision=o.captured_person_revision
 AND o.library_id=? AND o.provider=? AND o.subject_kind='person' AND o.provider_id=? AND o.language=? AND o.region=? AND o.request_shape=?
 AND o.subject_id=? AND o.subject_incarnation=? AND o.subject_revision=?
 AND s.provider=o.provider AND s.subject_kind='person' AND s.provider_id=o.provider_id AND s.language=o.language AND s.region=o.region AND s.request_shape=o.request_shape AND s.incarnation=o.subject_incarnation AND s.revision=o.subject_revision
 AND o.policy_incarnation=? AND o.policy_revision=? AND f.incarnation=o.policy_incarnation AND f.revision=o.policy_revision AND f.enabled=1 AND f.region=o.region
 AND o.consent_revision=? AND c.revision=o.consent_revision AND c.confirmed=1 AND m.online_lookups=1 AND v.enabled=1
 AND o.lease_owner=? AND o.lease_generation=? AND o.lease_until_ms=?
 AND o.phase=? AND o.next_attempt_ms IS ? AND l.metadata_language=? AND l.data_metadata_revision=? AND (CASE WHEN l.metadata_language='' THEN m.default_language WHEN instr(l.metadata_language,'-')=0 THEN l.metadata_language ELSE substr(l.metadata_language,1,instr(l.metadata_language,'-')-1) END)=o.language`, "tbiiitiitttttibibiitiitIti"),
		ipPersonDefinition(ipPPSource, stmt.Read, `SELECT 1 WHERE (?=0 AND NOT EXISTS(SELECT 1 FROM metadata_person_sources WHERE person_id=? AND provider=? AND role=?)) OR (? > 0 AND EXISTS(SELECT 1 FROM metadata_person_sources WHERE person_id=? AND provider=? AND role=? AND provider_id=? AND incarnation=? AND revision=? AND accepted_publication_id IS ? AND accepted_evidence_id IS ?))`, "iittiitttbiTT"),
		ipPersonDefinition(ipPPBuild, stmt.Read, `SELECT content_revision,CASE state WHEN 'building' THEN 1 WHEN 'ready' THEN 2 WHEN 'retiring' THEN 3 ELSE 4 END,cursor,staged_rows,staged_bytes,materialized_charge FROM metadata_person_builds WHERE id=? AND evidence_id=? AND owner_id=? AND owner_incarnation=? AND owner_generation=? AND generation=? AND provider=? AND subject_kind='person' AND provider_id=? AND expected_rows=? AND expected_bytes=? AND expected_algorithm=? AND expected_commitment=?`, "tttbiittiitb"),
		ipPersonDefinition(ipPPHeaderOctets, stmt.Read, `SELECT octet_length(title)+octet_length(biography)+octet_length(birth_date)+octet_length(death_date)+octet_length(birth_place)+octet_length(observed_at)+octet_length(source_url) FROM metadata_evidence WHERE id=?`, "t"),
		ipPersonDefinition(ipPPHeader, stmt.Read, `SELECT title,biography,birth_date,death_date,birth_place,observed_at,source_url FROM metadata_evidence WHERE id=? AND subject_id=? AND provider=? AND subject_kind='person' AND provider_id=? AND language=? AND region=? AND request_shape=? AND parser_version=? AND digest=? AND sealed=? AND seal_proof_owner IS NULL AND seal_proof_incarnation IS NULL AND seal_proof_sequence IS NULL AND etag='' AND last_modified='' AND original_title='' AND overview='' AND tagline='' AND content_rating='' AND studio='' AND network='' AND country='' AND release_date='' AND artist='' AND wikidata_id='' AND biography_source='' AND biography_source_url='' AND disambiguation='' AND release_group_id='' AND group_title='' AND primary_type='' AND barcode='' AND year=0 AND length_millis IS NULL AND artist_type IS NULL AND begin_year IS NULL AND end_year IS NULL`, "tittttttti"),
		ipPersonDefinition(ipPPOtherChildren, stmt.Read, `SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM metadata_evidence_terms WHERE evidence_id=?) AND NOT EXISTS(SELECT 1 FROM metadata_evidence_credits WHERE evidence_id=?) AND NOT EXISTS(SELECT 1 FROM metadata_evidence_ratings WHERE evidence_id=?) AND NOT EXISTS(SELECT 1 FROM metadata_evidence_episodes WHERE evidence_id=?) AND NOT EXISTS(SELECT 1 FROM metadata_evidence_media WHERE evidence_id=?) AND NOT EXISTS(SELECT 1 FROM metadata_evidence_tracks WHERE evidence_id=?) AND NOT EXISTS(SELECT 1 FROM metadata_evidence_attachments WHERE evidence_id=?) AND NOT EXISTS(SELECT 1 FROM metadata_evidence_collections WHERE evidence_id=?)`, "tttttttt"),
		ipPersonDefinition(ipPPAliasKeys, stmt.Read, `SELECT ordinal FROM metadata_evidence_aliases WHERE evidence_id=? AND ordinal>? ORDER BY ordinal LIMIT ?`, "tii"),
		ipPersonDefinition(ipPPAliasOctets, stmt.Read, `SELECT octet_length(requested_id)+octet_length(canonical_id)+octet_length(alias)+octet_length(observed_at) FROM metadata_evidence_aliases WHERE evidence_id=? AND ordinal=?`, "ti"),
		ipPersonDefinition(ipPPAlias, stmt.Read, `SELECT requested_id,canonical_id,alias,observed_at FROM metadata_evidence_aliases WHERE evidence_id=? AND ordinal=?`, "ti"),
		ipPersonDefinition(ipPPExternalOctets, stmt.Read, `SELECT octet_length(provider)+octet_length(subject_kind)+octet_length(provider_id) FROM metadata_evidence_external_ids WHERE evidence_id=? ORDER BY provider,subject_kind,provider_id LIMIT ?`, "ti"),
		ipPersonDefinition(ipPPExternals, stmt.Read, `SELECT provider,subject_kind,provider_id FROM metadata_evidence_external_ids WHERE evidence_id=? ORDER BY provider,subject_kind,provider_id LIMIT ?`, "ti"),
		ipPersonDefinition(ipPPEvidenceInsert, stmt.Write, `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,title,biography,birth_date,death_date,birth_place) VALUES(?,?,?,?,'person',?,?,?,?,?,?,?,?,?,?,?,?)`, "titttttttttttttt"),
		ipPersonDefinition(ipPPBuildInsert, stmt.Write, `INSERT INTO metadata_person_builds(id,evidence_id,provider,subject_kind,provider_id,owner_id,owner_incarnation,owner_generation,generation,content_revision,state,cursor,expected_rows,expected_bytes,expected_algorithm,expected_commitment,staged_rows,staged_bytes,materialized_charge) VALUES(?,?,?,'person',?,?,?,?,?,1,'building',1,?,?,?,?,1,?,?)`, "tttttbiiiitbii"),
		ipPersonDefinition(ipPPAliasInsert, stmt.Write, `INSERT INTO metadata_evidence_aliases(evidence_id,ordinal,requested_id,canonical_id,alias,observed_at) VALUES(?,?,?,?,?,?)`, "titttt"),
		ipPersonDefinition(ipPPExternalInsert, stmt.Write, `INSERT INTO metadata_evidence_external_ids(evidence_id,provider,subject_kind,provider_id) VALUES(?,?,'person',?)`, "ttt"),
		ipPersonDefinition(ipPPBuildAdvance, stmt.Write, `UPDATE metadata_person_builds SET content_revision=content_revision+1,cursor=?,staged_rows=?,staged_bytes=?,materialized_charge=? WHERE id=? AND content_revision=? AND state='building' AND cursor=? AND staged_rows=? AND staged_bytes=?`, "iiiitiiii"),
		ipPersonDefinition(ipPPEvidenceSeal, stmt.Write, `UPDATE metadata_evidence SET sealed=1 WHERE id=? AND sealed=0 AND seal_proof_owner IS NULL`, "t"),
		ipPersonDefinition(ipPPBuildSeal, stmt.Write, `UPDATE metadata_person_builds SET state='ready',content_revision=content_revision+1,seal_owner=?,seal_incarnation=?,seal_sequence=? WHERE id=? AND state='building' AND staged_rows=expected_rows AND staged_bytes=expected_bytes AND seal_owner IS NULL`, "tbit"),
		ipPersonDefinition(ipPPOwnerAdvance, stmt.Write, `UPDATE metadata_person_operations SET revision=revision+1,last_command_sequence=?,phase=? WHERE id=? AND command_incarnation=? AND revision=? AND last_command_sequence=? AND acknowledged_command_sequence=?`, "ittbiii"),
		ipPersonDefinition(ipPPSave, stmt.Write, `INSERT INTO metadata_person_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,result_count,evidence_id,build_id,publication_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "tbibbibtiiiTTTTIBII"),
		ipPersonDefinition(ipPPLease, stmt.Write, `UPDATE metadata_person_operations SET lease_owner=?,lease_generation=?,lease_until_ms=?,next_attempt_ms=NULL WHERE id=? AND command_incarnation=? AND revision=? AND lease_owner=? AND lease_generation=? AND lease_until_ms=?`, "tiitbitii"),
		ipPersonDefinition(ipPPRetry, stmt.Write, `UPDATE metadata_person_operations SET next_attempt_ms=?,lease_until_ms=? WHERE id=? AND command_incarnation=? AND revision=? AND lease_owner=? AND lease_generation=? AND lease_until_ms=?`, "Iitbitii"),
		ipPersonDefinition(ipPPPublication, stmt.Write, `INSERT INTO metadata_person_publications(id,person_id,provider,subject_kind,provider_id,evidence_id,operation_id,proof_owner,proof_incarnation,proof_sequence,accepted_revision,observed_at) VALUES(?,?,?,'person',?,?,?,?,?,?,?,?)`, "titttttbiit"),
		ipPersonDefinition(ipPPProof, stmt.Read, `SELECT 1 FROM metadata_person_builds b JOIN metadata_person_receipts r ON r.owner_id=b.seal_owner AND r.command_incarnation=b.seal_incarnation AND r.command_sequence=b.seal_sequence AND r.command_kind='evidence_seal' AND r.evidence_id=b.evidence_id WHERE b.id=? AND b.state='ready' AND b.seal_owner=? AND b.seal_incarnation=? AND b.seal_sequence=? AND r.seal_algorithm=? AND r.seal_version=1 AND r.seal_commitment=? AND r.seal_rows=? AND r.seal_bytes=?`, "ttbitbii"),
		ipPersonDefinition(ipPPSourceInsert, stmt.Write, `INSERT INTO metadata_person_sources(person_id,provider,role,provider_id,incarnation,revision,accepted_publication_id,accepted_evidence_id) VALUES(?,?,?,?,?,1,?,?)`, "itttbtt"),
		ipPersonDefinition(ipPPSourceUpdate, stmt.Write, `UPDATE metadata_person_sources SET revision=revision+1,accepted_publication_id=?,accepted_evidence_id=? WHERE person_id=? AND provider=? AND role=? AND provider_id=? AND incarnation=? AND revision=? AND accepted_publication_id IS ? AND accepted_evidence_id IS ?`, "ttitttbiTT"),
		ipPersonDefinition(ipPPFields, stmt.Read, `SELECT CASE field WHEN 'name' THEN 0 WHEN 'biography' THEN 1 WHEN 'birth_date' THEN 2 WHEN 'death_date' THEN 3 ELSE 4 END,locked,value_present,automatic_present FROM metadata_owner_fields WHERE kind='person' AND entity_id=? AND field IN('name','biography','birth_date','death_date','birth_place') ORDER BY field LIMIT 6`, "i"),
		ipPersonDefinition(ipPPFieldOctets, stmt.Read, `SELECT octet_length(value)+octet_length(automatic_value) FROM metadata_owner_fields WHERE kind='person' AND entity_id=? AND field=?`, "it"),
		ipPersonDefinition(ipPPFieldValues, stmt.Read, `SELECT value,automatic_value FROM metadata_owner_fields WHERE kind='person' AND entity_id=? AND field=?`, "it"),
		ipPersonDefinition(ipPPFieldUpdate, stmt.Write, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at,value_present,automatic_present) VALUES('person',?,?,?,?,0,?,?,1,1) ON CONFLICT(kind,entity_id,field) DO UPDATE SET automatic_value=excluded.automatic_value,automatic_present=1,value=CASE WHEN metadata_owner_fields.locked=1 THEN metadata_owner_fields.value ELSE excluded.value END,value_present=CASE WHEN metadata_owner_fields.locked=1 THEN metadata_owner_fields.value_present ELSE 1 END,actor=CASE WHEN metadata_owner_fields.locked=1 THEN metadata_owner_fields.actor ELSE excluded.actor END,observed_at=excluded.observed_at`, "ittttt"),
		ipPersonDefinition(ipPPPersonUpdate, stmt.Write, `UPDATE catalog_people SET name=?,sort_name=?,biography=?,birth_date=?,death_date=?,birth_place=?,biography_source=?,revision=revision+1 WHERE id=? AND token=? AND revision=?`, "tttttttiti"),
		ipPersonDefinition(ipPPOrigin, stmt.Write, `INSERT INTO metadata_person_field_origins(person_id,field,provider,provider_id,publication_id,evidence_id,source_url,observed_at,language,region,selection_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(person_id,field) DO UPDATE SET provider=excluded.provider,provider_id=excluded.provider_id,publication_id=excluded.publication_id,evidence_id=excluded.evidence_id,source_url=excluded.source_url,observed_at=excluded.observed_at,language=excluded.language,region=excluded.region,selection_revision=excluded.selection_revision`, "ittttttttti"),
		ipPersonDefinition(ipPPPersonOctets, stmt.Read, `SELECT octet_length(name)+octet_length(sort_name)+octet_length(biography)+octet_length(birth_date)+octet_length(death_date)+octet_length(birth_place)+octet_length(biography_source) FROM catalog_people WHERE id=? AND token=? AND revision=?`, "iti"),
		ipPersonDefinition(ipPPPersonValues, stmt.Read, `SELECT name,sort_name,biography,birth_date,death_date,birth_place,biography_source FROM catalog_people WHERE id=? AND token=? AND revision=?`, "iti"),
		ipPersonDefinition(ipPPPolicyMode, stmt.Read, `SELECT CASE refresh_mode WHEN 'fill_missing' THEN 1 WHEN 'replace_unlocked' THEN 2 ELSE 0 END FROM metadata_provider_policy WHERE library_id=? AND provider=? AND incarnation=? AND revision=?`, "itbi"),
	}, append(ipPersonLifecycleDefinitions(), ipPersonRecoveryDefinitions()...)...)
}
func ipPersonDefinition(id stmt.ID, k stmt.Kind, q, types string) stmt.Definition {
	d := stmt.Definition{ID: id, Kind: k, SQL: q, Owner: "IP.person", CallSites: []string{"writer/ip_person_provider_native.go", "writer/ip_person_provider_evidence.go", "writer/ip_person_provider_accept.go", "writer/ip_person_provider_lifecycle.go", "writer/ip_person_provider_recovery.go"}}
	var args []any
	for j, c := range types {
		p := stmt.Param{Name: fmt.Sprintf("p%d", j)}
		var v any
		switch c {
		case 't', 'T':
			p.Type = stmt.Text
			v = ""
		case 'i', 'I':
			p.Type = stmt.Integer
			v = int64(0)
		case 'b', 'B':
			p.Type = stmt.Blob
			v = make([]byte, 16)
		default:
			panic("compiled person parameter")
		}
		if c == 'T' || c == 'I' || c == 'B' {
			p.Nullable = true
			v = nil
		}
		d.Params = append(d.Params, p)
		args = append(args, v)
	}
	d.Cohorts = []stmt.Cohort{{Name: "draft_missing_person", Bindings: args}}
	return d
}
