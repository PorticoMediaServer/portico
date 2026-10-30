// Package metadatalanguage reads the catalogue's published original-language
// fact. It never infers it from a library preference or a file's audio tracks.
package metadatalanguage

import (
	"context"
	"database/sql"
	"strings"

	"golang.org/x/text/language"
)

// Primary returns a BCP 47 primary subtag, or empty for an unknown language.
// The providers and stream tags share this spelling; cn is TMDB's Cantonese
// code, not the Chinese macrolanguage.
func Primary(raw string) string {
	if strings.Contains(raw, "_") {
		return ""
	}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "-")
	switch parts[0] {
	case "", "xx", "und", "mul", "zxx":
		return ""
	case "cn":
		parts[0] = "yue"
	case "jpn":
		parts[0] = "ja"
	case "chi", "zho":
		parts[0] = "zh"
	}
	tag, err := language.Parse(strings.Join(parts, "-"))
	if err != nil {
		return ""
	}
	base, _ := tag.Base()
	if base.String() == "und" {
		return ""
	}
	return base.String()
}

type Reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Published reads only a current accepted provider publication. The caller
// must first authorize visibility of the title in the same read snapshot.
// Movies use their own fact; episodes and seasons use their show's fact.
// Library metadata_language, owner overrides and client values are not inputs.
func Published(ctx context.Context, db Reader, publicID string) (string, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT json_extract(f.value,'$')
 FROM screen_metadata_fields f JOIN screen_metadata_work w ON w.target_kind=f.target_kind AND w.target_id=f.target_id AND w.accepted_publication=f.publication_id
 JOIN screen_metadata_publications p ON p.id=f.publication_id AND p.target_kind=f.target_kind AND p.target_id=f.target_id AND p.decision='accepted' AND p.provider=f.provider
 WHERE f.field='language' AND f.provider IN('tmdb','tvdb','anilist') AND json_type(f.value)='text'
 AND f.target_kind=CASE WHEN e.kind=1 THEN 'item' ELSE 'show' END
 AND f.target_id=CASE e.kind WHEN 1 THEN e.id WHEN 2 THEN e.id WHEN 3 THEN se.show_id WHEN 4 THEN ep.show_id END),'')
 FROM catalog_entities e LEFT JOIN catalog_seasons se ON se.entity_id=e.id LEFT JOIN catalog_episodes ep ON ep.entity_id=e.id
 WHERE e.public_id=pid_blob(?) AND e.retired=0`, publicID).Scan(&raw)
	return Primary(raw), err
}
