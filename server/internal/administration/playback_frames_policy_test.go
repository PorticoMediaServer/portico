package administration

import (
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"testing"
)

func TestPlaybackFramesMatrixAndDepthRetainExplicitOperation(t *testing.T) {
	found := false
	for _, op := range AnalysisOperations() {
		if op.ID == catalog.PlaybackFramesOperation {
			found = true
			if op.CostClass != "decode" || len(op.Requires) != 1 || op.Requires[0] != "probe" || op.ProducesArtifacts || op.Network {
				t.Fatal("wrong frame operation contract", op)
			}
		}
	}
	if !found || analysisRuntime[catalog.PlaybackFramesOperation] != catalog.PlaybackFramesOperation {
		t.Fatal("matrix/runtime drops frame operation")
	}
	complete, err := catalog.ValidateScanPolicy(catalog.ScanPolicy{Tier: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	if scanDepthFor(complete) != "complete" || !containsOperation(analysisOperations(complete), catalog.PlaybackFramesOperation) {
		t.Fatal("Complete loses frames", complete, analysisOperations(complete))
	}
	custom := catalog.ScanPolicy{Tier: "custom", Operations: []string{"probe", "local_metadata", "subtitles", catalog.PlaybackFramesOperation}}
	if scanDepthFor(custom) != "custom" {
		t.Fatal("frame-only Custom was mislabeled Basic", custom)
	}
}
