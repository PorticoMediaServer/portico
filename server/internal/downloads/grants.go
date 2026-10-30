package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Grant is the address of one ready preparation's bytes. The URL is a path on
// this server; the token is the whole authority, so it is returned once and
// stored only as a digest.
//
// The address is good for as long as it is used or renewed, and for GrantIdle
// after the last of either. RenewAfterMs tells a client when to renew a
// transfer that is still unfinished; renewing keeps the same address, which is
// what lets a phone's own downloader pick a paused transfer up again hours
// later without the app having to restart it.
type Grant struct {
	PreparationID string   `json:"preparationId"`
	ItemID        string   `json:"itemId"`
	URL           string   `json:"url"`
	Token         string   `json:"token"`
	IssuedAt      string   `json:"issuedAt"`
	ExpiresAt     string   `json:"expiresAt"`
	RenewAfterMs  int64    `json:"renewAfterMs"`
	Renewed       bool     `json:"renewed"`
	Artifact      Artifact `json:"artifact"`
}

// GrantRequest asks for a transfer address. Renew names the token of an
// address this client already holds for the same download: when it still
// belongs to the same file it is extended and returned; otherwise a new one is
// issued, and the client continues its partial file under the new address,
// because the file's identity is its SHA-256, not where it was fetched from.
type GrantRequest struct {
	OperationID string `json:"operationId"`
	Renew       string `json:"renew,omitempty"`
}

// ErrUnavailable is a claim the server can no longer transfer: its file was
// replaced or removed since it was prepared. The claim has been ended; asking
// for the title again prepares what is there now.
var ErrUnavailable = errors.New("the download can no longer be transferred")

// ErrSourceOffline is a file that should be there and cannot be read right
// now (a library drive that is not mounted). Nothing has changed; a transfer
// waits and tries again.
var ErrSourceOffline = errors.New("the library file cannot be read right now")

// usable confirms, inside the caller's transaction, that a ready claim still
// points at bytes this server can hand over, and ends the claim when it does
// not. A converted file must still be published; an original must still be the
// file that was read.
func (s *Service) usable(ctx context.Context, tx *sql.Tx, id, item, kind, ref, digest string, size, modifiedNS, now int64, write bool) (pinned, error) {
	var file pinned
	reason := ""
	switch kind {
	case "prepared":
		var published bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM prepared_media_versions WHERE id=? AND digest=? AND size=? AND state='published')`, ref, digest, size).Scan(&published); e != nil {
			return file, e
		}
		if !published {
			reason = ReasonNotOptimized
		}
	case "source":
		current, linked, e := currentFile(ctx, tx, item, ref)
		if e != nil {
			return file, e
		}
		file = current
		switch {
		case current.path == "" || current.size != size || current.modifiedNS != modifiedNS:
			reason = ReasonSourceChanged
		case !linked:
			return file, ErrSourceOffline
		}
	default:
		return file, ErrNotReady
	}
	if reason == "" {
		return file, nil
	}
	if !write {
		return file, errNeedsWriter
	}
	if e := endClaim(ctx, tx, id, StateUnavailable, reason, now, `('ready')`); e != nil {
		return file, e
	}
	return file, ErrUnavailable
}

// IssueGrant hands out, or renews, the transfer address of a ready preparation.
// It is idempotent on operationId, which matters: a client that retried the
// request must not be handed a second address it will never use.
func (s *Service) IssueGrant(ctx context.Context, p identity.Principal, id string, r GrantRequest) (Grant, error) {
	var out Grant
	if !validID.MatchString(id) || !validID.MatchString(r.OperationID) || len(r.Renew) > 200 {
		return out, ErrInput
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	now := s.millis()
	key := operations.ViewerKey(p)
	scope := "downloads-grant:" + key
	raw, digest, e := operations.Receipt(tx, scope, r.OperationID, []any{id, identity.Digest(r.Renew)}, now)
	if e != nil {
		return out, e
	}
	allowed, e := ProfileAllowsDownloads(tx, p.Viewer)
	if e != nil {
		return out, e
	}
	if !allowed {
		return out, ErrPolicy
	}
	current, e := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE id=? AND profile_key=?`, id, key))
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if !current.Title.OnServer {
		return out, ErrNotFound
	}
	if e = s.visibleItem(ctx, tx, p, current.ItemID); e != nil {
		if errors.Is(e, identity.ErrContentRestricted) {
			return out, ErrNotFound
		}
		return out, e
	}
	if raw != "" {
		return out, json.Unmarshal([]byte(raw), &out)
	}
	if current.State != StateReady {
		return out, ErrNotReady
	}
	var modifiedNS int64
	if e = tx.QueryRowContext(ctx, `SELECT source_modified_ns FROM download_preparations WHERE id=?`, id).Scan(&modifiedNS); e != nil {
		return out, e
	}
	if _, e = s.usable(ctx, tx, id, current.ItemID, current.Artifact.Kind, current.Artifact.Ref, current.Artifact.SHA256, current.Artifact.Bytes, modifiedNS, now, true); e != nil {
		if errors.Is(e, ErrUnavailable) {
			// The claim was ended in this transaction: that has to be kept.
			if err := gated.Commit(); err != nil {
				return out, err
			}
			s.signal()
		}
		return out, e
	}
	expires := now + int64(GrantIdle/time.Millisecond)
	token, issued, renewed := "", now, false
	if r.Renew != "" {
		// Only an address of this viewer, for this download, of this exact file
		// is extended. Anything else — somebody else's, another download's, one
		// that was withdrawn, one for a file since replaced — gets a fresh one.
		var held int64
		e = tx.QueryRowContext(ctx, `SELECT issued_ms FROM download_grants WHERE hash=? AND preparation_id=? AND profile_key=? AND artifact_digest=? AND revoked=0`, identity.Digest(r.Renew), id, key, current.Artifact.SHA256).Scan(&held)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e == nil {
			if _, e = tx.ExecContext(ctx, `UPDATE download_grants SET expires_ms=? WHERE hash=?`, expires, identity.Digest(r.Renew)); e != nil {
				return out, e
			}
			token, issued, renewed = r.Renew, held, true
		}
	}
	if token == "" {
		token = identity.Token()
		entity, e := entityid.Resolve(ctx, tx, current.ItemID)
		if errors.Is(e, entityid.ErrNotFound) {
			return out, ErrNotFound
		}
		if e != nil {
			return out, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,artifact_digest,issued_ms,expires_ms) VALUES(?,?,?,?,?,?,?)`, identity.Digest(token), id, key, entity, current.Artifact.SHA256, now, expires); e != nil {
			return out, e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET used_ms=?,updated_ms=? WHERE id=?`, now, now, id); e != nil {
		return out, e
	}
	// Whoever takes or renews the address is who it stands for from now on:
	// the same viewer, as they are signed in today. What is fetched under the
	// address (the file, its artwork, its subtitles) is checked against that.
	if principal, err := json.Marshal(p); err == nil {
		if _, e = tx.ExecContext(ctx, `UPDATE download_preparation_authority SET principal=? WHERE preparation_id=?`, string(principal), id); e != nil {
			return out, e
		}
	}
	out = Grant{PreparationID: id, ItemID: current.ItemID, URL: s.grantPath + token, Token: token, IssuedAt: stamp(issued), ExpiresAt: stamp(expires), RenewAfterMs: GrantIdle.Milliseconds() / 4, Renewed: renewed, Artifact: current.Artifact}
	if e = operations.SaveReceipt(tx, scope, r.OperationID, digest, out, now); e != nil {
		return out, e
	}
	return out, gated.Commit()
}

// GrantScope is who and what a transfer address stands for, once the address
// has been checked. The routes layer serves a download's extras (artwork,
// subtitles) under it.
type GrantScope struct {
	PreparationID string
	ItemID        string
	LibraryID     string
	Quality       string
	Viewer        identity.Viewer
	Principal     identity.Principal
	Artifact      Artifact
	file          pinned
}

// Transfer is an open artifact plus everything the HTTP layer needs to serve it
// with Range, ETag and resumption. The caller closes Reader.
type Transfer struct {
	Reader    io.ReadSeekCloser
	Artifact  Artifact
	Viewer    identity.Viewer
	ItemID    string
	LibraryID string
	// SaveAs is a readable name for a person saving the file from a browser.
	SaveAs   string
	Modified time.Time
}

// ResolveGrant checks a transfer address without opening the file: the address
// is known, not withdrawn and not run out, the claim is still ready and still
// points at bytes this server has, and the viewer may still see the title and
// still download. Every request made with the address passes through here, so
// an open transfer can use this same check to stop on a withdrawn permission.
//
// Using an address keeps it alive: its lifetime slides forward, written at
// most once every few minutes. A transfer is made of many ranged requests, so
// the check itself only reads; the writer is taken just for that occasional
// slide, or to end a claim whose file turns out to be gone.
func (s *Service) ResolveGrant(ctx context.Context, token string) (GrantScope, error) {
	out, e := s.resolveGrant(ctx, token, false)
	if errors.Is(e, errNeedsWriter) {
		return s.resolveGrant(ctx, token, true)
	}
	return out, e
}

var errNeedsWriter = errors.New("this check has something to write")

func (s *Service) resolveGrant(ctx context.Context, token string, write bool) (GrantScope, error) {
	var out GrantScope
	if token == "" || len(token) > 200 {
		return out, ErrGrant
	}
	hash := identity.Digest(token)
	begin := dbwork.BeginSnapshot
	if write {
		begin = func(ctx context.Context, db *sql.DB) (*dbwork.Write, error) {
			return dbwork.Begin(ctx, db, dbwork.ClassForegroundTransfer)
		}
	}
	gated, e := begin(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	now := s.millis()
	var key string
	var expires int64
	var revoked int
	e = tx.QueryRowContext(ctx, `SELECT preparation_id,profile_key,expires_ms,revoked FROM download_grants WHERE hash=?`, hash).Scan(&out.PreparationID, &key, &expires, &revoked)
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrGrant
	}
	if e != nil {
		return out, e
	}
	if revoked != 0 || expires <= now {
		return out, ErrGrant
	}
	var state, kind, ref, artifactDigest, container, authority, account, profile, stored string
	var size, modifiedNS int64
	var present bool
	e = tx.QueryRowContext(ctx, `SELECT state,item_public,library_id,quality,artifact_kind,artifact_ref,artifact_digest,artifact_container,bytes_total,source_modified_ns,authority,account_id,profile_id,NOT (`+titleGone+`),COALESCE((SELECT principal FROM download_preparation_authority WHERE preparation_id=download_preparations.id),'') FROM download_preparations WHERE id=? AND profile_key=?`, out.PreparationID, key).Scan(&state, &out.ItemID, &out.LibraryID, &out.Quality, &kind, &ref, &artifactDigest, &container, &size, &modifiedNS, &authority, &account, &profile, &present, &stored)
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrGrant
	}
	if e != nil {
		return out, e
	}
	if state != StateReady || !present {
		return out, ErrUnavailable
	}
	if e = tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, out.ItemID).Scan(&out.LibraryID); e != nil {
		return out, e
	}
	out.Viewer = identity.Viewer{AccountID: account, ProfileID: profile, Authority: authority}
	out.Principal = identity.Principal{Viewer: out.Viewer}
	// The claim remembers who asked, role included, so a member's library
	// ceiling is applied here exactly as it was when the claim was made.
	if stored != "" {
		var asked identity.Principal
		if json.Unmarshal([]byte(stored), &asked) == nil && asked.AccountID == account && asked.ProfileID == profile && asked.Authority == authority {
			out.Principal = asked
		}
	}
	if e = s.visibleItem(ctx, tx, out.Principal, out.ItemID); e != nil {
		return out, ErrGrant
	}
	allowed, e := ProfileAllowsDownloads(tx, out.Viewer)
	if e != nil {
		return out, e
	}
	if !allowed {
		return out, ErrPolicy
	}
	file, e := s.usable(ctx, tx, out.PreparationID, out.ItemID, kind, ref, artifactDigest, size, modifiedNS, now, write)
	if errors.Is(e, ErrUnavailable) {
		if err := gated.Commit(); err != nil {
			return out, err
		}
		s.signal()
		return out, e
	}
	if e != nil {
		return out, e
	}
	out.file = file
	contentType, fileName := artifactDelivery(container, out.PreparationID)
	out.Artifact = Artifact{Kind: kind, Ref: ref, SHA256: artifactDigest, Bytes: size, Container: container, ContentType: contentType, FileName: fileName}
	wanted := now + int64(GrantIdle/time.Millisecond)
	if wanted-expires < int64(5*time.Minute/time.Millisecond) {
		return out, gated.Commit()
	}
	if !write {
		return out, errNeedsWriter
	}
	if _, e = tx.ExecContext(ctx, `UPDATE download_grants SET expires_ms=?,first_use_ms=CASE WHEN first_use_ms=0 THEN ? ELSE first_use_ms END,uses=uses+1 WHERE hash=?`, wanted, now, hash); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET used_ms=? WHERE id=?`, now, out.PreparationID); e != nil {
		return out, e
	}
	return out, gated.Commit()
}

// OpenGrant redeems a transfer address for the file itself.
func (s *Service) OpenGrant(ctx context.Context, token string) (*Transfer, error) {
	scope, e := s.ResolveGrant(ctx, token)
	if e != nil {
		return nil, e
	}
	out := &Transfer{
		Artifact: scope.Artifact, Viewer: scope.Viewer, ItemID: scope.ItemID, LibraryID: scope.LibraryID,
		SaveAs: s.saveName(ctx, scope),
		// The bytes are identified by their digest, not by a time: a zero time
		// makes ServeContent omit Last-Modified rather than claim 1970.
		Modified: time.Time{},
	}
	switch scope.Artifact.Kind {
	case "source":
		reader, err := s.openSource(ctx, scope.file)
		if err != nil {
			// Opened as the pinned file or not at all. Which of "changed" and
			// "unreachable" it was is settled by the library's own next look at
			// the file; until then nothing is ended and the transfer waits.
			return nil, ErrSourceOffline
		}
		out.Reader = reader
	case "prepared":
		if s.artifacts == nil {
			return nil, ErrNotReady
		}
		reader, err := s.artifacts.OpenDownloadArtifact(ctx, scope.Artifact.Ref, scope.Artifact.SHA256, scope.Artifact.Bytes)
		if err != nil {
			return nil, ErrUnavailable
		}
		out.Reader = reader
	default:
		return nil, ErrNotReady
	}
	return out, nil
}

// saveName is what a browser saves the file as: "Film (2019).mkv",
// "Show - S01E02 - Title.mkv", "03 Song.flac". A title that has no usable name
// keeps the claim's own file name.
func (s *Service) saveName(ctx context.Context, scope GrantScope) string {
	var raw string
	if e := s.db.QueryRowContext(ctx, `SELECT title_json FROM download_preparations WHERE id=?`, scope.PreparationID).Scan(&raw); e != nil {
		return scope.Artifact.FileName
	}
	return readableFileName(storedTitle(raw, true), path.Ext(scope.Artifact.FileName), scope.Artifact.FileName)
}

func readableFileName(t Title, extension, fallback string) string {
	name := t.Name
	switch {
	case t.Kind == "episode" && t.ParentTitle != "":
		position := ""
		if t.GroupNumber != nil && t.Number != nil {
			position = " - S" + pad(*t.GroupNumber) + "E" + pad(*t.Number)
		} else if t.Number != nil {
			position = " - " + pad(*t.Number)
		}
		name = t.ParentTitle + position
		if t.Name != "" {
			name += " - " + t.Name
		}
	case t.Kind == "movie" && t.Year > 0:
		name += " (" + strconv.Itoa(t.Year) + ")"
	case (t.Kind == "song" || t.Kind == "audiobook_file") && t.Number != nil:
		name = pad(*t.Number) + " " + name
	}
	clean := strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f, strings.ContainsRune(`\/:*?"<>|`, r):
			return ' '
		}
		return r
	}, name)
	clean = strings.Join(strings.Fields(clean), " ")
	clean = strings.Trim(clean, ". ")
	if clean == "" {
		return fallback
	}
	if runes := []rune(clean); len(runes) > 120 {
		clean = strings.TrimSpace(string(runes[:120]))
	}
	return clean + extension
}

func pad(n int) string {
	if n >= 0 && n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
