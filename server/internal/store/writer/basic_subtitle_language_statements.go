package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicSubtitleLanguageRead   stmt.ID = "scan.basic_stage.subtitle_language_read"
	basicSubtitleLanguageInsert stmt.ID = "scan.basic_stage.subtitle_language_insert"
	basicSubtitleLanguageCAS    stmt.ID = "scan.basic_stage.subtitle_language_cas"
	basicSubtitleLanguageDelete stmt.ID = "scan.basic_stage.subtitle_language_delete"
)

func basicSubtitleLanguageDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	defs := []stmt.Definition{
		{ID: basicSubtitleLanguageRead, Kind: stmt.Read, SQL: `SELECT tracks FROM asset_subtitle_languages WHERE language=?`, Params: []stmt.Param{t("language")}, Cohorts: []stmt.Cohort{{Name: "current_language", Bindings: []any{"en"}}}},
		{ID: basicSubtitleLanguageInsert, Kind: stmt.Write, SQL: `INSERT INTO asset_subtitle_languages(language,tracks) VALUES(?,?) RETURNING language,tracks`, Params: []stmt.Param{t("language"), i("initial_tracks")}, Cohorts: []stmt.Cohort{{Name: "new_language", Bindings: []any{"fr", int64(1)}}}},
		{ID: basicSubtitleLanguageCAS, Kind: stmt.Write, SQL: `UPDATE asset_subtitle_languages SET tracks=? WHERE language=? AND tracks=? RETURNING language,tracks`, Params: []stmt.Param{i("next_tracks"), t("language"), i("old_tracks")}, Cohorts: []stmt.Cohort{{Name: "exact_count_delta", Bindings: []any{int64(2), "en", int64(1)}}}},
		{ID: basicSubtitleLanguageDelete, Kind: stmt.Write, SQL: `DELETE FROM asset_subtitle_languages WHERE language=? AND tracks=? RETURNING language,tracks`, Params: []stmt.Param{t("language"), i("old_tracks")}, Cohorts: []stmt.Cohort{{Name: "last_track_leaves", Bindings: []any{"en", int64(1)}}}},
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "store/writer Basic domain"
		d.CallSites = []string{"store/writer/basic_subtitle_languages.go"}
		d.Plan.Hot = true
		if d.ID != basicSubtitleLanguageInsert {
			d.Plan.PrimaryKeys = []string{"asset_subtitle_languages"}
			d.Plan.Seeks = []string{"language=?"}
		}
	}
	return defs
}
