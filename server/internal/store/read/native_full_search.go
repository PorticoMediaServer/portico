package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// NativeFullSearchReader performs one public LIVE operation across catalogue
// kinds in one library. It owns no opener, publisher or admission issuer.
// Construct the supplied pool from NativeFullSearchDefinitions and an actual
// Reader Capture receipt. No runtime registration or default is installed.
type NativeFullSearchReader struct{ snapshots *governor.Snapshots }

func NewNativeFullSearchReader(pool *governor.Snapshots) (*NativeFullSearchReader, error) {
	if pool == nil {
		return nil, ErrInput
	}
	return &NativeFullSearchReader{snapshots: pool}, nil
}

// Read is assignable to FullSearchRead. View.Kind==0 selects all kinds;
// positive Kind narrows the result before ranking. Each entity hit includes
// Kind; People use Subject=ManifestPerson and Kind=0. The global page holds at
// most View.Limit (<=20) hits, not that many per kind. RS may group that page
// without issuing serial reads against an already used LIVE scope.
// One call owns population/candidate windows and the fresh publication fence;
// the caller joins output/encoding consumers before ConsumeForeground returns.
func (r *NativeFullSearchReader) Read(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in FullSearchInput, query search.Query) (FullSearchPage, *BrowseReadResult, error) {
	if r == nil || ctx == nil || query.Mode() != search.ReadFull || in.View.Kind < 0 || in.View.Kind > 99 {
		return FullSearchPage{}, nil, ErrInput
	}
	// Draft implementation is completed by the native execution change before
	// publication; absence of the executor cannot authorize a partial result.
	return FullSearchPage{}, nil, ErrUnqualified
}
