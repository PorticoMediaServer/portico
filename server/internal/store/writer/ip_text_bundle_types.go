package writer

import (
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"math"
	"strings"
)

// Finite descriptive field bundle, one owner/revision/history/result. Target
// namespaces are explicit; a person never supplies a fake entity or borrows
// artwork_incarnation as metadata authority. Current field snapshots are original
// immutable inputs. Actual admission/disclosure and birth stay with W's driver.
type ipTextBundleSource struct {
	OwnerID, EntityID, PersonID, LibraryID           int64
	Kind, Actor                                      string
	OwnerRevision, SubjectRevision, MetadataRevision int64
	MetadataIncarnation                              [16]byte
	Projection                                       ipTextProjection
}
type ipTextBundleField struct {
	Field  fieldmeta.TextField
	Before fieldmeta.TextState
	Edit   fieldmeta.TextEdit
}
type ipTextBundleRequest struct {
	Identity CommandIdentity
	Kind     CommandID
	Source   ipTextBundleSource
	Fields   []ipTextBundleField
}
type ipTextBundleCommand struct {
	request            ipTextBundleRequest
	digest             RequestDigest
	after              []fieldmeta.TextChange
	projection         ipTextProjection
	changed, effective bool
}
type ipTextBundleResult struct {
	OwnerID, EntityID, PersonID, Revision, SubjectRevision, AppliedMS int64
	History                                                           EvidenceInteger
	Fields                                                            []ipTextBundleFieldResult
}
type ipTextBundleFieldResult struct {
	Field  fieldmeta.TextField
	Change fieldmeta.TextChange
}
type ipTextBundleSavedResult struct {
	Kind  CommandID
	Value ipTextBundleResult
}
type ipTextBundleNative struct {
	decision receiptDecision
	value    ipTextBundleResult
	digest   ResultDigest
}

func ipTextBundleTarget(s ipTextBundleSource) int64 {
	if s.EntityID > 0 {
		return s.EntityID
	}
	return s.PersonID
}
func newIPTextBundleCommand(q ipTextBundleRequest) (ipTextBundleCommand, error) {
	s := q.Source
	switch s.Kind {
	case "item", "show", "season", "album", "artist", "book", "person", "collection":
	default:
		return ipTextBundleCommand{}, ErrInvalid
	}
	if !ipPolicyValidIdentity(q.Identity) || s.OwnerID <= 0 || (s.EntityID > 0) == (s.PersonID > 0) || s.EntityID < 0 || s.PersonID < 0 || s.OwnerRevision <= 0 || s.OwnerRevision == math.MaxInt64 || s.SubjectRevision <= 0 || s.SubjectRevision == math.MaxInt64 || len(q.Fields) == 0 || len(q.Fields) > 11 || !ipPolicyValidText(s.Kind, 64) || !ipPolicyValidText(s.Actor, 4096) || (q.Kind != IPFieldEdit && q.Kind != IPFieldRestore && q.Kind != IPFieldLock) {
		return ipTextBundleCommand{}, ErrInvalid
	}
	if s.PersonID > 0 {
		if s.Kind != "person" || s.LibraryID != 0 || s.MetadataRevision != 0 || s.MetadataIncarnation != [16]byte{} || s.Projection != (ipTextProjection{}) {
			return ipTextBundleCommand{}, ErrInvalid
		}
	} else {
		if s.Kind == "person" || s.LibraryID <= 0 || s.MetadataRevision <= 0 || s.MetadataRevision == math.MaxInt64 || s.MetadataIncarnation == [16]byte{} {
			return ipTextBundleCommand{}, ErrInvalid
		}
	}
	// Complete-root byte preflight/hash precedes any field snapshot cloning.
	digest, err := canonicalRequestDigest(q.Kind, 1, q.Identity, q)
	if err != nil {
		return ipTextBundleCommand{}, err
	}
	c := ipTextBundleCommand{digest: digest}
	last := ""
	hasKeys := false
	p := s.Projection
	for _, f := range q.Fields {
		if string(f.Field) <= last {
			return c, ErrInvalid
		}
		last = string(f.Field)
		if q.Kind == IPFieldLock && f.Edit.Action != fieldmeta.LockText && f.Edit.Action != fieldmeta.UnlockText || q.Kind == IPFieldRestore && f.Edit.Action != fieldmeta.RestoreAutomatic {
			return c, ErrInvalid
		}
		change, err := fieldmeta.PrepareTextEdit(f.Field, f.Before, f.Edit)
		if err != nil {
			return c, ErrInvalid
		}
		if s.PersonID > 0 && f.Field != fieldmeta.Description {
			return c, ErrInvalid
		}
		if change.EffectiveChanged && s.EntityID > 0 {
			switch f.Field {
			case fieldmeta.Title, fieldmeta.SortTitle, fieldmeta.MetadataLanguage:
				hasKeys = true
				before := s.Projection.Title
				if f.Field == fieldmeta.SortTitle {
					before = s.Projection.SortTitle
				}
				if f.Field == fieldmeta.MetadataLanguage {
					before = s.Projection.Language
				}
				if before != f.Before.Effective.Value {
					return c, ErrInvalid
				}
				switch f.Field {
				case fieldmeta.Title:
					p.Title = change.State.Effective.Value
				case fieldmeta.SortTitle:
					p.SortTitle = change.State.Effective.Value
				case fieldmeta.MetadataLanguage:
					p.Language = change.State.Effective.Value
				}
			case fieldmeta.Description, fieldmeta.OriginalTitle, fieldmeta.Tagline, fieldmeta.Studio, fieldmeta.Network, fieldmeta.Country:
			default:
				return c, ErrInvalid // identity/admission variants still required
			}
		}
		c.after = append(c.after, change)
		c.changed = c.changed || change.Changed
		c.effective = c.effective || change.EffectiveChanged
	}
	if hasKeys {
		old, err := ProjectFilenameMetadata(s.Projection.Title, s.Projection.SortTitle, s.Projection.Language)
		if err != nil || old.SortKey != s.Projection.SortKey || old.SortHead != s.Projection.SortHead || old.SortKeyKeepArticles != s.Projection.SortKeyKeepArticles || old.SortHeadKeepArticles != s.Projection.SortHeadKeepArticles {
			return c, ErrInvalid
		}
		next, err := ProjectFilenameMetadata(p.Title, p.SortTitle, p.Language)
		if err != nil {
			return c, err
		}
		c.projection = ipTextProjection{next.Title, next.SortTitle, next.MetadataLanguage, next.SortKey, next.SortHead, next.SortKeyKeepArticles, next.SortHeadKeepArticles}
	} else if s.Projection != (ipTextProjection{}) {
		return c, ErrInvalid
	}
	s.Kind = strings.Clone(s.Kind)
	s.Actor = strings.Clone(s.Actor)
	s.Projection = ipTextCloneProjection(s.Projection)
	q.Source = s
	q.Fields = append([]ipTextBundleField(nil), q.Fields...)
	for j := range q.Fields {
		f := &q.Fields[j]
		f.Field = fieldmeta.TextField(strings.Clone(string(f.Field)))
		f.Before.Effective.Value = strings.Clone(f.Before.Effective.Value)
		f.Before.Automatic.Value = strings.Clone(f.Before.Automatic.Value)
		f.Edit.Value = strings.Clone(f.Edit.Value)
	}
	c.request = q
	return c, nil
}
func preflightIPTextBundleResult(v ipTextBundleSavedResult) error {
	r := v.Value
	if (v.Kind != IPFieldEdit && v.Kind != IPFieldLock && v.Kind != IPFieldRestore) || r.OwnerID <= 0 || (r.EntityID > 0) == (r.PersonID > 0) || r.EntityID < 0 || r.PersonID < 0 || r.Revision <= 0 || r.SubjectRevision <= 0 || r.AppliedMS < 0 || len(r.Fields) == 0 || len(r.Fields) > 11 {
		return ErrInvalid
	}
	bytes := maxCanonicalResultBytes - 512
	last := ""
	changed := false
	for _, f := range r.Fields {
		if string(f.Field) <= last {
			return ErrInvalid
		}
		last = string(f.Field)
		c := f.Change
		s := c.State
		if c.EffectiveChanged && !c.Changed || s.Locked && !s.Effective.Present || !s.Effective.Present && s.Effective.Value != "" || !s.Automatic.Present && s.Automatic.Value != "" {
			return ErrInvalid
		}
		for _, s := range []string{string(f.Field), s.Effective.Value, s.Automatic.Value} {
			if len(s) > bytes {
				return ErrInvalid
			}
			bytes -= len(s)
			if !sealNativeText(s) {
				return ErrInvalid
			}
		}
		changed = changed || c.Changed
	}
	if r.History.Present != changed || !r.History.Present && r.History.Value != 0 || r.History.Present && r.History.Value <= 0 {
		return ErrInvalid
	}
	return nil
}

// Bundle wire: identity prefix purpose ip_field_bundle, uint16 command kind,
// six U64 owner/entity/person/revision/subjectRevision/appliedMS, nullable history,
// U64 count, then sorted LPfield/value/automatic and five bool presence/presence/
// lock/changed/effectiveChanged. It is a finite result schema in W's sole codec.
func writeIPTextBundleResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipTextBundleSavedResult) error {
	writeIPResultIdentity(e, "ip_field_bundle", i, q)
	e.bytes([]byte{byte(v.Kind >> 8), byte(v.Kind)})
	r := v.Value
	for _, n := range []int64{r.OwnerID, r.EntityID, r.PersonID, r.Revision, r.SubjectRevision, r.AppliedMS} {
		e.number(uint64(n))
	}
	e.flag(r.History.Present)
	if r.History.Present {
		e.number(uint64(r.History.Value))
	}
	e.number(uint64(len(r.Fields)))
	for _, f := range r.Fields {
		e.text(string(f.Field))
		e.text(f.Change.State.Effective.Value)
		e.text(f.Change.State.Automatic.Value)
		for _, b := range []bool{f.Change.State.Effective.Present, f.Change.State.Automatic.Present, f.Change.State.Locked, f.Change.Changed, f.Change.EffectiveChanged} {
			e.flag(b)
		}
	}
	return e.err
}

// Person biography uses its native person-field key. Description is this
// bundle's typed descriptive API field; entity description remains overview.
// Provider automatic publication and owner edits must share the biography lock.
func ipBundleOwnerField(s ipTextBundleSource, f fieldmeta.TextField) string {
	if s.PersonID > 0 && f == fieldmeta.Description {
		return "biography"
	}
	return string(f)
}
