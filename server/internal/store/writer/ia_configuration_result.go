package writer

import (
	"crypto/sha256"
	"strings"
	"unicode"
	"unicode/utf8"
)

const iaConfigurationMaxRootBytes = 8192

// Original configuration namespace facts come from the actual retained owner,
// never from mutable source rows. The original request digest binds its full
// observed roots, source facts and actor. Decoding this result does not return
// those request facts.
type iaConfigurationReceiptOriginal struct {
	operationID          [16]byte
	requestDigest        RequestDigest
	purpose              CommandID
	libraryID            int64
	libraryKey, sourceID string
}

// These facts are read from immutable configuration receipts, never live rows.
// Remote request witnesses remain bound by original.requestDigest; they are not
// columns in the selected result relation.
type iaConfigurationSourceResult struct {
	ordinal                                    int64
	id, name, incarnation                      string
	generation                                 int64
	configuredRoot, resolvedRoot, rootIdentity string
	kind, classification                       string
	enabled                                    bool
	health                                     string
	followSymlinks                             bool
	intervalSeconds                            int64
	identityConfirmed                          bool
	nextScanAt, lastCompleteAt, lastProgressAt string
}
type iaConfigurationResult struct {
	original                    iaConfigurationReceiptOriginal
	acceptedRevision, appliedMS int64
	libraryName, libraryKind    string // absent (SQL NULL) for 101
	resultCount                 int64
	sources                     []iaConfigurationSourceResult
}
type iaConfigureLibraryResult struct{ facts iaConfigurationResult }
type iaConfigureSourceResult struct{ facts iaConfigurationResult }

func iaConfigurationResultKindName(kind CommandID) (string, error) {
	switch kind {
	case IAConfigureLibrary:
		return "configure_library", nil
	case IAConfigureSource:
		return "configure_source", nil
	}
	return "", invalid("ia_configuration_result_kind")
}
func iaConfigurationResultSourceTexts(s iaConfigurationSourceResult) [12]string {
	return [12]string{s.id, s.name, s.incarnation, s.configuredRoot, s.resolvedRoot, s.rootIdentity, s.kind, s.classification, s.health, s.nextScanAt, s.lastCompleteAt, s.lastProgressAt}
}

// Every prefix, presence flag, scalar and fixed blob is included. Neither this
// wire charge nor the native logical charge describes SQLite pages or Go heap.
const iaConfigurationResultFixedBytes = len(canonicalResultCodec) + 2 + 3*8 + len("configuration") + len("applied") + 72 + 48 + 2*8 + 2*8 + 1 + 2*8 + 1 + 2*8 + 2*8
const iaConfigurationSourceResultFixedBytes = 12*8 + 3*8 + 3

func preflightIAConfigurationResult(kind CommandID, id CommandIdentity, request RequestDigest, v iaConfigurationResult) error {
	name, err := iaConfigurationResultKindName(kind)
	if err != nil {
		return err
	}
	if len(v.sources) == 0 || len(v.sources) > 64 || v.resultCount != int64(len(v.sources)) {
		return invalid("ia_configuration_result_count")
	}
	// Bound the COMPLETE prospective wire before UTF-8/control scans or hashing.
	remaining := maxCanonicalResultBytes - iaConfigurationResultFixedBytes - len(name) - len(v.sources)*iaConfigurationSourceResultFixedBytes
	charge := func(s string) bool {
		if len(s) > remaining {
			return false
		}
		remaining -= len(s)
		return true
	}
	for _, s := range []string{v.original.libraryKey, v.original.sourceID, v.libraryName, v.libraryKind} {
		if !charge(s) {
			return invalid("ia_configuration_result_bytes")
		}
	}
	for _, s := range v.sources {
		for _, text := range iaConfigurationResultSourceTexts(s) {
			if !charge(text) {
				return invalid("ia_configuration_result_bytes")
			}
		}
	}
	o := v.original
	if id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) || id.Sequence != 1 || o.operationID != id.OperationID || o.requestDigest != request || o.purpose != kind || o.libraryID <= 0 || o.libraryKey == "" || v.acceptedRevision <= 0 || v.appliedMS < 0 {
		return invalid("ia_configuration_result_original")
	}
	if kind == IAConfigureLibrary {
		if o.sourceID != "" || v.libraryName == "" || len(v.libraryName) > 100 || strings.TrimSpace(v.libraryName) != v.libraryName || strings.IndexFunc(v.libraryName, unicode.IsControl) >= 0 {
			return invalid("ia_configuration_result_library")
		}
		switch v.libraryKind {
		case "movie", "tv", "anime", "music", "audiobook", "book", "comic":
		default:
			return invalid("ia_configuration_result_library_kind")
		}
	} else if o.sourceID == "" || v.libraryName != "" || v.libraryKind != "" || len(v.sources) != 1 || v.sources[0].id != o.sourceID {
		return invalid("ia_configuration_result_source_variant")
	}
	validText := func(s string) bool { return utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 }
	for _, s := range []string{o.libraryKey, o.sourceID, v.libraryName, v.libraryKind} {
		if !validText(s) {
			return invalid("ia_configuration_result_text")
		}
	}
	seen := make(map[string]bool, len(v.sources))
	for i, s := range v.sources {
		if s.ordinal != int64(i) || seen[s.id] || s.id == "" || s.incarnation == "" || s.generation <= 0 || s.configuredRoot == "" || s.resolvedRoot == "" || s.rootIdentity == "" || s.health == "" || (s.kind != "local" && s.kind != "remote") || (s.classification != "local" && s.classification != "network") || s.intervalSeconds < 0 || s.intervalSeconds > 0 && s.intervalSeconds < 900 {
			return invalid("ia_configuration_result_source")
		}
		seen[s.id] = true
		for _, text := range iaConfigurationResultSourceTexts(s) {
			if !validText(text) {
				return invalid("ia_configuration_result_text")
			}
		}
		if len(s.name) > 100 || len(s.configuredRoot) > iaConfigurationMaxRootBytes || len(s.resolvedRoot) > iaConfigurationMaxRootBytes || strings.IndexFunc(s.name, unicode.IsControl) >= 0 || strings.IndexFunc(s.configuredRoot, unicode.IsControl) >= 0 || strings.IndexFunc(s.resolvedRoot, unicode.IsControl) >= 0 {
			return invalid("ia_configuration_result_root")
		}
	}
	return nil
}

// Caller preflights before writing, as in the closed canonical dispatcher.
func writeIAConfigurationResult(e *requestEncoder, kind CommandID, id CommandIdentity, request RequestDigest, v iaConfigurationResult) error {
	name, err := iaConfigurationResultKindName(kind)
	if err != nil {
		return err
	}
	o := v.original
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(name)
	e.text("configuration")
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.bytes(o.operationID[:])
	e.bytes(o.requestDigest[:])
	e.number(uint64(o.purpose))
	e.number(uint64(o.libraryID))
	e.text(o.libraryKey)
	e.flag(kind == IAConfigureSource)
	e.text(o.sourceID)
	e.number(uint64(v.acceptedRevision))
	e.number(uint64(v.appliedMS))
	e.flag(kind == IAConfigureLibrary)
	e.text(v.libraryName)
	e.text(v.libraryKind)
	e.number(uint64(v.resultCount))
	e.number(uint64(len(v.sources)))
	for _, s := range v.sources {
		e.number(uint64(s.ordinal))
		e.text(s.id)
		e.text(s.name)
		e.text(s.incarnation)
		e.number(uint64(s.generation))
		e.text(s.configuredRoot)
		e.text(s.resolvedRoot)
		e.text(s.rootIdentity)
		e.text(s.kind)
		e.text(s.classification)
		e.flag(s.enabled)
		e.text(s.health)
		e.flag(s.followSymlinks)
		e.number(uint64(s.intervalSeconds))
		e.flag(s.identityConfirmed)
		e.text(s.nextScanAt)
		e.text(s.lastCompleteAt)
		e.text(s.lastProgressAt)
	}
	return e.err
}

// History decoder uses this only after authority/original-owner disclosure and
// full cohort fetch. A partially reclaimed receipt cannot satisfy count/digest.
func checkIAConfigurationResultDigest(kind CommandID, id CommandIdentity, request RequestDigest, v iaConfigurationResult, want ResultDigest) error {
	if err := preflightIAConfigurationResult(kind, id, request, v); err != nil {
		return err
	}
	e := requestEncoder{h: sha256.New()}
	if err := writeIAConfigurationResult(&e, kind, id, request, v); err != nil {
		return err
	}
	var got ResultDigest
	copy(got[:], e.h.Sum(nil))
	if got != want {
		return ErrConflict
	}
	return nil
}

func iaConfigurationHeaderRetainedBytes(kind CommandID, v iaConfigurationResult) (int64, error) {
	name, err := iaConfigurationResultKindName(kind)
	if err != nil {
		return 0, err
	}
	n := int64(160 + len(name))
	for _, s := range []string{v.libraryName, v.libraryKind} {
		if int64(len(s)) > int64(maxCanonicalResultBytes)-n {
			return 0, invalid("ia_configuration_retained_bytes")
		}
		n += int64(len(s))
	}
	return n, nil
}
func iaConfigurationSourceRetainedBytes(kind CommandID, s iaConfigurationSourceResult) (int64, error) {
	name, err := iaConfigurationResultKindName(kind)
	if err != nil {
		return 0, err
	}
	n := int64(96 + len(name))
	for _, text := range iaConfigurationResultSourceTexts(s) {
		if int64(len(text)) > int64(maxCanonicalResultBytes)-n {
			return 0, invalid("ia_configuration_retained_bytes")
		}
		n += int64(len(text))
	}
	return n, nil
}
