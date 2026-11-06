package writer

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
)

// Preparation-only controls. None of these budgets grants writer/native work.
func searchFragmentPreparationFixture() searchFragmentPreparationBudget {
	return searchFragmentPreparationBudget{bytes: 6144, revisionBytes: 1024, tokenWork: 8192, preparationBytes: 128 << 10}
}

func TestSearchFragmentPreparationKeepsIndependentRawStreams(t *testing.T) {
	rows := []struct {
		row  searchmanifest.Row
		work uint64
	}{
		{searchmanifest.Row{Field: 1, Revision: "opaque:revision/09"}, 0},
		{searchmanifest.Row{Field: 1, Ordinal: 4, Revision: "09", Prefix: "000061 000062000063", Lexical: "a 東京", Normalized: "a東京"}, 18},
		{searchmanifest.Row{Field: 1, Revision: "11", Prefix: "000061", Lexical: "unrelated", Normalized: "辛哫"}, 38},
		// Boundary words retain their WHOLE prepared bytes; this validator does
		// not clip an oversized token into a different accepted manifest.
		{searchmanifest.Row{Field: 1, Revision: "11", Prefix: strings.Repeat("000061", 12)}, 13},
		{searchmanifest.Row{Field: 1, Revision: "11", Prefix: strings.Repeat("000031", 6)}, 7},
	}
	for _, tt := range rows {
		p, err := prepareSearchFragment(tt.row, searchFragmentPreparationFixture())
		if err != nil || !p.valid || p.row != tt.row || p.tokenWork != tt.work {
			t.Fatal("prepared streams were changed/aligned", p, err)
		}
		if p.bytes != uint64(len(tt.row.Prefix)+len(tt.row.Lexical)+len(tt.row.Normalized)) {
			t.Fatal("opaque revision entered prepared-byte count", p.bytes)
		}
		leaf, err := searchmanifest.Leaf(tt.row)
		if err != nil || p.leaf != leaf {
			t.Fatal("raw native TEXT/column bytes changed before hashing", err)
		}
	}
}

func TestSearchFragmentPreparationBoundsBeforeCopies(t *testing.T) {
	budget := searchFragmentPreparationFixture()
	for _, mode := range []string{"prepared_bytes", "revision_bytes", "reservation"} {
		t.Run(mode, func(t *testing.T) {
			row := searchmanifest.Row{Field: 1, Revision: "11"}
			b := budget
			switch mode {
			case "prepared_bytes":
				row.Prefix = strings.Repeat("x", 4<<20)
			case "revision_bytes":
				row.Revision = strings.Repeat("x", 4<<20)
			case "reservation":
				row.Normalized = strings.Repeat("x", 6000)
				b.preparationBytes = 1
			}
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			p, err := prepareSearchFragment(row, b)
			runtime.ReadMemStats(&after)
			if err == nil || p != (searchPreparedFragment{}) {
				t.Fatal("refusal released a prepared prefix", p, err)
			}
			if n := after.TotalAlloc - before.TotalAlloc; n >= 64<<10 {
				t.Fatal("oversized/refused input copied before admission", n)
			}
		})
	}
}

func TestSearchFragmentPreparationExactByteWorkAndMemoryEdges(t *testing.T) {
	budget := searchFragmentPreparationFixture()
	for _, delta := range []int{-1, 0, 1} {
		row := searchmanifest.Row{Field: 1, Revision: "11", Normalized: strings.Repeat("x", int(budget.bytes)+delta)}
		p, err := prepareSearchFragment(row, budget)
		if delta <= 0 {
			if err != nil || p.bytes != uint64(len(row.Normalized)) {
				t.Fatal(delta, p, err)
			}
		} else if err == nil || p != (searchPreparedFragment{}) {
			t.Fatal(delta, p, err)
		}
	}
	row := searchmanifest.Row{Field: 1, Revision: "11", Lexical: "a"}
	for _, limit := range []uint64{3, 4, 5} {
		b := budget
		b.tokenWork = limit
		p, err := prepareSearchFragment(row, b)
		if limit >= 4 {
			if err != nil || p.tokenWork != 4 {
				t.Fatal(limit, p, err)
			}
		} else if err == nil || p != (searchPreparedFragment{}) {
			t.Fatal(limit, p, err)
		}
	}
	_, needed, err := searchFragmentPayloadBytes(row, budget)
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []int64{-1, 0, 1} {
		b := budget
		b.preparationBytes = uint64(int64(needed) + extra)
		p, err := prepareSearchFragment(row, b)
		if extra >= 0 {
			if err != nil || !p.valid {
				t.Fatal(extra, p, err)
			}
		} else if err == nil || p != (searchPreparedFragment{}) {
			t.Fatal(extra, p, err)
		}
	}
	if n := testing.AllocsPerRun(20, func() { _, _, _ = searchFragmentPayloadBytes(row, budget) }); n != 0 {
		t.Fatal("prospective payload memory check allocated", n)
	}
}

func TestSearchFragmentPreparationClonesEveryRetainedString(t *testing.T) {
	backing := strings.Repeat("000061", 1<<18)
	row := searchmanifest.Row{Field: 1, Revision: backing[:2], Prefix: backing[:6], Lexical: backing[6:9], Normalized: backing[9:12]}
	p, err := prepareSearchFragment(row, searchFragmentPreparationFixture())
	if err != nil {
		t.Fatal(err)
	}
	for i, pair := range [][2]string{{row.Revision, p.row.Revision}, {row.Prefix, p.row.Prefix}, {row.Lexical, p.row.Lexical}, {row.Normalized, p.row.Normalized}} {
		if pair[0] != pair[1] || unsafe.StringData(pair[0]) == unsafe.StringData(pair[1]) {
			t.Fatal("retained large backing string", i)
		}
	}
}

func TestSearchFragmentPreparationRejectsMalformedNativeRows(t *testing.T) {
	for _, prefix := range []string{"00006A", "00d800", "110000", "0000610", " 000061", "000061 ", "000061  000062", "000061\t000062", strings.Repeat("000061", 13), strings.Repeat("000031", 7)} {
		p, err := prepareSearchFragment(searchmanifest.Row{Field: 1, Revision: "11", Prefix: prefix}, searchFragmentPreparationFixture())
		if err == nil || p != (searchPreparedFragment{}) {
			t.Fatal(prefix, p, err)
		}
	}
	for _, row := range []searchmanifest.Row{{Field: 0}, {Field: 65}, {Field: 1, Ordinal: -1}, {Field: 1, Revision: string([]byte{0xff})}, {Field: 1, Lexical: string([]byte{0xff})}} {
		p, err := prepareSearchFragment(row, searchFragmentPreparationFixture())
		if err == nil || p != (searchPreparedFragment{}) {
			t.Fatal(row, p, err)
		}
	}
}
