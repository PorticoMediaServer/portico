package reading

import "github.com/PorticoMediaServer/portico/server/internal/readingcap"

// Source formats describe original physical publications, never engine formats.
// SQL fragments contain only fixed literals; no caller text participates.
func (s *Service) bookFormatsSQL() string {
	if s.Mobi != nil {
		return "'epub','pdf','mobi','azw3'"
	}
	return "'epub','pdf'"
}
func (s *Service) sourceFormatsSQL() string {
	formats := "'epub','pdf'," + readingcap.ComicArchiveFormatsSQL()
	if s.Mobi != nil {
		formats += ",'mobi','azw3'"
	}
	return formats
}

// SourceFormatScope is the exact runtime source-format domain this build admits.
// Cursors and admission witnesses include it so a build with a different
// admitted domain cannot reuse their pages or facts.
func (s *Service) SourceFormatScope() string { return s.sourceFormatsSQL() }

// Apply conversion readiness BEFORE choosing a primary version and LIMIT, so an
// unsupported conversion cannot conceal an otherwise eligible native version.
// PrimaryPublicationSQL exposes the canonical primary-version rule to the
// registered preparation worker without duplicating the MOBI-aware SQL.
func (s *Service) PrimaryPublicationSQL() string { return s.primaryPublicationSQL() }

func (s *Service) primaryPublicationSQL() string {
	return `(SELECT candidate.asset_id FROM reading_publications candidate WHERE candidate.item_id=e.id AND candidate.format IN(` + s.sourceFormatsSQL() + `) AND ` + readingcap.LocalComicArchiveSQL("candidate") + `
 ORDER BY EXISTS(SELECT 1 FROM inventory_item_availability eligible WHERE eligible.item_id=candidate.item_id AND eligible.asset_id=(SELECT token FROM catalog_assets WHERE id=candidate.asset_id) AND eligible.available=1) DESC,candidate.asset_id LIMIT 1)`
}
