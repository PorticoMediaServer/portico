package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const uoBirthStartNative stmt.ID = "uo.birth.start.native"

func uoBirthStartDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	return []stmt.Definition{{ID: uoBirthStartNative, Kind: stmt.Read, Owner: "store/writer UO exact 700 birth original", CallSites: []string{"store/writer/uo_birth_start.go"},
		SQL:     `SELECT 1 FROM playback_start_preparations INDEXED BY mp_start_command_owner WHERE id=? AND device_id=? AND start_key_digest=? AND start_body_digest=? AND item_id=? AND asset_id=? AND part_index=? AND replaces_session_id=? AND replaces_generation=? AND token_expires_ms=? AND created_ms=? AND deadline_ms=? AND mp_command_incarnation=? AND mp_user_root_id=? AND mp_owner_member_incarnation=? AND mp_owner_profile_lifetime=? AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0 AND mp_owner_revision=1 AND mp_receipt_rows=0 AND mp_receipt_variable_bytes=0`,
		Params:  []stmt.Param{t("preparation"), t("device"), t("key_digest"), t("body_digest"), t("item"), t("asset"), i("part"), t("replaces"), i("replaces_generation"), i("token_expires"), i("created"), i("deadline"), b("worker"), t("root"), t("member"), t("lifetime")},
		Cohorts: []stmt.Cohort{{Name: "unqualified_exact_birth_shape", Bindings: []any{"preparation-original", "issuer-device", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "original-item", "original-asset", int64(0), "", int64(0), int64(4102444800000), int64(1762641000000), int64(1762641060000), []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "abcdefghijklmnopqrstuv", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}},

		Plan: stmt.Plan{Hot: true, Indexes: []string{"mp_start_command_owner"}, Seeks: []string{"id=? AND mp_command_incarnation=?"}},
	}}
}
