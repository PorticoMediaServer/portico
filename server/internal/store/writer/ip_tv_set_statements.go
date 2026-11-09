package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP TV child publication set 213/214/215. The exact provider-operation owner
// (metadata_provider_operations) and the IP-05 lease gate stay the admitted
// owner; the publication set/pages/members are the existing embedded publication
// body. The IP proof tuple on metadata_publication_sets and the receipt set
// reverse index are group D/E and fixture-qualified until those groups are
// embedded, so these definitions make no Capture claim.
const (
	ipTVSetSourceStmt       stmt.ID = "ip.tv.set.source"
	ipTVSetHierarchyStmt    stmt.ID = "ip.tv.set.hierarchy"
	ipTVSetExistsStmt       stmt.ID = "ip.tv.set.exists"
	ipTVSetInsertStmt       stmt.ID = "ip.tv.set.insert"
	ipTVSetHeaderStmt       stmt.ID = "ip.tv.set.header"
	ipTVSetLastPageStmt     stmt.ID = "ip.tv.set.last_page"
	ipTVSetMemberCountStmt  stmt.ID = "ip.tv.set.member_count"
	ipTVSetMemberStmt       stmt.ID = "ip.tv.set.member"
	ipTVSetInsertPageStmt   stmt.ID = "ip.tv.set.insert_page"
	ipTVSetInsertMemberStmt stmt.ID = "ip.tv.set.insert_member"
	ipTVSetAdvanceSetStmt   stmt.ID = "ip.tv.set.advance_set"
	ipTVSetSealSetStmt      stmt.ID = "ip.tv.set.seal_set"
	ipTVSetBeginCountStmt   stmt.ID = "ip.tv.set.begin_count"
	ipTVSetBeginDeclStmt    stmt.ID = "ip.tv.set.begin_decl"
	ipTVSetSealReverseStmt  stmt.ID = "ip.tv.set.seal_reverse"
	ipTVSetPagesStmt        stmt.ID = "ip.tv.set.pages"
	ipTVSetMembersStmt      stmt.ID = "ip.tv.set.members"
	ipTVSetUnsealedStmt     stmt.ID = "ip.tv.set.unsealed"
	ipTVSetReceiptStmt      stmt.ID = "ip.tv.set.receipt"
	ipTVSetRecordStmt       stmt.ID = "ip.tv.set.record"
)

// The set body is the complete ordered episode evidence list of one sealed
// provider hierarchy. Begin materializes the set header only; page appends one
// raw bounded page of the next contiguous ordinal range; seal proves every page
// and member against the begin declaration before marking the set complete.
func ipTVSetDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	nb := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob, Nullable: true} }
	defs := []stmt.Definition{
		{
			// The sealed series/season source is the exact evidence body plus the
			// ready build and the one IP evidence_seal receipt. Every join is an
			// exact primary-key or unique tuple; no child scan chooses a source.
			ID: ipTVSetSourceStmt, SQL: `SELECT e.subject_id,e.parser_version,e.provider,e.subject_kind,e.provider_id,b.state,b.phase,b.content_revision,b.generation,r.command_kind,r.terminal,r.evidence_id,r.evidence_subject_id,r.parser_version FROM metadata_evidence AS e JOIN metadata_evidence_builds AS b ON b.id=? AND b.evidence_id=e.id JOIN metadata_ip_receipts AS r ON r.owner_id=e.ip_seal_owner AND r.command_incarnation=e.ip_seal_incarnation AND r.command_sequence=e.ip_seal_sequence AND r.command_kind='evidence_seal' AND r.evidence_id=e.id WHERE e.id=?`,
			Params: []stmt.Param{t("build"), t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence", "metadata_evidence_builds", "metadata_ip_receipts"}},
		},
		{
			// The operation's own hierarchy and selection witnesses are read on
			// the provider-namespace PK; a source or hierarchy change is refused.
			ID: ipTVSetHierarchyStmt, SQL: `SELECT hierarchy_revision,selection_revision FROM metadata_provider_operations WHERE id=? AND provider=?`,
			Params: []stmt.Param{t("operation"), t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_operations"}},
		},
		{
			ID: ipTVSetExistsStmt, SQL: `SELECT count(*) FROM metadata_publication_sets WHERE id=?`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_sets"}},
		},
		{
			// One immutable set header under the exact owner incarnation and the
			// captured coverage basis. complete stays zero until the seal.
			ID: ipTVSetInsertStmt, SQL: `INSERT INTO metadata_publication_sets(member_kind,id,entity_id,provider,incarnation,revision,generation,subject_kind,provider_id,episode_order,consent_revision,policy_incarnation,policy_revision,complete,coverage_source_revision,coverage_hierarchy_revision,coverage_selection_revision,coverage_after_item,coverage_complete,ip_operation_id,ip_operation_incarnation) VALUES('episode',?,?,?,?,1,?,?,?,?,?,?,?,0,?,?,?,0,0,?,?)`,
			Params: []stmt.Param{
				t("set"), i("entity"), t("provider"), b("incarnation"), i("generation"),
				t("subject_kind"), t("provider_id"), t("episode_order"), i("consent"),
				b("policy_incarnation"), i("policy_revision"), i("source_revision"),
				i("hierarchy_revision"), i("selection_revision"),
				t("operation"), b("operation_incarnation"),
			},
			Plan: stmt.Plan{Hot: true},
		},
		{
			ID: ipTVSetHeaderStmt, SQL: `SELECT provider,subject_kind,provider_id,episode_order,incarnation,revision,generation,complete,entity_id,ip_proof_owner,coverage_source_revision,coverage_hierarchy_revision,coverage_selection_revision,coverage_after_item,coverage_complete FROM metadata_publication_sets WHERE id=?`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_sets"}},
		},
		{
			// The highest page is the exact next-page cursor. The primary key
			// (set_id,page) serves the descending bound without a temp sort.
			ID: ipTVSetLastPageStmt, SQL: `SELECT page,next_page FROM metadata_publication_pages WHERE set_id=? ORDER BY page DESC LIMIT 1`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_pages"}},
		},
		{
			ID: ipTVSetMemberCountStmt, SQL: `SELECT count(*) FROM metadata_publication_page_members WHERE set_id=?`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_page_members"}},
		},
		{
			// A member is the exact sealed episode evidence in this provider
			// namespace. A scalar header, unsealed body or foreign provider row
			// cannot enter a set.
			ID: ipTVSetMemberStmt, SQL: `SELECT count(*) FROM metadata_evidence WHERE id=? AND provider=? AND subject_kind='episode' AND sealed=1 AND ip_seal_owner IS NOT NULL`,
			Params: []stmt.Param{t("member"), t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence"}},
		},
		{
			// The page header records the provider's exact next-page truth. A
			// NULL next_page is the only no-next declaration.
			ID: ipTVSetInsertPageStmt, SQL: `INSERT INTO metadata_publication_pages(set_id,page,next_page,row_count,byte_count,result_digest,content_revision,validated_revision) VALUES(?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{
				t("set"), i("page"), n("next_page"), i("rows"), i("bytes"),
				t("digest"), i("content_revision"), i("validated_revision"),
			},
			Plan: stmt.Plan{Hot: true},
		},
		{
			// The member ordinal is the set-global raw position, not a page-local
			// index. The page/ordinal and set/evidence uniques are the physical
			// duplicate guards.
			ID: ipTVSetInsertMemberStmt, SQL: `INSERT INTO metadata_publication_page_members(provider,subject_kind,set_id,page,ordinal,evidence_id) VALUES(?,?,?,?,?,?)`,
			Params: []stmt.Param{t("provider"), t("subject_kind"), t("set"), i("page"), i("ordinal"), t("evidence")},
			Plan:   stmt.Plan{Hot: true},
		},
		{
			// One page advances exactly one set content revision under the exact
			// owner incarnation/generation; a sealed or foreign set cannot move.
			ID: ipTVSetAdvanceSetStmt, SQL: `UPDATE metadata_publication_sets SET revision=revision+1 WHERE id=? AND provider=? AND incarnation=? AND generation=? AND revision=? AND complete=0 AND ip_proof_owner IS NULL RETURNING revision`,
			Params: []stmt.Param{t("set"), t("provider"), b("incarnation"), i("generation"), i("revision")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_sets"}},
		},
		{
			// The only complete transition: exact current revision, still open,
			// with the captured coverage basis and the set_seal proof tuple.
			ID: ipTVSetSealSetStmt, SQL: `UPDATE metadata_publication_sets SET revision=revision+1,complete=1,coverage_source_revision=?,coverage_hierarchy_revision=?,coverage_selection_revision=?,coverage_after_item=?,coverage_complete=1,ip_proof_owner=?,ip_proof_incarnation=?,ip_proof_sequence=? WHERE id=? AND provider=? AND incarnation=? AND generation=? AND revision=? AND complete=0 AND ip_proof_owner IS NULL RETURNING revision`,
			Params: []stmt.Param{
				i("source_revision"), i("hierarchy_revision"), i("selection_revision"),
				i("after_item"), t("proof_owner"), b("proof_incarnation"), i("proof_sequence"),
				t("set"), t("provider"), b("incarnation"), i("generation"), i("revision"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_sets"}},
		},
		{
			// Exactly one begin receipt declares the complete member count and
			// digest for this set. The reverse set index is the exact path.
			ID: ipTVSetBeginCountStmt, SQL: `SELECT count(*) FROM metadata_ip_receipts WHERE set_id=? AND command_kind='set_begin'`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_ip_receipt_set"}},
		},
		{
			ID: ipTVSetBeginDeclStmt, SQL: `SELECT result_count,after_key,evidence_id,build_id FROM metadata_ip_receipts WHERE set_id=? AND command_kind='set_begin'`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_ip_receipt_set"}},
		},
		{
			ID: ipTVSetSealReverseStmt, SQL: `SELECT count(*) FROM metadata_ip_receipts WHERE set_id=? AND command_kind='set_seal'`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_ip_receipt_set"}},
		},
		{
			ID: ipTVSetPagesStmt, SQL: `SELECT page,next_page,row_count,byte_count,result_digest FROM metadata_publication_pages WHERE set_id=? ORDER BY page`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_pages"}},
		},
		{
			ID: ipTVSetMembersStmt, SQL: `SELECT page,ordinal,evidence_id FROM metadata_publication_page_members WHERE set_id=? ORDER BY page,ordinal`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_page_members"}},
		},
		{
			// Every persisted member must still be the exact sealed episode
			// evidence. The member PK prefix drives the bounded evidence probe.
			ID: ipTVSetUnsealedStmt, SQL: `SELECT count(*) FROM metadata_publication_page_members AS m JOIN metadata_evidence AS e ON e.id=m.evidence_id WHERE m.set_id=? AND (e.sealed<>1 OR e.ip_seal_owner IS NULL)`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_page_members", "metadata_evidence"}},
		},
		{
			// The exact saved receipt header, including the set and seal witnesses
			// used by typed replay. A replay never reads mutable set/page state.
			ID: ipTVSetReceiptStmt, SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_phase,terminal,after_family,after_ordinal,after_key,result_count,result_bytes,evidence_id,evidence_subject_id,parser_version,build_id,set_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes FROM metadata_ip_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}},
		},
		{
			// Begin/page receipts stay non-terminal and carry no seal proof; the
			// seal receipt is the one terminal set proof with pin count one.
			ID: ipTVSetRecordStmt, SQL: `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,evidence_id,evidence_subject_id,parser_version,build_id,set_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes,proof_pin_count,entity_id,provider,evidence_subject_kind,evidence_provider_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{
				t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"),
				t("kind"), i("accepted_revision"), i("applied_ms"), b("result_digest"),
				t("phase"), i("after_family"), i("after_ordinal"), t("after_key"),
				i("result_count"), i("result_bytes"), i("terminal"), nt("evidence"),
				n("subject"), nt("parser"), nt("build"), t("set"), nt("algorithm"),
				n("version"), nb("commitment"), n("seal_rows"), n("seal_bytes"),
				i("proof_pins"), i("entity"), t("provider"), nt("subject_kind"), nt("provider_id"),
			},
			Plan: stmt.Plan{Hot: true},
		},
	}
	for x := range defs {
		defs[x].Owner = "store/writer IP TV publication set"
		defs[x].Kind = stmt.Read
		switch defs[x].ID {
		case ipTVSetInsertStmt, ipTVSetInsertPageStmt, ipTVSetInsertMemberStmt,
			ipTVSetAdvanceSetStmt, ipTVSetSealSetStmt, ipTVSetRecordStmt:
			defs[x].Kind = stmt.Write
		}
		defs[x].CallSites = []string{"store/writer/ip_tv_set.go", "store/writer/ip_tv_set_statements.go"}
		var args []any
		for _, p := range defs[x].Params {
			switch p.Type {
			case stmt.Text:
				args = append(args, "")
			case stmt.Blob:
				args = append(args, make([]byte, 16))
			case stmt.Real:
				args = append(args, 1.0)
			default:
				if p.Nullable {
					args = append(args, nil)
				} else {
					args = append(args, int64(1))
				}
			}
		}
		defs[x].Cohorts = []stmt.Cohort{{Name: "missing_native_keys", Bindings: args}}
	}
	return defs
}
