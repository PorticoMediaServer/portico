package writer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func profileNumber(n int64) *int64 { return &n }

// Fabricated observer values, not native execution or physical profile proof.
func TestN1ProfilePerIDPreservesPartialMaxAndMissingDimensions(t *testing.T) {
	trace := newN1Trace(time.Now(), true)
	trace.ids = map[string]stmt.ID{"sql": "used", "not-used": "unused"}
	trace.uses = map[stmt.ID]int{"used": 2, "unused": 0}
	first := n1Unit{ID: "used", Order: 1, UseIndex: 1, StartedNS: profileNumber(120), ReturnedNS: profileNumber(125), ElapsedNS: profileNumber(5), NativeElapsedNS: profileNumber(4), RowsReturned: profileNumber(2), exclusive: true}
	first.VMOps.set(3)
	first.Before = &n1NativeSnapshot{ConnectionID: 1, WALIO: &sqlite.WALIOStats{Qualified: true, WrittenBytes: 10, HeaderWrites: 1}}
	first.After = &n1NativeSnapshot{ConnectionID: 1, WALIO: &sqlite.WALIOStats{Qualified: true, WrittenBytes: 20, HeaderWrites: 2}}
	second := n1Unit{ID: "used", Order: 2, UseIndex: 2, Error: "partial-return control", StartedNS: profileNumber(130)}
	record := &n1Attempt{ID: "turn", OriginalID: "original", RegistrySHA256: strings.Repeat("0", 64), Cohort: "native-counters", Units: []n1Unit{first, second}, Clock: n1Clock{HoldStartedNS: profileNumber(100), PhysicalReturnedNS: profileNumber(150)}, WALBefore: &n1WAL{State: &governor.WALState{Salt: [8]byte{1}}}, WALAfter: &n1WAL{State: &governor.WALState{Salt: [8]byte{1}}}}
	profile := n1BuildProfile(record, trace)
	if len(profile.PerID) != 2 || profile.PerID[0].ID != "unused" || profile.PerID[0].UseCount != 0 || profile.PerID[1].UseCount != 2 || profile.PerID[1].ReturnedUses != 1 || profile.PerID[1].ErrorUses != 1 {
		t.Fatal("ID census lost failed or unused IDs", profile.PerID)
	}
	metric := profile.PerID[1].Metrics["elapsed_ns"]
	if metric.Value != nil || metric.ObservedMax == nil || *metric.ObservedMax != 5 || metric.KnownUses != 1 || metric.MissingUses != 1 {
		t.Fatal("partial subset maximum labelled complete", metric)
	}
	for _, key := range []string{"native_pages", "rows_examined", "all_io_bytes", "wal_append_bytes", "wal_header_bytes"} {
		m := profile.PerID[1].Metrics[key]
		if m.Value != nil || m.Reason == "" {
			t.Fatal("unavailable dimension fabricated", key, m)
		}
	}
	if profile.Boundaries["Setup"].ElapsedNS != nil || profile.Boundaries["Terminal"].ElapsedNS != nil || profile.Boundaries["header_sync"].ElapsedNS != nil || profile.FullPrefix != nil || profile.ProfileReady || profile.LivePeaks["native"].Value != nil {
		t.Fatal("unavailable owner/MAX/LIVE gate was guessed")
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"complete_full_prefix":null`) {
		t.Fatal("missing prefix became false/zero instead of unavailable")
	}
}

func TestN1ProfileCounterAttributionAndNoHeaderSubtraction(t *testing.T) {
	u := n1Unit{exclusive: true, Before: &n1NativeSnapshot{ConnectionID: 1, WALIO: &sqlite.WALIOStats{Qualified: true, WrittenBytes: 100, HeaderWrites: 1}}, After: &n1NativeSnapshot{ConnectionID: 1, WALIO: &sqlite.WALIOStats{Qualified: true, WrittenBytes: 110, HeaderWrites: 2}}}
	fact := n1IDProfile("unit", true, []*n1Unit{&u})
	if n := fact.Metrics["wal_written_bytes"].Value; n == nil || *n != 10 {
		t.Fatal("native write delta lost", fact)
	}
	if n := fact.Metrics["wal_header_writes"].Value; n == nil || *n != 1 {
		t.Fatal("observed header count lost")
	}
	if fact.Metrics["wal_header_bytes"].Value != nil || fact.Metrics["wal_append_bytes"].Value != nil {
		t.Fatal("fabricated header bytes or append subtraction")
	}
	u.exclusive = false
	if n, _ := n1WALDelta(&u, "wal_written_bytes"); n != nil {
		t.Fatal("overlapped request attributed to one ID")
	}
	u.exclusive = true
	u.After.ConnectionID = 2
	if n, _ := n1WALDelta(&u, "wal_written_bytes"); n != nil {
		t.Fatal("counter delta crossed physical connection")
	}
}

func TestN1ProfileNoRegistryOrMAXIsNotSynthesized(t *testing.T) {
	trace := newN1Trace(time.Now(), false)
	record := &n1Attempt{ID: "unreturned-registry"}
	p := n1BuildProfile(record, trace)
	if len(p.PerID) != 0 || p.RegistrationReason == "" {
		t.Fatal("compiled default list fabricated actual registration")
	}
	input := n1InputProfile(inventoryFreshRequest(t), n1Case{"active", "current", "present", "fixture"})
	if input.LegalMAX != nil || input.LegalMIN != nil || input.MissingReason == "" || input.TextBytes["text_00"] == 0 || input.TitleLimitCodepoints != MaxTitleCharacters {
		t.Fatal("forecast or fixture promoted to MAX")
	}
}

func TestN1InputProfileEscapingKeepsObservedZero(t *testing.T) {
	command := inventoryFreshRequest(t)
	facts := n1InputProfile(command, n1Case{})
	plain, empty := false, false
	for i, text := range inventoryRequestTexts(&command.request) {
		key := stringIndex(i)
		count, exists := facts.Escaping[key]
		if !exists {
			t.Fatal("actual zero omitted", key)
		}
		if *text == "" {
			empty = true
			if count != 0 {
				t.Fatal("empty text escaping nonzero")
			}
		}
		if *text == "ordinary_batch" {
			plain = true
			if count != 0 {
				t.Fatal("plain text escaping nonzero")
			}
		}
	}
	if !plain || !empty {
		t.Fatal("control missed plain/empty fields")
	}
	command.request.files[0].title = "<&\""
	escaped := n1InputProfile(command, n1Case{})
	for i, text := range inventoryRequestTexts(&command.request) {
		if *text == "<&\"" && escaped.Escaping[stringIndex(i)] != 3 {
			t.Fatal("escaped scalar count missing")
		}
	}
}
