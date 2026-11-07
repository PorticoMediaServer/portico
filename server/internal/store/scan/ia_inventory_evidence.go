package scan

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// MediaEvidence is local filename evidence for one exact observed file. It
// retains alternatives and parser diagnostics, not accepted identities or
// technical facts. In particular, an episode list is never reduced to its
// first member and a version label is never substituted for a split part.
type MediaEvidence struct {
	file    FileObservation
	kind    string
	reading naming.Reading
	book    naming.AudiobookFolder
	bytes   int64
}

func (e MediaEvidence) File() FileObservation { return e.file }
func (e MediaEvidence) Kind() string          { return e.kind }
func (e MediaEvidence) Bytes() int64          { return e.bytes }

// Naming returns an independently owned value, including nested episode lists.
// Callers must reserve working memory for this copy as well as the retained
// evidence. Strings are immutable; no mutable backing slice escapes.
func (e MediaEvidence) Naming() naming.Reading { return iaCloneReading(e.reading) }

// AudiobookFolder preserves structured provisional folder evidence separately
// from generic track/album naming. File tags and sidecars can supersede every
// field. The bool is false for non-audiobooks and paths without a book folder;
// an empty field is never evidence to clear accepted metadata.
func (e MediaEvidence) AudiobookFolder() (naming.AudiobookFolder, bool) {
	return iaCloneAudiobookFolder(e.book), e.book.Title != ""
}

// PrepareMediaEvidence preserves the existing naming algorithm for non-movie
// domains. Movie/N1 preparation remains SC's ProjectOrdinaryFilename. Input
// byte lengths and native representability are checked before naming or copy.
// byteBudget bounds retained string payloads, including derived alternatives;
// it is not a Go heap/RSS bound. The driver owns admission before this call.
// No source lookup, provider request, identity allocation, cursor or acceptance
// occurs here. Basic/provider/owner evidence can later supersede these names.
func PrepareMediaEvidence(kind string, file FileObservation, byteBudget int64) (MediaEvidence, error) {
	if !iaMediaKind(kind) || !file.Witness.Source.valid() || file.SourceKey == "" {
		return MediaEvidence{}, fmt.Errorf("%w: media evidence scope", ErrInvalid)
	}
	remaining := byteBudget
	charge := func(value string) bool {
		if int64(len(value)) > remaining {
			return false
		}
		remaining -= int64(len(value))
		return true
	}
	if byteBudget <= 0 {
		return MediaEvidence{}, ErrCapacity
	}
	w := file.Witness
	for _, value := range []string{kind, file.SourceKey, w.Source.SourceID, w.Source.Incarnation, w.Source.Revision, w.RelativePath, w.ObjectIdentity, w.Revision} {
		if !charge(value) {
			return MediaEvidence{}, ErrCapacity
		}
	}
	// Structural/NUL invalidity is distinct from a native filename refusal.
	if strings.IndexByte(w.RelativePath, 0) >= 0 {
		return MediaEvidence{}, fmt.Errorf("%w: media file witness", ErrInvalid)
	}
	if !utf8.ValidString(w.RelativePath) {
		return MediaEvidence{}, &writer.PolicyError{Field: "ia_relative_path", Cause: writer.ErrFilenameUnrepresentable}
	}
	if !w.valid() {
		return MediaEvidence{}, fmt.Errorf("%w: media file witness", ErrInvalid)
	}
	for _, value := range []string{file.SourceKey, w.Source.SourceID, w.Source.Incarnation, w.Source.Revision, w.ObjectIdentity, w.Revision} {
		if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
			return MediaEvidence{}, fmt.Errorf("%w: media witness text", ErrInvalid)
		}
	}
	reading := naming.Read(w.RelativePath, kind)
	if !iaChargeReading(reading, charge) {
		return MediaEvidence{}, ErrCapacity
	}
	var book naming.AudiobookFolder
	if kind == "audiobook" {
		book = naming.ReadAudiobookFolder(w.RelativePath)
		for _, value := range []string{book.Title, book.Author, book.Series, book.Sequence, book.Year, book.ASIN} {
			if !charge(value) {
				return MediaEvidence{}, ErrCapacity
			}
		}
		for _, narrator := range book.Narrators {
			if !charge(narrator) {
				return MediaEvidence{}, ErrCapacity
			}
		}
	}
	w.Source.SourceID = strings.Clone(w.Source.SourceID)
	w.Source.Incarnation = strings.Clone(w.Source.Incarnation)
	w.Source.Revision = strings.Clone(w.Source.Revision)
	w.RelativePath = strings.Clone(w.RelativePath)
	w.ObjectIdentity = strings.Clone(w.ObjectIdentity)
	w.Revision = strings.Clone(w.Revision)
	return MediaEvidence{
		file: FileObservation{Witness: w, SourceKey: strings.Clone(file.SourceKey)},
		kind: strings.Clone(kind), reading: iaCloneReading(reading), book: iaCloneAudiobookFolder(book), bytes: byteBudget - remaining,
	}, nil
}

func iaCloneAudiobookFolder(book naming.AudiobookFolder) naming.AudiobookFolder {
	book.Title = strings.Clone(book.Title)
	book.Author = strings.Clone(book.Author)
	book.Series = strings.Clone(book.Series)
	book.Sequence = strings.Clone(book.Sequence)
	book.Year = strings.Clone(book.Year)
	book.ASIN = strings.Clone(book.ASIN)
	book.Narrators = slices.Clone(book.Narrators)
	for i := range book.Narrators {
		book.Narrators[i] = strings.Clone(book.Narrators[i])
	}
	return book
}

func iaMediaKind(kind string) bool {
	switch kind {
	case "tv", "anime", "music", "audiobook", "book", "comic", "magazine", "newspaper":
		return true
	default:
		return false
	}
}

func iaChargeEvidence[T any](values []naming.Evidence[T], charge func(string) bool, value func(T) bool) bool {
	for _, e := range values {
		if !charge(string(e.Source)) || !charge(e.Path) || !value(e.Value) {
			return false
		}
	}
	return true
}

func iaChargeReading(r naming.Reading, charge func(string) bool) bool {
	for _, list := range [][]naming.Evidence[string]{r.Titles, r.AirDates, r.Editions, r.Versions, r.Artists, r.Albums} {
		if !iaChargeEvidence(list, charge, charge) {
			return false
		}
	}
	for _, list := range [][]naming.Evidence[int]{r.Years, r.Seasons, r.AbsoluteNumbers, r.Parts, r.TrackNumbers, r.DiscNumbers} {
		if !iaChargeEvidence(list, charge, func(int) bool { return true }) {
			return false
		}
	}
	if !iaChargeEvidence(r.Episodes, charge, func([]int) bool { return true }) ||
		!iaChargeEvidence(r.ProviderIDs, charge, func(v naming.ProviderID) bool { return charge(v.Provider) && charge(v.ID) }) ||
		!iaChargeEvidence(r.Extras, charge, func(v naming.Extra) bool { return charge(v.Kind) && charge(v.ParentPath) }) ||
		!iaChargeEvidence(r.Discs, charge, func(v naming.Disc) bool { return charge(v.Format) && charge(v.ParentPath) }) {
		return false
	}
	for _, diagnostic := range r.Diagnostics {
		if !charge(diagnostic) {
			return false
		}
	}
	return true
}

func iaCloneEvidence[T any](values []naming.Evidence[T], clone func(T) T) []naming.Evidence[T] {
	result := slices.Clone(values)
	for i := range result {
		result[i].Path = strings.Clone(result[i].Path)
		result[i].Value = clone(result[i].Value)
	}
	return result
}

func iaCloneReading(r naming.Reading) naming.Reading {
	identity := func(v int) int { return v }
	r.Titles = iaCloneEvidence(r.Titles, strings.Clone)
	r.Years = iaCloneEvidence(r.Years, identity)
	r.Seasons = iaCloneEvidence(r.Seasons, identity)
	r.Episodes = iaCloneEvidence(r.Episodes, slices.Clone[[]int])
	r.AbsoluteNumbers = iaCloneEvidence(r.AbsoluteNumbers, identity)
	r.AirDates = iaCloneEvidence(r.AirDates, strings.Clone)
	r.Parts = iaCloneEvidence(r.Parts, identity)
	r.Editions = iaCloneEvidence(r.Editions, strings.Clone)
	r.Versions = iaCloneEvidence(r.Versions, strings.Clone)
	r.ProviderIDs = iaCloneEvidence(r.ProviderIDs, func(v naming.ProviderID) naming.ProviderID {
		return naming.ProviderID{Provider: strings.Clone(v.Provider), ID: strings.Clone(v.ID)}
	})
	r.Extras = iaCloneEvidence(r.Extras, func(v naming.Extra) naming.Extra {
		return naming.Extra{Kind: strings.Clone(v.Kind), ParentPath: strings.Clone(v.ParentPath)}
	})
	r.Discs = iaCloneEvidence(r.Discs, func(v naming.Disc) naming.Disc {
		return naming.Disc{Format: strings.Clone(v.Format), ParentPath: strings.Clone(v.ParentPath)}
	})
	r.TrackNumbers = iaCloneEvidence(r.TrackNumbers, identity)
	r.DiscNumbers = iaCloneEvidence(r.DiscNumbers, identity)
	r.Artists = iaCloneEvidence(r.Artists, strings.Clone)
	r.Albums = iaCloneEvidence(r.Albums, strings.Clone)
	r.Diagnostics = slices.Clone(r.Diagnostics)
	for i := range r.Diagnostics {
		r.Diagnostics[i] = strings.Clone(r.Diagnostics[i])
	}
	return r
}
