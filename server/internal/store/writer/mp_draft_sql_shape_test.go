package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"testing"
)

// The compiler cannot check variadic native binding widths. This tiny static
// check catches malformed closed statement declarations before runner execution;
// it does not certify types, plans, authority, rows, FKs or transaction behavior.
func TestMPDraftStatementParameterShapes(t *testing.T) {
	for _, catalog := range [][]stmt.Definition{mpStartPrepDefinitions(), mpTranscodeDefinitions(), mpPrepReceiptDefinitions(), mpExpiryDefinitions(), mpVersionWorkerPayloadDefinitions(), mpVersionCleanupDefinitions(), mpReceiptAckDefinitions(), mpReceiptReclaimDefinitions(), mpQueueDefinitions(), mpQueueCleanupDefinitions(), mpQueueSaveDefinitions(), mpQueueResultDefinitions(), mpNextDefinitions(), mpNextResultDefinitions(), mpReadingDefinitions(), mpSessionDomainDefinitions(), mpSessionWorkerPayloadDefinitions(), mpPhysicalWorkerPayloadDefinitions(), mpSourcePinsDefinitions(), mpHLSDomainDefinitions()} {
		for _, d := range catalog {
			if got, want := len(d.Params), strings.Count(d.SQL, "?"); got != want {
				t.Errorf("%s: declared %d params for %d placeholders", d.ID, got, want)
			}
		}
	}
}
