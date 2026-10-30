package reading

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

const sessionIdle = 30 * time.Minute

// Session is an ephemeral reader lifecycle, never a bearer grant. Current
// durable identity/library/title authority is checked for every acquisition and
// extent. It captures exactly one publication version and cannot be retargeted.
type Session struct {
	mobi         *mobiCache
	ID           string
	selection    Selection
	actor, owner string
	family       string
	observed     storage.ObservedFileObservation
	remote       *remotesources.SourceReadLease
	life         context.Context
	cancel       context.CancelFunc
	timer        *time.Timer
	mu           sync.Mutex
	closed       bool
	active       int
}

func (s *Session) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	if s.timer != nil {
		s.timer.Stop()
	}
	remote := s.remote
	cache := s.mobi
	s.mobi = nil
	s.mu.Unlock()
	if cache != nil {
		cache.close()
	}
	if remote != nil {
		_ = remote.Close()
	}
}

func (s *Session) touch() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.life.Err() != nil {
		return false
	}
	if s.active == 0 {
		s.timer.Reset(sessionIdle)
	}
	return true
}

func (s *Session) claim() (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.life.Err() != nil {
		return nil, false
	}
	s.active++
	s.timer.Stop()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.active--
			if !s.closed && s.active == 0 {
				s.timer.Reset(sessionIdle)
			}
		})
	}, true
}

func (s *Service) BeginSession(ctx context.Context, p identity.Principal, item, sourceID string) (*Session, error) {
	f, err := s.Open(ctx, p, item, sourceID, "")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err = f.prepareMobi(); err != nil {
		return nil, err
	}
	if err = f.Validate(); err != nil {
		return nil, err
	}
	session, err := s.captureSession(p, f)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Service) captureSession(p identity.Principal, f *Source) (*Session, error) {
	session, err := s.captureSessionState(p, f)
	if err != nil {
		return nil, err
	}
	// Publish the session into the revocation map before the final live fence.
	// A family change racing capture either reaches that map or fails this
	// check. Never hold sessionMu across a database transaction.
	if err = f.Validate(); err == nil {
		err = session.life.Err()
	}
	if err != nil {
		session.close()
		return nil, err
	}
	return session, nil
}

func (s *Service) captureSessionState(p identity.Principal, f *Source) (*Session, error) {
	if p.ExpiresAt.IsZero() || !p.ExpiresAt.After(time.Now()) {
		return nil, identity.ErrUnauthorized
	}
	// Authorization already ran against the actual principal. Capture its real
	// family identity for immediate retirement; this lookup grants no authority.
	var family string
	err := s.DB.QueryRowContext(f.ctx, `SELECT t.family_id FROM authorization_family_tokens t JOIN authorization_session_families f ON f.id=t.family_id WHERE t.token_hash=? AND f.revoked=0 AND t.generation=f.current_generation`, p.Hash).Scan(&family)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.sessionsClosed {
		return nil, ErrUnavailable
	}
	if s.sessions == nil {
		s.sessions = map[string]*Session{}
	}
	owner := identity.PersonalKey(p.Viewer)
	count := 0
	for id, old := range s.sessions {
		if old.life.Err() != nil {
			old.close()
			delete(s.sessions, id)
			continue
		}
		if old.owner == owner {
			count++
		}
	}
	if len(s.sessions) >= 128 || count >= 8 {
		return nil, ErrLimit
	}
	// This is the actual authenticated token/horizon deadline, independent of
	// idle expiry or provider throttling. It cannot extend captured authority.
	out := f.remoteSession
	if out == nil {
		life, cancel := context.WithDeadline(context.Background(), p.ExpiresAt)
		out = &Session{ID: identity.Token(), selection: f.Selection, actor: p.Hash, owner: owner, observed: f.observed, life: life, cancel: cancel}
	}
	out.mu.Lock()
	if out.closed || out.life.Err() != nil {
		out.mu.Unlock()
		return nil, ErrChanged
	}
	out.family = family
	if f.derived != nil {
		out.mobi = f.derived.cache
		f.derivedRetained = true
	}
	out.timer = time.AfterFunc(sessionIdle, out.close)
	out.mu.Unlock()
	f.remoteRetained = true
	context.AfterFunc(out.life, out.close)
	s.sessions[out.ID] = out
	return out, nil
}

// OnFamilyRevokedTx composes with identity's existing family lifecycle hook.
// It performs no SQL mutation or physical wait inside the caller's transaction.
// Closing a narrowed/revoked family retires only its captured reader sessions;
// live actor/library/title/source checks remain mandatory on every extent.
func (s *Service) OnFamilyRevokedTx(ctx context.Context, _ *sql.Tx, family identity.FamilyState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	for id, session := range s.sessions {
		if family.ID != "" && session.family == family.ID {
			session.close()
			delete(s.sessions, id)
		}
	}
	return nil
}

// Prepare inspects exactly the selected publication and captures its reader
// lifecycle from that same physical acquisition. No second source/version is
// substituted between manifest metadata and later per-session reads.
func (s *Service) Prepare(ctx context.Context, p identity.Principal, item, sourceID string) (*Session, Publication, error) {
	f, err := s.Open(ctx, p, item, sourceID, "")
	if err != nil {
		return nil, Publication{}, err
	}
	defer f.Close()
	if err = f.prepareMobi(); err != nil {
		return nil, Publication{}, err
	}
	document, err := Inspect(f)
	if err != nil {
		return nil, Publication{}, f.StopAndRecheck(err)
	}
	if err = s.adoptPublicationType(ctx, p, f.Selection, document.Metadata); err != nil {
		return nil, Publication{}, err
	}
	if err = s.adoptEmbeddedFacts(ctx, p, f.Selection, document.EmbeddedAuthor, document.EmbeddedSeries, document.EmbeddedSeriesNumber); err != nil {
		return nil, Publication{}, err
	}
	if err = s.adoptTextCount(ctx, p, f); err != nil {
		return nil, Publication{}, err
	}
	if err = s.adoptComicPageCount(ctx, p, f.Selection, len(document.PageNames)); err != nil {
		return nil, Publication{}, err
	}
	// Catalogue identity is authoritative; do not resurrect package creators.
	// This is a current authorized DB snapshot, with no added provider Stat or
	// archive read and no positive eligibility cache.
	values, cover, err := s.publicationAttributions(ctx, p, f.Selection)
	if err != nil {
		return nil, Publication{}, err
	}
	if cover == "" && s.AdoptCover != nil && document.cover != nil {
		if err = s.adoptPublicationCover(f, document); err != nil {
			return nil, Publication{}, err
		}
		values, cover, err = s.publicationAttributions(ctx, p, f.Selection)
		if err != nil {
			return nil, Publication{}, err
		}
	}
	document.Metadata.CoverURL = cover
	document.PublicationType = values.PublicationType
	if values.Author != nil {
		document.Metadata.Author = values.Author.Value
	}
	var pageCount *int64
	document.Metadata.WordCount, document.Metadata.OpeningLines, pageCount, err = s.publicationTextFacts(ctx, p, f.Selection)
	if err != nil {
		return nil, Publication{}, err
	}
	if document.Metadata.WordCount != nil {
		seconds := (*document.Metadata.WordCount*60 + 249) / 250
		document.Metadata.EstimatedSeconds = &seconds
	}
	if (document.Selection.Format == "cbz" || document.Selection.Format == "cb7" || document.Selection.Format == "cbr") && len(document.PageNames) > 0 {
		count := int64(len(document.PageNames))
		document.Metadata.PageCount = &count
	} else if document.Selection.Format == "pdf" && pageCount != nil {
		// Exact structural pages prepared from the same canonical source/rule;
		// never used for reading time or Short reads membership.
		document.Metadata.PageCount = pageCount
	}
	if values.Series != nil {
		document.Metadata.SeriesLibraryID = document.Selection.library
		series := &SeriesValue{ID: compactcatalog.ReadingSeriesID(document.Selection.library, values.Series.Value), Name: values.Series.Value, Source: values.Series.Source}
		if values.SeriesNumber != nil {
			number := values.SeriesNumber.Value
			series.Number = &number
		}
		document.Metadata.Series = series
	}
	if err = f.Validate(); err != nil {
		return nil, Publication{}, err
	}
	session, err := s.captureSession(p, f)
	return session, document, err
}

func (s *Service) SessionProgress(ctx context.Context, p identity.Principal, id string) (*Progress, error) {
	f, err := s.OpenSession(ctx, p, id)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Progress()
}
func (s *Service) SaveSessionProgress(ctx context.Context, p identity.Principal, id string, expected int64, location Location) (*Progress, bool, error) {
	f, err := s.OpenSession(ctx, p, id)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	return f.SaveProgress(expected, location)
}

func (s *Service) findSession(p identity.Principal, id string) (*Session, error) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.sessionsClosed {
		return nil, ErrUnavailable
	}
	session := s.sessions[id]
	if session == nil || session.actor != p.Hash || session.owner != identity.PersonalKey(p.Viewer) || !session.touch() {
		return nil, identity.ErrForbidden
	}
	return session, nil
}

// OpenSession binds the request's cancellation to the retained reader lifetime.
// Local acquisitions are freshly rooted/observed and must match the captured
// version; remote leases will attach here only through their checked producer.
func (s *Service) OpenSession(ctx context.Context, p identity.Principal, id string) (*Source, error) {
	session, err := s.findSession(p, id)
	if err != nil {
		return nil, err
	}
	release, ok := session.claim()
	if !ok {
		return nil, identity.ErrForbidden
	}
	request, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(session.life, cancel)
	owner := &sessionRequestContext{Context: request, owner: session.life}
	var f *Source
	if session.remote != nil {
		var v Selection
		v, err = s.Resolve(owner, p, session.selection.ItemID, session.selection.SourceID)
		if err == nil && v.Revision != session.selection.Revision {
			err = ErrChanged
		}
		if err == nil {
			f, err = s.openRemote(owner, p, v, session)
		}
	} else {
		f, err = s.Open(owner, p, session.selection.ItemID, session.selection.SourceID, session.selection.Revision)
	}
	if err != nil {
		stop()
		cancel()
		release()
		if !errors.Is(err, storage.ErrBusy) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			session.close()
		}
		return nil, err
	}
	if f.observed.ObjectBindingID != session.observed.ObjectBindingID || f.observed.ChangeToken != session.observed.ChangeToken {
		f.Close()
		stop()
		cancel()
		release()
		session.close()
		return nil, ErrChanged
	}
	session.mu.Lock()
	cache := session.mobi
	closed := session.closed
	session.mu.Unlock()
	if closed {
		f.Close()
		stop()
		cancel()
		release()
		return nil, ErrChanged
	}
	if cache != nil {
		provenance := cache.provenance
		if provenance.OriginalRevision != f.Revision || provenance.OriginalSourceID != f.SourceID || provenance.OriginalFormat != f.Format || cache.size() == 0 || s.Mobi == nil || provenance.WorkerSHA256 != s.Mobi.bundle.WorkerSHA256 {
			f.Close()
			stop()
			cancel()
			release()
			session.close()
			return nil, ErrChanged
		}
		f.derived = &DerivedMobi{source: f, cache: cache}
		f.derivedRetained = true
	}
	f.onClose = func() { stop(); cancel(); release() }
	f.onFailure = session.close
	return f, nil
}

// CloseSessionContext also covers client PDF decode failures, whose parser
// runs outside the server. Every explicit remote close releases child bodies,
// rechecks the captured epoch without reopening media, and retires the reader.
// Cleanup remains allowed for one's own hidden title and publishes no facts.
func (s *Service) CloseSessionContext(ctx context.Context, p identity.Principal, id string) error {
	session, err := s.findSession(p, id)
	if err != nil {
		return err
	}
	if session.remote != nil {
		_ = session.remote.StopAndRecheck(ctx)
	}
	return s.closeCapturedSession(id, session)
}

func (s *Service) CloseSession(p identity.Principal, id string) error {
	session, err := s.findSession(p, id)
	if err != nil {
		return err
	}
	return s.closeCapturedSession(id, session)
}
func (s *Service) closeCapturedSession(id string, session *Session) error {
	session.close()
	s.sessionMu.Lock()
	delete(s.sessions, id)
	s.sessionMu.Unlock()
	return nil
}
func (s *Service) Close() error {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	s.sessionsClosed = true
	for id, session := range s.sessions {
		session.close()
		delete(s.sessions, id)
	}
	return nil
}
