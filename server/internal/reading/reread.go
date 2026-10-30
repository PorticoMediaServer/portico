package reading

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// ErrRereadMetadataConflict means the metadata the owner reviewed changed
// before the refreshed file facts could be accepted.
var ErrRereadMetadataConflict = errors.New("metadata revision changed")

// RereadRequest names the exact publication revision the owner opened and the
// metadata revision from the metadata GET that the owner reviewed.
type RereadRequest struct {
	ItemID                   string `json:"-"`
	SourceID                 string `json:"sourceId"`
	ExpectedSourceRevision   string `json:"expectedSourceRevision"`
	ExpectedMetadataRevision string `json:"expectedMetadataRevision"`
}

// RereadAuthority supplies the actual current-owner proof and the canonical
// metadata revision from the HTTP composition. Reading does not import the
// metadata or identity revision model.
type RereadAuthority struct {
	Owner            func(ctx context.Context, tx *sql.Tx) error
	MetadataRevision func(ctx context.Context, tx *sql.Tx, itemID string) (string, error)
}

// RereadResult reports only which fact families were refreshed or left unknown.
// The owner re-reads metadata for the accepted values.
type RereadResult struct {
	SourceID         string   `json:"sourceId"`
	SourceRevision   string   `json:"sourceRevision"`
	MetadataRevision string   `json:"metadataRevision"`
	Refreshed        []string `json:"refreshed"`
	Unknown          []string `json:"unknown,omitempty"`
}

type rereadFacts struct {
	author, series    string
	seriesNumber      *float64
	authorKnown       bool
	seriesKnown       bool
	seriesNumberKnown bool
}

// adopter is the shared closed-field writer. Fields absent from the map keep
// their original rows and evidence; only determined fields are replaced.
func adoptRereadFactsTx(ctx context.Context, tx *sql.Tx, current Selection, facts rereadFacts) error {
	fields := map[string]fileMetadataFact{}
	if facts.authorKnown {
		fields["author"] = requestedFileMetadataFact(facts.author, sql.NullFloat64{}, current)
	}
	if facts.seriesKnown {
		fields["series"] = requestedFileMetadataFact(facts.series, sql.NullFloat64{}, current)
		if facts.series != "" {
			if facts.seriesNumberKnown {
				number := sql.NullFloat64{}
				if facts.seriesNumber != nil {
					number = sql.NullFloat64{Float64: *facts.seriesNumber, Valid: true}
				}
				fields["seriesNumber"] = requestedFileMetadataFact("", number, current)
			}
		} else if facts.seriesNumberKnown {
			// A genuinely empty series cannot retain a position. Clear the old
			// seriesNumber rather than admitting an orphan number.
			fields["seriesNumber"] = requestedFileMetadataFact("", sql.NullFloat64{}, current)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return adoptFileMetadataFieldsTx(ctx, tx, current, fields)
}

// rereadPDFAuthor asks the existing bounded PDF text helper for the exact
// Info/Author. pages != nil is the helper's complete-determination signal; a
// failed or incomplete read leaves the fact unknown instead of clearing it.
func rereadPDFAuthor(f *Source) (string, bool, error) {
	_, _, author, pages, err := countPDFText(f.ctx, f, f.Size)
	if err != nil {
		return "", false, err
	}
	if pages == nil {
		return "", false, nil
	}
	return acceptedEmbeddedAuthor(author), true, nil
}

// Reread reopens the exact source revision the owner opened and publishes only
// file-metadata facts for the same current observation. It never writes the
// file, changes selected artwork, or substitutes a catalogue title.
func (s *Service) Reread(ctx context.Context, p identity.Principal, req RereadRequest, auth RereadAuthority) (RereadResult, error) {
	out := RereadResult{SourceID: req.SourceID, Refreshed: []string{}}
	if s == nil || s.DB == nil || s.Storage == nil {
		return out, ErrUnavailable
	}
	if auth.Owner == nil || auth.MetadataRevision == nil || req.ItemID == "" || req.SourceID == "" || req.ExpectedSourceRevision == "" || req.ExpectedMetadataRevision == "" {
		return out, ErrInvalid
	}
	initial, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return out, err
	}
	if err = auth.Owner(ctx, initial.Tx()); err != nil {
		initial.Rollback()
		return out, err
	}
	initial.Rollback()

	f, err := s.Open(ctx, p, req.ItemID, req.SourceID, req.ExpectedSourceRevision)
	if err != nil {
		return out, err
	}
	selected := f.Selection
	defer f.Close()
	if err = f.prepareMobi(); err != nil {
		return out, f.StopAndRecheck(err)
	}
	pub, err := Inspect(f)
	if err != nil {
		return out, f.StopAndRecheck(err)
	}
	facts := rereadFacts{}
	if selected.Format == "pdf" {
		author, determined, authorErr := rereadPDFAuthor(f)
		if authorErr != nil {
			return out, f.StopAndRecheck(authorErr)
		}
		facts.author, facts.authorKnown = author, determined
		out.Unknown = []string{"series", "seriesNumber"}
		if determined {
			out.Refreshed = []string{"author"}
		} else {
			out.Unknown = []string{"author", "series", "seriesNumber"}
		}
	} else {
		facts.author, facts.series, facts.seriesNumber = pub.EmbeddedAuthor, pub.EmbeddedSeries, pub.EmbeddedSeriesNumber
		facts.authorKnown, facts.seriesKnown, facts.seriesNumberKnown = true, true, true
		out.Refreshed = []string{"author", "series", "seriesNumber"}
	}
	if err = f.Validate(); err != nil {
		return out, err
	}

	// The accepting writer is bound to the opened source's lifetime, not the
	// outer request alone. If the original lease retires while waiting for the
	// writer, every accepting query is canceled and the commit is refused.
	accept := f.ctx
	gate, err := dbwork.Begin(accept, s.DB, dbwork.ClassFrom(accept, dbwork.ClassBackgroundMedia))
	if err != nil {
		return out, err
	}
	defer gate.Rollback()
	tx := gate.Tx()
	if err = auth.Owner(accept, tx); err != nil {
		return out, err
	}
	current, err := s.selectTx(accept, tx, p, selected.ItemID, selected.SourceID)
	if err != nil {
		return out, err
	}
	if current.Entity != selected.Entity || current.Asset != selected.Asset || current.Format != selected.Format || current.Revision != selected.Revision ||
		current.object != selected.object || current.inventory != selected.inventory || current.inventoryRow != selected.inventoryRow {
		return out, ErrChanged
	}
	before, err := auth.MetadataRevision(accept, tx, selected.ItemID)
	if err != nil {
		return out, err
	}
	if before != req.ExpectedMetadataRevision {
		return out, ErrRereadMetadataConflict
	}
	if err = adoptRereadFactsTx(accept, tx, current, facts); err != nil {
		return out, err
	}
	if selected.Format == "epub" {
		// Explicit owner reread is the bounded way to retry an unchanged EPUB.
		// The state delete trigger owns sections, FTS postings, budget and epoch.
		if _, err = tx.ExecContext(accept, `DELETE FROM reading_text_index_state WHERE item_id=? AND asset_id=?`, current.Entity, current.Asset); err != nil {
			return out, err
		}
		if s.TextIndexChanged != nil {
			// Signal only after this transaction commits, before the writer gate
			// releases. The callback is the existing worker's nonblocking Wake.
			if err = dbwork.AfterCommit(tx, s.TextIndexChanged); err != nil {
				return out, err
			}
		}
	}
	after, err := auth.MetadataRevision(accept, tx, selected.ItemID)
	if err != nil {
		return out, err
	}
	if err = accept.Err(); err != nil {
		return out, err
	}
	if err = gate.Commit(); err != nil {
		return out, err
	}
	out.SourceRevision = selected.Revision
	out.MetadataRevision = after
	return out, nil
}
