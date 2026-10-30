package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// These exact paths need a second, live occurrence check. Being in this set
// is only permission to authenticate a narrowed family, never resource access.
func castOccurrenceResource(method, path string) (item, queue string) {
	if method == http.MethodGet && strings.HasPrefix(path, "/v1/items/") {
		id := strings.TrimPrefix(path, "/v1/items/")
		if id != "" && !strings.ContainsAny(id, "/:") {
			return id, ""
		}
	}
	if strings.HasPrefix(path, "/v1/queues/") {
		id := strings.TrimPrefix(path, "/v1/queues/")
		if method == http.MethodPost && strings.HasSuffix(id, ":advance") {
			id = strings.TrimSuffix(id, ":advance")
		} else if method != http.MethodGet {
			return "", ""
		}
		if id != "" && !strings.ContainsAny(id, "/:") {
			return "", id
		}
	}
	return "", ""
}
func castOccurrenceRoute(method, path string) bool {
	item, queue := castOccurrenceResource(method, path)
	return item != "" || queue != ""
}

func (d Dependencies) castOccurrenceTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, queue string) error {
	if p.Grant != identity.GrantCastTarget {
		return nil
	}
	if d.Identity == nil || tx == nil {
		return identity.ErrForbidden
	}
	current, err := d.Identity.CastPlaybackResourceTx(ctx, tx, p, item, queue)
	if err != nil {
		return err
	}
	_, err = d.playbackAuthorityTx(ctx, tx, p, current)
	return err
}
func (d Dependencies) castOccurrenceAllowed(ctx context.Context, p identity.Principal, item, queue string) error {
	if p.Grant != identity.GrantCastTarget {
		return nil
	}
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return d.castOccurrenceTx(ctx, tx, p, item, queue)
	}
	return dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error { return d.castOccurrenceTx(ctx, dbwork.Snapshot(ctx), p, item, queue) })
}
func (d Dependencies) castQueueAuthorityTx(ctx context.Context, tx *sql.Tx, c playbackv1.Caller, queue string) error {
	return d.castOccurrenceTx(ctx, tx, c.Principal, "", queue)
}

// castQueueStartAuthorityTx admits the paired screen's initial start before any
// adopted occurrence exists. Identity proves the current family/device, the
// active pairing and the viewer's live sessionless device queue; playbackv1
// proves the exact selected entry and resolved item in the same transaction.
func (d Dependencies) castQueueStartAuthorityTx(ctx context.Context, tx *sql.Tx, c playbackv1.Caller, queue, entry, item string) error {
	if c.Principal.Grant != identity.GrantCastTarget {
		return nil
	}
	if d.Identity == nil || tx == nil {
		return identity.ErrForbidden
	}
	return d.Identity.CastQueueStartTx(ctx, tx, c.Principal, queue, entry, item)
}
