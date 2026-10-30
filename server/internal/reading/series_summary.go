package reading

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

// SeriesSummary describes the complete visible canonical membership, not one
// resident page. Author is present only when every member has the same exact
// accepted value and provenance; missing or mixed values remain absent.
type SeriesSummary struct {
	ID, Name         string
	Count, ReadCount int64
	Author           *compactcatalog.ReadingValue
}

func seriesSummaryTx(ctx context.Context, tx *sql.Tx, profile, series, base string, args []any) (*SeriesSummary, error) {
	query := `WITH eligible AS MATERIALIZED(
 SELECT m.series_value,m.series_source,m.author_value,m.author_source,` + personalstate.CompactSQL("?", "e.id") + ` AS read` + base + `),
 spellings AS(SELECT series_value,count(*) AS frequency,sum(series_source='owner') AS owners FROM eligible GROUP BY series_value),
 label AS(SELECT series_value FROM spellings ORDER BY owners>0 DESC,CASE WHEN owners>0 THEN owners ELSE frequency END DESC,series_value COLLATE BINARY LIMIT 1),
 counts AS(SELECT count(*) AS total,coalesce(sum(read),0) AS read_count,count(author_value) AS authored,count(author_source) AS sourced,count(DISTINCT author_value) AS authors,count(DISTINCT author_source) AS sources,min(author_value) AS author,min(author_source) AS source FROM eligible)
 SELECT label.series_value,c.total,c.read_count,
 CASE WHEN c.authored=c.total AND c.sourced=c.total AND c.authors=1 AND c.sources=1 THEN c.author END,
 CASE WHEN c.authored=c.total AND c.sourced=c.total AND c.authors=1 AND c.sources=1 THEN c.source END
 FROM label CROSS JOIN counts c`
	values := append([]any{profile}, args...)
	out := &SeriesSummary{ID: series}
	var author, source sql.NullString
	if err := tx.QueryRowContext(ctx, query, values...).Scan(&out.Name, &out.Count, &out.ReadCount, &author, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnavailable
		}
		return nil, err
	}
	if !utf8.ValidString(out.Name) || out.Name == "" || utf8.RuneCountInString(out.Name) > 4096 || out.Count < 1 || out.Count > 9007199254740991 || out.ReadCount < 0 || out.ReadCount > out.Count || author.Valid != source.Valid {
		return nil, ErrChanged
	}
	if author.Valid {
		provenance := compactcatalog.ReadingValueSource(source.String)
		switch provenance {
		case compactcatalog.ReadingOwner, compactcatalog.ReadingFileMetadata, compactcatalog.ReadingFileName, compactcatalog.ReadingFolder:
		default:
			return nil, ErrChanged
		}
		if !utf8.ValidString(author.String) || author.String == "" || utf8.RuneCountInString(author.String) > 4096 {
			return nil, ErrChanged
		}
		out.Author = &compactcatalog.ReadingValue{Value: author.String, Source: provenance}
	}
	return out, nil
}
