// Package searchpolicy owns the compiled identity of prepared search content.
// It has no database handles, preparation dependency or startup failure policy.
package searchpolicy

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	RecipeV1 = "A-CJK-uni-bi-v1"

	SQLiteVersion  = "3.53.4"
	SQLiteSourceID = "2026-07-24 19:02:57 bf7c7f30031888f4e796e429ab3978879485813aaca6f641c7b33e4e09459bcc"
	SQLiteModule   = "modernc.org/sqlite@v1.58.0"
	TextModule     = "golang.org/x/text@v0.42.0"
	TextModuleSum  = "h1:JbOZXgfeCPU9gacVtYliJqOhD+zhrEqK4LfdpmlUZqI="

	// EngineVersion is the exact search_generations.engine_version encoding.
	// SourceID is preserved verbatim; neither a short hash nor a research label
	// attests a different native engine.
	EngineVersion = "sqlite/" + SQLiteVersion + ";" + SQLiteModule + ";source_id/" + SQLiteSourceID

	ProfileAlgorithm = "portico-search-profile-sha256-v1"
	// These pins are checked against the production source closure and native
	// declarations. A reviewed change to either requires a new profile, even
	// when the public recipe name stays the same.
	PreparationSHA256  = "58564e06768384fda0fd093fabeeae2b6105eeea81fb50d4f6d95aeba7c1e43f"
	NativeFormatSHA256 = "6a5236a7fee8f8777c11de4e808945ea3cfd9a87cd9b8357284d134c1b5dee30"
)

// Profile is the immutable tuple stored on a search generation. A caller must
// compare it with the actual physical engine before initializing or using a
// bank. Compiled alone is not evidence that a connection uses that engine.
type Profile struct {
	RecipeVersion      string
	EngineVersion      string
	UnicodeFingerprint string
}

// Engine is read from the actual physical connection, not from the bank row.
type Engine struct {
	Version  string
	SourceID string
}

type State uint8

const (
	Unavailable State = iota
	Current
	Stale
)

type Reason uint8

const (
	StoredMissing Reason = iota
	StoredMalformed
	NativeEngineMismatch
	ProfileMismatch
	ProfileMatches
)

// Compatibility is evidence for routing, never a writer grant or permission.
// Stale and Unavailable must not abort server startup. Keep accepting raw
// source invalidations, degrade incompatible search, and rebuild under the
// governor. EngineAttested describes only the actual connection; it does not attest an
// old bank format or authorize reclaim or a head flip.
type Compatibility struct {
	State          State
	Reason         Reason
	EngineAttested bool
}

// Compiled uses the Unicode tables actually selected by this Go build. Go
// versions can select different x/text tables with an unchanged module pin.
// Do not substitute runtime.Version, a hard-coded Unicode version or an old
// experiment's fingerprint for these compiled versions.
func Compiled() Profile {
	return Profile{
		RecipeVersion:      RecipeV1,
		EngineVersion:      EngineVersion,
		UnicodeFingerprint: fingerprint(unicode.Version, norm.Version),
	}
}

// Compare is read-only and intentionally returns no fatal opening error.
// Missing/corrupt initialization is distinct from an ordinary profile change;
// both prohibit presenting incompatible normalized hits as current. Raw-title
// fallback needs its own independent, bounded proof.
func Compare(stored Profile, actual Engine) Compatibility {
	native := actual.Version == SQLiteVersion && actual.SourceID == SQLiteSourceID
	result := Compatibility{EngineAttested: native}
	if !native {
		result.Reason = NativeEngineMismatch
		return result
	}
	if stored == (Profile{}) {
		result.Reason = StoredMissing
		return result
	}
	if !StoredWellFormed(stored) {
		result.Reason = StoredMalformed
		return result
	}
	if stored == Compiled() {
		result.State, result.Reason = Current, ProfileMatches
	} else {
		result.State, result.Reason = Stale, ProfileMismatch
	}
	return result
}

// StoredWellFormed validates only the stored tuple's encoding and bounds.
// It does not compare Compiled, consult an engine or attest compatibility.
// RAW source admission may use it while preserving a well-formed stale profile;
// normalization, native publication and serving require separate eligibility.
func StoredWellFormed(p Profile) bool {
	if p.RecipeVersion == "" || len(p.RecipeVersion) > 128 || p.EngineVersion == "" || len(p.EngineVersion) > 512 {
		return false
	}
	const prefix = ProfileAlgorithm + ":"
	if !strings.HasPrefix(p.UnicodeFingerprint, prefix) || len(p.UnicodeFingerprint) != len(prefix)+64 {
		return false
	}
	for _, c := range p.UnicodeFingerprint[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func fingerprint(goUnicode, textUnicode string) string {
	h := sha256.New()
	h.Write([]byte(ProfileAlgorithm + "\x00"))
	for _, value := range []string{
		RecipeV1, EngineVersion, goUnicode, textUnicode, TextModule, TextModuleSum,
		PreparationSHA256, NativeFormatSHA256,
	} {
		writeString(h, value)
	}
	return ProfileAlgorithm + ":" + hex.EncodeToString(h.Sum(nil))
}

func writeString(h hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	h.Write(size[:])
	h.Write([]byte(value))
}
