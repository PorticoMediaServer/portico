package writer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// A subtitle Step is a fresh accepting turn of 125. Every required cell runs
// against the real fixture: forged issuer, stale authority, cancelled lifetime,
// and exact replay after ACK (Retired, zero payload). No clock assertion.
func TestIARefreshSubtitleFamilyAuthorityAndRetiredReplay(t *testing.T) {
	for _, mode := range []string{"forged_issuer", "stale_lease", "source_denied", "cancelled_lifetime", "replay_after_ack", "reused_operation"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p, q := iaRefreshSubtitleFixture(t)
			iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
			old := iaRefreshSubtitleRow{ID: "sub", AssetID: "refresh-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt", Language: "en", Title: "old"}
			iaRefreshSeed(t, w, old)
			begin := iaRefreshApply(t, w, r, p, q)
			next := old
			next.Title = "new"
			stepQ := iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{7, old.ID, 0}, next)
			switch mode {
			case "forged_issuer":
				p.witness.issuer.memberIncarnation = strings.Repeat("x", 16)
			case "stale_lease":
				wc008NativeExec(t, w, `UPDATE catalog_info_changes SET job_lease_generation=2 WHERE id=51`)
			case "source_denied":
				wc008NativeExec(t, w, `UPDATE library_sources SET enabled=0 WHERE id='refresh-source'`)
			case "replay_after_ack":
				step := iaRefreshApply(t, w, r, p, stepQ)
				wc008NativeExec(t, w, `UPDATE catalog_info_refresh_undo SET acknowledged_command_sequence=last_command_sequence WHERE refresh_id=?`, step.result.refreshID)
				replay := iaRefreshApply(t, w, r, p, stepQ)
				if replay.decision != receiptRetired || replay.result != (iaRefreshResult{}) || replay.digest != (ResultDigest{}) {
					t.Fatal("retired replay disclosed or repeated the result", replay)
				}
				if got, found := iaRefreshSubtitleNow(t, w, old.ID); !found || !reflect.DeepEqual(got, next) {
					t.Fatal("retired replay changed effects", got)
				}
				return
			case "reused_operation":
				if got, found := iaRefreshSubtitleNow(t, w, old.ID); !found || !reflect.DeepEqual(got, old) {
					t.Fatal("precondition row changed", got)
				}
				iaRefreshApply(t, w, r, p, stepQ)
				conflict := stepQ
				conflict.row = old
				iaRefreshSign(t, &conflict)
				turn, b := iaRefreshTurn(t, w, r, conflict)
				out, err := runIARefresh(t.Context(), turn, conflict, p, t.Context(), b)
				if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
					t.Fatal("reused operation id accepted a different request", out, err)
				}
				turn.transaction.rollback(err)
				if got, found := iaRefreshSubtitleNow(t, w, old.ID); !found || !reflect.DeepEqual(got, next) {
					t.Fatal("conflicting replay changed effects", got)
				}
				return
			}
			life := t.Context()
			if mode == "cancelled_lifetime" {
				var cancel context.CancelFunc
				life, cancel = context.WithCancel(t.Context())
				cancel()
			}
			turn, b := iaRefreshTurn(t, w, r, stepQ)
			out, err := runIARefresh(t.Context(), turn, stepQ, p, life, b)
			if err == nil || !reflect.DeepEqual(out, iaRefreshOutcome{}) {
				t.Fatal("denied subtitle Step produced output", out, err)
			}
			if mode == "cancelled_lifetime" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled lifetime was not observed", err)
			}
			turn.transaction.rollback(err)
			if got, found := iaRefreshSubtitleNow(t, w, old.ID); !found || !reflect.DeepEqual(got, old) {
				t.Fatal("denied subtitle Step changed the row", got)
			}
			if iaRefreshCount(t, w, `SELECT count(*) FROM catalog_info_refresh_receipts WHERE command_kind='refresh_step'`) != 0 {
				t.Fatal("denied subtitle Step wrote a receipt")
			}
		})
	}
}

// An uncertain COMMIT is never success: both the new subtitle effect and its
// receipt must be read back through a FRESH read-only connection.
func TestIARefreshSubtitleStepUnknownCommitReadsBackFresh(t *testing.T) {
	w, r, p, q := iaRefreshSubtitleFixture(t)
	iaRefreshSeedSubtitleFact(t, w, "refresh-asset")
	old := iaRefreshSubtitleRow{ID: "sub", AssetID: "refresh-asset", Origin: "embedded", Locator: "0", StreamIndex: 1, Format: "srt", Title: "old"}
	iaRefreshSeed(t, w, old)
	begin := iaRefreshApply(t, w, r, p, q)
	next := old
	next.Title = "new"
	stepQ := iaRefreshNext(t, w, q, begin, IARefreshStep, iaRefreshKey{7, old.ID, 0}, next)
	path := iaConfigureLibraryPath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	turn, b := iaRefreshTurn(t, w, r, stepQ)
	out, err := runIARefresh(t.Context(), turn, stepQ, p, t.Context(), b)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unknown COMMIT was reported as success", result, err)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var receipts int
	if err = db.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_refresh_receipts`).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatal("fresh read-only connection lost a receipt", receipts, err)
	}
	var title string
	if err = db.QueryRowContext(t.Context(), `SELECT title FROM asset_subtitles WHERE id='sub'`).Scan(&title); err != nil || title != "new" {
		t.Fatal("fresh read-only connection lost the committed subtitle", title, err)
	}
}
