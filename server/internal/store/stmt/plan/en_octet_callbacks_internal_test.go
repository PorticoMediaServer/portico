package plan

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestENOctetEQPCallbacksRefuseMalformedPlans(t *testing.T) {
	raw, err := os.ReadFile("testdata/en-effective-source-octets-input.json")
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Definitions []stmt.Definition `json:"definitions"`
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	definitions := make(map[stmt.ID]stmt.Definition)
	for _, d := range input.Definitions {
		definitions[d.ID] = d
	}
	// History definitions come from their separately reviewed native baseline.
	// Expected EQP answers below remain literal and independent of that data.
	file, err := os.Open("testdata/en-seal-octets.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	z, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	historyRaw, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	var history struct {
		Manifest []stmt.Definition `json:"manifest"`
	}
	if err = json.Unmarshal(historyRaw, &history); err != nil {
		t.Fatal(err)
	}
	for _, d := range history.Manifest {
		definitions[d.ID] = d
	}
	if len(definitions) != 5 {
		t.Fatal("unexpected callback definition inventory")
	}

	// These answers are independent of the production seek/point helpers.
	for _, tc := range []struct {
		id                    stmt.ID
		want, scan, wrongSeek string
	}{
		{"enrichment.seal.evidence_header_lengths", "SEARCH metadata_evidence USING INDEX sqlite_autoindex_metadata_evidence_1 (id=?)", "SCAN metadata_evidence", "SEARCH metadata_evidence USING INDEX sqlite_autoindex_metadata_evidence_1 (subject_id=?)"},
		{"enrichment.seal.receipt_lengths", "SEARCH enrichment_command_receipts USING PRIMARY KEY (provider_operation_id=? AND command_incarnation=? AND command_sequence=?)", "SCAN enrichment_command_receipts", "SEARCH enrichment_command_receipts USING PRIMARY KEY (provider_operation_id=? AND command_incarnation=?)"},
		{"enrichment.effective_source.pin_evidence_lengths", "SEARCH metadata_evidence USING INDEX sqlite_autoindex_metadata_evidence_1 (id=?)", "SCAN metadata_evidence", "SEARCH metadata_evidence USING INDEX sqlite_autoindex_metadata_evidence_1 (id>?)"},
		{"enrichment.effective_source.pin_publication_lengths", "SEARCH credit_source_publications USING INDEX sqlite_autoindex_credit_source_publications_1 (publication_id=?)", "SCAN credit_source_publications", "SEARCH credit_source_publications USING INDEX sqlite_autoindex_credit_source_publications_1 (entity_id=?)"},
		{"enrichment.effective_source.pin_receipt_lengths", "SEARCH enrichment_command_receipts USING PRIMARY KEY (provider_operation_id=? AND command_incarnation=? AND command_sequence=?)", "SCAN enrichment_command_receipts", "SEARCH enrichment_command_receipts USING PRIMARY KEY (provider_operation_id=? AND command_incarnation=?)"},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			d, ok := definitions[tc.id]
			if !ok || !closedOctetDefinition(d) {
				t.Fatal("fixture must reach the actual registered callback")
			}
			row := func(detail string) []any { return []any{int64(1), int64(0), int64(0), detail} }
			columns := []string{"id", "parent", "notused", "detail"}
			if err := checkOctetPoint(d, Rows{Columns: columns, Values: [][]any{row(tc.want)}}); err != nil {
				t.Fatal("valid exact point refused", err)
			}
			for _, bad := range []struct {
				name   string
				values [][]any
			}{
				{"empty", nil},
				{"scan", [][]any{row(tc.scan)}},
				{"wrong_seek", [][]any{row(tc.wrongSeek)}},
				{"extra_row", [][]any{row(tc.want), row(tc.want)}},
			} {
				t.Run(bad.name, func(t *testing.T) {
					if err := checkOctetPoint(d, Rows{Columns: columns, Values: bad.values}); err == nil {
						t.Fatal("malformed point plan admitted")
					}
				})
			}
		})
	}
}
