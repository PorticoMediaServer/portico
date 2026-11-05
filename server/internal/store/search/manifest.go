package search

import "github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"

// Public preparation APIs retain the shared portable protocol's exact types.
// Writer imports searchmanifest directly and rederives hashes from native facts.
const ManifestAlgorithm = searchmanifest.Algorithm

var ErrManifest = searchmanifest.ErrManifest

type ManifestSubject = searchmanifest.Subject

const (
	ManifestEntity = searchmanifest.Entity
	ManifestPerson = searchmanifest.Person
)

type ManifestInput = searchmanifest.Input
type ManifestRow = searchmanifest.Row
type ManifestCheckpoint = searchmanifest.Checkpoint
type Manifest = searchmanifest.Manifest

func (f Fragment) ManifestRow(field uint16, ordinal int64, revision string) ManifestRow {
	return ManifestRow{Field: field, Ordinal: ordinal, Revision: revision,
		Prefix: f.prefix, Lexical: f.lexical, Normalized: f.normalized}
}

func RestoreManifest(algorithm string, seed, digest [32]byte, count, bytes int64) (Manifest, error) {
	return searchmanifest.Restore(algorithm, seed, digest, count, bytes)
}

func ManifestSeed(in ManifestInput, count, bytes int64) ([32]byte, error) {
	return searchmanifest.Seed(in, count, bytes)
}

func ManifestLeaf(row ManifestRow) ([32]byte, error) {
	return searchmanifest.Leaf(row)
}

func ManifestStep(previous, leaf [32]byte) [32]byte {
	return searchmanifest.Step(previous, leaf)
}

func AdvanceManifest(cp ManifestCheckpoint, row ManifestRow) (ManifestCheckpoint, error) {
	return searchmanifest.Advance(cp, row)
}

func PrepareManifest(in ManifestInput, rows []ManifestRow) (Manifest, error) {
	return searchmanifest.Prepare(in, rows)
}
