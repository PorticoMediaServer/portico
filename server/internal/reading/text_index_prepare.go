package reading

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf16"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// PrepareTextIndex builds the EPUB text index for one canonical background
// publication. It reads only admitted spine XHTML with the same complete text
// walker the counter uses, keeps the whole document or nothing, and accepts the
// result under the same source/observation fences as the text count. The derived
// text is private: it is never written into catalog metadata or returned by List
// or Search cards.
func (s *Service) PrepareTextIndex(ctx context.Context, library, item, asset string) error {
	v, err := s.resolveBackgroundDomain(ctx, textIndexWorkerDomain, library, item, asset)
	if err != nil {
		return err
	}
	// EPUB-first: no other format invents an anchor or a text producer here.
	if v.Format != "epub" {
		return nil
	}
	determined, err := s.textIndexDetermined(ctx, v)
	if err != nil || determined {
		return err
	}
	f, err := s.OpenBackground(ctx, v)
	if err != nil {
		return err
	}
	defer f.Close()
	e, err := f.epub()
	if err != nil {
		return err
	}
	sections, reason, err := extractEPUBTextIndex(ctx, e)
	if err != nil {
		return err
	}
	if err = f.Validate(); err != nil {
		return err
	}
	// The accepting writer is bound to the opened source's lifetime, not the
	// outer worker context alone. If the original lease retires while waiting
	// for the writer, the accepting queries are canceled and the commit is
	// refused, so no retired observation publishes an index.
	accept := f.ctx
	gate, err := dbwork.Begin(accept, s.DB, dbwork.ClassFrom(accept, dbwork.ClassBackgroundMedia))
	if err != nil {
		return err
	}
	defer gate.Rollback()
	current, err := s.resolveBackgroundTx(accept, gate.Tx(), textIndexWorkerDomain, library, item, asset)
	if err != nil {
		return err
	}
	if current.Revision != v.Revision || current.object != v.object || current.inventory != v.inventory || current.inventoryRow != v.inventoryRow {
		return ErrChanged
	}
	if err = adoptTextIndexTx(accept, gate.Tx(), current, sections, reason); err != nil {
		return err
	}
	if err = accept.Err(); err != nil {
		return err
	}
	return gate.Commit()
}

// extractEPUBTextIndex walks the admitted spine in order. A whole document is
// either extracted completely or declines with one bounded reason; a section
// that the complete text walker cannot read makes the document unsupported
// rather than a partial index. The walker applies the same head/script/style
// exclusion and block boundaries as the text count, and its script-segmentation
// refusal is retained, so an unsupported script becomes a determined
// zero-payload outcome instead of an estimate.
func extractEPUBTextIndex(ctx context.Context, e *EPUB) ([]textIndexSection, string, error) {
	if e == nil {
		return nil, "malformed", nil
	}
	if len(e.Spine) > maxTextIndexSections {
		return nil, "sections", nil
	}
	var sections []textIndexSection
	var sourceBytes, textBytes int64
	for ordinal, name := range e.Spine {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		file := e.archive.files[name]
		if file == nil || e.allowed[name] != "application/xhtml+xml" {
			return nil, "malformed", nil
		}
		if file.UncompressedSize64 > maxTextIndexDocument || sourceBytes+int64(file.UncompressedSize64) > maxTextIndexSource {
			return nil, "oversized", nil
		}
		data, err := e.archive.read(ctx, name, maxTextIndexDocument)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			return nil, "malformed", nil
		}
		sourceBytes += int64(len(data))
		var text strings.Builder
		if _, ok := countXHTMLTextObserved(ctx, data, func(value string) { text.WriteString(value) }); !ok {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			return nil, "malformed", nil
		}
		normalized := normalizeTextIndex(text.String())
		if normalized == "" {
			continue
		}
		textBytes += int64(len(normalized))
		if textBytes > maxTextIndexDocument {
			return nil, "oversized", nil
		}
		sections = append(sections, textIndexSection{ordinal: ordinal, path: name, text: normalized, utf16: utf16Count(normalized)})
		if len(sections) > maxTextIndexSections {
			return nil, "sections", nil
		}
	}
	return sections, "", nil
}

// normalizeTextIndex makes one stored section's text a stable, single-spaced
// string: every whitespace run becomes one space and both edges are trimmed.
// The anchor contract is expressed against this normalized text.
func normalizeTextIndex(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// utf16Count is the exact number of UTF-16 code units in a Go string.
func utf16Count(value string) int {
	count := 0
	for _, r := range value {
		count += utf16.RuneLen(r)
	}
	return count
}

// textIndexDetermined reports whether this exact observation and rule already
// carries one determined index outcome, whatever it is. The candidate window
// normally skips such a document before it is opened; this recheck closes the
// gap between the window and the accepting transaction.
func (s *Service) textIndexDetermined(ctx context.Context, selected Selection) (bool, error) {
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return false, err
	}
	defer g.Rollback()
	var cached int
	err = g.Tx().QueryRowContext(ctx, `SELECT 1 FROM reading_text_index_state
 WHERE item_id=? AND asset_id=? AND inventory_object_id=? AND source_revision=? AND index_rule_version=?`,
		selected.Entity, selected.Asset, selected.inventoryRow, selected.inventory, readingIndexRuleVersion).Scan(&cached)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}
