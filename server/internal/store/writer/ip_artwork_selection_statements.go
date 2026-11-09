package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP-21's finite selection family. Group E is not embedded yet, so these
// definitions are qualified only against the draft fixture in
// testdata/ip-artwork-selection-draft.sql (loaded after the IP-19 material and
// IP-20 publish fixtures); they make no Capture or adoption claim. The receipt
// statement is this command's OWN read: the frozen E labels 'select' and 'clear'
// map to 226/227, while any other label maps to 0 and is refused.
//
// Every read is an exact receipt/subject/selection primary key or the artwork
// source readiness point join; there is no OFFSET, temp sort or filtered scan.
// Writes are bounded PK inserts/updates/deletes, one exact subject artwork
// revision CAS and one exact subject-job CAS.
const (
	ipASReceiptOctets   stmt.ID = "ip.art.select.receipt_octets"
	ipASReceipt         stmt.ID = "ip.art.select.receipt"
	ipASSourceReady     stmt.ID = "ip.art.select.source_ready"
	ipASSelectionRead   stmt.ID = "ip.art.select.current"
	ipASSelectionInsert stmt.ID = "ip.art.select.write"
	ipASSelectionUpdate stmt.ID = "ip.art.select.update"
	ipASSelectionDelete stmt.ID = "ip.art.clear.remove"
	ipASEntityBump      stmt.ID = "ip.art.select.entity_revision"
	ipASPersonBump      stmt.ID = "ip.art.select.person_revision"
	ipASHistory         stmt.ID = "ip.art.select.history"
	ipASHistoryPin      stmt.ID = "ip.art.select.history_pin"
	ipASSave            stmt.ID = "ip.art.select.save"
)

func ipArtworkSelectionDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	defs := []stmt.Definition{
		{
			// Point octet preflight before copying the pinned role/digests. The
			// empty locked choice stores a NULL digest, so 0 bytes is valid.
			ID: ipASReceiptOctets, SQL: `SELECT coalesce(octet_length(role),0),coalesce(octet_length(selected_digest),0),coalesce(octet_length(previous_digest),0) FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			// Only a select/clear row maps to 226/227; any other frozen label at
			// this key maps to 0 and can never replay as an owner choice.
			ID: ipASReceipt, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'select' THEN 226 WHEN 'clear' THEN 227 ELSE 0 END,accepted_revision,applied_ms,result_count,coalesce(role,''),coalesce(choice,''),coalesce(selection_revision,0),coalesce(selected_digest,''),coalesce(previous_digest,''),history_id FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			// A selected source is ready custody only when all three slots are
			// present/ready and the source is not retiring. A NULL locked digest
			// is not a source and never reaches this statement.
			ID: ipASSourceReady, SQL: `SELECT 1 FROM artwork_sources s JOIN artwork_blobs b ON b.digest=s.small_blob AND b.status='ready' AND b.deletion_token IS NULL JOIN artwork_blobs l ON l.digest=s.large_blob AND l.status='ready' AND l.deletion_token IS NULL JOIN artwork_blobs a ON a.digest=s.ambient_blob AND a.status='ready' AND a.deletion_token IS NULL WHERE s.digest=? AND s.retiring=0 AND s.retirement_token IS NULL`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "b", "l", "a"}},
			Cohorts: []stmt.Cohort{
				{Name: "ready", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			// The exact current locked row. An absent row and a locked-empty row
			// (NULL digest) are different observations.
			ID: ipASSelectionRead, SQL: `SELECT revision,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,actor,source_object_id,source_object_revision FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND choice='locked'`,
			Params: []stmt.Param{t("kind"), i("subject"), t("role")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_selections"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"entity", int64(778), "poster"}},
				{Name: "missing", Bindings: []any{"entity", int64(1 << 40), "poster"}},
			},
		},
		{
			// Every v1 owner select writes choice='locked', including the
			// locked-empty choice whose digest is NULL.
			ID: ipASSelectionInsert, SQL: `INSERT INTO artwork_selections(kind,entity_id,role,choice,revision,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,actor,source_object_id,source_object_revision,observed_at) VALUES(?,?,?,'locked',?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params:  []stmt.Param{t("kind"), i("entity"), t("role"), i("revision"), nt("digest"), t("provider"), t("image"), t("origin"), t("attribution"), t("locale"), t("language"), t("fence"), t("actor"), nt("object"), nt("object_revision"), t("observed_at")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_selections"}},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{"entity", int64(778), "poster", int64(1), digest, "tmdb", "image-001", "https://example.invalid/art", "Example", "", "en", "provider-fence", "owner", nil, nil, "2026-11-08T00:00:00Z"}}},
		},
		{
			// CAS on the exact previous row revision and digest. `digest IS ?`
			// keeps the NULL locked-empty digest comparable.
			ID: ipASSelectionUpdate, SQL: `UPDATE artwork_selections SET revision=?,digest=?,provider=?,image_id=?,origin=?,attribution=?,locale=?,effective_language=?,source_fence=?,actor=?,source_object_id=?,source_object_revision=?,observed_at=? WHERE kind=? AND entity_id=? AND role=? AND choice='locked' AND revision=? AND digest IS ?`,
			Params:  []stmt.Param{i("revision"), nt("digest"), t("provider"), t("image"), t("origin"), t("attribution"), t("locale"), t("language"), t("fence"), t("actor"), nt("object"), nt("object_revision"), t("observed_at"), t("kind"), i("entity"), t("role"), i("before_revision"), nt("before_digest")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_selections"}},
			Cohorts: []stmt.Cohort{{Name: "updated", Bindings: []any{int64(2), digest, "tmdb", "image-001", "https://example.invalid/art", "Example", "", "en", "provider-fence", "owner", nil, nil, "2026-11-08T00:00:00Z", "entity", int64(778), "poster", int64(1), digest}}},
		},
		{
			// Clear deletes the locked row alone; matched rows, sources, blobs and
			// installed material are outside this statement.
			ID: ipASSelectionDelete, SQL: `DELETE FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND choice='locked' AND revision=? AND digest IS ?`,
			Params: []stmt.Param{t("kind"), i("entity"), t("role"), i("revision"), nt("digest")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_selections"}},
			Cohorts: []stmt.Cohort{
				{Name: "removed", Bindings: []any{"entity", int64(778), "poster", int64(1), digest}},
				{Name: "empty", Bindings: []any{"entity", int64(778), "poster", int64(1), nil}},
			},
		},
		{
			ID: ipASEntityBump, SQL: `UPDATE catalog_entities SET artwork_revision=artwork_revision+1 WHERE id=? AND artwork_incarnation=? AND artwork_revision=? AND retired=0`,
			Params:  []stmt.Param{i("entity"), b("incarnation"), i("revision")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}},
			Cohorts: []stmt.Cohort{{Name: "bumped", Bindings: []any{int64(778), make([]byte, 16), int64(1)}}},
		},
		{
			ID: ipASPersonBump, SQL: `UPDATE catalog_people SET artwork_revision=artwork_revision+1 WHERE id=? AND artwork_incarnation=? AND artwork_revision=?`,
			Params:  []stmt.Param{i("person"), b("incarnation"), i("revision")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}},
			Cohorts: []stmt.Cohort{{Name: "bumped", Bindings: []any{int64(778), make([]byte, 16), int64(1)}}},
		},
		{
			// Legacy audit JSON is history only; the exact digest closure is the
			// metadata_owner_history_artwork pin below.
			ID: ipASHistory, SQL: `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES(?,?,?,?,?,?,?,?,?) RETURNING id`,
			Params:  []stmt.Param{t("kind"), i("entity"), t("base"), t("revision"), t("trigger"), t("actor"), t("observed_at"), t("before"), t("after")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_owner_history"}},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{"entity", int64(778), "1", "2", "artwork_select", "owner", "2026-11-08T00:00:00Z", "{}", "{}"}}},
		},
		{
			// A locked-empty choice has no digest and therefore no pin.
			ID: ipASHistoryPin, SQL: `INSERT INTO metadata_owner_history_artwork(history_id,role,choice,digest) VALUES(?,?,'locked',?)`,
			Params:  []stmt.Param{i("history"), t("role"), t("digest")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_owner_history_artwork"}},
			Cohorts: []stmt.Cohort{{Name: "pinned", Bindings: []any{int64(1), "poster", digest}}},
		},
		{
			// The receipt carries the closed owner choice witness. `history_id` is
			// set exactly when the command changed the row.
			ID: ipASSave, SQL: `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,lease_generation,lease_until_ms,role,choice,selection_revision,selected_digest,previous_digest,history_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'locked',?,?,?,?)`,
			Params:  []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("applied"), b("result"), t("kind"), i("accepted"), i("count"), i("lease_generation"), i("lease_until"), t("role"), i("selection_revision"), nt("selected"), nt("previous"), ni("history")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(901), make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), "select", int64(1), int64(1), int64(1), int64(1 << 50), "poster", int64(1), digest, nil, int64(1)}}},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipASSelectionInsert, ipASSelectionUpdate, ipASSelectionDelete, ipASEntityBump, ipASPersonBump,
			ipASHistory, ipASHistoryPin, ipASSave:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP artwork selection"
		defs[n].CallSites = []string{"store/writer/ip_artwork_selection_native.go"}
	}
	return defs
}
