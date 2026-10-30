package reading

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// SessionSearchResult is one exact page of the retained reader session's own
// persisted text index. It names the captured session/source revision and the
// index state so an empty page is never reported as "no matches" when the
// publication simply has no index yet.
type SessionSearchResult struct {
	Hits           []TextIndexHit
	Next           string
	Coverage       TextIndexCoverage
	SessionID      string
	SourceID       string
	SourceRevision string
	IndexRevision  int64
}

// SearchSession searches the text index for exactly the publication observation
// the retained session captured. It claims that same session for the request
// and releases it on return; it opens no source bytes, provider read or decode.
// Identity, library, title and the exact current observation are re-admitted for
// every retained format — including before an unsupported-format answer — and
// again in a fresh post-read snapshot, so a closed session, a retired family, a
// restriction change or a replaced source refuses late results instead of
// retargeting the latest version.
func (s *Service) SearchSession(ctx context.Context, p identity.Principal, sessionID, query, cursor string, limit int) (SessionSearchResult, error) {
	var out SessionSearchResult
	if s == nil || s.DB == nil {
		return out, ErrUnavailable
	}
	// Validate every input before any format-specific shortcut.
	if _, _, err := textIndexValidateQuery(query, limit); err != nil {
		return out, err
	}
	if _, err := textIndexDecodeCursor(cursor); err != nil {
		return out, err
	}
	session, err := s.findSession(p, sessionID)
	if err != nil {
		return out, err
	}
	release, ok := session.claim()
	if !ok {
		return out, identity.ErrForbidden
	}
	request, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(session.life, cancel)
	life := &sessionRequestContext{Context: request, owner: session.life}
	defer func() {
		stop()
		cancel()
		release()
	}()
	if err = life.Err(); err != nil {
		return out, identity.ErrForbidden
	}
	selection := session.selection
	out.SessionID, out.SourceID, out.SourceRevision = session.ID, selection.SourceID, selection.Revision
	if selection.Format != "epub" {
		// Re-admit the retained scope and observation before answering. The
		// producer is EPUB-first; a PDF/CBZ session keeps its client-local search
		// and receives an explicit coverage answer, not a fake empty index.
		if err = s.recheckSessionSelection(life, p, selection); err != nil {
			return out, err
		}
		out.Coverage = TextIndexCoverage{Status: "unsupported", Reason: "format"}
		return out, nil
	}
	if s.Visible == nil {
		return out, identity.ErrForbidden
	}
	item := selection.Entity
	var scopeSQL string
	var scopeArgs []any
	prepareScope := func(ctx context.Context, tx *sql.Tx) (string, error) {
		filter, args, err := s.Visible(ctx, tx, p, "s.item_id")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(filter) == "" {
			return "", identity.ErrForbidden
		}
		scopeSQL = "(" + filter + ") AND s.item_id = ?"
		scopeArgs = append(append([]any{}, args...), item)
		composed := strings.Join([]string{
			session.ID,
			p.Hash,
			selection.ItemID,
			strconv.FormatInt(selection.Entity, 10),
			strconv.FormatInt(selection.Asset, 10),
			selection.inventoryRow,
			selection.inventory,
			selection.Revision,
			scopeSQL,
			fmt.Sprint(scopeArgs),
		}, "\x00")
		return fmt.Sprintf("%x", sha256.Sum256([]byte(composed))), nil
	}
	visible := func(ctx context.Context, tx *sql.Tx, principal identity.Principal, itemSQL string) (string, []any, error) {
		if itemSQL != "s.item_id" || scopeSQL == "" {
			return "", nil, identity.ErrForbidden
		}
		return scopeSQL, scopeArgs, nil
	}
	accept := func(ctx context.Context, tx *sql.Tx) error {
		current, err := s.selectTx(ctx, tx, p, selection.ItemID, selection.SourceID)
		if err != nil {
			return err
		}
		if !sameRetainedSelection(current, selection) {
			return ErrChanged
		}
		return nil
	}
	authorize := func(ctx context.Context, tx *sql.Tx, candidate int64) (bool, error) {
		return candidate == item, nil
	}
	result, err := s.SearchTextIndex(life, TextIndexSearch{
		Query:       query,
		Limit:       limit,
		Cursor:      cursor,
		FenceFunc:   prepareScope,
		Principal:   p,
		Visible:     visible,
		Authorize:   authorize,
		Accept:      accept,
		Observation: &TextIndexObservation{ItemID: selection.Entity, AssetID: selection.Asset, ObjectID: selection.inventoryRow, Revision: selection.inventory},
	})
	if err != nil {
		return out, err
	}
	if err = life.Err(); err != nil {
		return out, identity.ErrForbidden
	}
	// The snapshot that produced the hits is gone. Recheck the retained actor,
	// source and captured version, and recompute the actual viewer scope, in a
	// fresh snapshot: a restriction or library change after the query refuses
	// the page even when the session itself is still alive.
	if err = s.recheckSessionAfterRead(life, p, selection, item, scopeSQL, scopeArgs); err != nil {
		return out, err
	}
	if err = life.Err(); err != nil {
		return out, identity.ErrForbidden
	}
	out.Hits, out.Next, out.Coverage, out.IndexRevision = result.Hits, result.Next, result.Coverage, result.IndexRevision
	return out, nil
}

// recheckSessionSelection re-admits the retained principal/library/title and
// requires the captured publication observation to still be exactly current.
func (s *Service) recheckSessionSelection(ctx context.Context, p identity.Principal, selection Selection) error {
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return err
	}
	defer g.Rollback()
	current, err := s.selectTx(ctx, g.Tx(), p, selection.ItemID, selection.SourceID)
	if err != nil {
		return err
	}
	if !sameRetainedSelection(current, selection) {
		return ErrChanged
	}
	return nil
}

// recheckSessionAfterRead rechecks, in one fresh snapshot, both the retained
// actor/source/version and the actual viewer visibility predicate. A restriction
// or library change after the query refuses the page even when the session
// itself is still alive. It is not a catalogue-generation proof; it is the same
// live scope the query admitted.
func (s *Service) recheckSessionAfterRead(ctx context.Context, p identity.Principal, selection Selection, item int64, scopeSQL string, scopeArgs []any) error {
	if s.Visible == nil || scopeSQL == "" {
		return identity.ErrForbidden
	}
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return err
	}
	defer g.Rollback()
	current, err := s.selectTx(ctx, g.Tx(), p, selection.ItemID, selection.SourceID)
	if err != nil {
		return err
	}
	if !sameRetainedSelection(current, selection) {
		return ErrChanged
	}
	filter, args, err := s.Visible(ctx, g.Tx(), p, "s.item_id")
	if err != nil {
		return err
	}
	if strings.TrimSpace(filter) == "" {
		return identity.ErrForbidden
	}
	wantSQL := "(" + filter + ") AND s.item_id = ?"
	wantArgs := append(append([]any{}, args...), item)
	if wantSQL != scopeSQL || !reflect.DeepEqual(wantArgs, scopeArgs) {
		return ErrChanged
	}
	return nil
}

// sameRetainedSelection compares the exact captured publication observation.
func sameRetainedSelection(a, b Selection) bool {
	return a.Entity == b.Entity && a.Asset == b.Asset && a.SourceID == b.SourceID &&
		a.Revision == b.Revision && a.object == b.object && a.inventory == b.inventory &&
		a.inventoryRow == b.inventoryRow
}
