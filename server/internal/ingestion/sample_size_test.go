package ingestion

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

func TestFileListScanKeepsFullSizeSampleNamedFiles(t *testing.T) {
	f := newLoopFixture(t, "movie")
	files := map[string]int64{
		"Avatar (2009)-sample.mkv":         naming.SampleLimitBytes - 1,
		"Pulp Fiction (1994)-sample.mkv":   naming.SampleLimitBytes,
		"Samples/Blade Runner (1982).mkv":  naming.SampleLimitBytes + 1,
		"Samples/Batman Begins (2005).mkv": naming.SampleLimitBytes - 1,
		"messy_file.mkv":                   0,
	}
	for name, size := range files {
		f.write(name, "")
		file, err := os.OpenFile(filepath.Join(f.root, name), os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		err = file.Truncate(size)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	f.finish(f.queue())
	for name, size := range files {
		want := 1
		if name != "messy_file.mkv" && size < naming.SampleLimitBytes {
			want = 0
		}
		if got := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path=? AND state='available'`, name); got != want {
			t.Errorf("%s size=%d inventory=%d want=%d", name, size, got, want)
		}
	}
	if got := f.count(`SELECT count(*) FROM catalog_asset_links`); got != 3 {
		t.Fatalf("full-size samples or forgiving messy fallback not published: %d", got)
	}
}
