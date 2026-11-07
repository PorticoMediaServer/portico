package writer

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// These exercise native SQL-fact component frames with synthetic budgets.
// They establish neither supervised filesystem observations nor qualification.
func TestScanDirectorySmallActualByteFunding(t *testing.T) {
	for _, grown := range []bool{false, true} {
		t.Run(map[bool]string{false: "small_payload", true: "native_payload_exceeds_preparation"}[grown], func(t *testing.T) {
			w, e, a, s := directoryFixture(t)
			p := w.effective.directoryPlans[0]
			// A real 512 KiB grant, with 128 KiB for the actual payload and
			// 384 KiB reserved. A maximum-request reservation cannot fit.
			p.need[governor.MemoryBytes] = 512 << 10
			p.directoryMemory = commandMemoryBudget{128 << 10, 384 << 10}
			c := directoryCommandFixture(t, s, InventoryDirectoryDiscover)
			before := directoryCounts(t, e)
			if grown {
				// Native octet metadata must reject this BEFORE reading a
				// mismatching snapshot or attempting any command effects.
				if _, err := e.db.Exec(`UPDATE jobs SET error=? WHERE id='lifecycle-run'`, strings.Repeat("é", 2048)); err != nil {
					t.Fatal(err)
				}
			}
			out, err := directoryExecute(t, w, c)
			if grown {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, scanDirectoryCommandResult{}) {
					t.Fatal("native byte growth did not refuse preparation capacity", out, err)
				}
				if got := directoryCounts(t, e); got != before {
					t.Fatal("capacity refusal changed command facts", got, before)
				}
			} else {
				if err != nil || out.Outcome.State != Applied || out.Value.Directory.Path != "child" {
					t.Fatal("small actual payload could not use small funded preparation", out, err)
				}
				q := c.reference()
				replay, _, err := w.effective.runScanDirectory(t.Context(), w, inventoryTestDisclosure(t, w, q), q, c.purpose, nil, false)
				if err != nil || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, out.Value) || replay.Digest != out.Digest {
					t.Fatal("small funded saved result changed", replay, err)
				}
			}
			if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
				t.Fatal("small funded turn leaked custody")
			}
		})
	}
}

func TestScanDirectoryWrongDisclosureCannotApplyResolveOrAck(t *testing.T) {
	w, e, a, s := directoryFixture(t)
	c := directoryCommandFixture(t, s, InventoryDirectoryDiscover)
	q := c.reference()
	wrong := inventoryTestDisclosure(t, w, q)
	wrong.scope.libraryID++
	before := directoryCounts(t, e)
	out, floor, err := w.effective.runScanDirectory(t.Context(), w, wrong, q, c.purpose, c, false)
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, scanDirectoryCommandResult{}) || !reflect.DeepEqual(floor, AckResult{}) || directoryCounts(t, e) != before {
		t.Fatal("wrong disclosure applied fresh effects", out, floor, err)
	}
	if applied, err := directoryExecute(t, w, c); err != nil || applied.Outcome.State != Applied {
		t.Fatal("valid disclosure failed positive control", applied, err)
	}
	before = directoryCounts(t, e)
	for _, ack := range []bool{false, true} {
		out, floor, err := w.effective.runScanDirectory(t.Context(), w, wrong, q, c.purpose, nil, ack)
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, scanDirectoryCommandResult{}) || !reflect.DeepEqual(floor, AckResult{}) || directoryCounts(t, e) != before {
			t.Fatal("wrong disclosure exposed history or advanced floor", ack, out, floor, err)
		}
	}
	if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("disclosure refusal leaked custody")
	}
}

func TestScanDirectorySavedResultCorruptionRefusesResolveAndAck(t *testing.T) {
	for _, fault := range []string{"digest", "extra_member"} {
		t.Run(fault, func(t *testing.T) {
			w, e, a, s := directoryFixture(t)
			c := directoryCommandFixture(t, s, InventoryDirectoryDiscover)
			out, err := directoryExecute(t, w, c)
			if err != nil || out.Outcome.State != Applied {
				t.Fatal("valid receipt positive control failed", out, err)
			}
			if fault == "digest" {
				if _, err := e.db.Exec(`UPDATE scan_command_receipts SET result_digest=zeroblob(32) WHERE run_id='lifecycle-run' AND command_sequence=2`); err != nil {
					t.Fatal(err)
				}
			} else {
				// A real FK-valid unexpected cohort member; the scalar header
				// and its digest remain untouched and still claim zero members.
				for _, sql := range []string{
					`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
					`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(41,'directory-control-asset',7,'/media/movie.mkv',42,1,'','','',0,0,0)`,
				} {
					if _, err := e.db.Exec(sql); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := e.db.Exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(31,?,7,1)`, bytes.Repeat([]byte{0x31}, 16)); err != nil {
					t.Fatal(err)
				}
				if _, err := e.db.Exec(`INSERT INTO scan_command_results SELECT run_id,command_incarnation,command_sequence,0,0,0,'unexpected-directory-member',7,31,41,1 FROM scan_command_receipts WHERE run_id='lifecycle-run' AND command_sequence=2`); err != nil {
					t.Fatal(err)
				}
				var members, brokenFKs int
				if err := e.db.QueryRow(`SELECT count(*) FROM scan_command_results WHERE run_id='lifecycle-run' AND command_sequence=2`).Scan(&members); err != nil || members != 1 {
					t.Fatal("extra member was not seeded", members, err)
				}
				if err := e.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&brokenFKs); err != nil || brokenFKs != 0 {
					t.Fatal("extra-member fixture broke native foreign keys", brokenFKs, err)
				}
			}
			before := directoryCounts(t, e)
			q := c.reference()
			cap := inventoryTestDisclosure(t, w, q)
			for _, ack := range []bool{false, true} {
				got, floor, err := w.effective.runScanDirectory(t.Context(), w, cap, q, c.purpose, nil, ack)
				if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(got, scanDirectoryCommandResult{}) || !reflect.DeepEqual(floor, AckResult{}) {
					t.Fatal("corrupt saved result disclosed or acknowledged", ack, got, floor, err)
				}
				if directoryCounts(t, e) != before {
					t.Fatal("corruption refusal changed command facts")
				}
			}
			if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
				t.Fatal("corruption refusal leaked custody")
			}
		})
	}
}
