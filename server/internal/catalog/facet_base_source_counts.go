package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// UnrestrictedFacetBaseSource names the existing maintained branch, including
// its own-show genre/year/collection and episode descriptive/file semantics.
// It must never be substituted for the restricted show/member/decade oracles.
const UnrestrictedFacetBaseSource = "simple-maintained-unrestricted-v1"

type unrestrictedFacetWitness struct {
	Version int                               `json:"version"`
	Fence   compactcatalog.FacetProducerFence `json:"fence"`
}
type unrestrictedFacetCursor struct {
	Has    bool   `json:"has"`
	Value  string `json:"value"`
	Rating string `json:"rating"`
	Label  string `json:"label"`
	Total  int64  `json:"total"`
}

type facetBasePolicyDocument struct {
	MaximumAge         *int     `json:"maximumAge"`
	BlockUnrated       bool     `json:"blockUnrated"`
	BlockedLabels      []string `json:"blockedLabels"`
	MemberDeniedLabels []string `json:"memberDeniedLabels"`
	BlockRecordings    bool     `json:"blockRecordings"`
}

func parseFacetBasePolicy(raw string) (identity.ContentRestrictions, error) {
	var p facetBasePolicyDocument
	if json.Unmarshal([]byte(raw), &p) != nil {
		return identity.ContentRestrictions{}, ErrFacetBaseInput
	}
	r := identity.ContentRestrictions{MaximumAge: p.MaximumAge, BlockUnrated: p.BlockUnrated, BlockedLabels: p.BlockedLabels, MemberDeniedLabels: p.MemberDeniedLabels, BlockRecordings: p.BlockRecordings}
	if FacetBasePolicy(r) != raw {
		return r, ErrFacetBaseInput
	}
	return r, nil
}

// CaptureUnrestrictedFacetBaseWitness captures the ACTUAL producer-family fence
// in the caller's snapshot, not no-op CheckReadiness. A changed accepted source,
// in-progress stage/failure, classification change or serving transition rejects
// a later quantum/publication. A completed output remains independently usable
// through content churn, until bounded retirement or live authority revocation.
func CaptureUnrestrictedFacetBaseWitness(ctx context.Context, q compactcatalog.ReadQuery, scope FacetBaseScope) (string, error) {
	if !scope.valid() || scope.Source != UnrestrictedFacetBaseSource {
		return "", ErrFacetBaseInput
	}
	r, err := parseFacetBasePolicy(scope.Policy)
	if err != nil {
		return "", err
	}
	if r.Active() {
		return "", ErrFacetBaseUnavailable
	}
	var kind string
	err = q.QueryRowContext(ctx, `SELECT l.kind FROM catalog_libraries c JOIN libraries l ON l.id=c.library_id WHERE c.id=? AND c.library_id=? AND c.retired=0`, scope.LibraryID, scope.Library).Scan(&kind)
	if err != nil {
		return "", err
	}
	if isReadingLibrary(kind) {
		return "", ErrFacetBaseUnavailable
	}
	fence, err := compactcatalog.CaptureFacetProducerFence(ctx, q)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(unrestrictedFacetWitness{Version: 1, Fence: fence})
	return string(raw), err
}

// UnrestrictedFacetBaseProducer is a real source adapter over the existing
// accepted counts, never a duplicated holder graph. It reads at most eight RAW
// count-PK rows per quantum, reducing one value across arbitrarily many buckets.
// Its cursor is durable BASE progress, including exact long scalar PK bytes.
// Source writes remain independent; a changed fence aborts this staged image,
// never publishes mixed rows. The background owner restarts/retires candidates.
// Restricted policies return unavailable; no bucket visibility approximation.
type UnrestrictedFacetBaseProducer struct{}

func (p UnrestrictedFacetBaseProducer) validate(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot) error {
	if s.Scope.Source != UnrestrictedFacetBaseSource {
		return ErrFacetBaseUnavailable
	}
	r, err := parseFacetBasePolicy(s.Scope.Policy)
	if err != nil {
		return err
	}
	if r.Active() {
		return ErrFacetBaseUnavailable
	}
	var w unrestrictedFacetWitness
	if json.Unmarshal([]byte(s.Witness), &w) != nil || w.Version != 1 {
		return ErrFacetBaseInput
	}
	return compactcatalog.ValidateFacetProducerFence(ctx, tx, w.Fence)
}
func (p UnrestrictedFacetBaseProducer) ValidatePublication(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot) error {
	return p.validate(ctx, tx, s)
}
func (p UnrestrictedFacetBaseProducer) Next(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot, cursor string) (FacetBaseBatch, error) {
	if err := p.validate(ctx, tx, s); err != nil {
		return FacetBaseBatch{}, err
	}
	var c unrestrictedFacetCursor
	if cursor != "" && json.Unmarshal([]byte(cursor), &c) != nil {
		return FacetBaseBatch{}, ErrFacetBaseInput
	}
	q := `SELECT value,rating_key,label_key,total FROM catalog_facet_counts WHERE library_id=? AND field=?`
	args := []any{s.Scope.LibraryID, s.Scope.Field}
	if c.Has {
		q += ` AND (value,rating_key,label_key)>(?,?,?)`
		args = append(args, c.Value, c.Rating, c.Label)
	}
	q += ` ORDER BY value,rating_key,label_key LIMIT 8`
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return FacetBaseBatch{}, err
	}
	// Close the source cursor before any output-label lookup or dictionary write.
	type rawFact struct {
		value, rating, label string
		total                int64
	}
	raw := make([]rawFact, 0, 8)
	bytes := 0
	for rows.Next() {
		var f rawFact
		if err = rows.Scan(&f.value, &f.rating, &f.label, &f.total); err != nil {
			rows.Close()
			return FacetBaseBatch{}, err
		}
		width := len(f.value) + len(f.rating) + len(f.label)
		if len(raw) > 0 && bytes+width > 256<<10 {
			break
		}
		raw = append(raw, f)
		bytes += width
		if bytes >= 256<<10 {
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return FacetBaseBatch{}, err
	}
	out := FacetBaseBatch{}
	outBytes := 0
	yield := errors.New("facet source output byte quantum")
	emit := func() error {
		if !c.Has || c.Value == "" || c.Total == 0 {
			return nil
		}
		f, err := unrestrictedFacetPublicFact(ctx, tx, s.Scope.Field, c.Value, c.Total)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		width := len(f.Value) + len(f.Label)
		if len(out.Facts) > 0 && outBytes+width > 256<<10 {
			return yield
		}
		outBytes += width
		out.Facts = append(out.Facts, f)
		return nil
	}
	for _, f := range raw {
		if c.Has && c.Value != f.value {
			if err = emit(); err != nil {
				if errors.Is(err, yield) {
					break
				}
				return out, err
			}
			c.Total = 0
		}
		if f.total < 0 || c.Total > math.MaxInt64-f.total {
			return out, ErrFacetBaseInput
		}
		c.Has = true
		c.Value, c.Rating, c.Label = f.value, f.rating, f.label
		c.Total += f.total
	}
	// Only an EMPTY raw result proves completion: short byte-limited batches do
	// not. A final carried value is emitted in its own constant activation step.
	if len(raw) == 0 {
		if err = emit(); err != nil {
			return out, err
		}
		out.Done = true
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		return out, err
	}
	out.Cursor = string(encoded)
	return out, nil
}
func unrestrictedFacetPublicFact(ctx context.Context, tx *sql.Tx, field, value string, total int64) (FacetBaseFact, error) {
	f := FacetBaseFact{Value: value, Label: value, Count: total}
	switch field {
	case "decade":
		f.Label = value + "s"
	case "resolution":
		if value == "4k" {
			f.Label = "4K"
		}
		if value == "sd" {
			f.Label = "SD"
		}
	case "collection":
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return f, ErrFacetBaseInput
		}
		if err = tx.QueryRowContext(ctx, `SELECT pid(public_id),title FROM catalog_entities WHERE id=?`, id).Scan(&f.Value, &f.Label); err != nil {
			return f, err
		}
	}
	return f, nil
}
