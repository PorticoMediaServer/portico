package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoPersonalFlagsSubject        stmt.ID = "uo.personal.flags.subject"
	uoPersonalFlagsRead           stmt.ID = "uo.personal.flags.read"
	uoPersonalFlagsInsert         stmt.ID = "uo.personal.flags.insert"
	uoPersonalFlagsUpdate         stmt.ID = "uo.personal.flags.update"
	uoPersonalFlagsRevisionRead   stmt.ID = "uo.personal.flags.revision_read"
	uoPersonalFlagsRevisionInsert stmt.ID = "uo.personal.flags.revision_insert"
	uoPersonalFlagsRevisionUpdate stmt.ID = "uo.personal.flags.revision_update"

	// UO-03c viewing-authority proof. These are the writer-owned copies of the
	// native read fence's item/principal shapes (store/read/browse_statements.go
	// plus admittedItem in store/read/browse.go); the writer must not import the
	// read package into its core.
	uoFlagsTargetEntity         stmt.ID = "uo.personal.flags.target.entity"
	uoFlagsTargetPrincipal      stmt.ID = "uo.personal.flags.target.principal"
	uoFlagsTargetPrivateLibrary stmt.ID = "uo.personal.flags.target.private_library"
	uoFlagsTargetRestrictions   stmt.ID = "uo.personal.flags.target.restrictions"
	uoFlagsTargetAccessLimits   stmt.ID = "uo.personal.flags.target.access_limits"
)

// Inert finite domain statements, not a registered production command.
func uoPersonalFlagsDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	viewerKey := stmt.AutoIndexBinding{Table: "viewer_revisions", Name: "sqlite_autoindex_viewer_revisions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "profile_id", Collation: "BINARY"}, {Name: "library_id", Collation: "BINARY"}}}
	viewerPlan := stmt.Plan{Hot: true, Indexes: []string{viewerKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{viewerKey}}
	def := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param, p stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: id, Owner: "store/writer UO personal", Kind: kind, SQL: sql, Params: params, CallSites: []string{"store/writer/uo_personal_flags.go:applyUOPersonalFlags"}, Plan: p}
	}
	visibilityDef := func(id stmt.ID, sql string, params []stmt.Param, p stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: id, Owner: "store/writer UO personal", Kind: stmt.Read, SQL: sql, Params: params, CallSites: []string{"store/writer/uo_flags_dispatch.go:proveUOFlagsTargetVisible"}, Plan: p}
	}
	definitions := []stmt.Definition{
		def(uoPersonalFlagsSubject, stmt.Read, `SELECT e.id FROM catalog_entities AS e
CROSS JOIN catalog_libraries AS l ON l.id=e.library_id
WHERE e.id=? AND e.library_id=? AND e.public_id=? AND e.revision=? AND e.admission_revision=? AND l.library_id=? AND e.retired=0 AND l.retired=0`,
			[]stmt.Param{i("subject"), i("library"), b("public"), i("entity_revision"), i("admission_revision"), s("library_key")}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "l"}}),
		def(uoPersonalFlagsRead, stmt.Read, `SELECT revision,watchlisted,favorite,not_interested,rating FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=? AND item_public_id=?`,
			[]stmt.Param{s("profile"), i("subject"), b("public")}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}}),
		def(uoPersonalFlagsInsert, stmt.Write, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,watchlisted,favorite,not_interested,rating) VALUES(?,?,?,?,?,?,?,?) RETURNING revision`,
			[]stmt.Param{s("profile"), b("public"), i("subject"), i("library"), i("watchlisted"), i("favorite"), i("not_interested"), ni("rating")}, stmt.Plan{Hot: true}),
		def(uoPersonalFlagsUpdate, stmt.Write, `UPDATE personal_items INDEXED BY personal_current_live SET revision=?,watchlisted=?,favorite=?,not_interested=?,rating=? WHERE profile_id=? AND item_id=? AND item_public_id=? AND revision=? RETURNING revision`,
			[]stmt.Param{i("next_revision"), i("watchlisted"), i("favorite"), i("not_interested"), ni("rating"), s("profile"), i("subject"), b("public"), i("old_revision")}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}}),
		def(uoPersonalFlagsRevisionRead, stmt.Read, `SELECT revision,selection_revision FROM viewer_revisions INDEXED BY sqlite_autoindex_viewer_revisions_1 WHERE profile_id=? AND library_id=?`,
			[]stmt.Param{s("profile"), s("library")}, viewerPlan),
		def(uoPersonalFlagsRevisionInsert, stmt.Write, `INSERT INTO viewer_revisions(profile_id,library_id,revision,selection_revision) VALUES(?,?,?,?) RETURNING revision,selection_revision`,
			[]stmt.Param{s("profile"), s("library"), i("initial_revision"), i("initial_selection")}, stmt.Plan{Hot: true}),
		def(uoPersonalFlagsRevisionUpdate, stmt.Write, `UPDATE viewer_revisions INDEXED BY sqlite_autoindex_viewer_revisions_1 SET revision=?,selection_revision=? WHERE profile_id=? AND library_id=? AND revision=? AND selection_revision=? RETURNING revision,selection_revision`,
			[]stmt.Param{i("next_revision"), i("next_selection"), s("profile"), s("library"), i("old_revision"), i("old_selection")}, viewerPlan),
		// The public item id -> its CURRENT entity/library row. The legacy PUT
		// personal-state path resolves the library through LibraryForItem, which
		// joins catalog_kinds.playable=1 and catalog_libraries.retired=0, then
		// VisibleItem rejects a retired entity. The kind name is read so a later
		// kind change cannot be mistaken for the same target.
		//
		// The bridge to libraries carries the native age-publication fence
		// (browse_statements.go "library"): a pending age operation or an input
		// age not yet published refuses rather than admitting a stale own age.
		// dvr_catalog_provenance marks a published recording, which
		// contentaccess.VisibleItemTx hides when the profile cannot record.
		visibilityDef(uoFlagsTargetEntity, `SELECT e.id,e.library_id,l.library_id,e.kind,k.name,k.playable,e.retired,e.revision,e.admission_revision,e.admission_own_age,lib.age_input_revision,lib.age_published_revision,lib.age_pending_operations,l.retired,dvr.item_id
FROM catalog_entities AS e
JOIN catalog_libraries AS l ON l.id=e.library_id
JOIN libraries AS lib ON lib.id=l.library_id
JOIN catalog_kinds AS k ON k.id=e.kind
LEFT JOIN dvr_catalog_provenance AS dvr ON dvr.item_id=e.id
WHERE e.public_id=?`,
			[]stmt.Param{b("public")}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_entities_1", "sqlite_autoindex_libraries_1"}, PrimaryKeys: []string{"l", "k", "dvr"}, Seeks: []string{"public_id=?"}}),
		// The exact current membership/profile pair the proof identity names. The
		// incarnation and lifetime values are compared with the validated proof so
		// a superseded membership/profile cannot keep an old root's write path.
		visibilityDef(uoFlagsTargetPrincipal, `SELECT m.role,m.disabled,m.allowed_libraries,m.incarnation,m.revision,p.allowed_libraries,p.deleted,p.lifetime,p.revision
FROM direct_memberships AS m
JOIN direct_profiles AS p ON p.account_id=m.account_id
WHERE m.account_id=? AND p.id=?`,
			[]stmt.Param{s("account"), s("profile")}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_profiles_2", "sqlite_autoindex_direct_memberships_1"}, Seeks: []string{"account_id=? AND id=?", "account_id=?"}}),
		// The private Recorded-TV owner of the target library, if any
		// (recordingaccess/access.go:32-48). This is a keyed primary-key read of
		// the exact native row; no grant list or role is consulted first. A miss
		// means the library is shared and the ordinary grants decide, while a
		// hit is owned by one profile: another profile is refused outright, and
		// the owner is admitted only while its Recordings switch is on, without
		// needing the library in its shared grants.
		visibilityDef(uoFlagsTargetPrivateLibrary, `SELECT authority,account_id,profile_id FROM dvr_private_libraries WHERE library_id=?`,
			[]stmt.Param{s("library")}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_dvr_private_libraries_1"}, Seeks: []string{"library_id=?"}}),
		// A missing restriction row is the documented default: nothing blocked
		// EXCEPT recordings, which are off for every profile but the server
		// owner's primary profile. The COALESCE answers identity.DefaultRestrictions
		// and defaultRestrictions exactly (identity/restrictions.go:114), so a
		// missing row cannot be read as permission to record. The principal row
		// is already proved enabled and not deleted before this read; the CASE
		// still states the full rule so it cannot drift from the identity copy.
		visibilityDef(uoFlagsTargetRestrictions, `SELECT COALESCE(r.maximum_age,-1),COALESCE(r.allow_unrated,1),COALESCE(r.blocked_labels,'[]'),COALESCE(r.revision,0),COALESCE(r.allow_dvr,CASE WHEN p.is_primary=1 AND p.deleted=0 AND m.role='owner' AND m.disabled=0 THEN 1 ELSE 0 END)
FROM direct_profiles AS p
JOIN direct_memberships AS m ON m.account_id=p.account_id
LEFT JOIN profile_restrictions AS r ON r.profile_id=p.id
WHERE p.id=?`,
			[]stmt.Param{s("profile")}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_profiles_1", "sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_profile_restrictions_1"}, Seeks: []string{"id=?", "account_id=?", "profile_id=?"}}),
		// The native read fence refuses an account carrying a stored access-limits
		// document because those account-level ceilings/labels have no native
		// predicate yet. The writer refuses rather than failing open. Accepted
		// invitations write exactly such a row (access/invitations.go:101), so
		// invited members are refused today; this MUST be evaluated (the limits
		// applied) instead of refused before the 600 primitive is wired. Native
		// returns ErrUnqualified here while this is a terminal disclosure denial.
		visibilityDef(uoFlagsTargetAccessLimits, `SELECT account_id FROM access_limits WHERE account_id=?`,
			[]stmt.Param{s("account")}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_limits_1"}, Seeks: []string{"account_id=?"}}),
	}
	public := []byte{3, 128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	bindings := [][]any{
		{int64(2111), int64(7), public, int64(1), int64(1), "library-one"},
		{"profile-one", int64(2111), public},
		{"profile-two", public, int64(2111), int64(7), int64(1), int64(0), int64(0), nil},
		{int64(2), int64(1), int64(1), int64(0), int64(0), "profile-one", int64(2111), public, int64(1)},
		{"profile-one", "library-one"},
		{"profile-one", "library-one", int64(1), int64(1)},
		{int64(2), int64(2), "profile-one", "library-one", int64(1), int64(1)},
		{public},
		{"account", "profile"},
		{"library-one"},
		{"profile"},
		{"account"},
	}
	for n := range definitions {
		definitions[n].Cohorts = []stmt.Cohort{{Name: "personal_flags_point", Bindings: bindings[n]}}
	}
	return definitions
}
