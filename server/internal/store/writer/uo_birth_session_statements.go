package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoBirthSessionLogical  stmt.ID = "uo.birth.session.logical"
	uoBirthSessionPhysical stmt.ID = "uo.birth.session.physical"
)

// Existing native session/physical rows only. No new DDL: the logical row's
// device-start-key UNIQUE and the physical occurrence PK already own birth
// identity; both carry separate command incarnations with zero worker counters.
func uoBirthSessionDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	return []stmt.Definition{
		{ID: uoBirthSessionLogical, Kind: stmt.Read, Owner: "store/writer UO exact 710 logical session birth", CallSites: []string{"store/writer/uo_birth_session.go"},
			SQL:     `SELECT 1 FROM playback_v1_sessions INDEXED BY mp_session_command_owner WHERE id=? AND device_id=? AND account_id=? AND profile_id=? AND start_key=? AND start_digest=? AND revision=1 AND state=? AND media_session_id=? AND media_generation=? AND lease_expires_ms=? AND created_ms=? AND mp_command_incarnation=? AND mp_user_root_id=? AND mp_owner_member_incarnation=? AND mp_owner_profile_lifetime=? AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0 AND mp_owner_revision=1 AND mp_receipt_rows=0 AND mp_receipt_variable_bytes=0`,
			Params:  []stmt.Param{t("session"), t("device"), t("account"), t("profile"), t("start_key"), t("start_digest"), t("state"), t("physical"), i("physical_generation"), i("lease_expires"), i("created"), b("logical"), t("root"), t("member"), t("lifetime")},
			Cohorts: []stmt.Cohort{{Name: "unqualified_exact_session_birth", Bindings: []any{"ps_original", "issuer-device", "issuer", "issuer-profile", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "playing", "media-original", int64(1), int64(4102444800000), int64(1762641000000), []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "abcdefghijklmnopqrstuv", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"mp_session_command_owner"}, Seeks: []string{"id=? AND mp_command_incarnation=?"}}},

		{ID: uoBirthSessionPhysical, Kind: stmt.Read, Owner: "store/writer UO exact 710 physical session birth", CallSites: []string{"store/writer/uo_birth_session.go"},
			SQL:     `SELECT 1 FROM playback_sessions INDEXED BY mp_physical_command_owner WHERE id=? AND generation=? AND mp_command_incarnation=? AND mp_user_root_id=? AND account_id=? AND profile_id=? AND mp_owner_member_incarnation=? AND mp_owner_profile_lifetime=? AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0 AND mp_owner_revision=1 AND mp_receipt_rows=0 AND mp_receipt_variable_bytes=0`,
			Params:  []stmt.Param{t("physical"), i("generation"), b("physical_incarnation"), t("root"), t("account"), t("profile"), t("member"), t("lifetime")},
			Cohorts: []stmt.Cohort{{Name: "unqualified_exact_physical_birth", Bindings: []any{"media-original", int64(1), []byte{10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "abcdefghijklmnopqrstuv", "issuer", "issuer-profile", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"mp_physical_command_owner"}, Seeks: []string{"id=? AND mp_command_incarnation=?"}}},
	}
}
