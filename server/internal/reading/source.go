package reading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/readingidentity"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/sourceaccess"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Authorization is supplied by the HTTP composition and runs in the same
// transaction as selection: live session, library permission and title fence.
// The service never treats a filesystem observation as actor authorization.
type Authorization func(context.Context, *sql.Tx, identity.Principal, string, string) error
type Visibility func(context.Context, *sql.Tx, identity.Principal, string) (string, []any, error)
type Service struct {
	DB          *sql.DB
	Storage     *storage.Client
	Mounts      *mounts.Service
	Remote      *remotesources.Service
	Authorize   Authorization
	Visible     Visibility
	SeriesScope SeriesScopeResolver
	Mobi        *MobiConverter
	PDFRaster   *PDFRaster
	AdoptCover  CoverAdopter
	// TextIndexChanged is the bounded post-commit wake for explicit owner
	// reread invalidation. It must only signal an existing worker; it performs
	// no indexing, IO or new work in the request.
	TextIndexChanged func()
	// Changed queues metadata inventory for the actual captured folder source.
	// It runs only outside source/DB locks, never as content-read authority.
	Changed        func(library, folderSource string)
	sessionMu      sync.Mutex
	sessions       map[string]*Session
	sessionsClosed bool
}

type Selection struct {
	ItemID, SourceID, Format, Title, Language, Revision                                                                string
	Entity, Asset, Size, ModifiedNS                                                                                    int64
	library, token, path, root, relative, rootID, rootIdentity, incarnation, object, inventory, evidence, inventoryRow string
	generation                                                                                                         int64
}

const publicationFields = `e.id,p.asset_id,p.format,e.title,e.metadata_language,cl.library_id,a.token,a.path,a.size,a.modified_ns,s.id,s.root,s.generation,s.incarnation,s.root_identity,o.relative_path,o.object_identity,o.revision,o.evidence_json,o.id`

const publicationJoins = ` CROSS JOIN reading_publications p ON p.item_id=e.id
 CROSS JOIN catalog_libraries cl ON cl.id=e.library_id CROSS JOIN libraries lib ON lib.id=cl.library_id
 CROSS JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id
 CROSS JOIN catalog_assets a ON a.id=link.asset_id
 CROSS JOIN inventory_objects o INDEXED BY inventory_objects_asset ON o.asset_id=a.token
 CROSS JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id
 CROSS JOIN inventory_item_availability available ON available.item_id=e.id AND available.asset_id=a.token`

const publicationObjectEligible = `o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.root_incarnation=s.incarnation
 AND o.size=a.size AND o.modified_ns=a.modified_ns AND s.enabled=1 AND s.health NOT IN('offline','root_changed','removing')`

// publicationChosenObject is the observation the public opener would choose for
// the enclosing publication/asset: the first admitted inventory object in
// o2.id order. It mirrors publicationObjectEligible exactly, using the o2/s2
// aliases of the correlated subquery.
const publicationChosenObject = `(SELECT o2.id FROM inventory_objects o2
 JOIN library_sources s2 ON s2.id=o2.source_id AND s2.library_id=cl.library_id
 WHERE o2.asset_id=a.token AND o2.retired=0 AND o2.state='available' AND o2.unsupported_reason=''
 AND o2.root_incarnation=s2.incarnation AND o2.size=a.size AND o2.modified_ns=a.modified_ns
 AND s2.enabled=1 AND s2.health NOT IN('offline','root_changed','removing')
 AND (p.format NOT IN('cb7','cbr') OR s2.kind='local')
 ORDER BY o2.id LIMIT 1)`

var publicationEligible = `e.retired=0 AND cl.retired=0 AND e.kind IN(20,21)
 AND ((lib.kind='book' AND e.kind=20 AND p.format IN('epub','pdf','mobi','azw3')) OR (lib.kind='comic' AND e.kind=21 AND p.format IN(` + readingcap.ComicFormatsSQL() + `))) AND link.available=1 AND a.available=1 AND available.available=1
 AND (p.format NOT IN('cb7','cbr') OR s.kind='local') AND ` + publicationObjectEligible

var selectPublication = `SELECT ` + publicationFields + ` FROM catalog_entities e` + publicationJoins + `
 WHERE e.public_id=pid_blob(?) AND (?='' OR a.token=?) AND ` + publicationEligible + ` AND (p.format NOT IN('mobi','azw3') OR ?=1) ORDER BY a.id,o.id LIMIT 1`

func (s *Service) selectTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, sourceID string) (Selection, error) {
	var v Selection
	v.ItemID = item
	err := tx.QueryRowContext(ctx, selectPublication, item, sourceID, sourceID, s.Mobi != nil).Scan(selectionColumns(&v)...)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrUnavailable
	}
	if err != nil {
		return v, err
	}
	if s.Authorize == nil {
		return v, identity.ErrForbidden
	}
	if err = s.Authorize(ctx, tx, p, v.library, item); err != nil {
		return v, err
	}
	if err = s.selectionValid(v); err != nil {
		return v, err
	}
	v.SourceID = v.token
	v.Revision = v.fence()
	return v, nil
}

// selectionValid checks structural source custody only; authorization and
// currentness remain the opener's own checks.
func (s *Service) selectionValid(v Selection) error {
	if v.Size <= 0 || v.Size > MaxSource || !filepath.IsAbs(v.root) || !filepath.IsLocal(v.relative) || v.relative == "." || v.rootIdentity == "" {
		return ErrInvalid
	}
	if v.Format != "epub" && v.Format != "pdf" && v.Format != "cbz" && v.Format != "mobi" && v.Format != "azw3" && !(v.Format == "cb7" && readingcap.CB7()) && !(v.Format == "cbr" && readingcap.CBR()) {
		return ErrInvalid
	}
	if filepath.Clean(filepath.Join(v.root, v.relative)) != filepath.Clean(v.path) {
		return ErrChanged
	}
	return nil
}
func selectionColumns(v *Selection) []any {
	return []any{&v.Entity, &v.Asset, &v.Format, &v.Title, &v.Language, &v.library, &v.token, &v.path, &v.Size, &v.ModifiedNS, &v.rootID, &v.root, &v.generation, &v.incarnation, &v.rootIdentity, &v.relative, &v.object, &v.inventory, &v.evidence, &v.inventoryRow}
}

func (v Selection) fence() string {
	return readingidentity.Revision(v.Entity, v.Asset, v.Format, v.token, v.Size, v.ModifiedNS, v.rootID, v.generation, v.incarnation, v.rootIdentity, v.relative, v.object, v.inventory, v.evidence)
}
func (s *Service) Resolve(ctx context.Context, p identity.Principal, item, sourceID string) (Selection, error) {
	if s == nil || s.DB == nil || s.Storage == nil {
		return Selection{}, ErrUnavailable
	}
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return Selection{}, err
	}
	defer g.Rollback()
	return s.selectTx(ctx, g.Tx(), p, item, sourceID)
}
func (s *Service) validate(ctx context.Context, p identity.Principal, selected Selection) error {
	now, err := s.Resolve(ctx, p, selected.ItemID, selected.SourceID)
	if err != nil {
		return err
	}
	if now.Revision != selected.Revision {
		return ErrChanged
	}
	return nil
}

// Source owns a rooted, helper-acquired local descriptor. Every read checks live
// actor/association authority, and the helper checks observed identity before
// and after each bounded extent. No immutable/no-splice claim is made from it.
type Source struct {
	derived         *DerivedMobi
	derivedRetained bool
	Selection
	ctx      context.Context
	p        identity.Principal
	service  *Service
	reader   *storage.ObservedPlaybackReader
	roots    *sourceaccess.Registry
	mu       sync.Mutex
	observed storage.ObservedFileObservation
	// validateCurrent is the exact opener's currentness check: a viewer's live
	// authorization or the preparation worker's catalog/fence check. Reads never
	// substitute one for the other.
	validateCurrent func(context.Context) error
	remoteSession   *Session
	remoteRange     *remotesources.SourceRange
	remoteRetained  bool
	remoteCancel    func()
	onClose         func()
	onFailure       func()
	closeOnce       sync.Once
}

// Context carries both this request's cancellation and the captured reader
// session's retirement. Buffered parsing and image work use the same lifetime
// as source extents; a closed book must also stop work after bytes were read.
func (f *Source) Context() context.Context { return f.ctx }

func (s *Service) Open(ctx context.Context, p identity.Principal, item, sourceID, expected string) (*Source, error) {
	return s.open(ctx, p, item, sourceID, expected, false)
}

// localOnly is checked against the exact resolved selection before any provider
// IO. Offline preparation cannot acquire an interactive remote Reading lease.
func (s *Service) open(ctx context.Context, p identity.Principal, item, sourceID, expected string, localOnly bool) (*Source, error) {
	v, err := s.Resolve(ctx, p, item, sourceID)
	if err != nil {
		return nil, err
	}
	if expected != "" && expected != v.Revision {
		return nil, ErrChanged
	}
	// Remote reads use the retained interactive session resolver, never a
	// playback/analysis scheduling pool as operation authority.
	if s.Storage.IsRemote(v.path) {
		if localOnly || v.Format == "cb7" || v.Format == "cbr" {
			return nil, ErrUnavailable
		}
		return s.openRemote(ctx, p, v, nil)
	}
	// A typed nil mount service must not become a nonnil mountOwner interface.
	// Unmanaged local roots still use the actual registration/helper boundary.
	var roots *sourceaccess.Registry
	if s.Mounts == nil {
		roots = sourceaccess.New(s.Storage, nil)
	} else {
		roots = sourceaccess.New(s.Storage, s.Mounts)
	}
	lease, err := roots.Borrow(ctx, sourceaccess.Authority{OwnerID: "read:" + identity.Token(), RootID: v.rootID, RootPath: v.root, RelativePath: v.relative, Revision: v.incarnation + ":" + strconv.FormatInt(v.generation, 10), Lifetime: ctx, Validate: func(ctx context.Context) error { return s.validate(ctx, p, v) }})
	if err != nil {
		roots.Close()
		return nil, err
	}
	r, err := s.Storage.OpenObservedPlayback(ctx, lease)
	if err != nil {
		roots.Close()
		return nil, err
	}
	f := &Source{Selection: v, ctx: ctx, p: p, service: s, reader: r, roots: roots, validateCurrent: func(ctx context.Context) error { return s.validate(ctx, p, v) }}
	o, err := r.Observe(ctx)
	if err == nil {
		err = f.matchInventory(o)
	}
	if err == nil {
		err = s.validate(ctx, p, v)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	f.observed = o
	return f, nil
}

func (f *Source) matchInventory(o storage.ObservedFileObservation) error {
	_, root, ok := strings.Cut(o.RootBindingID, ":")
	if !ok || !storage.SameRoot(f.rootIdentity, root) || !o.SizeKnown || !o.ModifiedKnown || o.Size != f.Size || o.ModifiedNS != f.ModifiedNS {
		return ErrChanged
	}
	var evidence mediasource.InventoryEvidence
	if json.Unmarshal([]byte(f.evidence), &evidence) != nil {
		return ErrInvalid
	}
	actual := storage.StableObjectIdentity(o.ObjectBindingID, root)
	if evidence.Object != actual || (evidence.ChangeToken != "" && evidence.ChangeToken != o.ChangeToken) {
		return ErrChanged
	}
	return nil
}
func (f *Source) checkObservation(o storage.ObservedFileObservation) error {
	if err := f.matchInventory(o); err != nil {
		return err
	}
	if f.observed.RootBindingID != "" && (o.RootBindingID != f.observed.RootBindingID || o.ObjectBindingID != f.observed.ObjectBindingID || o.ChangeToken != f.observed.ChangeToken) {
		return ErrChanged
	}
	return nil
}
func (f *Source) ReadAt(b []byte, offset int64) (int, error) {
	return f.ReadAtContext(f.ctx, b, offset)
}

// ReadAtContext retains this exact physical reader/lease, composing an operation
// deadline with source retirement all the way into acquisition and extent IO.
func (f *Source) ReadAtContext(caller context.Context, b []byte, offset int64) (result int, failure error) {
	ctx, cancel := context.WithCancel(caller)
	stop := context.AfterFunc(f.ctx, cancel)
	defer stop()
	defer cancel()
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	defer func() {
		if failure != nil && failure != io.EOF && !errors.Is(failure, storage.ErrBusy) && !errors.Is(failure, context.Canceled) && !errors.Is(failure, context.DeadlineExceeded) && f.onFailure != nil {
			f.onFailure()
			f.Close()
		}
	}()
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 || offset > f.Size {
		return 0, ErrInvalid
	}
	if len(b) == 0 {
		return 0, nil
	}
	if err := f.validateCurrent(ctx); err != nil {
		return 0, err
	}
	if f.remoteSession != nil {
		if f.Sequential() {
			return 0, storage.ErrRemoteRange
		}
		if f.remoteRange == nil {
			var err error
			f.remoteRange, err = f.remoteSession.remote.OpenRange(ctx)
			if err != nil {
				return 0, fmt.Errorf("reading range acquisition: %w", err)
			}
		}
		n, err := f.remoteRange.ReadAt(ctx, b, offset)
		if canceled := ctx.Err(); canceled != nil {
			clear(b[:n])
			return 0, canceled
		}
		if err != nil && err != io.EOF {
			return n, fmt.Errorf("reading range extent: %w", err)
		}
		return n, err
	}
	wanted := min(int64(len(b)), f.Size-offset)
	done := 0
	for int64(done) < wanted {
		n := int(min(wanted-int64(done), storage.ObservedReadLimit))
		data, err := f.reader.ReadExtent(ctx, offset+int64(done), n)
		if err != nil {
			return done, err
		}
		o, err := f.reader.Observe(ctx)
		if err == nil {
			err = f.checkObservation(o)
		}
		if err == nil {
			err = f.validateCurrent(ctx)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return done, err
		}
		copy(b[done:], data)
		done += n
	}
	if done < len(b) {
		return done, io.EOF
	}
	return done, nil
}
func (f *Source) Close() error {
	f.closeOnce.Do(func() {
		if f.derived != nil && !f.derivedRetained {
			f.derived.Close()
		}
		if f.remoteCancel != nil {
			f.remoteCancel()
		}
		if f.reader != nil {
			f.reader.Close()
		}
		if f.roots != nil {
			f.roots.Close()
		}
		f.mu.Lock()
		if f.remoteRange != nil {
			f.remoteRange.Close()
		}
		f.mu.Unlock()
		if f.remoteSession != nil && !f.remoteRetained {
			f.remoteSession.close()
		}
		if f.onClose != nil {
			f.onClose()
		}
	})
	return nil
}

// Validate is also called after buffered parsing/rendering and immediately
// before publication; CPU work must not turn a revoked read into a response.
func (f *Source) Validate() error { return f.ValidateContext(f.ctx) }
func (f *Source) ValidateContext(caller context.Context) (failure error) {
	ctx, cancel := context.WithCancel(caller)
	stop := context.AfterFunc(f.ctx, cancel)
	defer stop()
	defer cancel()
	if err := f.ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if failure != nil && !errors.Is(failure, storage.ErrBusy) && !errors.Is(failure, context.Canceled) && !errors.Is(failure, context.DeadlineExceeded) && f.onFailure != nil {
			f.onFailure()
			f.Close()
		}
	}()
	if f.derived != nil && (f.derived.cache.size() == 0 || f.service.Mobi == nil || f.service.Mobi.bundle.WorkerSHA256 != f.derived.cache.provenance.WorkerSHA256) {
		return ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.validateCurrent(ctx); err != nil {
		return err
	}
	if f.remoteSession != nil {
		f.mu.Lock()
		reader := f.remoteRange
		f.mu.Unlock()
		if reader != nil {
			return reader.Check(ctx)
		}
		return f.remoteSession.remote.Check(ctx)
	}
	o, err := f.reader.Observe(ctx)
	if err != nil {
		return err
	}
	if err := f.checkObservation(o); err != nil {
		return err
	}
	return ctx.Err()
}

// CheckSelectionTx proves this already-acquired source's captured Reading
// authority/revision inside a caller transaction. It performs no filesystem IO
// and grants no download, offline or write authority: callers still need their
// actual command policy and Validate/ReadAt observation checks outside the writer.
// The original Source.Context must own the surrounding operation lifetime.
func (f *Source) CheckSelectionTx(ctx context.Context, tx *sql.Tx) error {
	if f == nil || f.service == nil || ctx == nil || tx == nil {
		return ErrInvalid
	}
	if err := f.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := f.service.selectTx(ctx, tx, f.p, f.ItemID, f.SourceID)
	if err != nil {
		return err
	}
	if current.Revision != f.Revision {
		return ErrChanged
	}
	if f.remoteSession != nil {
		if err = f.remoteSession.remote.CheckTx(ctx, tx); err != nil {
			return err
		}
	}
	if err = f.ctx.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

// StopAndRecheck ends a failed decode without reopening or retargeting its
// source. Local custody supplies a fresh descriptor observation before closing.
// Request cancellation only retires that request; it does not close the book.
func (f *Source) StopAndRecheck(cause error) error {
	if err := f.ctx.Err(); err != nil {
		return err
	}
	if errors.Is(cause, storage.ErrBusy) || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	var err error
	if f.remoteSession != nil {
		err = f.remoteSession.remote.StopAndRecheck(f.ctx)
		f.remoteSession.close()
	} else {
		err = f.Validate()
	}
	if f.onFailure != nil {
		f.onFailure()
	}
	f.Close()
	if err != nil {
		return err
	}
	return cause
}

// PDFRange never buffers the publication, and never substitutes a full body
// for a finite request. The HTTP layer supplies exact 206/conditional semantics.
func (f *Source) PDFRange(start, endExclusive int64) (io.Reader, error) {
	if f.Format != "pdf" || start < 0 || endExclusive <= start || endExclusive > f.Size || endExclusive-start > 2<<20 {
		return nil, ErrInvalid
	}
	return io.NewSectionReader(f, start, endExclusive-start), nil
}

// LocalArchiveStaging grants only bounded staging through an already acquired
// local rooted reader. A remote range lease or a derived publication is not
// authority to download and stage the entire source.
func (f *Source) LocalArchiveStaging() bool {
	return f != nil && f.reader != nil && f.validateCurrent != nil && f.derived == nil && f.remoteSession == nil && f.remoteRange == nil && !f.remoteRetained
}

// Recheck uses the actual opener's captured currentness callback and physical
// observation, combining its retirement with the helper's remaining deadline.
func (f *Source) Recheck(ctx context.Context) error {
	if !f.LocalArchiveStaging() {
		return ErrArchiveFormat
	}
	return f.ValidateContext(ctx)
}
