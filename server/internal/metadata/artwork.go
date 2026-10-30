package metadata

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var ErrArtworkVersion = errors.New("artwork version is no longer selected")

var ErrArtworkPending = errors.New("artwork is queued or unavailable")

func (s *Service) SetArtworkDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	s.cacheRoot = path
	return nil
}
func validArtworkRole(role string) bool {
	switch role {
	case "poster", "backdrop", "thumbnail", "still", "logo", "square", "portrait", "banner", "cover":
		return true
	}
	return false
}

// ArtworkSize names one served representation of a selected source. Every
// representation was made when the source arrived; nothing is resized here.
type ArtworkSize int

const (
	ArtworkLarge ArtworkSize = iota
	ArtworkSmall
	ArtworkAmbient
)

// ArtworkSizeForWidth maps a legacy width request to its slot: widths up to
// the small edge are the small slot, every larger width is the large slot.
func ArtworkSizeForWidth(width int) ArtworkSize {
	if width <= artworkSmallEdge {
		return ArtworkSmall
	}
	return ArtworkLarge
}

func (size ArtworkSize) column() string {
	switch size {
	case ArtworkSmall:
		return "small_blob"
	case ArtworkAmbient:
		return "ambient_blob"
	}
	return "large_blob"
}

func (s *Service) Artwork(ctx context.Context, item, role string) (*os.File, string, error) {
	return s.EntityArtwork(ctx, RepairTarget{"item", item}, role, "", false)
}

// artworkSelection is the selection lookup every reader shares: the selected
// source, and the target and role it was inherited from.
func artworkSelection(ctx context.Context, tx *sql.Tx, t RepairTarget, role, subject string) (string, RepairTarget, string, error) {
	var digest string
	selectedTarget, selectedRole := t, role
	entity, resolveErr := artworkEntity(ctx, tx, t.ID)
	if resolveErr != nil {
		return "", selectedTarget, selectedRole, resolveErr
	}
	err := tx.QueryRowContext(ctx, `SELECT a.digest FROM artwork_selections a JOIN artwork_sources o ON o.digest=a.digest WHERE a.kind=? AND a.entity_id=? AND a.role=? AND a.subject=?`, t.Kind, entity, role, subject).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) && t.Kind == "item" && (role == "poster" || role == "cover") {
		err = tx.QueryRowContext(ctx, `SELECT a.digest,pid(al.public_id) FROM catalog_songs cs JOIN artwork_selections a ON a.kind='album' AND a.entity_id=cs.album_id AND a.role='cover' AND a.subject='' JOIN artwork_sources o ON o.digest=a.digest JOIN catalog_entities al ON al.id=cs.album_id WHERE cs.entity_id=?`, entity).Scan(&digest, &selectedTarget.ID)
		if err == nil {
			selectedTarget.Kind = "album"
			selectedRole = "cover"
		}
	}
	if errors.Is(err, sql.ErrNoRows) && t.Kind == "item" && (role == "poster" || role == "cover") {
		err = tx.QueryRowContext(ctx, `SELECT a.digest,a.kind,pid(e.public_id),a.role FROM artwork_selections a JOIN artwork_sources o ON o.digest=a.digest JOIN catalog_entities e ON e.id=a.entity_id WHERE a.subject='' AND ((a.kind='item' AND a.entity_id=? AND a.role='cover') OR (a.kind='book' AND a.entity_id IN(SELECT book_id FROM catalog_book_files WHERE entity_id=?) AND a.role='cover')) ORDER BY a.kind DESC LIMIT 1`, entity, entity).Scan(&digest, &selectedTarget.Kind, &selectedTarget.ID, &selectedRole)
	}
	if errors.Is(err, sql.ErrNoRows) && (t.Kind == "item" || t.Kind == "season") && (role == "poster" || role == "backdrop") {
		err = tx.QueryRowContext(ctx, `WITH parents(kind,id,priority) AS (
          SELECT 'season',season_id,0 FROM catalog_episodes WHERE entity_id=? AND season_id IS NOT NULL
          UNION ALL SELECT 'show',show_id,1 FROM catalog_episodes WHERE entity_id=?
          UNION ALL SELECT 'show',show_id,1 FROM catalog_seasons WHERE entity_id=?
        ) SELECT a.digest,a.kind,pid(e.public_id),a.role FROM parents p JOIN artwork_selections a ON a.kind=p.kind AND a.entity_id=p.id AND a.role=? AND a.subject='' JOIN artwork_sources o ON o.digest=a.digest JOIN catalog_entities e ON e.id=a.entity_id ORDER BY p.priority LIMIT 1`, entity, entity, entity, role).Scan(&digest, &selectedTarget.Kind, &selectedTarget.ID, &selectedRole)
	}
	return digest, selectedTarget, selectedRole, err
}

// artworkSourceBlob names the installed file holding one representation of a
// published source.
func artworkSourceBlob(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, source string, size ArtworkSize) (string, string, error) {
	var digest, mime string
	err := q.QueryRowContext(ctx, `SELECT b.digest,b.mime FROM artwork_sources s JOIN artwork_blobs b ON b.digest=s.`+size.column()+` AND b.status='ready' WHERE s.digest=?`, source).Scan(&digest, &mime)
	return digest, mime, err
}

func (s *Service) EntityArtwork(ctx context.Context, t RepairTarget, role, subject string, thumbnail bool) (*os.File, string, error) {
	size := ArtworkLarge
	if thumbnail {
		size = ArtworkSmall
	}
	return s.EntityArtworkVariant(ctx, t, role, subject, size, "")
}

// EntityArtworkVariant opens one representation of the selected source.
// Version identifies the selected source, independent of the representation.
// File existence is established before HTTP conditional response evaluation,
// and nothing is decoded, encoded or written here.
func (s *Service) EntityArtworkVariant(ctx context.Context, t RepairTarget, role, subject string, size ArtworkSize, version string) (*os.File, string, error) {
	if size != ArtworkSmall && size != ArtworkLarge && size != ArtworkAmbient {
		return nil, "", ErrRepairInput
	}
	if !validArtworkRole(role) || len(subject) > 160 {
		return nil, "", ErrRepairInput
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, "", err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = validateArtworkTarget(ctx, tx, t); err != nil {
		return nil, "", err
	}
	source, selectedTarget, selectedRole, err := artworkSelection(ctx, tx, t, role, subject)
	if err == nil {
		if version != "" && version != source {
			return nil, "", ErrArtworkVersion
		}
		digest, mime, e := artworkSourceBlob(ctx, tx, source, size)
		var f *os.File
		if e == nil {
			f, e = s.openArtworkObject(digest)
		}
		if e != nil {
			if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				return nil, "", e
			}
			// A missing file is re-acquired in the background from its source's
			// custody; never rebuilt from another representation.
			s.queueMissingArtwork(selectedTarget, selectedRole, subject)
			return nil, "", ErrArtworkPending
		}
		if e = gated.Commit(); e != nil {
			f.Close()
			return nil, "", e
		}
		return f, mime, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	if size == ArtworkAmbient {
		return nil, "", ErrArtworkPending
	}
	// Already installed local evidence remains available during initial migration.
	// Remote URLs are never followed, including when the worker is not configured.
	if t.Kind == "item" && (role == "poster" || role == "backdrop") && s.LocalArtwork != nil {
		column := "poster_url"
		if role == "backdrop" {
			column = "backdrop_url"
		}
		var value string
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(d.`+column+`, '') FROM catalog_entities e LEFT JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.public_id=pid_blob(?)`, t.ID).Scan(&value); err != nil {
			return nil, "", err
		}
		if strings.HasPrefix(value, "local:") {
			if err = gated.Commit(); err != nil {
				return nil, "", err
			}
			return s.LocalArtwork(strings.TrimPrefix(value, "local:"))
		}
	}
	return nil, "", ErrArtworkPending
}

// verifyRestorableArtwork refuses an undo whose sources are no longer
// published with all their files.
func (s *Service) verifyRestorableArtwork(ctx context.Context, choices []ArtworkChoice) error {
	for _, choice := range choices {
		if !artDigest(choice.Digest) {
			return ErrRepairInput
		}
		for _, size := range []ArtworkSize{ArtworkSmall, ArtworkLarge, ArtworkAmbient} {
			digest, _, err := artworkSourceBlob(ctx, s.db, choice.Digest, size)
			if err == nil {
				var f *os.File
				if f, err = s.openArtworkObject(digest); err == nil {
					f.Close()
				}
			}
			if err != nil {
				return errors.New("previous artwork bytes are missing; repair the retained selection before restoring")
			}
		}
	}
	return nil
}

// Open a content-addressed, regular cache object without serving a symlink or a
// file replaced between inspection and open. The descriptor remains valid if a
// later install/cleanup renames or unlinks the pathname.
func (s *Service) openArtworkObject(digest string) (*os.File, error) {
	if s.cacheRoot == "" || !artDigest(digest) {
		return nil, ErrArtworkPending
	}
	path := filepath.Join(s.cacheRoot, digest+".img")
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() == 0 {
		return nil, ErrArtworkPending
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		f.Close()
		return nil, ErrArtworkPending
	}
	return f, nil
}
