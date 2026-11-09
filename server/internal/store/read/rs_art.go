package read

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"time"
)

// ArtworkInput selects one public role or detail-page slot. PageSlot applies
// poster/cover/portrait/square aliases and container logo inheritance. A direct
// role uses art-route fallback rules. Neither grants access to the art owner's
// media. Reading and restricted containers use the shared compiled policy.
type ArtworkInput struct {
	View     BrowseInput
	Item     int64
	Role     string
	PageSlot bool
}

// ArtworkFact contains accepted opaque identities and ready rendition facts.
// Provider URLs, paths, origins and source fences never leave this recipe.
// Present=false is successful absence, not pending external artwork work.
type ArtworkFact struct {
	Present              bool
	Owner, OwnerKind     int64
	OwnerPublicID        [16]byte
	Role, Choice, Digest string
	Revision             int64
	Small, Large         ArtworkRendition
}
type ArtworkRendition struct {
	Digest, MIME         string
	Width, Height, Bytes int64
}

// Artwork requires ConsumeForeground custody through the final consumer.
func (r *ForegroundReader) Artwork(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ArtworkInput) (ArtworkFact, BrowseReadResult, error) {
	if r == nil {
		return ArtworkFact{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (ArtworkFact, BrowseReadResult, error) {
		in.View.Now = at
		return r.artwork(ctx, request, budget, output, in)
	})
}

// rsArtPreflightCalls declares the complete bounded fence/work call recipe for
// one art window. A Reading leaf fence binds the capped accepted-publication
// list in the same window: the common principal/library/kind points (5), the
// accepted source and book library points (2), the capped publication list (1)
// and, for each candidate converter publication, its asset-key/local pair
// (2*cap), plus the art root point. Catalogue/detail recipes keep the shared
// common eight-call window.
func rsArtPreflightCalls(in ArtworkInput) uint16 {
	switch in.View.policy.recipe {
	case rsReadingNativePolicy, rsReadingMobiPolicy:
		return 5 + 3 + 2*rsReadingPublicationCap + 1
	default:
		return 0
	}
}

func (r *ForegroundReader) artwork(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in ArtworkInput) (ArtworkFact, BrowseReadResult, error) {
	if r == nil || in.Item <= 0 || (in.View.policy.recipe != 0 && in.View.policy.subject != in.Item) {
		return ArtworkFact{}, BrowseReadResult{}, ErrInput
	}
	if in.Role == "thumbnail" {
		in.Role = "still"
	}
	if in.PageSlot {
		switch in.Role {
		case "poster", "backdrop", "logo", "still":
		default:
			return ArtworkFact{}, BrowseReadResult{}, ErrInput
		}
	} else {
		switch in.Role {
		case "poster", "cover", "portrait", "square", "backdrop", "logo", "still", "banner":
		default:
			return ArtworkFact{}, BrowseReadResult{}, ErrInput
		}
	}
	switch in.View.Kind {
	case 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 20, 21:
	default:
		return ArtworkFact{}, BrowseReadResult{}, ErrUnqualified
	}

	windows := &rsByteWindow{source: r.snapshots, preflightCalls: rsArtPreflightCalls(in), goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	var source []any
	var selected []any
	var choices []rsArtCandidate
	next := 0
	var pending *rsArtCandidate
	root := func(q browseQuery) ([]any, error) {
		rows, err := q("rs.art.source", rsArtSourceArgs(in.Item, in.View.Library, in.View.Kind)...)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		current := rows[0]
		// All changing hierarchy/incarnation heads bind the candidate work. The
		// selected choice additionally rechecks exact provenance/renditions by PK.
		if selected != nil && !equalSource(source, current) {
			selected = nil
		}
		if selected != nil && equalSource(source, current) {
			name, args := "rs.art.verify", selected[:20]
			if selected[20] != nil {
				name = "rs.art.verify_local"
				args = selected
			}
			v, err := q(name, args...)
			if err != nil {
				return nil, err
			}
			if len(v) != 1 {
				selected = nil
				return nil, ErrStale
			}
		}
		source = current
		return current, nil
	}
	probe := func(ctx context.Context, q browseQuery, f browseFence, d *ArtworkFact) error {
		if pending != nil {
			rows, err := q("rs.art.point", pending.id, pending.role)
			if err != nil {
				return err
			}
			if len(rows) != 1 {
				return ErrStale
			}
			selected = rows[0]
			pending = nil
			if !rsArtReady(selected) {
				return ErrNotReady
			}
			return rsArtFact(selected, d)
		}
		if selected != nil {
			return ctx.Err()
		}
		// At most eight absent scalar points in a quantum; a hit arms only the next
		// business window. QueryExactBytes repeats its lengths before any decoding.
		for calls := 0; calls < 8 && next < len(choices); calls++ {
			c := choices[next]
			next++
			sizes, err := q("rs.art.octets", c.id, c.role)
			if err != nil {
				return err
			}
			if len(sizes) == 0 {
				continue
			}
			if len(sizes) != 1 {
				return ErrUnqualified
			}
			bytes, err := rsPointBytes(sizes[0])
			if err != nil {
				return err
			}
			pending = &c
			return windows.arm(bytes, 2)
		}
		return ctx.Err()
	}
	steps := []readStep[ArtworkFact]{func(ctx context.Context, q browseQuery, f browseFence, d *ArtworkFact) error {
		selected = nil
		pending = nil
		next = 0
		if f.container == 0 && ((source[3] == nil && f.allowUnrated == 0) || (source[3] != nil && f.maximumAge >= 0 && source[3].(int64) > f.maximumAge)) {
			return ErrDenied
		}
		choices = rsArtChoices(source, in)
		return ctx.Err()
	}, probe, probe, probe}
	return publishRead(ctx, windows, request, budget, output, in.View, steps, root)
}
func rsArtReady(v []any) bool {
	if v[17] != "ready" || v[18] != "ready" || v[19] == "" {
		return false
	}
	if v[20] == nil {
		return true
	}
	return v[22] != nil && v[21] == v[23] && v[24] == int64(0) && v[25] == v[26] && v[27] == v[28] && v[29] == int64(1) && v[30] == v[32] && v[31] == v[33]
}
func rsArtFact(v []any, d *ArtworkFact) error {
	id, ok := v[5].([]byte)
	if !ok || len(id) != 16 {
		return fmt.Errorf("invalid art owner identity")
	}
	*d = ArtworkFact{Present: true, Owner: v[0].(int64), OwnerKind: v[1].(int64), Role: v[2].(string), Choice: v[3].(string), Digest: v[4].(string), Revision: v[6].(int64), Small: ArtworkRendition{Digest: v[7].(string), MIME: v[8].(string), Width: v[9].(int64), Height: v[10].(int64), Bytes: v[11].(int64)}, Large: ArtworkRendition{Digest: v[12].(string), MIME: v[13].(string), Width: v[14].(int64), Height: v[15].(int64), Bytes: v[16].(int64)}}
	copy(d.OwnerPublicID[:], id)
	return nil
}

type rsArtCandidate struct {
	id   int64
	role string
}

func rsArtChoices(head []any, in ArtworkInput) []rsArtCandidate {
	var choices []rsArtCandidate
	add := func(id any, roles ...string) {
		if id == nil {
			return
		}
		for _, role := range roles {
			choices = append(choices, rsArtCandidate{id.(int64), role})
		}
	}
	if in.PageSlot {
		roles := []string{in.Role}
		if in.Role == "poster" {
			roles = []string{"poster", "cover", "portrait", "square"}
		}
		add(head[0], roles...)
		if in.Role != "still" {
			switch in.View.Kind {
			case 3:
				add(head[15], roles...)
			case 4:
				add(head[30], roles...)
				add(head[15], roles...)
			case 7:
				add(head[45], roles...)
			case 9:
				add(head[60], roles...)
			}
		}
	} else {
		add(head[0], in.Role)
		if in.Role == "poster" || in.Role == "cover" {
			if in.View.Kind == 7 {
				add(head[45], "cover")
			}
			if in.View.Kind == 1 || in.View.Kind == 4 || in.View.Kind == 7 || in.View.Kind == 9 || in.View.Kind == 20 || in.View.Kind == 21 {
				add(head[0], "cover")
			}
			if in.View.Kind == 9 {
				add(head[60], "cover")
			}
		}
		if in.Role == "poster" || in.Role == "backdrop" {
			switch in.View.Kind {
			case 3:
				add(head[15], in.Role)
			case 4:
				add(head[30], in.Role)
				add(head[15], in.Role)
			}
		}
	}
	return choices
}
