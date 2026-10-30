package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

type selectionActionInput struct {
	Action   string                   `json:"action"`
	Selector catalog.JobSelector      `json:"selector"`
	Expected catalog.JobExpected      `json:"expected"`
	Options  selectionactions.Options `json:"options"`
}

func (in selectionActionInput) validate(library string) error {
	q := in.Selector.Query
	if q == nil || in.Selector.Items != nil || in.Selector.Container != nil || q.LibraryID != library || q.SelectionBasis == "" || catalog.ValidateSelectionBasis(q.SelectionBasis) != nil || len(in.Expected.CatalogRevision) != 71 {
		return selectionactions.ErrInput
	}
	switch q.Pivot {
	case "movies", "episodes", "songs", "parts":
	default:
		return selectionactions.ErrInput
	}
	switch in.Action {
	case "delete":
		if in.Options.DeleteFiles == nil || in.Options.DeviceID != "" || in.Options.Quality != "" {
			return selectionactions.ErrInput
		}
	case "download":
		var quality downloads.RequestQuality
		raw, _ := json.Marshal(in.Options.Quality)
		if json.Unmarshal(raw, &quality) != nil || in.Options.DeleteFiles != nil || !actionWireID(in.Options.DeviceID) || in.Options.Quality == "" {
			return selectionactions.ErrInput
		}
	default:
		return selectionactions.ErrInput
	}
	return nil
}

type selectionActionIntent struct {
	Input   selectionActionInput
	Viewer  catalog.Viewer
	Preview selectionactions.Preview
	Digest  string
}

// This source owns no SQL handle, request, whole-result set or disk scratch.
// It is used once by readprep and once after explicit confirmation; both owners
// reserve its full bounded retained/transient memory before each quantum.
type selectionActionSource struct {
	d            Dependencies
	p            identity.Principal
	input        selectionActionInput
	viewer       catalog.Viewer
	access       catalog.SelectionPreparationAccess
	walk         *catalog.SelectionActionWalk
	target       *selectionactions.Target
	deletion     *administration.SelectionDeleteCursor
	download     *downloads.SelectionDownloadCapture
	reserve      func(int64) error
	captureLease *readprep.Result
	cold         bool
}

func (s *selectionActionSource) Bytes() int64 {
	n := int64(unsafe.Sizeof(*s)) + s.access.CapturedBytes + int64(s.walk.Bytes()) + int64(len(s.p.Hash)+len(s.input.Selector.Query.Filter)) + 16384
	if s.target != nil {
		n += int64(len(s.target.ItemID) + len(s.target.Title) + len(s.target.LibraryID) + len(s.target.Identity) + len(s.target.DownloadIdentity) + len(s.target.Lifetime) + 4096)
	}
	if s.deletion != nil {
		n += s.deletion.Bytes()
	}
	if s.download != nil {
		n += s.download.Bytes()
	}
	return n
}
func (s *selectionActionSource) check(cat *catalog.Service) error {
	if err := s.access.Check(cat); err != nil {
		return err
	}
	return s.d.selectionActionAuthorize(cat.Context(), dbwork.Snapshot(cat.Context()), s.p, s.input.Action, s.input.Selector.Query.LibraryID, nil, s.input.Options.DeviceID)
}
func (s *selectionActionSource) checkFresh(ctx context.Context) error {
	return dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.d.DB, func(inner context.Context) error {
		cat := s.d.Catalog.WithContext(inner)
		if err := s.check(cat); err != nil {
			return err
		}
		return cat.CheckBrowseSelectionBasisTx(inner, dbwork.Snapshot(inner), s.viewer, s.input.Selector.Query.LibraryID, s.input.Selector.Query.SelectionBasis)
	})
}
func (s *selectionActionSource) Step(ctx context.Context) (selectionactions.Batch, error) {
	// A retryable read failure must leave the volatile continuation unchanged.
	// The shared walkers clone their continuations; the download cursor is a
	// bounded value whose immutable scalar/source facts can be shared.
	next := *s
	if s.download != nil {
		download := *s.download
		next.download = &download
	}
	batch, err := next.step(ctx)
	if err == nil {
		*s = next
	}
	return batch, actionPreparationError(err)
}

func (s *selectionActionSource) step(ctx context.Context) (selectionactions.Batch, error) {
	out := selectionactions.Batch{Records: []selectionactions.Record{}}
	if s.captureLease != nil {
		s.reserve = func(peak int64) error {
			return s.captureLease.ResizeDelivery((8 << 20) + peak + int64(8*len(s.captureLease.Payload)) + 65536)
		}
		defer func() { s.reserve = nil }()
		if s.cold {
			if dbwork.ColdReadShouldYield() {
				return out, nil
			}
			release, acquired, err := s.d.Catalog.ReadPreparations().TryColdQuantum(*s.captureLease)
			if err != nil {
				return out, err
			}
			if !acquired {
				return out, nil
			}
			defer release()
		}
	}
	if s.Bytes() > selectionactions.MaxSourceBytes {
		return out, readprep.ErrCapacity
	}
	if err := s.checkFresh(ctx); err != nil {
		return out, err
	}
	if s.target == nil {
		next, target, done, err := s.d.Catalog.StepSelectionActionWalk(ctx, s.walk, s.check, s.reserve)
		if err != nil {
			return out, err
		}
		s.walk = next
		if target == nil {
			out.Done = done
			return out, nil
		}
		s.target = target
		if s.input.Action == "delete" {
			s.deletion = administration.NewSelectionDeleteCursor(*target)
			out.Records = append(out.Records, selectionactions.Record{Target: target})
			return out, nil
		}
		s.download = &downloads.SelectionDownloadCapture{}
	}
	if s.input.Action == "download" {
		var pinned selectionactions.Target
		var done bool
		err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.d.DB, func(inner context.Context) error {
			var err error
			pinned, done, err = downloads.CaptureSelectionDownload(inner, dbwork.Snapshot(inner), *s.target, s.download)
			return err
		})
		if err != nil {
			return out, err
		}
		if !done {
			return out, nil
		}
		if err = s.checkFresh(ctx); err != nil {
			return out, err
		}
		out.Records = append(out.Records, selectionactions.Record{Target: &pinned}, selectionactions.Record{Finish: pinned.EntityID})
		s.target = nil
		s.download = nil
		return out, nil
	}
	next, records, done, err := s.d.Administration.StepSelectionDelete(ctx, s.deletion)
	if err != nil {
		return out, err
	}
	if len(records) > selectionactions.MaxRecords {
		return out, selectionactions.ErrCapacity
	}
	if err = s.checkFresh(ctx); err != nil {
		return out, err
	}
	s.deletion = next
	out.Records = records
	if done {
		s.target = nil
		s.deletion = nil
	}
	return out, nil
}

func (d Dependencies) newSelectionActionSource(ctx context.Context, p identity.Principal, input selectionActionInput, viewer catalog.Viewer) (selectionactions.Source, error) {
	access, err := d.browseSelectionAccess(p, input.Selector.Query.LibraryID, viewer)
	if err != nil {
		return nil, err
	}
	cold, err := catalog.SelectionActionColdSearch(*input.Selector.Query)
	if err != nil {
		return nil, err
	}
	source := &selectionActionSource{d: d, p: p, input: input, viewer: viewer, access: access, cold: cold}
	err = dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), d.DB, func(inner context.Context) error {
		cat := d.Catalog.WithContext(inner)
		if err := source.check(cat); err != nil {
			return err
		}
		var err error
		source.walk, err = cat.NewSelectionActionWalk(*input.Selector.Query, input.Expected, viewer)
		return err
	})
	if err != nil {
		return nil, err
	}
	if source.Bytes() > selectionactions.MaxSourceBytes {
		return nil, readprep.ErrCapacity
	}
	return source, nil
}

type selectionActionComputation struct {
	d            Dependencies
	p            identity.Principal
	intent       selectionActionIntent
	source       selectionactions.Source
	digest       selectionactions.Digest
	sampleBytes  int
	fixed        int64
	payloadBytes int64
}

func (c *selectionActionComputation) AccountedBytes() int64 {
	return c.fixed + int64(c.sampleBytes) + c.source.Bytes() + c.payloadBytes + 16384
}
func (c *selectionActionComputation) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	if err := b.Resize(c.AccountedBytes() + 2*selectionactions.MaxSourceBytes + 65536); err != nil {
		return readprep.StepResult{}, err
	}
	source := c.source.(*selectionActionSource)
	source.reserve = func(peak int64) error { return b.Resize(c.AccountedBytes() + int64(c.source.Bytes()) + peak + 65536) }
	batch, err := source.Step(ctx)
	source.reserve = nil
	if err != nil {
		return readprep.StepResult{}, actionPreparationError(err)
	}
	preview := &c.intent.Preview
	if len(batch.Records) > selectionactions.MaxRecords {
		return readprep.StepResult{}, readprep.ErrCapacity
	}
	for _, record := range batch.Records {
		c.digest.Add(record)
		if target := record.Target; target != nil {
			preview.Total++
			if len(preview.Sample) < 20 {
				c.sampleBytes += len(target.Title) + len(target.ItemID) + len(target.Kind) + 128
				if c.sampleBytes > 60<<10 {
					return readprep.StepResult{}, readprep.ErrCapacity
				}
				preview.Sample = append(preview.Sample, selectionactions.Sample{ItemID: target.ItemID, Title: target.Title, Kind: target.Kind})
			}
		}
		if file := record.File; file != nil {
			preview.Files++
			if !file.Shared && file.Present {
				if file.Size < 0 || file.Size > math.MaxInt64-preview.Bytes {
					return readprep.StepResult{}, selectionactions.ErrCapacity
				}
				preview.Bytes += file.Size
			}
		}
	}
	if preview.Total > 9007199254740991 || preview.Bytes > 9007199254740991 || preview.Files > 9007199254740991 {
		return readprep.StepResult{}, readprep.ErrCapacity
	}
	if !batch.Done {
		return readprep.StepResult{}, nil
	}
	if preview.Total == 0 {
		return readprep.StepResult{}, selectionactions.ErrNotFound
	}
	preview.Confirmation = strconv.FormatInt(preview.Total, 10)
	preview.ConfirmationKind = "count"
	if preview.Total == 1 {
		preview.Confirmation = preview.Sample[0].Title
		preview.ConfirmationKind = "title"
	}
	preview.Kind = "selection-action-preview"
	preview.Action = c.intent.Input.Action
	preview.Basis = c.intent.Input.Selector.Query.SelectionBasis
	preview.BytesKnown = c.intent.Input.Action == "delete"
	preview.ExpiresAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	c.intent.Digest = c.digest.String()
	raw, err := json.Marshal(c.intent)
	if err != nil {
		return readprep.StepResult{}, err
	}
	if len(raw) > 128<<10 {
		return readprep.StepResult{}, readprep.ErrCapacity
	}
	if err = b.Resize(c.AccountedBytes() + int64(8*len(raw)) + 65536); err != nil {
		return readprep.StepResult{}, err
	}
	c.payloadBytes = int64(cap(raw))
	return readprep.StepResult{Done: true, Payload: raw}, nil
}

func actionPreparationError(err error) error {
	if errors.Is(err, catalog.ErrSelectionBatchStale) || errors.Is(err, catalog.ErrStaleContinuation) || errors.Is(err, selectionactions.ErrStale) || errors.Is(err, sql.ErrNoRows) {
		return readprep.ErrStale
	}
	if errors.Is(err, catalog.ErrSelectionBatchCapacity) || errors.Is(err, selectionactions.ErrCapacity) {
		return readprep.ErrCapacity
	}
	return err
}
func actionWireID(id string) bool {
	if len(id) < 1 || len(id) > 160 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func (d Dependencies) submitSelectionActionPreparation(ctx context.Context, p identity.Principal, viewer catalog.Viewer, input selectionActionInput) (selectionactions.Pending, error) {
	access, err := d.browseSelectionAccess(p, input.Selector.Query.LibraryID, viewer)
	if err != nil {
		return selectionactions.Pending{}, err
	}
	// Canonical bytes are a private memory key; they are never a durable selector.
	spec, err := json.Marshal([]any{input, viewer, access.Caller})
	if err != nil {
		return selectionactions.Pending{}, err
	}
	fixed := int64(4*len(spec)) + access.CapturedBytes + int64(unsafe.Sizeof(d)) + 16384
	store := d.Catalog.ReadPreparations()
	if store == nil {
		return selectionactions.Pending{}, readprep.ErrCapacity
	}
	key := readprep.Key{Namespace: "selection-action-v1", Basis: input.Selector.Query.SelectionBasis, Canonical: string(spec)}
	cold, err := catalog.SelectionActionColdSearch(*input.Selector.Query)
	if err != nil {
		return selectionactions.Pending{}, err
	}
	plan := readprep.Plan{InitialBytes: fixed, ColdSearch: cold, ShouldYield: func() bool { return cold && dbwork.ColdReadShouldYield() }, New: func(inner context.Context, b *readprep.Budget) (readprep.Computation, error) {
		if err := b.Resize(fixed + 2*selectionactions.MaxSourceBytes + 65536); err != nil {
			return nil, err
		}
		source, err := d.newSelectionActionSource(inner, p, input, viewer)
		if err != nil {
			return nil, actionPreparationError(err)
		}
		intent := selectionActionIntent{Input: input, Viewer: viewer, Preview: selectionactions.Preview{Sample: []selectionactions.Sample{}}}
		return &selectionActionComputation{d: d, p: p, intent: intent, source: source, fixed: fixed}, nil
	}}
	handle, err := store.Submit(ctx, access.Caller, key, plan)
	if err != nil {
		return selectionactions.Pending{}, err
	}
	return selectionactions.Pending{Kind: "selection-action-preparing", PreparationID: string(handle), RetryAfterMs: selectionactions.RetryAfterMs}, nil
}

func (d Dependencies) selectionActionAuthorize(ctx context.Context, tx *sql.Tx, p identity.Principal, action, library string, target *selectionactions.Target, device string) error {
	if tx == nil {
		return sql.ErrTxDone
	}
	live, err := d.Identity.ReauthorizeTx(ctx, tx, p)
	if err != nil {
		return err
	}
	if err = d.allowedLibraryTx(ctx, live, library, tx); err != nil {
		return err
	}
	switch action {
	case "delete":
		if d.Administration == nil {
			return administration.ErrUnavailable
		}
		if err = d.administrationAuthority(p)(ctx, tx); err != nil {
			return err
		}
		allowed, err := administration.SelectionDeleteAllowedTx(ctx, tx, library)
		if err != nil {
			return err
		}
		if !allowed {
			return errors.Join(identity.ErrForbidden, administration.ErrDenied)
		}
	case "download":
		if d.Downloads == nil {
			return downloads.ErrPolicy
		}
		allowed, err := downloads.ProfileAllowsDownloads(tx, p.Viewer)
		if err != nil {
			return err
		}
		if !allowed {
			return errors.Join(identity.ErrForbidden, downloads.ErrPolicy)
		}
		if device != "" {
			if _, _, err = d.downloadRequestAccess(ctx, tx, p, device, "i.id"); err != nil {
				return err
			}
		}
	default:
		return selectionactions.ErrInput
	}
	if target != nil {
		if target.OperationID != "" {
			family, err := d.Identity.SessionFamilyTx(ctx, tx, live)
			if err != nil {
				return err
			}
			var state string
			err = tx.QueryRowContext(ctx, `SELECT state FROM selection_action_groups WHERE binding=? AND operation_id=?`, actionBinding(p, family.ID), target.OperationID).Scan(&state)
			if err != nil {
				return err
			}
			if state == "cancelling" {
				return selectionactions.ErrCancelled
			}
		}
		var visible bool
		where, args, err := d.bulkAccess(ctx, tx, p, "i.id")
		if err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id WHERE i.public_id=pid_blob(?) AND l.library_id=? AND (`+where+`))`, append([]any{target.ItemID, library}, args...)...).Scan(&visible); err != nil {
			return err
		}
		if !visible {
			return identity.ErrForbidden
		}
	}
	return nil
}

func (d Dependencies) actionExposure(ctx context.Context, p identity.Principal, viewer catalog.Viewer, input selectionActionInput) error {
	return dbwork.WithReadSnapshot(ctx, d.DB, func(inner context.Context) error {
		tx := dbwork.Snapshot(inner)
		if err := d.selectionActionAuthorize(inner, tx, p, input.Action, input.Selector.Query.LibraryID, nil, input.Options.DeviceID); err != nil {
			return err
		}
		return d.Catalog.WithContext(inner).CheckBrowseSelectionBasisTx(inner, tx, viewer, input.Selector.Query.LibraryID, input.Selector.Query.SelectionBasis)
	})
}
func actionBinding(p identity.Principal, family string) string {
	raw, _ := json.Marshal([]any{"selection-action", p.ServerID, family, p.Authority, p.AccountID, p.ProfileID, p.Epoch, p.Grant})
	return identity.Digest(string(raw))
}

// Confirmed command lookup survives ordinary token renewal within the verified
// session family. Volatile preparation handles remain exact-token private.
func (d Dependencies) selectionActionBinding(ctx context.Context, p identity.Principal) (string, error) {
	var binding string
	err := dbwork.WithReadSnapshot(ctx, d.DB, func(inner context.Context) error {
		family, err := d.Identity.SessionFamilyTx(inner, dbwork.Snapshot(inner), p)
		if err != nil {
			return err
		}
		binding = actionBinding(p, family.ID)
		return nil
	})
	return binding, err
}
