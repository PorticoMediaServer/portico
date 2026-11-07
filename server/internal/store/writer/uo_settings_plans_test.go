package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Native draft evidence only: Capture still refuses new closed octet shapes on
// this base. Do not mistake these EXPLAIN controls for HN-adopted receipts.
func TestUOSettingsDraftNativePlans(t *testing.T) {
	w := wc008NativeExecutor(t)
	uoSettingsSeed(t, w)
	type record struct {
		ID        stmt.ID
		SQLSHA256 string
		EQP       [][]any
		VMSHA256  string
	}
	var records []record
	for _, d := range append(uoPreferenceDefinitions(), uoSettingsDefinitions()...) {
		args := uoStatementBindings(d.Params)
		var rec record
		rec.ID = d.ID
		hash := sha256.Sum256([]byte(d.SQL))
		rec.SQLSHA256 = hex.EncodeToString(hash[:])
		query := func(prefix string) [][]any {
			rows, e := w.db.QueryContext(t.Context(), prefix+d.SQL, args...)
			if e != nil {
				t.Fatal(d.ID, e)
			}
			defer rows.Close()
			cols, e := rows.Columns()
			if e != nil {
				t.Fatal(e)
			}
			var out [][]any
			for rows.Next() {
				row := make([]any, len(cols))
				dest := make([]any, len(cols))
				for i := range row {
					dest[i] = &row[i]
				}
				if e = rows.Scan(dest...); e != nil {
					t.Fatal(e)
				}
				out = append(out, row)
			}
			if e = rows.Err(); e != nil {
				t.Fatal(e)
			}
			return out
		}
		rec.EQP = query("EXPLAIN QUERY PLAN ")
		vm := query("EXPLAIN ")
		if len(vm) == 0 {
			t.Fatal("no native VM", d.ID)
		}
		encoded, e := json.Marshal(vm)
		if e != nil {
			t.Fatal(e)
		}
		hash = sha256.Sum256(encoded)
		rec.VMSHA256 = hex.EncodeToString(hash[:])
		for _, r := range rec.EQP {
			detail := r[3].(string)
			if strings.Contains(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") {
				t.Fatal("unbounded draft plan", d.ID, detail)
			}
		}
		if strings.Contains(d.SQL, "octet_length(") && len(plan.LintSQL(d.SQL)) == 0 {
			t.Fatal("base unexpectedly admits new byte shape; adopt exact Capture proof", d.ID)
		}
		records = append(records, rec)
	}
	data, e := json.MarshalIndent(records, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "uo-preferences-settings-native-plans.json"), append(data, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("%d native statement plans; exact octet adoption remains HN-owned", len(records))
}
