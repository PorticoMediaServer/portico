package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// FileInfoIdentity names an exact opening root, not an authorization grant.
// The public id and full item incarnation must agree with the live point row.
type FileInfoIdentity struct {
	Item, Library int64
	PublicItem    [16]byte
}

// FileInfoAdmission is rebuilt by the request's live authority guard. Detail
// and FileInfo scopes deliberately have separate audience fences. No current
// content revision is consulted when checking an existing opening.
type FileInfoAdmission struct {
	Identity                  FileInfoIdentity
	ItemIncarnation           [16]byte
	Head                      compactcatalog.InfoHead
	ConfigurationRevision     int64
	DetailScope, Scope        DetailScope
	Kind                      string
	SubtitleActor             string
	DiscSubtitlesAvailable    bool
	CaptureBytes              int64
	Children, Playable, Owner bool
	ChildVisible              fileInfoChildCheck
}

type FileInfoAuthorityCheck func(context.Context, readprep.Caller, FileInfoIdentity) (FileInfoAdmission, error)
type fileInfoAuthorityContextKey struct{}

var ErrFileInfoAuthorityUnavailable = errors.New("file information authority is not installed")

func WithFileInfoAuthority(ctx context.Context, check FileInfoAuthorityCheck) context.Context {
	return context.WithValue(ctx, fileInfoAuthorityContextKey{}, check)
}
func CheckFileInfoAuthority(ctx context.Context, caller readprep.Caller, id FileInfoIdentity) (FileInfoAdmission, error) {
	check, ok := ctx.Value(fileInfoAuthorityContextKey{}).(FileInfoAuthorityCheck)
	if !ok || check == nil {
		return FileInfoAdmission{}, ErrFileInfoAuthorityUnavailable
	}
	return check(ctx, caller, id)
}

// FileInfoRuntime shares the process readprep budget and supervision. It owns
// only process keys; preparation handles and summaries never enter SQLite.
type FileInfoRuntime struct {
	db       *sql.DB
	store    *readprep.Store
	check    FileInfoAuthorityCheck
	anchors  *fileInfoAnchorCodec
	openings *fileInfoOpeningCodec
	now      func() time.Time
}

func NewFileInfoRuntime(db *sql.DB, store *readprep.Store, check FileInfoAuthorityCheck) (*FileInfoRuntime, error) {
	if db == nil || store == nil || check == nil {
		return nil, readprep.ErrInvalid
	}
	anchors, err := newFileInfoAnchorCodec()
	if err != nil {
		return nil, err
	}
	openings, err := newFileInfoOpeningCodec(anchors)
	if err != nil {
		return nil, err
	}
	return &FileInfoRuntime{db: db, store: store, check: check, anchors: anchors, openings: openings, now: time.Now}, nil
}

// AcceptDetailOpening is called while composing an accepted Detail in its
// admitted snapshot, after FI1 readiness and before the HTTP conditional-304
// decision. The caller passes that exact Detail revision; continuations cannot
// mint another opening or adopt a newer revision.
func (r *FileInfoRuntime) AcceptDetailOpening(ctx context.Context, caller readprep.Caller, id FileInfoIdentity, revision ContentRevision) (string, error) {
	if r == nil || caller == "" || dbwork.Snapshot(ctx) == nil {
		return "", readprep.ErrInvalid
	}
	ad, err := r.check(ctx, caller, id)
	if err != nil {
		return "", err
	}
	if ad.Identity != id || !ad.validScope() {
		return "", readprep.ErrStale
	}
	if !ad.Head.Ready() {
		return "", compactcatalog.ErrInfoPreparing
	}
	proof := fileInfoOpeningProof{Item: id.Item, Library: id.Library, ItemIncarnation: ad.ItemIncarnation, PublicItem: id.PublicItem, InfoEpoch: ad.Head.Applied, ConfigurationRevision: ad.ConfigurationRevision, Revision: revision, Expires: r.now().Add(30 * time.Minute), ScopeDigest: fileInfoScopeDigest(ad.DetailScope)}
	return r.openings.encodeOpening(caller, proof)
}
func (ad FileInfoAdmission) validScope() bool {
	return ad.Identity.Item > 0 && ad.Identity.Library > 0 && ad.Kind != "" && (!ad.Children || ad.ChildVisible != nil) && ad.DetailScope.ViewerFence != "" && ad.Scope.ServerID != "" && ad.Scope.LibraryID != "" && ad.Scope.ItemID != "" && ad.Scope.ViewerFence != "" &&
		ad.Scope.ServerID == ad.DetailScope.ServerID && ad.Scope.LibraryID == ad.DetailScope.LibraryID && ad.Scope.ItemID == ad.DetailScope.ItemID
}

func (r *FileInfoRuntime) opening(ctx context.Context, caller readprep.Caller, token string) (fileInfoOpeningProof, FileInfoAdmission, error) {
	if r == nil {
		return fileInfoOpeningProof{}, FileInfoAdmission{}, readprep.ErrInvalid
	}
	proof, err := r.openings.decodeOpening(caller, token)
	if err != nil {
		return proof, FileInfoAdmission{}, err
	}
	ad, err := r.checkOriginalBasis(ctx, caller, proof)
	return proof, ad, err
}
func (r *FileInfoRuntime) checkOriginalBasis(ctx context.Context, caller readprep.Caller, proof fileInfoOpeningProof) (FileInfoAdmission, error) {
	id := FileInfoIdentity{Item: proof.Item, Library: proof.Library, PublicItem: proof.PublicItem}
	ad, err := r.check(ctx, caller, id)
	if err != nil {
		return FileInfoAdmission{}, err
	}
	if ad.Identity != id || !ad.validScope() || ad.ItemIncarnation != proof.ItemIncarnation || ad.ConfigurationRevision != proof.ConfigurationRevision ||
		!ad.Head.Ready() || ad.Head.Desired != proof.InfoEpoch || ad.Head.Applied != proof.InfoEpoch || !proof.Expires.After(r.now()) {
		return FileInfoAdmission{}, readprep.ErrStale
	}
	if err = proof.CheckScope(ad.DetailScope); err != nil {
		return FileInfoAdmission{}, err
	}
	return ad, nil
}
func fileInfoOpeningDigest(token string) [32]byte {
	return sha256.Sum256([]byte("file-info-window-v1\x00" + token))
}

// A window captures its own audience independently of the original Detail.
// This fixed witness is part of the memory key/payload, never a database row.
type fileInfoWindowWitness struct {
	Scope                     [32]byte
	DiscSubtitlesAvailable    bool
	Kind                      string
	Owner, Children, Playable bool
}

func fileInfoWitness(ad FileInfoAdmission) fileInfoWindowWitness {
	return fileInfoWindowWitness{Scope: fileInfoScopeDigest(ad.Scope), Kind: ad.Kind, Owner: ad.Owner, Children: ad.Children, Playable: ad.Playable, DiscSubtitlesAvailable: ad.DiscSubtitlesAvailable}
}
func (r *FileInfoRuntime) checkWindowBasis(ctx context.Context, caller readprep.Caller, proof fileInfoOpeningProof, w fileInfoWindowWitness) (FileInfoAdmission, error) {
	ad, err := r.checkOriginalBasis(ctx, caller, proof)
	if err != nil {
		return FileInfoAdmission{}, err
	}
	if fileInfoWitness(ad) != w {
		return FileInfoAdmission{}, readprep.ErrStale
	}
	return ad, nil
}

// FileInfo admission/action points join an already accepted Detail snapshot or
// gated mutation transaction. Background scan quanta carry no snapshot and keep
// their existing single-statement read-pool behavior.
func fileInfoQuery(ctx context.Context, db *sql.DB) compactcatalog.InfoQuery {
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return tx
	}
	return dbwork.ReadHandle(ctx, db)
}
