package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

// resolveReportItem maps a report's public item id to the integer
// console_reports stores (0 when there is none). An unknown id stores 0: the
// readers blank unreachable media context through auth, so a dangling
// reference was never observable and 0 reads the same way.
func resolveReportItem(ctx context.Context, tx *sql.Tx, public string) (int64, error) {
	if public == "" {
		return 0, nil
	}
	entity, err := entityid.Resolve(ctx, tx, public)
	if errors.Is(err, entityid.ErrNotFound) {
		return 0, nil
	}
	return entity, err
}

// Notify is the local registration seam for feedback updates. The producer
// authorizes and supplies the exact viewer in its transaction. The generic
// notice carries no report action: old console_reports IDs are not current
// feedback report IDs and must not be sent to the current report reader.
// Callers may WakeNotifications after committing; revision/events are durable
// in this transaction and are never emitted for a rollback.
func Notify(tx *sql.Tx, now int64, p identity.Principal, code, target, dedupe string) error {
	if code != "feedback-updated" && code != "feedback-received" {
		return ErrInvalid
	}
	if !validID(target) || len(dedupe) > 200 {
		return ErrInvalid
	}
	title, body, category := "Your report has an update", "Your feedback has an update.", "feedback.updated"
	if code == "feedback-received" {
		title, body, category = "New viewer feedback", "New viewer feedback is ready for review.", "feedback.received"
	}
	key, _ := json.Marshal([]string{code, target, dedupe})
	digest := sha256.Sum256(key)
	_, err := notify.Raise(tx, now, notify.Draft{Audience: notify.AudienceProfile, Scope: ViewerKey(p), Severity: notify.SeverityInfo, Source: notify.SourceFeedback, Category: category, Title: title, Body: body, DedupeKey: fmt.Sprintf("console:%x", digest)})
	return err
}
