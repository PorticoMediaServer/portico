package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dirsync"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/progressio"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

const artworkBytes = 8 << 20

var archiveItemPath = regexp.MustCompile(`^/[0-9]+/items/mbid-`)
var archiveHost = regexp.MustCompile(`^ia[0-9]+\.(us|eu)\.archive\.org$`)

func artworkURLAllowed(raw, provider string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || u.RawQuery != "" || strings.Contains(u.Path, "..") || strings.ContainsAny(u.Path, "\\\x00") || u.RawPath != "" {
		return false
	}
	switch provider {
	case "tmdb":
		return u.Host == "image.tmdb.org" && strings.HasPrefix(u.Path, "/t/p/")
	case "tvdb":
		return u.Host == "artworks.thetvdb.com" && strings.HasPrefix(u.Path, "/banners/")
	case "coverartarchive":
		return (u.Host == "coverartarchive.org" && strings.HasPrefix(u.Path, "/release/")) || ((u.Host == "archive.org" || archiveHost.MatchString(u.Host)) && (strings.HasPrefix(u.Path, "/download/mbid-") || archiveItemPath.MatchString(u.Path)))
	case "commons":
		return u.Host == "upload.wikimedia.org" && strings.HasPrefix(u.Path, "/wikipedia/commons/")
	}
	return false
}

// DNS is resolved at the dial boundary, so an allowed hostname cannot rebind to
// a private address between validation and connect. No proxy or credentials.
func newArtworkClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.MaxConnsPerHost = 2
	tr.MaxResponseHeaderBytes = 64 << 10
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 10 * time.Second
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		if len(ips) == 0 {
			return nil, ErrArtworkPending
		}
		for _, ip := range ips {
			v := ip.IP
			if !v.IsGlobalUnicast() || v.IsPrivate() || v.IsLoopback() || v.IsLinkLocalUnicast() || v.To4() != nil && (v.To4()[0] == 100 && v.To4()[1] >= 64 && v.To4()[1] <= 127 || v.To4()[0] == 198 && (v.To4()[1] == 18 || v.To4()[1] == 19)) {
				return nil, errors.New("non-public artwork origin")
			}
		}
		dial := net.Dialer{Timeout: 10 * time.Second}
		var last error
		for _, ip := range ips {
			conn, e := dial.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Transport: progressio.Transport{Base: tr, Idle: 30 * time.Second, Overall: 15 * time.Minute}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (s *Service) fetchArtwork(ctx context.Context, origin, provider string) ([]byte, error) {
	if provider == "local" {
		if !strings.HasPrefix(origin, "local:") || s.LocalArtwork == nil {
			return nil, ErrArtworkPending
		}
		f, _, err := s.LocalArtwork(strings.TrimPrefix(origin, "local:"))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return boundedArtworkRead(f)
	}
	return s.fetchArtworkRemote(ctx, origin, provider, artworkBytes, "image/*")
}
func (s *Service) fetchArtworkRemote(ctx context.Context, origin, provider string, limit int64, accept string) ([]byte, error) {
	ctx = s.lookupContext(ctx)
	check, err := metadataprovider.BeginNetworkAdmission(ctx)
	if err != nil {
		return nil, err
	}
	client := s.artClient
	if client == nil {
		client = newArtworkClient()
	}
	// Redirects are explicitly revalidated; caller/provider headers are never forwarded.
	for n := 0; n <= 4; n++ {
		if !artworkURLAllowed(origin, provider) {
			return nil, errors.New("origin_rejected")
		}
		req, err := http.NewRequestWithContext(ctx, "GET", origin, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Portico/0.1 (https://getportico.tv)")
		req.Header.Set("Accept", accept)
		if err := check(); err != nil {
			return nil, err
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, errors.New("offline")
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			next, e := response.Location()
			response.Body.Close()
			if e != nil || n == 4 {
				return nil, errors.New("redirect_rejected")
			}
			origin = next.String()
			continue
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			if response.StatusCode == 429 {
				return nil, &artworkRetry{code: "rate_limited", after: artworkRetryAfter(response.Header.Get("Retry-After"), s.publicationTime())}
			}
			if response.StatusCode == 404 {
				return nil, errors.New("not_found")
			}
			return nil, errors.New("provider_unavailable")
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if int64(len(raw)) > limit {
			e = errors.New("size_limit")
			raw = nil
		}
		response.Body.Close()
		if err := check(); err != nil {
			return nil, err
		}
		return raw, e
	}
	return nil, ErrArtworkPending
}

type artworkRetry struct {
	code  string
	after time.Time
}

func (e *artworkRetry) Error() string { return e.code }
func artworkRetryAfter(value string, now time.Time) time.Time {
	if n, e := strconv.Atoi(value); e == nil && n >= 0 {
		return now.Add(time.Duration(min(n, 86400)) * time.Second)
	}
	if v, e := http.ParseTime(value); e == nil && v.After(now) {
		if v.After(now.Add(24 * time.Hour)) {
			return now.Add(24 * time.Hour)
		}
		return v
	}
	return now.Add(time.Minute)
}
func boundedArtworkRead(r io.Reader) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, artworkBytes+1))
	if e != nil {
		return nil, e
	}
	if len(b) > artworkBytes {
		return nil, errors.New("size_limit")
	}
	return b, nil
}

type artworkInstalled struct {
	digest, mime  string
	width, height int
	size          int
	// raw keeps the bytes until the referencing transaction has committed, so
	// a concurrently retired file can be reinstalled (artwork_lifecycle.go).
	raw []byte
}

// ErrArtworkSpace is a refusal, not a failure: the image is fine, the volume is
// not. The work stays queued and runs once there is room, because artwork that
// fills the disk holding server.sqlite costs far more than artwork that arrives
// late.
var ErrArtworkSpace = errors.New("not enough free space to store artwork")

func (s *Service) installArtworkFile(raw []byte, w, h int) (artworkInstalled, error) {
	sum := sha256.Sum256(raw)
	a := artworkInstalled{digest: hex.EncodeToString(sum[:]), mime: http.DetectContentType(raw), width: w, height: h, size: len(raw), raw: raw}
	if !diskspace.Room(s.cacheRoot, diskspace.ProducerFloor) {
		return a, ErrArtworkSpace
	}
	target := filepath.Join(s.cacheRoot, a.digest+".img")
	f, err := os.CreateTemp(s.cacheRoot, "stage-")
	if err != nil {
		return a, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return a, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return a, err
	}
	if err = f.Close(); err != nil {
		return a, err
	}
	if existing, e := s.openArtworkObject(a.digest); e == nil {
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(existing, int64(len(raw))+1))
		existing.Close()
		if e == nil && n == int64(len(raw)) && hex.EncodeToString(h.Sum(nil)) == a.digest {
			return a, nil
		}
	}
	if err = os.Rename(temp, target); err != nil {
		return a, err
	}
	return a, dirsync.Sync(s.cacheRoot)
}

type artworkWork struct {
	id, lease                                                string
	target                                                   RepairTarget
	role, subject, candidate, origin, provider, fence, actor string
	revision                                                 int64
	preview                                                  bool
	sizeRefresh                                              bool
	locked                                                   bool
	attempts                                                 int
}

func (s *Service) artworkJobEnabled(ctx context.Context, tx *sql.Tx, j artworkWork) (bool, error) {
	// Generated pictures can only be remade by the episode runtime with its
	// source authority. They are never a provider or local-image acquisition.
	if j.provider == "generated" {
		return false, nil
	}
	ent, err := readArtworkTarget(ctx, tx, j.target)
	if err != nil {
		return false, err
	}
	allowed, err := EpisodeProviderStillAllowedTx(ctx, tx, ent.library, ent.itemKind, j.role, j.actor, j.provider)
	if err != nil || !allowed {
		return false, err
	}
	return s.artworkProviderEnabledForEntity(ctx, tx, ent, j.provider)
}

func (s *Service) ArtworkStep(ctx context.Context) error {
	if s.cacheRoot == "" || !diskspace.Room(s.cacheRoot, diskspace.ProducerFloor) {
		// Wait for capacity before acquiring a lease or downloading any bytes.
		return nil
	}
	ctx, checkPolicy, err := s.CaptureArtworkContext(ctx)
	if err != nil {
		return err
	}
	if err := s.seedArtwork(ctx); err != nil {
		return err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var j artworkWork
	var entity int64
	now := s.publicationTime()
	stamp := now.Format(time.RFC3339Nano)
	// One indexed probe per state, each reading one row, merged in the old
	// order (main artwork first, then the oldest job): the claim runs in a write
	// transaction, and a sort of every due job held the write gate for as long
	// as the queue was long. A due retry is taken in the order its retry time
	// came round.
	err = tx.QueryRowContext(ctx, artworkJobClaimSQL, stamp).Scan(&j.id, &j.target.Kind, &entity, &j.role, &j.subject, &j.candidate, &j.origin, &j.provider, &j.fence, &j.revision, &j.actor, &j.locked, &j.attempts, &j.preview, &j.sizeRefresh)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// A running job is claimed again only when its lease expired without a
	// publication or a recorded failure. Those turns count against the same
	// attempt cap as a failed fetch, so one job can't hold the lane forever.
	var claimed string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM artwork_jobs WHERE id=?`, j.id).Scan(&claimed); err != nil {
		return err
	}
	if j.target.ID, err = entityid.Public(ctx, tx, entity); errors.Is(err, entityid.ErrNotFound) {
		// The entity is gone; retire its job instead of wedging the step.
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',error='entity_missing',lease='',lease_until='' WHERE id=?`, j.id); err != nil {
			return err
		}
		return gated.Commit()
	} else if err != nil {
		return err
	}
	// A vanished entity is reported as such even at the attempt cap.
	if claimed == "running" && j.attempts >= artworkAttemptLimit {
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='failed',error='acquisition_failed',lease='',lease_until='' WHERE id=?`, j.id); err != nil {
			return err
		}
		return gated.Commit()
	}
	enabled, err := s.artworkJobEnabled(ctx, tx, j)
	if err != nil {
		return err
	}
	if !enabled {
		_, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='disabled',error='provider_disabled',lease='',lease_until='' WHERE id=?`, j.id)
		if err != nil {
			return err
		}
		return gated.Commit()
	}
	fence, err := artworkFence(ctx, tx, j.target)
	if err != nil {
		return err
	}
	if fence != j.fence {
		_, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',error='source_or_match_changed' WHERE id=?`, j.id)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO artwork_dirty VALUES(?,?)`, j.target.Kind, entity); err != nil {
			return err
		}
		return gated.Commit()
	}
	held, err := artworkSlotHeld(ctx, tx, j, entity)
	if err != nil {
		return err
	}
	if held {
		// Nothing is fetched for a slot this job may not fill.
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',error='slot_held',lease='',lease_until='' WHERE id=?`, j.id); err != nil {
			return err
		}
		return gated.Commit()
	}
	j.lease = identity.Token()
	_, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='running',lease=?,lease_until=?,attempts=attempts+1 WHERE id=?`, j.lease, now.Add(artworkLease).Format(time.RFC3339Nano), j.id)
	if err != nil {
		return err
	}
	if err = gated.Commit(); err != nil {
		return err
	}
	workCtx, cancel := s.artworkRequestContext(ctx, j.target, j.provider, j.fence,
		s.renewArtworkLease(`UPDATE artwork_jobs SET lease_until=? WHERE lease_until>? AND id=? AND lease=? AND status='running'`, j.id, j.lease))
	defer cancel()
	material, err := s.acquireArtworkMaterial(workCtx, j)
	if err != nil {
		return s.failArtwork(ctx, j, err)
	}
	var gated2 *dbwork.Write
	gated2, err = dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx = gated2.Tx()
	defer gated2.Rollback()
	commit := func() error {
		if err := gated2.Commit(); err != nil {
			return err
		}
		return s.ensureArtworkFiles(material.installed()...)
	}
	var live bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_jobs WHERE id=? AND status='running' AND lease=? AND lease_until>?)`, j.id, j.lease, s.publicationTime().Format(time.RFC3339Nano)).Scan(&live)
	if err != nil {
		return err
	}
	if !live {
		return nil
	}
	if err = checkPolicy(ctx, tx); err != nil {
		if !errors.Is(err, ErrEpisodeThumbnailEncodingStale) {
			return err
		}
		// Pixels acquired under an old policy cannot be published. The next
		// bounded turn captures the new policy before fetching/encoding again.
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='retry',next_attempt='',attempts=0,lease='',lease_until='',error='encoding_policy_changed' WHERE id=? AND lease=?`, j.id, j.lease); err != nil {
			return err
		}
		return gated2.Commit()
	}
	fence, err = artworkFence(ctx, tx, j.target)
	if err != nil {
		return err
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND subject=?),0)`, j.target.Kind, entity, j.role, j.subject).Scan(&revision); err != nil {
		return err
	}
	enabled, err = s.artworkJobEnabled(ctx, tx, j)
	if err != nil {
		return err
	}
	if fence != j.fence || (!j.preview && revision != j.revision) || !enabled {
		_, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',error='source_match_or_selection_changed',lease='',lease_until='' WHERE id=? AND lease=?`, j.id, j.lease)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO artwork_dirty VALUES(?,?)`, j.target.Kind, entity); err != nil {
			return err
		}
		return commit()
	}
	// The slot may have been taken while the image was being fetched.
	if held, err = artworkSlotHeld(ctx, tx, j, entity); err != nil {
		return err
	}
	if held {
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',error='slot_held',lease='',lease_until='' WHERE id=? AND lease=?`, j.id, j.lease); err != nil {
			return err
		}
		return commit()
	}
	before, ent, err := readRepairSnapshot(ctx, tx, j.target)
	if err != nil {
		return err
	}
	base, err := repairRevision(ctx, tx, j.target, before, ent)
	if err != nil {
		return err
	}
	stamp = s.publicationTime().Format(time.RFC3339Nano)
	if err = publishArtworkSource(ctx, tx, material, stamp); err != nil {
		return err
	}
	if j.preview {
		if _, err = tx.ExecContext(ctx, `INSERT INTO artwork_previews VALUES(?,?,?,?) ON CONFLICT(candidate_id) DO UPDATE SET digest=excluded.digest,source_fence=excluded.source_fence,created_at=excluded.created_at`, j.candidate, material.source, j.fence, stamp); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='complete',lease='',lease_until='',error='' WHERE id=? AND lease=?`, j.id, j.lease); err != nil {
			return err
		}
		return commit()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artwork_selections VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(kind,entity_id,role,subject) DO UPDATE SET candidate_id=excluded.candidate_id,digest=excluded.digest,locked=excluded.locked,revision=artwork_selections.revision+1,actor=CASE WHEN ? THEN artwork_selections.actor ELSE excluded.actor END,observed_at=excluded.observed_at`, j.target.Kind, entity, j.role, j.subject, j.candidate, material.source, j.locked, 1, j.actor, stamp, j.sizeRefresh); err != nil {
		return err
	}
	if err = projectSelectedArtwork(ctx, tx, j.target, j.role); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='complete',lease='',lease_until='',error='',selection_revision=? WHERE id=? AND lease=?`, revision+1, j.id, j.lease); err != nil {
		return err
	}
	if j.actor != "" {
		if err = recordRepair(ctx, tx, j.target, before, base, "artwork_published", j.actor); err != nil {
			return err
		}
	}
	return commit()
}

// artworkSlotHeld repeats automatic source preference and owner protection at
// publication, so a queued automatic local or online image cannot replace a
// later owner choice. Explicit owner requests and previews keep their authority.
func artworkSlotHeld(ctx context.Context, tx *sql.Tx, j artworkWork, entity int64) (bool, error) {
	if j.sizeRefresh {
		if j.actor != "" || j.preview || j.provider == "generated" {
			return true, nil
		}
		var exact bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND subject=? AND candidate_id=? AND revision=? AND locked=?)`, j.target.Kind, entity, j.role, j.subject, j.candidate, j.revision, j.locked).Scan(&exact)
		return !exact, err
	}
	if j.actor != "" || j.preview {
		return false, nil
	}
	return automaticArtworkOccupied(ctx, tx, j.target, entity, j.role, j.subject, j.provider, j.candidate, j.fence, false)
}

// artworkAttemptLimit is how many turns an artwork job or discovery gets
// before it fails for good.
const artworkAttemptLimit = 5

func (s *Service) failArtwork(ctx context.Context, j artworkWork, problem error) error {
	code := problem.Error()
	allowed := map[string]bool{"offline": true, "rate_limited": true, "not_found": true, "origin_rejected": true, "redirect_rejected": true, "size_limit": true, "unsupported_image": true, "dimension_limit": true, "malformed_image": true, "cap_unreachable": true, "storage_unavailable": true, "provider_unavailable": true}
	if !allowed[code] {
		code = "acquisition_failed"
	}
	status := "retry"
	if (j.attempts >= artworkAttemptLimit && code != "storage_unavailable") || code == "origin_rejected" || code == "unsupported_image" || code == "dimension_limit" || code == "cap_unreachable" {
		status = "failed"
	}
	delay := time.Duration(30*(1<<min(j.attempts, 8))) * time.Second
	if code == "storage_unavailable" {
		delay = 5 * time.Minute
	}
	next := s.publicationTime().Add(delay)
	var retry *artworkRetry
	if errors.As(problem, &retry) && retry.after.After(next) {
		next = retry.after
	}
	result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE artwork_jobs SET status=?,error=?,next_attempt=?,lease='',lease_until='' WHERE id=? AND lease=? AND status='running'`, status, code, next.Format(time.RFC3339Nano), j.id, j.lease)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 1 {
		return recordArtworkRefusal(ctx, s.db, problem)
	}
	return nil
}
func projectSelectedArtwork(ctx context.Context, tx *sql.Tx, t RepairTarget, role string) error {
	entity, err := artworkEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	// The projection writes the details column directly with the same guard the
	// old items UPDATE had; it does not go through the write API, so an owner
	// lock on poster_url/backdrop_url is not consulted here (same as before).
	// The direct write queues its derived work explicitly below.
	if t.Kind == "item" && (role == "poster" || role == "backdrop" || role == "cover") {
		col := "poster_url"
		if role == "backdrop" {
			col = "backdrop_url"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_item_details(entity_id,`+col+`) VALUES(?,?) ON CONFLICT(entity_id) DO UPDATE SET `+col+`=excluded.`+col+` WHERE catalog_item_details.`+col+`=''`, entity, "artwork:"+role); err != nil {
			return err
		}
		return compactcatalog.TouchTx(ctx, tx, compactcatalog.DomainBrowseRows, entity)
	}
	if t.Kind == "book" && role == "cover" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_item_details(entity_id,poster_url) SELECT entity_id,? FROM catalog_book_files WHERE book_id=? ON CONFLICT(entity_id) DO UPDATE SET poster_url=excluded.poster_url WHERE catalog_item_details.poster_url='' OR catalog_item_details.poster_url LIKE 'artwork:book:%'`, "artwork:book:"+t.ID, entity); err != nil {
			return err
		}
		members, err := artworkMemberEntities(ctx, tx, `SELECT entity_id FROM catalog_book_files WHERE book_id=?`, entity)
		if err != nil {
			return err
		}
		return compactcatalog.TouchTx(ctx, tx, compactcatalog.DomainBrowseRows, members...)
	}
	if t.Kind == "album" && role == "cover" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_item_details(entity_id,poster_url) SELECT entity_id,? FROM catalog_songs WHERE album_id=? ON CONFLICT(entity_id) DO UPDATE SET poster_url=excluded.poster_url WHERE catalog_item_details.poster_url='' OR catalog_item_details.poster_url LIKE 'artwork:album:%'`, "artwork:album:"+t.ID, entity); err != nil {
			return err
		}
		members, err := artworkMemberEntities(ctx, tx, `SELECT entity_id FROM catalog_songs WHERE album_id=?`, entity)
		if err != nil {
			return err
		}
		return compactcatalog.TouchTx(ctx, tx, compactcatalog.DomainBrowseRows, members...)
	}
	return nil
}

// artworkMemberEntities lists the member entities of a container for the
// projection's derived-work touch.
func artworkMemberEntities(ctx context.Context, tx *sql.Tx, query string, container int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, container)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GC is reference-aware, not an LRU over selected artwork. It runs in two
// passes, each paging 64 rows past an in-memory cursor that wraps at the end,
// so objects kept by a pin never starve the ones after them:
//
//  1. Sources nothing names (selection, upload, preview, size request or an
//     owner-history pin) are deleted. History is streamed once per page, and
//     the delete only commits while no history row was added since that scan.
//  2. Blobs no source names are retired with the tomb protocol
//     (artwork_lifecycle.go): marked deleting, file set aside, row deleted
//     only if still unnamed, otherwise restored.
//
// Open readers own their descriptors; on platforms forbidding unlink of an open
// file, deletion is retried later.
func (s *Service) CleanupArtwork(ctx context.Context) error {
	if s.cacheRoot == "" {
		return nil
	}
	// Expired previews go in bounded batches, each its own transaction.
	cutoff := s.publicationTime().Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)
	for {
		result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM artwork_previews WHERE rowid IN(SELECT rowid FROM artwork_previews WHERE created_at<? LIMIT 128)`, cutoff)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n < 128 {
			break
		}
		if !dbwork.Yield(ctx) {
			return ctx.Err()
		}
	}
	var running bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_jobs WHERE status='running')`).Scan(&running); err != nil || running {
		return err
	}
	cut := s.publicationTime().Add(-24 * time.Hour).Format(time.RFC3339Nano)
	if err := s.collectArtworkSources(ctx, cut); err != nil {
		return err
	}
	if err := s.collectArtworkBlobs(ctx, cut); err != nil {
		return err
	}
	// Crash-before-database-publication leaves only unreferenced content-addressed
	// files or private stages. Finite directory batches retain all younger writers.
	entries, err := s.nextArtworkDirectoryBatch()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Mode().IsRegular() || entry.ModTime().After(s.publicationTime().Add(-24*time.Hour)) {
			continue
		}
		name := entry.Name()
		digest := strings.TrimSuffix(name, ".img")
		if strings.HasPrefix(name, "stage-") {
			_ = os.Remove(filepath.Join(s.cacheRoot, name))
			continue
		}
		if strings.Contains(name, artworkTombMarker) {
			if err = s.settleArtworkTomb(ctx, name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if !artDigest(digest) || digest+".img" != name {
			continue
		}
		if _, err = s.retireArtworkFile(ctx, digest, func(ctx context.Context) (bool, error) {
			var used bool
			err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_blobs WHERE digest=?)`, digest).Scan(&used)
			return !used, err
		}); err != nil {
			return err
		}
	}
	return nil
}

// ArtCleanupInterval is how often the background loop should call
// CleanupArtwork: objects become collectable only a day after creation, so a
// pass per commit wake buys nothing. The throttle lives in the caller so tests
// and explicit maintenance can still sweep on demand.
const ArtCleanupInterval = 10 * time.Minute

// artSourceUnreferenced is the indexed "nothing names source o.digest"
// predicate: one NOT EXISTS per referencing column, never an OR across two.
// Owner-history pins are text, not keys, and are checked separately.
const artSourceUnreferenced = `NOT EXISTS(SELECT 1 FROM artwork_selections a WHERE a.digest=o.digest) AND NOT EXISTS(SELECT 1 FROM artwork_uploads u WHERE u.digest=o.digest) AND NOT EXISTS(SELECT 1 FROM artwork_previews p WHERE p.digest=o.digest) AND NOT EXISTS(SELECT 1 FROM metadata_artwork_size_requests r WHERE r.digest=o.digest) AND NOT EXISTS(SELECT 1 FROM (` + artworkref.AudioFallbackKeys + `) fallback WHERE fallback.artwork_key=o.digest)`

// artBlobUnreferenced is the indexed "no source names blob o.digest" predicate.
const artBlobUnreferenced = `NOT EXISTS(SELECT 1 FROM artwork_sources x WHERE x.small_blob=o.digest) AND NOT EXISTS(SELECT 1 FROM artwork_sources x WHERE x.large_blob=o.digest) AND NOT EXISTS(SELECT 1 FROM artwork_sources x WHERE x.ambient_blob=o.digest)`

// artNoBackupHold is the running backup's hold (backup/holds.go): while a
// backup copies, nothing it may name is deleted. Every destructive collector
// statement carries it, so a hold taken between reading a page and deleting
// from it still wins.
const artNoBackupHold = `NOT EXISTS(SELECT 1 FROM backup_blob_holds)`

// artworkSweepCursor is a disposable (created_at, digest) position.
type artworkSweepCursor struct{ created, digest string }

// nextArtworkPage reads the next page of unreferenced rows created before cut,
// wrapping to the start once the walk reaches the end.
func (s *Service) nextArtworkPage(ctx context.Context, table string, cursor *artworkSweepCursor, cut, predicate string) ([]string, error) {
	s.artSweepMu.Lock()
	at := *cursor
	s.artSweepMu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT o.digest,o.created_at FROM `+table+` o WHERE o.created_at<? AND (o.created_at>? OR o.created_at=? AND o.digest>?) AND `+predicate+` ORDER BY o.created_at,o.digest LIMIT 64`, cut, at.created, at.created, at.digest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := []string{}
	next := artworkSweepCursor{}
	for rows.Next() {
		if err = rows.Scan(&next.digest, &next.created); err != nil {
			return nil, err
		}
		page = append(page, next.digest)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(page) < 64 {
		next = artworkSweepCursor{}
	}
	s.artSweepMu.Lock()
	*cursor = next
	s.artSweepMu.Unlock()
	return page, nil
}

func (s *Service) collectArtworkSources(ctx context.Context, cut string) error {
	page, err := s.nextArtworkPage(ctx, "artwork_sources", &s.artSourceCursor, cut, artSourceUnreferenced)
	if err != nil || len(page) == 0 {
		return err
	}
	// The high-water mark is read before the history scan. Any pin written
	// after it raises MAX(id) (the id is AUTOINCREMENT, and only its revision
	// column is ever updated in place), and the delete below then refuses
	// this pass rather than racing a new undo point.
	var highWater int64
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM metadata_owner_history`).Scan(&highWater); err != nil {
		return err
	}
	pinned, err := s.ownerHistoryDigests(ctx, page)
	if err != nil {
		return err
	}
	for _, d := range page {
		if pinned[d] || !artDigest(d) {
			continue
		}
		if _, err = s.deleteUnreferencedArtworkSource(ctx, d, highWater); err != nil {
			return err
		}
	}
	return nil
}

// deleteUnreferencedArtworkSource deletes source only while nothing names it
// and no owner-history row was written after highWater was read.
func (s *Service) deleteUnreferencedArtworkSource(ctx context.Context, source string, highWater int64) (bool, error) {
	result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM artwork_sources AS o WHERE o.digest=? AND `+artSourceUnreferenced+` AND `+artNoBackupHold+` AND (SELECT COALESCE(MAX(id),0) FROM metadata_owner_history)=?`, source, highWater)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}

func (s *Service) collectArtworkBlobs(ctx context.Context, cut string) error {
	page, err := s.nextArtworkPage(ctx, "artwork_blobs", &s.artBlobCursor, cut, artBlobUnreferenced)
	if err != nil {
		return err
	}
	for _, d := range page {
		if !artDigest(d) {
			continue
		}
		marked, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE artwork_blobs AS o SET status='deleting' WHERE o.digest=? AND `+artBlobUnreferenced+` AND `+artNoBackupHold, d)
		if err != nil {
			return err
		}
		if n, _ := marked.RowsAffected(); n == 0 {
			continue // named since the page was read
		}
		gone, err := s.retireArtworkFile(ctx, d, func(ctx context.Context) (bool, error) {
			// Still unnamed now that the file is set aside? A publication that
			// named it meanwhile keeps it (and reset it to ready).
			deleted, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM artwork_blobs AS o WHERE o.digest=? AND o.status='deleting' AND `+artBlobUnreferenced+` AND `+artNoBackupHold, d)
			if err != nil {
				return false, err
			}
			n, _ := deleted.RowsAffected()
			return n == 1, nil
		})
		if err != nil {
			return err
		}
		if !gone {
			if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE artwork_blobs SET status='ready' WHERE digest=? AND status='deleting'`, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// Stream one history row at a time; never concatenate the whole owner journal.
func (s *Service) ownerHistoryDigests(ctx context.Context, candidates []string) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT before_json,after_json FROM metadata_owner_history`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var before, after string
		if err = rows.Scan(&before, &after); err != nil {
			return out, err
		}
		for _, digest := range candidates {
			if !out[digest] && (strings.Contains(before, digest) || strings.Contains(after, digest)) {
				out[digest] = true
			}
		}
		if len(out) == len(candidates) {
			break
		}
	}
	return out, rows.Err()
}

// nextArtworkDirectoryBatch returns the next 128 entries of the cache directory
// walk. Only the directory handle is guarded; no database work happens under
// the guard.
func (s *Service) nextArtworkDirectoryBatch() ([]os.FileInfo, error) {
	s.artDirMu.Lock()
	defer s.artDirMu.Unlock()
	if s.artDirectory == nil {
		directory, err := os.Open(s.cacheRoot)
		if err != nil {
			return nil, err
		}
		s.artDirectory = directory
	}
	entries, err := s.artDirectory.Readdir(128)
	if err == io.EOF {
		s.artDirectory.Close()
		s.artDirectory = nil
		return entries, nil
	}
	return entries, err
}

var artworkJobClaimSQL = `SELECT id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,selection_revision,actor,locked,attempts,preview,size_refresh FROM (
 SELECT * FROM (SELECT id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,selection_revision,actor,locked,attempts,preview,size_refresh,(CASE WHEN role IN('poster','cover','backdrop') THEN 0 ELSE 1 END) AS prime,created_at AS created FROM artwork_jobs INDEXED BY artwork_jobs_claim WHERE status='pending' AND (CASE WHEN role IN('poster','cover','backdrop') THEN 0 ELSE 1 END)=0 AND next_attempt<=?1 ORDER BY created_at,id LIMIT 1)
 UNION ALL SELECT * FROM (SELECT id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,selection_revision,actor,locked,attempts,preview,size_refresh,(CASE WHEN role IN('poster','cover','backdrop') THEN 0 ELSE 1 END),created_at FROM artwork_jobs INDEXED BY artwork_jobs_claim WHERE status='pending' AND (CASE WHEN role IN('poster','cover','backdrop') THEN 0 ELSE 1 END)=1 AND next_attempt<=?1 ORDER BY created_at,id LIMIT 1)
 UNION ALL SELECT * FROM (SELECT id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,selection_revision,actor,locked,attempts,preview,size_refresh,(CASE WHEN role IN('poster','cover','backdrop') THEN 0 ELSE 1 END),created_at FROM artwork_jobs INDEXED BY artwork_jobs_due WHERE status='retry' AND next_attempt<=?1 ORDER BY next_attempt LIMIT 1)
 UNION ALL SELECT * FROM (SELECT id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,selection_revision,actor,locked,attempts,preview,size_refresh,(CASE WHEN role IN('poster','cover','backdrop') THEN 0 ELSE 1 END),created_at FROM artwork_jobs INDEXED BY artwork_jobs_due WHERE status='running' AND lease_until<=?1 ORDER BY created_at,id LIMIT 1)
 UNION ALL SELECT * FROM (SELECT id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,selection_revision,actor,locked,attempts,preview,size_refresh,(CASE WHEN role IN('poster','cover','backdrop') THEN 0 ELSE 1 END),created_at FROM artwork_jobs INDEXED BY artwork_jobs_storage_retry WHERE status='failed' AND error='storage_unavailable' AND next_attempt<=?1 ORDER BY next_attempt LIMIT 1)
) ORDER BY prime,created,id LIMIT 1`
