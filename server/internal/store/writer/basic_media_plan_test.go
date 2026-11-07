package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordBasicMediaPlans = flag.Bool("record-basic-media-plans", false, "deliberately record closed Basic media families2..9 component plans")

// Native effect-fact populations only. No complete Basic command, receipt,
// source authority, production admission or physical budget is qualified here.
func TestBasicMediaStatementPlans(t *testing.T) {
	parts := []struct{ name, sha string }{
		{"basic_stage.go", "b2adbf700ce9928f1a2b388322124455b564780b85d71a6026861342aed52b23"},
		{"basic_stage_test.go", "8e8da49da22747d1d709405398198f8c3fd46400bc63c9fb434758c553a17966"},
		{"basic_media_rows.go", "0ca12d6358e85f844a91cedc58c35bb846a13bf99dcdec83b6251d2138f68afb"},
		{"basic_media_stage.go", "64462793b49e1977977793269e665742554c89530847f4a50456e17aa5fd0f7c"},
		{"basic_media_stage_test.go", "22e19f73b2d0bf0fca6f6a7040a71314bc9f0657d5f3ae884abcbd02d3acf1a6"},
		{"basic_media_statements.go", "86f393686fd1e27c7dbf3c2b3409b7c575623aa29755afbb3c584dd27776aafb"},
		{"basic_stream_rows.go", "0c0dec2b8e92354bf8bad787f52b102804af325e685b3a263bd37666cc6c4a34"},
		{"basic_stream_row_statements.go", "5b5e490c2e53c20aae60fbf9cfc99964026442c8140fd60e9c745bc3d3aaa3c9"},
		{"basic_stream_rows_test.go", "60ba70c1a9cdf6a6f3bb172d78e4ab0e94fca003958db77ba3151d1065ebb86b"},
	}
	h := sha256.New()
	for _, p := range parts {
		b, err := os.ReadFile(p.name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != p.sha {
			t.Fatal("frozen Basic source changed; review before recording", p.name)
		}
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(p.name))))
		h.Write([]byte(p.name))
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(b))))
		h.Write(b)
	}
	journal := hex.EncodeToString(h.Sum(nil))
	defs := basicMediaDefinitions()
	if len(defs) != 16 {
		t.Fatal("closed Basic definition inventory", len(defs))
	}
	var stage, seal []stmt.Definition
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 {
			t.Fatal("hot closed cohort inventory", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		if d.Kind == stmt.Read {
			seal = append(seal, d)
		} else {
			stage = append(stage, d)
		}
	}
	if len(stage) != 8 || len(seal) != 8 {
		t.Fatal("stage/seal inventory")
	}
	w, in := basicMediaFixture(t)
	// The owner stream/breadth control supplies these two family witnesses.
	// All other native facts come from the unchanged owner fixture.
	for _, family := range []int64{2, 3} {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected) VALUES(51,?,0,2)`, family); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	startup, assertErr := sqliteconn.Assert(t.Context(), conn, w.config)
	closeErr := conn.Close()
	if assertErr != nil || closeErr != nil || startup.Synchronous != 2 {
		t.Fatal(startup, assertErr, closeErr)
	}
	t.Logf("Basic FULL fixture startup: %+v", startup)
	// The owner helper selects NORMAL and registers its complete 26-ID
	// component set with an actual Capture receipt, without production admission.
	turn := basicComponentTurn(t, w, basicMediaTestDefs())
	tx := turn.transaction
	config := w.config
	config.Synchronous = sqliteconn.Normal
	settings, err := sqliteconn.Assert(tx.ctx, tx.conn, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Basic NORMAL capture boundary: %+v", settings)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	type population struct {
		Name   string      `json:"name"`
		Golden plan.Golden `json:"golden"`
	}
	captures := []population{}
	var fkJSON []byte
	destinationTable := map[stmt.ID]string{
		basicMediaStreamInsert:       "catalog_info_new_streams",
		basicMediaBreadthInsert:      "catalog_info_new_stream_breadth",
		basicMediaChapterFactInsert:  "catalog_info_new_chapter_facts",
		basicMediaChapterInsert:      "catalog_info_new_chapters",
		basicMediaSubtitleFactInsert: "catalog_info_new_subtitle_facts",
		basicMediaSidecarInsert:      "catalog_info_new_subtitle_sidecars",
		basicMediaSubtitleInsert:     "catalog_info_new_subtitles",
		basicMediaEvidenceInsert:     "catalog_info_new_subtitle_inventory_evidence",
	}
	destinations := make(map[stmt.ID]bool)
	for id := range destinationTable {
		destinations[id] = true
	}
	vm, search, inserts := 0, 0, 0
	families := basicMediaCaptureFamilies(t, in)
	for n, group := range [][]stmt.Definition{stage, seal} {
		name := "insert_before_owner_stage"
		if n == 1 {
			if got, err := stageBasicStreamFact(tx.ctx, turn, in); err != nil || got != (basicFamilyProgress{51, 7, 1, 0}) {
				t.Fatal("owner stream fact population", got, err)
			}
			basicMediaCommit(t, turn)
			for _, frame := range families {
				// Each existing synthetic component recipe funds one owner body,
				// as in the owner controls; no aggregate forecast is invented here.
				turn = basicComponentTurn(t, w, basicMediaTestDefs())
				got, err := stageBasicMediaRows(t.Context(), turn, frame, 0, int64(len(frame.rows)))
				if err != nil || got.rowsStaged != int64(len(frame.rows)) || got.sealed != 0 {
					t.Fatal("owner stage population", frame.family, got, err)
				}
				basicMediaCommit(t, turn)
				turn = basicComponentTurn(t, w, basicMediaTestDefs())
				got, err = sealBasicMediaFamily(t.Context(), turn, frame)
				if err != nil || got.rowsStaged != int64(len(frame.rows)) || got.sealed != 1 {
					t.Fatal("owner seal population", frame.family, got, err)
				}
				basicMediaCommit(t, turn)
			}
			turn = basicComponentTurn(t, w, basicMediaTestDefs())
			tx = turn.transaction
			if _, err := sqliteconn.Assert(tx.ctx, tx.conn, config); err != nil {
				t.Fatal(err)
			}
			name = "rows_after_owner_stage_and_seal"
		}
		catalogue, err := stmt.Describe(group)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		g, err := plan.Capture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: config, SHA256: journal, Statistics: plan.Unanalyzed, Implementations: "frozen basicMediaFixture plus owner stream/breadth witnesses; " + name + "; synthetic W component turn/forecasts; no command or resource qualification"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(group, g.Receipt()); err != nil {
			t.Fatal("exact population Capture receipt", err)
		}
		// UPDATE/read-only populations need the independently executed full FK
		// audit; Capture attaches it automatically only for INSERT destinations.
		if g.ForeignKeys == nil {
			fk, err := plan.ForeignKeys(tx.ctx, tx.conn, config)
			if err != nil {
				t.Fatal(err)
			}
			g.ForeignKeys = &fk
		}
		if len(g.Manifest) != len(group) || len(g.Plans) != len(group) || g.ForeignKeys == nil || len(g.ForeignKeys.Plans) != 686 {
			t.Fatal("incomplete Basic statement/destination/FK audit")
		}
		for _, p := range g.Plans {
			if len(p.Explain.Values) == 0 {
				t.Fatal("missing actual VM", p.ID)
			}
			vm += len(p.Explain.Values)
			if p.Insert != nil {
				if !destinations[p.ID] || p.Insert.Table != destinationTable[p.ID] {
					t.Fatal("unexpected INSERT destination", p.ID, p.Insert.Table)
				}
				inserts++
			}
			for _, row := range p.QueryPlan.Values {
				if len(row) != 4 || !strings.HasPrefix(row[3].(string), "SEARCH ") {
					t.Fatal("unexpected native plan", p.ID, row)
				}
				search++
			}
		}
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil || before != after {
			t.Fatal("Capture changed population", before, after, err)
		}
		fk, err := json.MarshalIndent(g.ForeignKeys, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if fkJSON == nil {
			fkJSON = fk
		} else if err := plan.CompareExactJSON(fkJSON, fk); err != nil {
			t.Fatal("population changed global FK audit", err)
		}
		g.ForeignKeys = nil
		captures = append(captures, population{name, g})
	}
	if inserts != 8 {
		t.Fatal("INSERT destination inventory", inserts)
	}
	reference := readScanFreshFK(t, filepath.Join("testdata", "scan-inventory-accept-fk.json.gz"))
	if err := plan.CompareExactJSON(reference, fkJSON); err != nil {
		t.Fatal("Basic changed existing complete686-FK image", err)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "scan-basic-media.json")
	fkPath := filepath.Join("testdata", "scan-basic-media-fk.json.gz")
	if *recordBasicMediaPlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, err := z.Write(fkJSON); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fkPath, b.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareExactJSON(readScanFreshFK(t, fkPath), fkJSON); err != nil {
		t.Fatal(err)
	}
	t.Logf("16IDs/16cohorts/two actual component populations/%dVM/%dSEARCH/8INSERT/686FK2058probes; seed journal%s; no Basic admission/authority/HOLD qualification", vm, search, journal)
}

// Exact normalized values from the owner's two native stage/seal controls.
// This is a component SQL-fact population, not a valid accepting command.
func basicMediaCaptureFamilies(t *testing.T, source basicStreamFactStage) []*basicMediaFamily {
	t.Helper()
	input := []struct {
		family int64
		rows   []basicMediaRow
	}{
		{2, []basicMediaRow{basicStreamRow{index: 0, streamType: "video", codec: "h264", isDefault: true, colorTransfer: "bt709", colorPrimaries: "bt709", detail: `{"width":1920}`}, basicStreamRow{index: 3, streamType: "audio", codec: "aac", language: "en", channels: 2, channelLayout: "stereo", isDefault: true}}},
		{3, []basicMediaRow{basicStreamBreadth{index: 0, chroma: "4:2:0", fpsNumerator: 24000, fpsDenominator: 1001, frameMode: "constant", interlace: "progressive", doviKnown: true}, basicStreamBreadth{index: 3, audioDescription: true, audioVariant: "main"}}},
		{4, []basicMediaRow{basicChapterFact{37, "known", "chapters:37"}}},
		{5, []basicMediaRow{basicChapter{1, "Opening", 0, 3}, basicChapter{2, "Closing", 3, 9}}},
		{6, []basicMediaRow{basicSubtitleFact{41, 0, true, "known", "subtitles:41"}}},
		{7, []basicMediaRow{basicSubtitleSidecar{"sidecar-1", "/media/Arrival.srt", "sidecar:30", 30, -7}}},
		{8, []basicMediaRow{basicSubtitle{id: "subtitle-1", origin: "embedded", locator: "2", streamIndex: 2, format: "subrip", language: "en", title: "English"}, basicSubtitle{id: "subtitle-2", origin: "sidecar", locator: "/media/Arrival.srt", sidecarID: "sidecar-1", streamIndex: -1, format: "subrip", language: "fr", title: "French", hearingImpaired: true}}},
		{9, []basicMediaRow{basicSubtitleEvidence{41, "inventory:41"}}},
	}
	out := make([]*basicMediaFamily, 0, len(input))
	for _, f := range input {
		frame, err := prepareBasicMediaFamily(source, f.family, 2, f.rows)
		if err != nil {
			t.Fatal(f.family, err)
		}
		out = append(out, frame)
	}
	return out
}
