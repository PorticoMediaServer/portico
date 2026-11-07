package read

// Pinned owner source: private239 4c974a33a1ba5bc3b7ca11376f67a2ffccb08a11,
// planning/v3/schema-v1/phase2/IP-READ-SOURCE-CONTRACT.md (draft ABI1).
// These are local classification semantics, NOT an accepted-source capability,
// a selected-source reader, or native nonmovie acceptance qualification.
//
// The future reader must prove kind6's exact musicbrainz/primary/release slot:
// selected with accepted publication/evidence pointers; accepted publication's
// same entity/provider/subject_kind/provider_id/evidence tuple and resulting
// selection revision equal to the current slot revision; sealed evidence; actual
// applied original operation and frozen publication generation; current source
// incarnation/identity, enabled library/provider policy with matching operation
// policy incarnation/revision/algorithm/refresh mode; current MusicBrainz opt-in
// and metadata_runtime_settings.online_lookups=1, with consent_revision matching
// lookup_epoch. Screen consent is NOT the MusicBrainz consent domain. Operation
// selection/metadata revisions are original write inputs, not current read heads.
// Canonical release provider_id may differ from requested_id after redirect and
// must never be replaced by evidence.release_group_id. Observation time remains
// the frozen acquisition time. Pending/latest evidence cannot be a fallback.
//
// An eligible source must also carry IP216/217's distinct complete release
// manifest proof (header plus complete secondary/media/track/credit children).
// FIRST's movie-only proof is insufficient. That native accepting proof is not
// adopted on this base; draft ABI1 alone cannot mint it. No query here pretends
// the source is accepted. Unknown primary/secondary text remains unchanged for
// detail; secondary ordinals are the original complete array, never a top-K.
const rbMBSecondaryVocabulary = "release_group_secondary_type"

type rbReleaseClass string

const (
	rbReleaseAlbum       rbReleaseClass = "album"
	rbReleaseCompilation rbReleaseClass = "compilation"
	rbReleaseEP          rbReleaseClass = "ep"
	rbReleaseSingle      rbReleaseClass = "single"
	rbReleaseAppearance  rbReleaseClass = "appearance"
)

type rbReleaseSecondary struct {
	Ordinal int64
	Value   string
}

// rbClassifyRelease is only a pure classifier after a future native source
// resolver has established acceptance and complete-array custody. It supplies
// no authority, source/relationship proof, group membership or section order.
// An artist-relative appearance comes from the actual relationship before type
// precedence. For a local album with no accepted source, use empty primary and
// no secondary values: album fallback, without inventing provider metadata.
func rbClassifyRelease(appearance bool, primary string, secondary []rbReleaseSecondary) rbReleaseClass {
	if appearance {
		return rbReleaseAppearance
	}
	for _, v := range secondary {
		if rbReleaseASCIIEqual(v.Value, "Compilation") {
			return rbReleaseCompilation
		}
	}
	if rbReleaseASCIIEqual(primary, "EP") {
		return rbReleaseEP
	}
	if rbReleaseASCIIEqual(primary, "Single") {
		return rbReleaseSingle
	}
	return rbReleaseAlbum
}
func rbReleaseASCIIEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
