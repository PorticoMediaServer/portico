package reading

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

type Publication struct {
	cover      func() ([]byte, error)
	Conversion *MobiProvenance
	Sequential bool
	Selection  Selection
	Metadata   Metadata
	// EmbeddedAuthor is the package's own single creator, when it carries
	// exactly one exact value. It is evidence for adoption only; it never
	// substitutes for the catalogue's accepted author.
	EmbeddedAuthor       string
	EmbeddedSeries       string
	EmbeddedSeriesNumber *float64
	Resources            []Resource
	Spine                []string
	PageNames            []string
	Contents             []ContentsEntry
	PublicationType      *compactcatalog.ReadingValue
}

// Inspect returns only actual, bounded package/archival facts. PDF page count
// and outline belong to the real client PDF parser, never a server estimate.
func Inspect(f *Source) (Publication, error) {
	out := Publication{Sequential: f.Format == "pdf" && f.Sequential(), Selection: f.PublicationSelection(), Conversion: f.ConversionProvenance(), Metadata: Metadata{Title: f.Title, Language: f.Language}}
	switch out.Selection.Format {
	case "epub":
		e, err := f.epub()
		if err != nil {
			return out, err
		}
		out.Metadata.Direction = e.Metadata.Direction
		out.Metadata.ExplicitPublicationType = e.Metadata.ExplicitPublicationType
		out.Metadata.PublicationTypeField, out.Metadata.PublicationTypeRaw, out.Metadata.PublicationTypeComplete = e.Metadata.PublicationTypeField, e.Metadata.PublicationTypeRaw, e.Metadata.PublicationTypeComplete
		out.EmbeddedAuthor = acceptedEmbeddedAuthor(e.Metadata.Author)
		out.EmbeddedSeries, out.EmbeddedSeriesNumber = acceptedEmbeddedSeries(e.Series, e.SeriesNumber)
		if e.CoverResource != "" {
			out.cover = func() ([]byte, error) { return e.archive.read(f.ctx, e.CoverResource, maxAutomaticCover) }
		}
		out.Resources = e.Resources
		out.Spine = e.Spine
		out.Contents, err = e.Contents(f.ctx)
		if err != nil {
			return out, err
		}
		if pageList, pageErr := e.PageListCount(f.ctx); pageErr != nil {
			return out, pageErr
		} else if pageList != nil {
			out.Metadata.PageCount = pageList
		}
	case "cbz", "cb7", "cbr":
		c, err := f.comic()
		if err != nil {
			return out, err
		}
		out.Metadata.Direction = c.Metadata.Direction
		out.Metadata.ExplicitPublicationType = c.Metadata.ExplicitPublicationType
		out.Metadata.PublicationTypeField, out.Metadata.PublicationTypeRaw, out.Metadata.PublicationTypeComplete = c.Metadata.PublicationTypeField, c.Metadata.PublicationTypeRaw, c.Metadata.PublicationTypeComplete
		out.EmbeddedAuthor = acceptedEmbeddedAuthor(c.Metadata.Author)
		out.EmbeddedSeries, out.EmbeddedSeriesNumber = acceptedEmbeddedSeries(c.Series, c.SeriesNumber)
		if len(c.Names) > 0 {
			out.cover = func() ([]byte, error) { return c.readMember(f.ctx, c.Names[0], maxAutomaticCover) }
		}
		out.PageNames = c.Names
	case "pdf":
		if err := f.CheckPDF(); err != nil {
			return out, err
		}
		if f.service != nil && f.service.PDFRaster != nil && f.LocalArchiveStaging() {
			out.cover = func() ([]byte, error) {
				return f.service.PDFRaster.RenderCoverPNG(f.ctx, f, f.Size)
			}
		}
	default:
		return out, ErrInvalid
	}
	if err := f.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

// acceptedEmbeddedAuthor keeps only an exact, storable creator value. A value
// the accepted-value reader would reject would make the whole title's accepted
// facts unreadable, so such a value publishes nothing rather than a repaired
// name. The file's own string is otherwise kept exactly, never split.
func acceptedEmbeddedAuthor(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}

// acceptedEmbeddedSeries keeps one exact series value and, only when it parses
// as a non-negative number, its position. A non-numeric position stays unknown
// instead of becoming an ordinal guess; the series itself still publishes.
func acceptedEmbeddedSeries(value, number string) (string, *float64) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", nil
	}
	number = strings.TrimSpace(number)
	if number == "" {
		return value, nil
	}
	n, err := strconv.ParseFloat(number, 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || n < 0 {
		return value, nil
	}
	return value, &n
}

// adoptEmbeddedFacts publishes the publication's own accepted facts as
// file-metadata evidence. It is called only from the Prepare that opened this
// exact source. The writer rechecks the same current inventory observation and
// same-source association in its own transaction before it writes, and an
// unchanged observation writes nothing. Owner overrides keep their existing
// precedence over these rows. The normalized accepted fields carry no title, so
// the embedded title is deliberately not published here; the catalogue title
// remains the path rule's title.
func (s *Service) adoptEmbeddedFacts(ctx context.Context, p identity.Principal, selected Selection, author, series string, number *float64) error {
	if (author == "" && series == "") || (selected.Format != "epub" && selected.Format != "cbz" && selected.Format != "cb7" && selected.Format != "cbr") {
		return nil
	}
	if s == nil || s.DB == nil {
		return ErrUnavailable
	}
	gate, err := dbwork.Begin(ctx, s.DB, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gate.Rollback()
	tx := gate.Tx()
	current, err := s.selectTx(ctx, tx, p, selected.ItemID, selected.SourceID)
	if err != nil {
		return err
	}
	// The opened publication is the evidence. A different current revision is a
	// different file, so its package facts are never attributed to this one.
	if current.Revision != selected.Revision || current.object != selected.object || current.inventory != selected.inventory || current.inventoryRow != selected.inventoryRow {
		return ErrChanged
	}
	if err = adoptFileMetadataTx(ctx, tx, current, author, series, number); err != nil {
		return err
	}
	return gate.Commit()
}

// fileMetadataFact is one file-metadata row's value and evidence. A field
// absent from a field map is unknown and keeps its original row and evidence.
type fileMetadataFact struct {
	text, object, revision string
	number                 sql.NullFloat64
}

var fileMetadataFields = [...]string{"author", "series", "seriesNumber"}

func requestedFileMetadataFact(text string, number sql.NullFloat64, current Selection) fileMetadataFact {
	fact := fileMetadataFact{text: text, number: number}
	if text != "" || number.Valid {
		fact.object, fact.revision = current.inventoryRow, current.inventory
	}
	return fact
}

// adoptFileMetadataTx publishes normalized file-metadata facts for the exact
// current observation. The caller holds the gate transaction and has already
// rechecked the observed item/asset/object/revision tuple, and it commits after
// this returns. Only author, series and seriesNumber are written; the writer
// rechecks the same current object at insertion and refuses a changed one.
// Owner overrides keep their existing precedence over these automatic values.
func adoptFileMetadataTx(ctx context.Context, tx *sql.Tx, current Selection, author, series string, number *float64) error {
	seriesNumber := sql.NullFloat64{}
	if series != "" && number != nil {
		seriesNumber = sql.NullFloat64{Float64: *number, Valid: true}
	}
	return adoptFileMetadataFieldsTx(ctx, tx, current, map[string]fileMetadataFact{
		"author":       requestedFileMetadataFact(author, sql.NullFloat64{}, current),
		"series":       requestedFileMetadataFact(series, sql.NullFloat64{}, current),
		"seriesNumber": requestedFileMetadataFact("", seriesNumber, current),
	})
}

// adoptFileMetadataFieldsTx writes only the fields present in fields. Unknown
// fields keep their original rows and evidence rather than being cleared or
// copied onto this observation.
func adoptFileMetadataFieldsTx(ctx context.Context, tx *sql.Tx, current Selection, fields map[string]fileMetadataFact) error {
	old := map[string]fileMetadataFact{}
	rows, err := tx.QueryContext(ctx, `SELECT field,COALESCE(text_value,''),number_value,source_revision,inventory_object_id FROM reading_source_values WHERE item_id=? AND asset_id=? AND source='file-metadata' AND field IN('author','series','seriesNumber')`, current.Entity, current.Asset)
	if err != nil {
		return err
	}
	for rows.Next() {
		var field string
		var value fileMetadataFact
		if err = rows.Scan(&field, &value.text, &value.number, &value.revision, &value.object); err != nil {
			rows.Close()
			return err
		}
		old[field] = value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	changed := false
	for _, field := range fileMetadataFields {
		fact, ok := fields[field]
		if ok && old[field] != fact {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	for _, field := range fileMetadataFields {
		if _, ok := fields[field]; !ok {
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM reading_source_values WHERE item_id=? AND asset_id=? AND source='file-metadata' AND field=?`, current.Entity, current.Asset, field); err != nil {
			return err
		}
	}
	for _, field := range fileMetadataFields {
		fact, ok := fields[field]
		if !ok || fact.text == "" && !fact.number.Valid {
			continue
		}
		var text, value any
		if fact.number.Valid {
			value = fact.number.Float64
		} else {
			text = fact.text
		}
		// Recheck the exact observation at publication, as the path publisher
		// does. The insert selects only a current object of this same source.
		result, err := tx.ExecContext(ctx, `INSERT INTO reading_source_values(item_id,asset_id,field,text_value,number_value,source,inventory_object_id,source_revision)
  SELECT ?1,?2,?3,?4,?5,'file-metadata',o.id,o.revision FROM inventory_objects o
  JOIN catalog_assets a ON a.token=o.asset_id AND a.id=?2
  JOIN reading_publications p ON p.item_id=?1 AND p.asset_id=a.id
  JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id
  JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_libraries cl ON cl.id=e.library_id
  JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id
  WHERE o.id=?6 AND o.revision=?7 AND o.root_incarnation=s.incarnation AND o.retired=0`, current.Entity, current.Asset, field, text, value, current.inventoryRow, current.inventory)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return ErrChanged
		}
	}
	automatic, err := compactcatalog.ReadingAutomaticFieldsTx(ctx, tx, current.Entity)
	if err != nil {
		return err
	}
	values := map[string]string{"author": "", "series": "", "seriesNumber": ""}
	if automatic.Author != nil {
		values["author"] = automatic.Author.Value
	}
	if automatic.Series != nil {
		values["series"] = automatic.Series.Value
	}
	if automatic.SeriesNumber != nil {
		values["seriesNumber"] = strconv.FormatFloat(automatic.SeriesNumber.Value, 'g', -1, 64)
	}
	for _, field := range []string{"author", "series", "seriesNumber"} {
		if _, err = tx.ExecContext(ctx, `UPDATE metadata_owner_fields SET automatic_value=? WHERE kind='item' AND entity_id=? AND field=? AND automatic_value<>?`, values[field], current.Entity, field, values[field]); err != nil {
			return err
		}
	}
	return nil
}

type LibraryRow struct {
	ID, LibraryID, Title, Format, SourceID string
	IssueDate                              string
	CoverURL                               string
	InventoryAvailable                     bool
	// Catalogue facts, distinct from package identity and read availability.
	Author          *compactcatalog.ReadingValue
	PublicationType *compactcatalog.ReadingValue
	Series          *SeriesValue
	// The current profile's own explicit completion mark on this exact row, present only when
	// the snapshot admitted it. Never derived from progress, counts or another profile's state.
	MarkRead     bool
	MarkRevision int64
	MarkKnown    bool
	entity       int64
	added        int64 // catalogue addition time, set only by recently-added rows
}

type SeriesValue struct {
	ID, Name string
	Number   *float64
	Source   compactcatalog.ReadingValueSource
}
type LibraryPage struct {
	Items      []LibraryRow
	NextCursor string
}

// Truncate keeps the first n items and continues after the last one kept.
func (p *LibraryPage) Truncate(n int) error {
	return truncatePage(&p.Items, &p.NextCursor, func(i int) (string, error) { return fmt.Sprint(p.Items[i].entity), nil }, n)
}

// List applies the same profile/member/library/title predicate as an item
// acquisition, before LIMIT. It reads one bounded page and no artwork/hidden
// title data, and does not infer that inventory availability proves decoding.
func (s *Service) List(ctx context.Context, p identity.Principal, library, cursor string, limit int) (LibraryPage, error) {
	out := LibraryPage{Items: []LibraryRow{}}
	if s.Visible == nil {
		return out, identity.ErrForbidden
	}
	if limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	after := int64(0)
	if cursor != "" {
		var err error
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 0 {
			return out, ErrInvalid
		}
	}
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return out, err
	}
	defer g.Rollback()
	tx := g.Tx()
	filter, args, err := s.Visible(ctx, tx, p, "e.id")
	if err != nil {
		return out, err
	}
	if filter == "" {
		return out, identity.ErrForbidden
	}
	query := `SELECT e.id,pid(e.public_id),cl.library_id,e.title,p.format,a.token,
 EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=e.id AND available.asset_id=a.token AND available.available=1)
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id
 JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + s.primaryPublicationSQL() + `
 JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id
 JOIN catalog_assets a ON a.id=p.asset_id
 WHERE e.retired=0 AND cl.retired=0
 AND ((lib.kind='book' AND e.kind=20 AND p.format IN(` + s.bookFormatsSQL() + `)) OR (lib.kind='comic' AND e.kind=21 AND p.format IN(` + readingcap.ComicFormatsSQL() + `)))
 AND e.id>? AND (?='' OR cl.library_id=?) AND (` + filter + `) ORDER BY e.id LIMIT ?`
	values := append([]any{after, library, library}, args...)
	values = append(values, limit+1)
	rows, err := tx.QueryContext(ctx, query, values...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last int64
	for rows.Next() {
		var id int64
		var row LibraryRow
		if err = rows.Scan(&id, &row.ID, &row.LibraryID, &row.Title, &row.Format, &row.SourceID, &row.InventoryAvailable); err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			out.NextCursor = fmt.Sprint(last)
			break
		}
		out.Items = append(out.Items, row)
		out.Items[len(out.Items)-1].entity = id
		last = id
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	err = readingAttributionProjectionTx(ctx, tx, out.Items)
	return out, err
}

type Version struct {
	SourceID, Format   string
	Size               int64
	InventoryAvailable bool
}

func (s *Service) Versions(ctx context.Context, p identity.Principal, item string) ([]Version, error) {
	out, err := s.VersionChoice(ctx, p, item)
	return out.Versions, err
}

// VersionChoice is a publication's actual versions with the one the server
// chooses to open, read in one authorized snapshot.
type VersionChoice struct {
	Versions []Version
	Chosen   string
}

// chooseReadingVersion is the server's reading version choice: the version
// holding the reader's latest progress while it can be opened, else the primary
// choice (the first available version in asset order, else the first version).
// Keep the whole policy here; it is one product rule (D5).
func chooseReadingVersion(versions []Version, progressed string) string {
	primary := ""
	for _, v := range versions {
		if v.InventoryAvailable && v.SourceID == progressed && progressed != "" {
			return v.SourceID
		}
		if primary == "" && v.InventoryAvailable {
			primary = v.SourceID
		}
	}
	if primary == "" && len(versions) > 0 {
		primary = versions[0].SourceID
	}
	return primary
}

func (s *Service) VersionChoice(ctx context.Context, p identity.Principal, item string) (VersionChoice, error) {
	versions, progressed, err := s.versions(ctx, p, item)
	return VersionChoice{Versions: versions, Chosen: chooseReadingVersion(versions, progressed)}, err
}

// versions also returns the source of this viewer's latest saved progress on the item.
func (s *Service) versions(ctx context.Context, p identity.Principal, item string) ([]Version, string, error) {
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return nil, "", err
	}
	defer g.Rollback()
	tx := g.Tx()
	var library string
	if err = tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id WHERE e.public_id=pid_blob(?) AND e.retired=0 AND cl.retired=0 AND ((lib.kind='book' AND e.kind=20) OR (lib.kind='comic' AND e.kind=21)) AND EXISTS(SELECT 1 FROM reading_publications format_check WHERE format_check.item_id=e.id AND format_check.format IN(`+s.sourceFormatsSQL()+`) AND `+readingcap.LocalComicArchiveSQL("format_check")+`)`, item).Scan(&library); err != nil {
		return nil, "", ErrUnavailable
	}
	if s.Authorize == nil {
		return nil, "", identity.ErrForbidden
	}
	if err = s.Authorize(ctx, tx, p, library, item); err != nil {
		return nil, "", err
	}
	// Generic availability is exact for every other format, but a local-only comic copy can
	// share its asset token through another library source. Its advertised
	// availability must come from this publication's own current local source
	// observation, with the same state/size/mtime/enabled/health facts the
	// opener checks.
	availability := `(CASE WHEN p.format IN('cb7','cbr') THEN EXISTS(
 SELECT 1 FROM catalog_assets cb7_asset
 JOIN inventory_objects cb7_object ON cb7_object.asset_id=cb7_asset.token
 JOIN library_sources cb7_source ON cb7_source.id=cb7_object.source_id
 WHERE cb7_asset.id=p.asset_id AND cb7_source.library_id=cl.library_id AND cb7_source.kind='local'
 AND cb7_object.retired=0 AND cb7_object.state='available' AND cb7_object.unsupported_reason=''
 AND cb7_object.root_incarnation=cb7_source.incarnation
 AND cb7_object.size=cb7_asset.size AND cb7_object.modified_ns=cb7_asset.modified_ns
 AND cb7_source.enabled=1 AND cb7_source.health NOT IN('offline','root_changed','removing'))
 ELSE EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=p.item_id AND available.asset_id=a.token AND available.available=1) END)`
	rows, err := tx.QueryContext(ctx, `SELECT a.token,p.format,a.size,`+availability+` FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id JOIN catalog_assets a ON a.id=link.asset_id WHERE e.public_id=pid_blob(?) AND p.format IN(`+s.sourceFormatsSQL()+`) AND `+readingcap.LocalComicArchiveSQL("p")+` ORDER BY a.id LIMIT 129`, item)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Version{}
	progressed := ""
	for rows.Next() {
		var v Version
		if err = rows.Scan(&v.SourceID, &v.Format, &v.Size, &v.InventoryAvailable); err != nil {
			return nil, "", err
		}
		if len(out) == 128 {
			return nil, "", ErrLimit
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	if err = rows.Close(); err != nil {
		return nil, "", err
	}
	if identity.CanUsePersonalState(p) {
		err = tx.QueryRowContext(ctx, `SELECT a.token FROM reading_progress rp JOIN catalog_entities e ON e.id=rp.item_id JOIN catalog_assets a ON a.id=rp.asset_id
 WHERE rp.owner_key=? AND e.public_id=pid_blob(?) ORDER BY rp.observed_at DESC,rp.asset_id DESC LIMIT 1`, identity.PersonalKey(p.Viewer), item).Scan(&progressed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, "", err
		}
	}
	return out, progressed, nil
}
