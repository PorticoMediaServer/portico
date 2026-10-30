package playback

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func discSubtitlePresentation(snapshot playbackdisc.Snapshot, item string) subtitles.PrivateDiscPresentation {
	return subtitles.PrivateDiscPresentation{
		SnapshotID: snapshot.ID, SessionID: snapshot.SessionID, Generation: snapshot.Generation,
		ItemID: item, AssetID: snapshot.Source.AssetID, ObjectID: snapshot.Source.InventoryObjectID,
		TitleKey: snapshot.Disc.SelectedTitleKey, FactsRevision: snapshot.Disc.FactsRevision,
		Owner: subtitles.ViewerKey(snapshot.Actor.Authority, snapshot.Actor.AccountID, snapshot.Actor.ProfileID, snapshot.Actor.ServerID),
		Size:  snapshot.Source.Size, ModifiedNS: snapshot.Source.ModifiedNS,
		Container: snapshot.Media.Container, DurationMS: snapshot.Media.DurationMs,
		OriginUS: snapshot.Media.OriginUS, TimingKnown: snapshot.Media.TimingKnown,
	}
}

// ResolveDiscSubtitlePresentationTx is the ordinary accepted-presentation
// resolver. It never borrows preparation or recovers process proof from rows.
// Its caller still retains its own viewer/session/resource grant and pin checks.
func (s *Service) ResolveDiscSubtitlePresentationTx(ctx context.Context, tx *sql.Tx, session string) (subtitles.PrivateDiscPresentation, bool, error) {
	snapshot, private, err := s.activePrivateDiscSnapshotTx(ctx, tx, session)
	if err != nil || !private {
		return subtitles.PrivateDiscPresentation{}, private, err
	}
	var item string
	err = tx.QueryRowContext(ctx, `SELECT pid(item.public_id) FROM playback_sessions session JOIN catalog_entities item ON item.id=session.item_id WHERE session.id=? AND session.generation=?`, snapshot.SessionID, snapshot.Generation).Scan(&item)
	if err != nil {
		return subtitles.PrivateDiscPresentation{}, true, err
	}
	return discSubtitlePresentation(snapshot, item), true, nil
}

func (b *discPlayBorrow) bindSubtitleResourcesTx(ctx context.Context, tx *sql.Tx, service *Service, principal identity.Principal, item string, automatic subtitles.AutoChoice) error {
	if err := b.checkContinuationTx(ctx, tx); err != nil {
		return err
	}
	b.measurement.mu.Lock()
	snapshot := b.measurement.snapshot
	bound := b.measurement.bound
	b.measurement.mu.Unlock()
	if !bound || snapshot.ID == "" {
		return playbackdisc.ErrCurrent
	}
	if service.Subtitles == nil {
		for _, stream := range snapshot.Media.Streams {
			if stream.Type == "subtitle" {
				return subtitles.ErrUnavailable
			}
		}
		return nil
	}
	if service.Subtitles.DiscPresentations != service {
		return subtitles.ErrUnavailable
	}
	if extractor, ok := service.Subtitles.Extractor.(subtitles.DiscSubtitleExtractor); !ok || !extractor.DiscSubtitleAvailable() {
		// Keep the real measured resources visible as unavailable, but do not
		// auto-select a document the configured host cannot actually extract.
		automatic.Off = true
	}
	return subtitles.PersistPrivateDiscResourcesTx(ctx, tx, principal, discSubtitlePresentation(snapshot, item), automatic, b.checkContinuationTx)
}
