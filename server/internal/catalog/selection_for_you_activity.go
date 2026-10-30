package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Revalidate the EXACT raw window recAbandoned reads, at the held instant.
// Timestamp-only changes need no writer epoch: a moved/replaced window or a
// changed position/completed input makes this private dependency stamp stale.
// Asset/facet/work/signal changes retain their existing complete base witness.
// The LIMIT precedes the progress/weight/duration predicates. Nothing is stored
// outside the existing volatile preparation content.
func (s *Service) smallForYouActivityFence(profile string, now time.Time, maxRows, maxBytes int, reserve func(int64) error) (string, SelectionBatchWork, error) {
	var work SelectionBatchWork
	if dbwork.Snapshot(s.Context()) == nil {
		return "", work, sql.ErrTxDone
	}
	if maxRows <= 0 || maxBytes <= 0 {
		return "", work, ErrSelectionBatchCapacity
	}
	if reserve != nil {
		if err := reserve(16384); err != nil {
			return "", work, err
		}
	}
	hash := sha256.New()
	hash.Write([]byte("for-you-abandonment-v1\x00"))
	hash.Write([]byte(now.UTC().Format(time.RFC3339Nano)))
	if profile == "" {
		return hex.EncodeToString(hash.Sum(nil)), work, nil
	}
	cutoff := now.AddDate(0, 0, -recAbandonAfter).UTC().Format("2006-01-02T15:04:05.000Z")
	rows, err := s.read().Query(`SELECT item_id FROM progress_activity INDEXED BY progress_home_profile_recent WHERE profile_id=? AND state!='ended' AND updated_at<? ORDER BY updated_at DESC,item_id DESC LIMIT 200`, profile, cutoff)
	if err != nil {
		return "", work, err
	}
	defer rows.Close()
	for i := 0; i < 200; i++ {
		if work.Rows >= maxRows {
			return "", work, ErrSelectionBatchCapacity
		}
		work.Rows++
		work.ExaminedBytes += selectionBatchProbeBytes
		if !rows.Next() {
			if err = rows.Err(); err != nil {
				return "", work, err
			}
			break
		}
		var id int64
		if err = rows.Scan(&id); err != nil {
			return "", work, err
		}
		if work.Rows+3 > maxRows {
			return "", work, ErrSelectionBatchCapacity
		}
		work.Rows++
		work.ExaminedBytes += selectionBatchProbeBytes
		var size int64
		if err = s.read().QueryRow(`SELECT length(CAST(updated_at AS BLOB)) FROM progress_activity WHERE profile_id=? AND item_id=?`, profile, id).Scan(&size); err != nil {
			return "", work, err
		}
		work.TransientScalarBytes = max(work.TransientScalarBytes, int(size))
		if size < 0 || size > int64(maxBytes-work.ExaminedBytes)/2 {
			return "", work, ErrSelectionBatchCapacity
		}
		if reserve != nil {
			if err = reserve(4*size + 16384); err != nil {
				return "", work, err
			}
		}
		var at string
		var position, completed int64
		if err = s.read().QueryRow(`SELECT a.updated_at,COALESCE(p.position,0),COALESCE(p.completed,0) FROM progress_activity a LEFT JOIN progress p ON p.profile_id=a.profile_id AND p.item_id=a.item_id WHERE a.profile_id=? AND a.item_id=?`, profile, id).Scan(&at, &position, &completed); err != nil {
			return "", work, err
		}
		work.Rows += 2
		work.ExaminedBytes += 2*selectionBatchProbeBytes + 2*int(size)
		work.DecodedBytes += int(size)
		if work.ExaminedBytes > maxBytes {
			return "", work, ErrSelectionBatchCapacity
		}
		var fixed [32]byte
		binary.LittleEndian.PutUint64(fixed[0:8], uint64(id))
		binary.LittleEndian.PutUint64(fixed[8:16], uint64(position))
		binary.LittleEndian.PutUint64(fixed[16:24], uint64(completed))
		binary.LittleEndian.PutUint64(fixed[24:32], uint64(len(at)))
		hash.Write(fixed[:])
		hash.Write([]byte(at))
	}
	return hex.EncodeToString(hash.Sum(nil)), work, nil
}

func (s *Service) checkSmallForYouActivityContent(content selectionPreparationContent, reserve func(int64) error) error {
	if len(content.Sorts) != 1 || content.Sorts[0].Field != "forYou" || !content.Ordered || len(content.IDs) == 0 {
		return nil
	}
	if content.ActivityFence == "" || content.OrderInstant.IsZero() {
		return ErrStaleContinuation
	}
	fence, _, err := s.smallForYouActivityFence(content.Profile, content.OrderInstant, SelectionBatchMaximumRows, SelectionBatchMaximumBytes, reserve)
	if err != nil {
		return err
	}
	if fence != content.ActivityFence {
		return ErrStaleContinuation
	}
	return nil
}
