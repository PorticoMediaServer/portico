package reading

import (
	"context"
	"database/sql"
	"strings"
)

// CoverAdopter stores bounded publication image bytes through the ordinary
// artwork pipeline, checking the original source inside its accepting writer.
type CoverAdopter func(context.Context, string, string, []byte, func(*sql.Tx) error) error

const maxAutomaticCover = 10 << 20

// epubCover selects only an explicit EPUB cover item. Multiple different cover
// declarations are unknown. Every returned path was admitted by the manifest;
// no filename guess, external reference or arbitrary archive entry is used.
func epubCover(p packageXML, ids, allowed map[string]string) string {
	if p.XMLName.Space != "http://www.idpf.org/2007/opf" || p.Metadata.XMLName.Space != p.XMLName.Space {
		return ""
	}
	candidates := map[string]bool{}
	add := func(name string) {
		switch allowed[name] {
		case "image/jpeg", "image/png", "image/webp":
			candidates[name] = true
		}
	}
	for _, item := range p.Manifest {
		for _, property := range strings.Fields(item.Properties) {
			if property == "cover-image" {
				add(ids[item.ID])
			}
		}
	}
	for _, meta := range p.Metadata.Meta {
		if meta.Name == "cover" {
			add(ids[meta.Content])
		}
	}
	if len(candidates) != 1 {
		return ""
	}
	for name := range candidates {
		return name
	}
	return ""
}

// Cover failures are optional metadata failures. The original source must
// still validate after the work: cancellation, revocation or replacement can
// never be hidden by an unavailable/invalid cover image.
func (s *Service) adoptPublicationCover(f *Source, document Publication) error {
	if s.AdoptCover == nil || document.cover == nil {
		return f.Validate()
	}
	raw, err := document.cover()
	if err == nil {
		if err = f.Validate(); err != nil {
			return err
		}
		_ = s.AdoptCover(f.ctx, f.ItemID, f.Revision, raw, func(tx *sql.Tx) error { return f.CheckSelectionTx(f.ctx, tx) })
	}
	return f.Validate()
}
