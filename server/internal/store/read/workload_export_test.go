package read

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// ForegroundWorkloadFixture exposes the existing actual-Capture/LIVE fixture to
// this package's external adapter tests. It adds no opener or production API.
type ForegroundWorkloadFixture struct {
	Governor   governor.Governor
	Authority  *governor.Authority
	Snapshots  *governor.Snapshots
	Browse     *BrowseReader
	Foreground *ForegroundReader
	Input      BrowseInput
}

func NewForegroundWorkloadFixture(t *testing.T) ForegroundWorkloadFixture {
	f, r := newLiveFixture(t)
	return ForegroundWorkloadFixture{f.governor, f.authority, f.snapshots, f.reader, r, f.input}
}

func ForegroundWorkloadRequests(id string) (governor.ForegroundRequest, governor.Request, governor.Request) {
	return liveRequests(id)
}
