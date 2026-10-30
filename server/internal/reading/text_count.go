package reading

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

// A positive bump makes every existing reading_text_counts row pending once:
// the cache join, the pending window and every consumer require this exact
// rule. Version 2 adds the exact PDF Info/Author determination to the same
// preparation, so already-counted PDFs run the bounded helper again.
const readingTextRuleVersion = readingcap.TextCountRuleVersion
const readingPageRuleVersion = 1
const maxCountDocument = 8 << 20
const maxCountPublication = 64 << 20

// Opening text is retained only from complete, valid admitted body text. It is
// a bounded excerpt, never an estimate or HTML returned for execution.
func inspectEPUBText(ctx context.Context, e *EPUB) (*int64, string, error) {
	var total, words int64
	opening := ""
	for _, name := range e.Spine {
		file := e.archive.files[name]
		if file == nil || e.allowed[name] != "application/xhtml+xml" || file.UncompressedSize64 > maxCountDocument || file.UncompressedSize64 > uint64(maxCountPublication-total) {
			return nil, opening, nil
		}
		data, err := e.archive.read(ctx, name, maxCountDocument)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			return nil, "", err
		}
		total += int64(len(data))
		excerpt := &openingText{}
		count, ok := countXHTMLTextObserved(ctx, data, excerpt.add)
		if !ok {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			return nil, opening, nil
		}
		words += count
		if opening == "" {
			opening = excerpt.text.String()
		}
	}
	return &words, opening, nil
}

type openingText struct {
	text        strings.Builder
	space, full bool
}

func (o *openingText) add(value string) {
	if o.full {
		return
	}
	for _, r := range value {
		if unicode.IsSpace(r) {
			o.space = o.text.Len() > 0
			continue
		}
		size := utf8.RuneLen(r)
		if o.space {
			size++
		}
		if o.text.Len()+size > 4096 {
			o.full = true
			return
		}
		if o.space {
			o.text.WriteByte(' ')
			o.space = false
		}
		o.text.WriteRune(r)
	}
}

func countXHTMLTextObserved(ctx context.Context, data []byte, observe func(string)) (int64, bool) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	const xhtml = "http://www.w3.org/1999/xhtml"
	depth, body, skip := 0, 0, 0
	root, closed, bodySeen := false, false, false
	var words int64
	inWord := false
	boundary := func() { inWord = false }
	for {
		if ctx.Err() != nil {
			return 0, false
		}
		token, err := d.Token()
		if err == io.EOF {
			return words, root && closed && bodySeen && depth == 0
		}
		if err != nil {
			return 0, false
		}
		switch v := token.(type) {
		case xml.Directive:
			return 0, false
		case xml.StartElement:
			depth++
			if depth > 256 || closed || v.Name.Space != xhtml {
				return 0, false
			}
			if !root {
				if depth != 1 || v.Name.Local != "html" {
					return 0, false
				}
				root = true
			}
			if v.Name.Local == "body" {
				if depth != 2 || bodySeen {
					return 0, false
				}
				body = depth
				bodySeen = true
			}
			if skip == 0 && (v.Name.Local == "script" || v.Name.Local == "style" || v.Name.Local == "template") {
				skip = depth
				boundary()
			}
			if textBlock(v.Name.Local) {
				boundary()
				if observe != nil && body > 0 && skip == 0 {
					observe(" ")
				}
			}
		case xml.EndElement:
			if textBlock(v.Name.Local) {
				boundary()
				if observe != nil && body > 0 && skip == 0 {
					observe(" ")
				}
			}
			if depth == skip {
				skip = 0
				boundary()
			}
			if depth == body {
				body = 0
				boundary()
			}
			depth--
			if depth == 0 {
				closed = true
			}
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(v)) != 0 {
				return 0, false
			}
			if body == 0 || skip != 0 {
				continue
			}
			if observe != nil {
				observe(string(v))
			}
			for _, r := range string(v) {
				if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar) {
					return 0, false
				}
				if unicode.IsLetter(r) || unicode.IsNumber(r) {
					if !inWord {
						words++
						inWord = true
					}
				} else if !unicode.IsMark(r) {
					boundary()
				}
			}
		}
	}
}
func textBlock(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "br", "dd", "div", "dl", "dt", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table", "td", "th", "tr", "ul":
		return true
	}
	return false
}

// textCountCached reports whether this exact admitted row/revision/rule (and
// the current opening-text rule) is already carried. A cached NULL word_count is
// a determined unsupported/partial publication fact, not a transient
// acquisition outcome. PDF rows also need the current structural page rule: a
// known word count without a validated page fact is still pending coverage.
func (s *Service) textCountCached(ctx context.Context, selected Selection) (bool, error) {
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return false, err
	}
	defer g.Rollback()
	query := `SELECT 1 FROM reading_text_counts WHERE item_id=? AND asset_id=? AND inventory_object_id=? AND source_revision=? AND rule_version=? AND opening_rule_version=1`
	if selected.Format == "pdf" {
		query += ` AND page_rule_version=1 AND page_count BETWEEN 1 AND 4096`
	}
	var cached int
	err = g.Tx().QueryRowContext(ctx, query, selected.Entity, selected.Asset, selected.inventoryRow, selected.inventory, readingTextRuleVersion).Scan(&cached)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

// adoptTextCountRow writes the one row for this exact item/asset; the caller
// has just rechecked that the selection is still current under its own opener.
// Opening text is retained only from the same complete, valid extraction.
func adoptTextCountRow(ctx context.Context, tx *sql.Tx, current Selection, count *int64, opening string, pageCount *int64, pageRule int) error {
	var openingValue any
	if opening != "" {
		openingValue = opening
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO reading_text_counts(item_id,asset_id,word_count,inventory_object_id,source_revision,rule_version,opening_lines,opening_rule_version,page_count,page_rule_version) VALUES(?,?,?,?,?,?,?,1,?,?) ON CONFLICT(item_id,asset_id) DO UPDATE SET word_count=excluded.word_count,inventory_object_id=excluded.inventory_object_id,source_revision=excluded.source_revision,rule_version=excluded.rule_version,opening_lines=excluded.opening_lines,opening_rule_version=excluded.opening_rule_version,page_count=excluded.page_count,page_rule_version=excluded.page_rule_version WHERE reading_text_counts.word_count IS NOT excluded.word_count OR reading_text_counts.inventory_object_id<>excluded.inventory_object_id OR reading_text_counts.source_revision<>excluded.source_revision OR reading_text_counts.rule_version<>excluded.rule_version OR reading_text_counts.opening_lines IS NOT excluded.opening_lines OR reading_text_counts.opening_rule_version<>1 OR reading_text_counts.page_count IS NOT excluded.page_count OR reading_text_counts.page_rule_version<>excluded.page_rule_version`, current.Entity, current.Asset, count, current.inventoryRow, current.inventory, readingTextRuleVersion, openingValue, pageCount, pageRule)
	return err
}

// Reuse one exact admitted source; never acquire a second version or retain an
// actor credential for background work. Cache only this row/revision/rule.
// Comics are not text publications; their page count is adopted separately.
func (s *Service) adoptTextCount(ctx context.Context, p identity.Principal, f *Source) error {
	selected := f.Selection
	// Viewer opens count EPUB only. Fixed-layout PDFs are covered by the
	// preparation worker so opening one does not synchronously parse the file.
	if selected.Format != "epub" {
		return nil
	}
	cached, err := s.textCountCached(ctx, selected)
	if err != nil || cached {
		return err
	}
	e, err := f.epub()
	if err != nil {
		return err
	}
	count, opening, err := inspectEPUBText(ctx, e)
	if err != nil {
		return err
	}
	if err = f.Validate(); err != nil {
		return err
	}
	gate, err := dbwork.Begin(ctx, s.DB, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gate.Rollback()
	current, err := s.selectTx(ctx, gate.Tx(), p, selected.ItemID, selected.SourceID)
	if err != nil {
		return err
	}
	if current.Revision != selected.Revision || current.inventoryRow != selected.inventoryRow || current.object != selected.object || current.inventory != selected.inventory {
		return ErrChanged
	}
	if err = adoptTextCountRow(ctx, gate.Tx(), current, count, opening, nil, 0); err != nil {
		return err
	}
	return gate.Commit()
}

// adoptComicPageCount retains the inspected comic archive's exact page count.
// The count is the same complete, naturally ordered member list Prepare already
// validated; no words or reading time are estimated for it. The writer rechecks
// the same current observation before it publishes, and an unchanged count
// writes nothing.
func (s *Service) adoptComicPageCount(ctx context.Context, p identity.Principal, selected Selection, pages int) error {
	if (selected.Format != "cbz" && selected.Format != "cb7" && selected.Format != "cbr") || pages < 1 || pages > MaxEntries {
		return nil
	}
	cached, err := s.comicPageCountCached(ctx, selected, pages)
	if err != nil || cached {
		return err
	}
	gate, err := dbwork.Begin(ctx, s.DB, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gate.Rollback()
	current, err := s.selectTx(ctx, gate.Tx(), p, selected.ItemID, selected.SourceID)
	if err != nil {
		return err
	}
	if current.Revision != selected.Revision || current.inventoryRow != selected.inventoryRow || current.object != selected.object || current.inventory != selected.inventory {
		return ErrChanged
	}
	count := int64(pages)
	if err = adoptTextCountRow(ctx, gate.Tx(), current, nil, "", &count, readingPageRuleVersion); err != nil {
		return err
	}
	return gate.Commit()
}

func (s *Service) comicPageCountCached(ctx context.Context, selected Selection, pages int) (bool, error) {
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return false, err
	}
	defer g.Rollback()
	var cached int
	err = g.Tx().QueryRowContext(ctx, `SELECT 1 FROM reading_text_counts WHERE item_id=? AND asset_id=? AND inventory_object_id=? AND source_revision=? AND rule_version=? AND page_rule_version=? AND page_count=?`, selected.Entity, selected.Asset, selected.inventoryRow, selected.inventory, readingTextRuleVersion, readingPageRuleVersion, pages).Scan(&cached)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

// This cached file text is published only by a prepared, source-authorized
// manifest. List/Search metadata never receives the body excerpt.
func (s *Service) publicationTextFacts(ctx context.Context, p identity.Principal, selected Selection) (*int64, string, *int64, error) {
	if selected.Format != "epub" && selected.Format != "pdf" {
		return nil, "", nil, nil
	}
	gate, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return nil, "", nil, err
	}
	defer gate.Rollback()
	current, err := s.selectTx(ctx, gate.Tx(), p, selected.ItemID, selected.SourceID)
	if err != nil {
		return nil, "", nil, err
	}
	if current.Revision != selected.Revision || current.inventoryRow != selected.inventoryRow || current.inventory != selected.inventory {
		return nil, "", nil, ErrChanged
	}
	var count, pages sql.NullInt64
	var opening sql.NullString
	var rule, pageRule int
	err = gate.Tx().QueryRowContext(ctx, `SELECT word_count,opening_lines,opening_rule_version,page_count,page_rule_version FROM reading_text_counts WHERE item_id=? AND asset_id=? AND inventory_object_id=? AND source_revision=? AND rule_version=?`, current.Entity, current.Asset, current.inventoryRow, current.inventory, readingTextRuleVersion).Scan(&count, &opening, &rule, &pages, &pageRule)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil, nil
	}
	if err != nil {
		return nil, "", nil, err
	}
	var words *int64
	if count.Valid {
		value := count.Int64
		words = &value
	}
	var pageCount *int64
	if selected.Format == "pdf" && pageRule == readingPageRuleVersion && pages.Valid && pages.Int64 >= 1 && pages.Int64 <= 4096 {
		value := pages.Int64
		pageCount = &value
	}
	text := ""
	if rule == 1 && opening.Valid {
		text = opening.String
	}
	return words, text, pageCount, nil
}
