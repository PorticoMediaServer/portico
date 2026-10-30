package playback

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// privateDiscBindingTx classifies an already named legacy occurrence, without
// granting it any authority. A binding for another generation is still private:
// it must refuse rather than fall through to the shared inventory/local path.
func privateDiscBindingTx(ctx context.Context, tx *sql.Tx, session string) (playbackdisc.Key, string, bool, error) {
	var key playbackdisc.Key
	var snapshot sql.NullString
	key.SessionID = session
	err := tx.QueryRowContext(ctx, `SELECT p.generation,d.id FROM playback_sessions p
 LEFT JOIN playback_disc_snapshots d ON d.session_id=p.id AND d.generation=p.generation
 WHERE p.id=? AND EXISTS(SELECT 1 FROM playback_disc_snapshots own WHERE own.session_id=p.id)`, session).Scan(&key.Generation, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return playbackdisc.Key{}, "", false, nil
	}
	if err != nil {
		return playbackdisc.Key{}, "", true, err
	}
	if key.Generation < 1 || !snapshot.Valid || snapshot.String == "" {
		return playbackdisc.Key{}, "", true, playbackdisc.ErrCurrent
	}
	return key, snapshot.String, true, nil
}

func (s *Service) activePrivateDiscSnapshotTx(ctx context.Context, tx *sql.Tx, session string) (playbackdisc.Snapshot, bool, error) {
	key, id, private, err := privateDiscBindingTx(ctx, tx, session)
	if err != nil || !private {
		return playbackdisc.Snapshot{}, private, err
	}
	if s == nil || s.discInputs == nil {
		return playbackdisc.Snapshot{}, true, disc.ErrRemoteUnqualified
	}
	snapshot, err := s.discInputs.store.ResolveTx(ctx, tx, key, id, s.checkActiveDiscSnapshotTx)
	return snapshot, true, err
}

// Replans consume only the same accepted snapshot as the reader. This does not
// acquire bytes, recover preparation state or use catalogue title hints.
func (s *Service) loadSessionDeliverySourceTx(ctx context.Context, tx *sql.Tx, session, asset string) (DeliverySource, error) {
	snapshot, private, err := s.activePrivateDiscSnapshotTx(ctx, tx, session)
	if err != nil {
		return DeliverySource{}, err
	}
	if !private {
		return loadLocalSessionDeliverySource(ctx, tx, session, asset)
	}
	if snapshot.Source.AssetID != asset {
		return DeliverySource{}, playbackdisc.ErrCurrent
	}
	facts := snapshot.Media
	return discSourceFacts(asset, discSnapshotMedia(facts)), nil
}

func discSnapshotMedia(facts playbackdisc.MediaFacts) assets.Facts {
	return assets.Facts{
		Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec,
		Width: facts.Width, Height: facts.Height, Duration: float64(facts.DurationMs) / 1000,
		OriginUS: facts.OriginUS, TimingKnown: facts.TimingKnown,
		Streams: facts.Streams, Chapters: facts.Chapters,
	}
}

func (s *Service) checkDiscSessionTx(ctx context.Context, tx *sql.Tx, session string) error {
	_, private, err := s.activePrivateDiscSnapshotTx(ctx, tx, session)
	if err != nil || private {
		return err
	}
	return checkDiscSessionTx(ctx, tx, session)
}

func (h *HLS) loadSessionDeliverySourceTx(ctx context.Context, tx *sql.Tx, session, asset string) (DeliverySource, error) {
	if h.discPlayback == nil {
		return loadLocalSessionDeliverySource(ctx, tx, session, asset)
	}
	return h.discPlayback.loadSessionDeliverySourceTx(ctx, tx, session, asset)
}
