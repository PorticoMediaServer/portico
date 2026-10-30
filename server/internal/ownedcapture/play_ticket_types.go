package ownedcapture

import (
	"context"
	"database/sql"
)

// PlaybackCaptureSource is a source-issued preflight value, never permission or
// an acquisition. It intentionally has the same scalar facts as the private
// presentation source, without importing the presentation store into custody.
type PlaybackCaptureSource struct {
	LibraryID, LibrarySourceID, AssetID, InventoryObjectID, InventoryRevision, RootIdentity, Incarnation, RemoteSourceID, EffectiveConfiguration, ReadPolicyWitness, ReadAuthorityWitness string
	LibraryGeneration, RawConfigurationGeneration, ProfileRevision, Size, ModifiedNS                                                                                                      int64
}

// PlaybackCaptureResolver is optional. Its method is DB-only and must resolve
// the original Request's one physical source, current family/device/content and
// full read policy. It may not reserve quota, start transport or acquire bytes.
// Capture admission repeats those ordinary source checks independently.
type PlaybackCaptureResolver interface {
	ResolvePlaybackCaptureTx(context.Context, *sql.Tx, string, Request) (PlaybackCaptureSource, error)
}
