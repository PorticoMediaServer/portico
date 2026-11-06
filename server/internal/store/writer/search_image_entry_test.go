package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestSearchImageFreshMemoryPreflight(t *testing.T) {
	c := searchImageStageNativeCommand(t, 29)
	if n, err := searchImageFreshRequiredBytes(nil); n != 0 || !errors.Is(err, ErrInvalid) {
		t.Fatal(n, err)
	}
	base, err := searchImageFreshRequiredBytes(c)
	if err != nil || base <= 65536 {
		t.Fatal(base, err)
	}
	if n := testing.AllocsPerRun(20, func() {
		if _, err := searchImageFreshRequiredBytes(c); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatal("preflight allocated", n)
	}
	// Full wire, including fixed overhead, is bounded before hashing/copying.
	copy := *c
	copy.request.header.titlePrefixTokens = ""
	without, err := searchImageFreshRequiredBytes(&copy)
	if err != nil {
		t.Fatal(err)
	}
	remaining := maxCanonicalRequestBytes - int((without-65536)/4)
	for _, delta := range []int{-1, 0, 1} {
		copy.request.header.titlePrefixTokens = strings.Repeat("x", remaining+delta)
		n, err := searchImageFreshRequiredBytes(&copy)
		if delta <= 0 {
			if err != nil || n != 65536+4*uint64(maxCanonicalRequestBytes+delta) {
				t.Fatal(delta, n, err)
			}
		} else if !errors.Is(err, ErrInvalid) || n != 0 {
			t.Fatal("over-limit forecast accepted", n, err)
		}
	}
}

func TestSearchImageEntryMemoryEngineAndLeaseGates(t *testing.T) {
	w, registry := searchImageStageFixture(t)
	var seq int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	var engineReads int
	var actual func() (string, string, error)
	w.openPool(connector, func(raw driver.Conn) driver.Conn {
		native := raw.(*ownedPhysicalConn)
		original := native.writerDriverConn
		native.writerDriverConn = &searchEngineProbeConn{original, func() (string, string, error) {
			engineReads++
			if actual != nil {
				return actual()
			}
			return original.(nativeEngineGetter).SQLiteEngine()
		}}
		return native
	})
	c := searchImageStageNativeCommand(t, 29)
	for _, mode := range []string{"memory_zero", "memory_short", "engine_wrong", "engine_error", "packet_changed", "lease_expired", "lease_short", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			actual = nil
			turn := searchImageStageTurn(t, w, registry)
			custody := turn.transaction.custody.(*governedTurn)
			before := custody.a.Usage()
			input := c
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			engineReads = 0
			want := ErrConflict
			var capacity RefusalCode
			switch mode {
			case "memory_zero":
				custody.searchImageMemory = searchImageMemoryBudget{}
				capacity = RefusalUnqualified
			case "memory_short":
				n, err := searchImageFreshRequiredBytes(c)
				if err != nil {
					t.Fatal(err)
				}
				custody.searchImageMemory = searchImageMemoryBudget{n - 1, (8 << 20) - n + 1}
				capacity = RefusalCapacity
			case "engine_wrong":
				actual = func() (string, string, error) { return searchpolicy.SQLiteVersion, "wrong source", nil }
			case "engine_error":
				want = errors.New("physical engine getter failure")
				actual = func() (string, string, error) { return "", "", want }
			case "packet_changed":
				copy := *c
				copy.request.header.titleFold = "tampered after freeze"
				input = &copy
			case "lease_expired", "lease_short":
				copy := *c
				expiry := w.now().UnixMilli()
				if mode == "lease_short" {
					expiry++
				}
				copy.request.owner.leaseExpiresMS = expiry
				// A genuinely frozen short original lease must fail W's WHOLE-turn
				// deadline check, even when it is live at the instant sampled.
				input, err = newSearchImageStageCommand(copy.identity, copy.request)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, "UPDATE search_publish_jobs SET job_lease_expires_ms=? WHERE id=29", expiry); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
				want = context.Canceled
			}
			out, err := applySearchImageStage(ctx, turn, input)
			if capacity != 0 {
				var refused *Refusal
				if !errors.As(err, &refused) || refused.Code != capacity {
					t.Fatal(mode, err)
				}
			} else if !errors.Is(err, want) {
				t.Fatal(mode, err)
			}
			if out != (SearchImageStageResult{}) {
				t.Fatal("refusal released result", out)
			}
			if (capacity != 0 || mode == "packet_changed" || mode == "cancelled") && engineReads != 0 {
				t.Fatal("fresh work before admission", mode, engineReads)
			}
			if custody.a.Usage() != before {
				t.Fatal("early refund on refusal")
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if custody.a.Usage() != ([4]governor.Amounts{}) || w.db.Stats().InUse != 0 {
				t.Fatal("custody outlived physical return")
			}
			var n int64
			if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM search_publications WHERE generation=5").Scan(&n); err != nil || n != 0 {
				t.Fatal("gate left an image prefix", n, err)
			}
		})
	}
}

func TestSearchImageEntryUsesOneAppliedTime(t *testing.T) {
	w, registry := searchImageStageFixture(t)
	c := searchImageStageNativeCommand(t, 29)
	turn := searchImageStageTurn(t, w, registry)
	// Every policy/quantum sample uses the same W clock. Saved applied_ms must
	// come from that clock, with no caller-time field or wall-clock reread.
	expected := w.now().UnixMilli()
	out, err := applySearchImageStage(t.Context(), turn, c)
	if err != nil || out.AppliedMS != expected || out.Outcome != (ReceiptOutcome{}) {
		t.Fatal(out, err)
	}
	if err := checkSearchImageTerminal(t.Context(), turn, c); err != nil {
		t.Fatal("live terminal lease", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}
