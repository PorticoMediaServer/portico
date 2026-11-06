package workload

import "github.com/PorticoMediaServer/portico/server/internal/store/internal/progresslineage"

// Stable sustained-v1 diagnostic values. W-private TEST fixtures can import
// the cycle-free values directly without importing workload/its writer facade.
const MaxPendingCaptures = progresslineage.MaxPendingCaptures
const MaxDrainEdges = progresslineage.MaxDrainEdges
const MaxObservedProgressIDs = progresslineage.MaxObservedIDs

var ErrProgressLinkage = progresslineage.ErrProgressLinkage

type ProgressRange = progresslineage.ProgressRange
type ProgressOwner = progresslineage.ProgressOwner
type ProgressPhysical = progresslineage.ProgressPhysical
type ProgressCaptureFact = progresslineage.ProgressCaptureFact
type ProgressCaptureEdge = progresslineage.ProgressCaptureEdge
type ProgressDrainFact = progresslineage.ProgressDrainFact
type ProgressFact = progresslineage.ProgressFact
type ProgressLinkStatus = progresslineage.ProgressLinkStatus
type ProgressEdgeDisposition = progresslineage.ProgressEdgeDisposition
type ProgressLinks = progresslineage.ProgressLinks

func NewProgressLinks() *ProgressLinks { return progresslineage.NewProgressLinks() }
