package downloads

import (
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

// DownloadNotifier observes server preparation transitions in the accepting
// transaction. Ready means ready to transfer, not downloaded onto a device.
// Completion notifications require verified device-local completion separately.
type DownloadNotifier interface {
	DownloadReady(tx *sql.Tx, now int64, viewer identity.Viewer, preparationID, itemID string) error
	DownloadFailed(tx *sql.Tx, now int64, viewer identity.Viewer, preparationID, itemID, reason string) error
}

// inboxNotifier keeps durable inbox notices free of content names: the inbox
// does not reproject stored titles when a profile's restrictions later change.
// Downloads resolves the real preparation and its current content authority
// when Open is used. Raw failure reasons never enter a notice.
type inboxNotifier struct{}

func (inboxNotifier) DownloadReady(tx *sql.Tx, now int64, viewer identity.Viewer, preparationID, _ string) error {
	_, err := notify.NotifyDownloadPreparation(tx, now, viewer.Authority, viewer.AccountID, viewer.ProfileID, preparationID, "Your download", notify.PreparationReady)
	return err
}
func (inboxNotifier) DownloadFailed(tx *sql.Tx, now int64, viewer identity.Viewer, preparationID, _, _ string) error {
	_, err := notify.NotifyDownloadPreparation(tx, now, viewer.Authority, viewer.AccountID, viewer.ProfileID, preparationID, "Your download", notify.PreparationFailed)
	return err
}
