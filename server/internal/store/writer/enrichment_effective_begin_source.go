package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Immutable typed FIRST producer capture (INT-FIRST-EN-SOURCE-MAP). These
// fields are native witnesses, not interchangeable generic revision numbers.
type effectiveBeginProducer struct {
	Library         effectiveBeginLibrary
	Policy          effectiveBeginPolicy
	Provider        effectiveBeginProvider
	ConsentRevision int64
	Selection       effectiveBeginSelection
	Evidence        effectiveBeginEvidence
	Subject         effectiveBeginSubject
	Publication     effectiveBeginPublication
	CreditHead      effectiveBeginCreditHead
	Capture         effectiveBeginCapture
	Association     effectiveBeginAssociation
	Asset           effectiveBeginAsset
	Object          effectiveBeginObject
	Source          effectiveBeginSource
}
type effectiveBeginLibrary struct {
	ID               int64
	ExternalID, Root string
	Retired          int64
	Kind, Language   string
}
type effectiveBeginPolicy struct {
	Incarnation                    [16]byte
	Revision, Enabled              int64
	Region, Algorithm, RefreshMode string
	CreditEnabled                  int64
}
type effectiveBeginProvider struct {
	Adapter string
	Enabled int64
}
type effectiveBeginSelection struct {
	Incarnation                       [16]byte
	Revision                          int64
	Role, Kind, ID, Status            string
	Evidence, Set, Publication, Track sql.NullString
}
type effectiveBeginEvidence struct {
	SubjectID                                                     int64
	Provider, Kind, ID, Language, Region, Shape, Digest, Observed string
	Sealed                                                        int64
}
type effectiveBeginSubject struct {
	Incarnation                                 [16]byte
	Revision                                    int64
	Provider, Kind, ID, Language, Region, Shape string
	AcceptedEvidence                            sql.NullString
}
type effectiveBeginPublication struct {
	EntityID                   int64
	Provider, Record, Observed string
	Count, Sealed              int64
}
type effectiveBeginCreditHead struct {
	Exists        bool
	PublicationID string
	Revision      int64
}
type effectiveBeginCapture struct {
	Ordinal, AssetID          int64
	Incarnation               [16]byte
	Part                      int64
	Start                     float64
	End                       sql.NullFloat64
	Path                      string
	Size, Modified, Available int64
	SourceID                  string
	Generation                int64
	Root, ObjectRevision      string
}
type effectiveBeginAssociation struct {
	AssetID, LibraryID, Part, Available int64
	Start                               float64
	End                                 sql.NullFloat64
	CUE                                 sql.NullInt64
}
type effectiveBeginAsset struct {
	Token, Path                          string
	Size, Modified, Available, LibraryID int64
	Incarnation                          [16]byte
	InfoOwner, InfoRevision              sql.NullString
}
type effectiveBeginObject struct {
	ID, SourceID, AssetToken, Root, Revision string
	Size, Modified                           int64
	State                                    string
	Retired                                  int64
	Unsupported                              string
}
type effectiveBeginSource struct {
	LibraryID                       string
	Generation                      int64
	Incarnation, Root, RootIdentity string
	Confirmed, Enabled              int64
	Health                          string
}

// Scan only through W's protected row API and the existing compiled turn. This
// helper neither constructs a turn nor exposes rows, a connection or arbitrary SQL.
func effectiveBeginPoint(ctx context.Context, turn *commandTurn, id stmt.ID, args []any, dest ...any) error {
	if turn == nil || ctx == nil {
		return ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return ErrInvalid
	}
	row, err := turn.queryRow(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	return row.Scan(dest...)
}

func effectiveBeginReadProducer(ctx context.Context, turn *commandTurn, ownerID string, entity effectiveBeginEntity, provider, evidenceID, publicationID, objectID string) (out effectiveBeginProducer, err error) {
	var raw []byte
	p := func(id stmt.ID, args []any, dest ...any) error {
		return effectiveBeginPoint(ctx, turn, id, args, dest...)
	}
	// These point facts are not current-disclosure proof; W establishes that
	// before this domain entry. Every fresh fact is checked AFTER receipt resolve.
	if err = p(enrichmentEffectiveLibrary, []any{entity.libraryID}, &out.Library.ID, &out.Library.ExternalID, &out.Library.Root, &out.Library.Retired); err != nil {
		return
	}
	if err = p(enrichmentEffectiveLanguage, []any{out.Library.ExternalID}, &out.Library.Kind, &out.Library.Language); err != nil {
		return
	}
	if err = p(enrichmentEffectivePolicy, []any{entity.libraryID, provider}, &raw, &out.Policy.Revision, &out.Policy.Enabled, &out.Policy.Region, &out.Policy.Algorithm, &out.Policy.RefreshMode, &out.Policy.CreditEnabled); err != nil {
		return
	}
	if len(raw) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.Policy.Incarnation[:], raw)
	if err = p(enrichmentEffectiveProvider, []any{provider}, &out.Provider.Adapter, &out.Provider.Enabled); err != nil {
		return
	}
	var confirmed int64
	if err = p(enrichmentEffectiveConsent, []any{int64(1)}, &out.ConsentRevision, &confirmed); err != nil {
		return
	}
	if confirmed != 1 {
		return out, ErrConflict
	}
	if err = p(enrichmentEffectiveSelection, []any{entity.id, provider, "primary"}, &raw, &out.Selection.Revision, &out.Selection.Role, &out.Selection.Kind, &out.Selection.ID, &out.Selection.Status, &out.Selection.Evidence, &out.Selection.Set, &out.Selection.Publication, &out.Selection.Track); err != nil {
		return
	}
	if len(raw) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.Selection.Incarnation[:], raw)
	if err = p(enrichmentEffectiveEvidence, []any{evidenceID}, &out.Evidence.SubjectID, &out.Evidence.Provider, &out.Evidence.Kind, &out.Evidence.ID, &out.Evidence.Language, &out.Evidence.Region, &out.Evidence.Shape, &out.Evidence.Digest, &out.Evidence.Observed, &out.Evidence.Sealed); err != nil {
		return
	}
	if err = p(enrichmentEffectiveSubject, []any{out.Evidence.SubjectID}, &raw, &out.Subject.Revision, &out.Subject.Provider, &out.Subject.Kind, &out.Subject.ID, &out.Subject.Language, &out.Subject.Region, &out.Subject.Shape, &out.Subject.AcceptedEvidence); err != nil {
		return
	}
	if len(raw) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.Subject.Incarnation[:], raw)
	if err = p(enrichmentEffectivePublication, []any{publicationID}, &out.Publication.EntityID, &out.Publication.Provider, &out.Publication.Record, &out.Publication.Observed, &out.Publication.Count, &out.Publication.Sealed); err != nil {
		return
	}
	err = p(enrichmentEffectiveCreditHead, []any{entity.id, provider}, &out.CreditHead.PublicationID, &out.CreditHead.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return
	} else {
		out.CreditHead.Exists = true
	}
	// LIMIT2 consumes an extra raw candidate to prove exactly ONE source/link;
	// it never reconstructs a capture from a newer current association list.
	if err = effectiveBeginSingle(ctx, turn, enrichmentEffectiveCapture, ownerID, &out.Capture.Ordinal, &out.Capture.AssetID, &raw, &out.Capture.Part, &out.Capture.Start, &out.Capture.End, &out.Capture.Path, &out.Capture.Size, &out.Capture.Modified, &out.Capture.Available, &out.Capture.SourceID, &out.Capture.Generation, &out.Capture.Root, &out.Capture.ObjectRevision); err != nil {
		return
	}
	if len(raw) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.Capture.Incarnation[:], raw)
	if err = effectiveBeginSingle(ctx, turn, enrichmentEffectiveAssociation, entity.id, &out.Association.AssetID, &out.Association.LibraryID, &out.Association.Part, &out.Association.Available, &out.Association.Start, &out.Association.End, &out.Association.CUE); err != nil {
		return
	}
	if err = p(enrichmentEffectiveAsset, []any{out.Capture.AssetID}, &out.Asset.Token, &out.Asset.Path, &out.Asset.Size, &out.Asset.Modified, &out.Asset.Available, &out.Asset.LibraryID, &raw, &out.Asset.InfoOwner, &out.Asset.InfoRevision); err != nil {
		return
	}
	if len(raw) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.Asset.Incarnation[:], raw)
	out.Object.ID = objectID
	if err = p(enrichmentEffectiveObject, []any{objectID}, &out.Object.SourceID, &out.Object.AssetToken, &out.Object.Root, &out.Object.Revision, &out.Object.Size, &out.Object.Modified, &out.Object.State, &out.Object.Retired, &out.Object.Unsupported); err != nil {
		return
	}
	if err = p(enrichmentEffectiveSource, []any{out.Capture.SourceID}, &out.Source.LibraryID, &out.Source.Generation, &out.Source.Incarnation, &out.Source.Root, &out.Source.RootIdentity, &out.Source.Confirmed, &out.Source.Enabled, &out.Source.Health); err != nil {
		return
	}
	return out, nil
}

func effectiveBeginSingle(ctx context.Context, turn *commandTurn, id stmt.ID, owner any, dest ...any) error {
	if turn == nil || ctx == nil {
		return ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return ErrInvalid
	}
	rows, err := turn.query(ctx, step.statementStep, owner, int64(2))
	if err != nil {
		return err
	}
	defer rows.Close()
	defer turn.transaction.guardPanic()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return ErrConflict
	}
	if err := rows.Scan(dest...); err != nil {
		return err
	}
	if rows.Next() {
		return ErrConflict
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

func effectiveBeginProducerShape(p effectiveBeginProducer, e effectiveBeginEntity, o effectiveBeginOwner, h effectiveBeginHeader) bool {
	return e.kind == 1 && e.retired == 0 && e.tvAbsent() && o.hierarchyRevision == 0 && o.selectionRevision == 0 && o.descriptiveRevision == 0 &&
		p.Library.ID == e.libraryID && p.Library.Retired == 0 && p.Library.Kind == "movie" && h.language == p.Library.Language &&
		p.Policy.Enabled == 1 && p.Policy.CreditEnabled == 1 && p.Policy.Incarnation == o.policyIncarnation && p.Policy.Revision == o.policyRevision && p.Policy.Algorithm == o.algorithm && p.Policy.RefreshMode == o.refreshMode && p.ConsentRevision == o.consentRevision && p.Provider.Enabled == 1 &&
		p.Selection.Role == "primary" && p.Selection.Kind == "movie" && p.Selection.Status == "selected" && p.Selection.ID == o.selectedID &&
		p.Evidence.Sealed == 1 && p.Evidence.Provider == o.provider && p.Evidence.Kind == "movie" && p.Evidence.ID == o.selectedID && p.Evidence.Language == h.language && p.Evidence.Region == p.Policy.Region &&
		p.Subject.Provider == p.Evidence.Provider && p.Subject.Kind == p.Evidence.Kind && p.Subject.ID == p.Evidence.ID && p.Subject.Language == p.Evidence.Language && p.Subject.Region == p.Evidence.Region && p.Subject.Shape == p.Evidence.Shape &&
		p.Publication.EntityID == e.id && p.Publication.Provider == o.provider && p.Publication.Sealed == 1 &&
		p.Capture.Ordinal == 0 && p.Capture.AssetID == p.Association.AssetID && p.Capture.Incarnation == p.Asset.Incarnation && p.Capture.Part == p.Association.Part && p.Capture.Start == p.Association.Start && p.Capture.End == p.Association.End &&
		p.Association.LibraryID == e.libraryID && p.Association.Available == 1 && !p.Association.CUE.Valid &&
		p.Asset.LibraryID == e.libraryID && p.Asset.Path == p.Capture.Path && p.Asset.Size == p.Capture.Size && p.Asset.Modified == p.Capture.Modified && p.Asset.Available == 1 && p.Capture.Available == 1 &&

		p.Object.SourceID == p.Capture.SourceID && p.Object.AssetToken == p.Asset.Token && p.Object.Root == p.Capture.Root && p.Object.Revision == p.Capture.ObjectRevision && p.Object.Size == p.Capture.Size && p.Object.Modified == p.Capture.Modified && p.Object.State == "available" && p.Object.Retired == 0 && p.Object.Unsupported == "" &&
		p.Source.LibraryID == p.Library.ExternalID && p.Source.Generation == p.Capture.Generation && p.Source.Incarnation == p.Capture.Root && p.Source.Enabled == 1 && p.Source.Confirmed == 1 && p.Source.Health != "offline" && p.Source.Health != "removing" && p.Source.Health != "root_changed" &&
		!math.IsNaN(p.Capture.Start) && !math.IsInf(p.Capture.Start, 0) && (!p.Capture.End.Valid || (!math.IsNaN(p.Capture.End.Float64) && !math.IsInf(p.Capture.End.Float64, 0)))
}
