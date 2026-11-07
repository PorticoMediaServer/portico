package read

import (
	"errors"
	"math"
)

var errHomeBindingUnresolved = errors.New("whole playable binding unresolved")
var errHomeBindingBudget = errors.New("whole playable binding raw budget exhausted")

// Values only. The caller owns authority, snapshot custody and fresh publication
// comparison. PreparedSourceIncarnation is the asset's actual origin namespace;
// the schema has no separate prepared-source incarnation column.
type homeWholePlayableBinding struct {
	Library, Item, Asset, Kind                                                   int64
	ItemPublic, ItemMetadataIncarnation, ItemIncarnation                         [16]byte
	ItemRevision, ItemMetadataRevision, ItemSourceRevision, ItemIdentityRevision int64
	AssetIncarnation, LinkIncarnation, PreparedSourceIncarnation                 [16]byte
	AssetRevision, LinkRevision, PreparedSourceRevision                          int64
	DurationSeconds                                                              float64
	Object, ObjectRevision, Source, SourceIncarnation                            string
	SourceGeneration                                                             int64
	Root                                                                         homeBindingRoot
}
type homeBindingRoot struct {
	LibraryIncarnation                                                 [16]byte
	LibraryOriginRevision, DataRevision, MetadataRevision, AgeRevision int64
	// Exact SQL NULLIF comparison proves bridge/current library roots agree;
	// generation/incarnation witnesses bind the selected source's current root.
	RootsAgree bool
}
type homeBindingProgress struct {
	ItemRows, LinkRows, ObjectRows int
	ObjectsExhausted               bool
	LinksExhausted                 bool
	QueryProgressUnknown           bool
	LastObject                     string
}
type homeBindingResult struct {
	Binding  homeWholePlayableBinding
	Progress homeBindingProgress
}

// readHomeWholePlayableBinding is deliberately not a viewer or product API.
// q must be the closed registered query of ONE admitted snapshot. No arbitrary
// SQL, eligibility callback, session, principal or caller proof is accepted.
func readHomeWholePlayableBinding(q browseQuery, library, item int64) (out homeBindingResult, err error) {
	if q == nil || library <= 0 || item <= 0 {
		return out, ErrInput
	}
	rows, e := q("rb.home.binding.item", item, library)
	out.Progress.ItemRows = len(rows)
	if e != nil {
		out.Progress.QueryProgressUnknown = true
		return out, e
	}
	if len(rows) != 1 {
		return out, errHomeBindingUnresolved
	}
	v := rows[0]
	if v[0].(int64) != 0 || v[9] != nil || v[10] != nil || v[11] != nil || v[12].(int64) != 0 || v[13] != v[14] {
		return out, errHomeBindingUnresolved
	}
	b := homeWholePlayableBinding{Library: library, Item: item, Kind: v[19].(int64)}
	blob := func(v any, d *[16]byte) bool {
		x, ok := v.([]byte)
		if !ok || len(x) != 16 {
			return false
		}
		copy(d[:], x)
		return true
	}
	if !blob(v[1], &b.ItemPublic) || !blob(v[2], &b.ItemMetadataIncarnation) || !blob(v[3], &b.ItemIncarnation) || !blob(v[15], &b.Root.LibraryIncarnation) {
		return out, errHomeBindingUnresolved
	}
	b.ItemRevision = v[4].(int64)
	b.ItemMetadataRevision = v[5].(int64)
	b.ItemSourceRevision = v[6].(int64)
	b.ItemIdentityRevision = v[7].(int64)
	b.Root.LibraryOriginRevision = v[16].(int64)
	b.Root.DataRevision = v[17].(int64)
	b.Root.MetadataRevision = v[18].(int64)
	b.Root.AgeRevision = v[13].(int64)
	b.Root.RootsAgree = true
	libraryKey := v[8].(string)
	rows, e = q("rb.home.binding.links", item, library, int64(2))
	out.Progress.LinkRows = len(rows)
	out.Progress.LinksExhausted = len(rows) < 2
	if e != nil {
		out.Progress.QueryProgressUnknown = true
		return out, e
	}
	// No prefilter hides partial, pending, unavailable-asset or extra links.
	if len(rows) != 1 {
		return out, errHomeBindingUnresolved
	}
	v = rows[0]
	if v[1].(int64) != 0 || v[2].(float64) != 0 || v[3] != nil || v[4] != nil || v[5].(int64) != 0 || v[6].(int64) != 1 {
		return out, errHomeBindingUnresolved
	}
	b.Asset = v[0].(int64)
	if !blob(v[7], &b.AssetIncarnation) || !blob(v[9], &b.LinkIncarnation) {
		return out, errHomeBindingUnresolved
	}
	b.AssetRevision = v[8].(int64)
	b.LinkRevision = v[10].(int64)
	b.PreparedSourceRevision = v[11].(int64)
	b.PreparedSourceIncarnation = b.AssetIncarnation
	// Accepted link duration wins; the asset duration is its fallback.
	duration := v[12].(float64)
	if v[13].(float64) > 0 {
		duration = v[13].(float64)
	}
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 7*24*60*60 {
		return out, errHomeBindingUnresolved
	}
	b.DurationSeconds = duration
	if b.ItemRevision <= 0 || b.ItemMetadataRevision <= 0 || b.ItemSourceRevision <= 0 || b.ItemIdentityRevision <= 0 || b.AssetRevision <= 0 || b.LinkRevision <= 0 || b.PreparedSourceRevision <= 0 || b.Root.LibraryOriginRevision <= 0 {
		return out, errHomeBindingUnresolved
	}
	rows, e = q("rb.home.binding.objects", libraryKey, v[14], int64(MaxRaw))
	out.Progress.ObjectRows = len(rows)
	if e != nil {
		out.Progress.QueryProgressUnknown = true
		return out, e
	}
	out.Progress.ObjectsExhausted = len(rows) < MaxRaw
	if len(rows) > 0 {
		out.Progress.LastObject = rows[len(rows)-1][0].(string)
	}
	if !out.Progress.ObjectsExhausted {
		return out, errHomeBindingBudget
	}
	for _, v := range rows {
		if v[4].(int64) != 0 || v[5] != nil || v[6] != nil || v[7] != nil || v[8] == nil || v[8].(int64) != 1 || v[9] == nil || v[10] == nil || v[12] != nil {
			continue
		}
		b.Object = v[0].(string)
		b.ObjectRevision = v[1].(string)
		b.Source = v[2].(string)
		b.SourceIncarnation = v[3].(string)
		b.SourceGeneration = v[11].(int64)
		if b.Object == "" || b.ObjectRevision == "" || b.Source == "" || b.SourceIncarnation == "" || b.SourceGeneration <= 0 {
			return out, errHomeBindingUnresolved
		}
		out.Binding = b
		return out, nil
	}
	return out, errHomeBindingUnresolved
}
