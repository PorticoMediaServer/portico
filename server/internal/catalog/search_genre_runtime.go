package catalog

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type GenreRuntime struct {
	db    *sql.DB
	store *readprep.Store
	check FacetWindowCheck
	bases *FacetRuntime
	key   [32]byte
	now   func() time.Time
}

func NewGenreRuntime(db *sql.DB, store *readprep.Store, check FacetWindowCheck, bases *FacetRuntime) (*GenreRuntime, error) {
	if db == nil || store == nil || check == nil || bases == nil {
		return nil, ErrFacetBaseUnavailable
	}
	if err := bases.RegisterSource(SearchVideoGenreSource, CaptureSearchGenreBasePlan); err != nil {
		return nil, err
	}
	r := &GenreRuntime{db: db, store: store, check: check, bases: bases, now: time.Now}
	if _, err := rand.Read(r.key[:]); err != nil {
		return nil, err
	}
	return r, nil
}

type genreSpec struct {
	Limit     int      `json:"limit"`
	Preview   string   `json:"preview"`
	Direction byte     `json:"direction"`
	Anchor    genreRow `json:"anchor"`
}
type genreDescriptor struct {
	ArtEpoch    int64               `json:"artEpoch"`
	Scopes      []FacetBaseScope    `json:"scopes"`
	Authorities []string            `json:"authorities"`
	Bases       []FacetBaseSnapshot `json:"bases,omitempty"`
	Spec        genreSpec           `json:"spec"`
}
type genreSummary struct {
	Bases          []FacetBaseSnapshot `json:"bases"`
	Rows           []genreRow          `json:"rows"`
	HasGenres      bool                `json:"hasGenres"`
	Earlier, Later bool
}
type genreRow struct {
	Base, Value, Count int64
	Backdrops, Posters int
	Backdrop           *genreArtRef  `json:"backdrop,omitempty"`
	Covers             []genreArtRef `json:"covers,omitempty"`
}
type genreArtRef struct {
	Base, Value, Position, Entity int64
	Role                          int
	Rating                        float64 `json:"-"`
}

func (r *GenreRuntime) Initial(ctx context.Context, caller readprep.Caller, v Viewer, req SearchGenreRequest) (SearchGenreResult, error) {
	if r == nil {
		return SearchGenreResult{}, ErrFacetBaseUnavailable
	}
	if req.Cursor != "" {
		return r.Continue(ctx, caller, v, req, req.Cursor)
	}
	limit := req.Limit
	if limit == 0 && !req.LimitPresent {
		limit = 50
	}
	if caller == "" || limit < 1 || limit > 50 || req.Preview != "" && req.Preview != "backdrop" && req.Preview != "covers" {
		return SearchGenreResult{}, ErrFacetBaseInput
	}
	d := genreDescriptor{Spec: genreSpec{Limit: limit, Preview: req.Preview}}
	err := dbwork.WithReadSnapshot(ctx, r.db, func(ctx context.Context) error {
		q := dbwork.Snapshot(ctx)
		var err error
		d.Scopes, err = captureGenreScopes(ctx, q, v)
		if err == nil {
			err = q.QueryRowContext(ctx, `SELECT art_epoch FROM catalog_search_genre_state WHERE singleton=1`).Scan(&d.ArtEpoch)
		}
		if err != nil {
			return err
		}
		for _, scope := range d.Scopes {
			a, err := CaptureFacetAuthority(ctx, q, scope)
			if err != nil {
				return err
			}
			d.Authorities = append(d.Authorities, a)
		}
		return nil
	})
	if err != nil {
		return SearchGenreResult{}, err
	}
	if err = r.authorize(ctx, caller, d, nil); err != nil {
		return SearchGenreResult{}, err
	}
	all := true
	for _, scope := range d.Scopes {
		s, err := r.bases.Demand(ctx, scope)
		if errors.Is(err, compactcatalog.ErrBuilding) {
			all = false
			continue
		}
		if err != nil {
			return SearchGenreResult{}, err
		}
		d.Bases = append(d.Bases, s)
	}
	if all {
		for _, base := range d.Bases {
			var w genreWitness
			if json.Unmarshal([]byte(base.Witness), &w) != nil || w.ArtEpoch != d.ArtEpoch {
				all = false
				break
			}
		}
	}
	if all && len(d.Bases) > 1 {
		for i := 1; i < len(d.Bases); i++ {
			if d.Bases[i].Witness != d.Bases[0].Witness {
				all = false
				break
			}
		}
	}
	if !all {
		d.Bases = nil
	}
	return r.submit(ctx, caller, d, time.Time{})
}
func (r *GenreRuntime) submit(ctx context.Context, caller readprep.Caller, d genreDescriptor, until time.Time) (SearchGenreResult, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return SearchGenreResult{}, err
	}
	basis := "awaiting-genre-base"
	if len(d.Bases) == len(d.Scopes) {
		ids := make([]int64, len(d.Bases))
		for i, s := range d.Bases {
			ids[i] = s.ID
		}
		b, _ := json.Marshal(ids)
		basis = string(b)
	}
	initial := int64(131072 + 8*len(raw))
	h, err := r.store.SubmitUntil(ctx, caller, readprep.Key{Namespace: "search-genres-v1", Basis: basis, Canonical: string(raw)}, readprep.Plan{InitialBytes: initial, New: func(_ context.Context, b *readprep.Budget) (readprep.Computation, error) {
		if err := b.Resize(initial); err != nil {
			return nil, err
		}
		return &genreComputation{runtime: r, descriptor: d, bytes: initial}, nil
	}}, until)
	if err != nil {
		return SearchGenreResult{}, err
	}
	out, err := r.Poll(ctx, caller, "G"+string(h))
	if err != nil {
		_ = r.store.Cancel(ctx, caller, h)
	}
	return out, err
}
func genreHandle(id string) (readprep.Handle, bool) {
	if len(id) != 44 || id[0] != 'G' {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(id[1:])
	return readprep.Handle(id[1:]), err == nil && len(raw) == 32
}
func (r *GenreRuntime) authorize(ctx context.Context, caller readprep.Caller, d genreDescriptor, bases []FacetBaseSnapshot) error {
	if len(d.Scopes) != len(d.Authorities) {
		return readprep.ErrStale
	}
	for i, scope := range d.Scopes {
		s := FacetBaseSnapshot{Scope: scope}
		if bases != nil {
			if i >= len(bases) || bases[i].Scope != scope {
				return readprep.ErrStale
			}
			s = bases[i]
			if _, err := facetBaseSnapshot(ctx, dbwork.ReadHandle(ctx, r.db), s.ID, true); err != nil {
				return readprep.ErrStale
			}
		}
		if err := r.check(ctx, caller, s); err != nil {
			return err
		}
		a, err := CaptureFacetAuthority(ctx, dbwork.ReadHandle(ctx, r.db), scope)
		if err != nil {
			return err
		}
		if a != d.Authorities[i] {
			return readprep.ErrStale
		}
	}
	return nil
}
func (r *GenreRuntime) Poll(ctx context.Context, caller readprep.Caller, id string) (out SearchGenreResult, err error) {
	if r == nil {
		return out, ErrFacetBaseUnavailable
	}
	h, ok := genreHandle(id)
	if !ok {
		return out, readprep.ErrStale
	}
	result, err := r.store.Poll(ctx, caller, h)
	if err != nil {
		return out, err
	}
	transferred := false
	defer func() {
		if !transferred {
			result.Release()
		}
	}()
	if result.Key.Namespace != "search-genres-v1" {
		return out, readprep.ErrStale
	}
	if err = result.ResizeDelivery(int64(65536 + 8*len(result.Key.Canonical))); err != nil {
		return out, err
	}
	var d genreDescriptor
	if json.Unmarshal([]byte(result.Key.Canonical), &d) != nil {
		return out, readprep.ErrStale
	}
	if err = r.authorize(ctx, caller, d, nil); err != nil {
		_ = r.store.Cancel(ctx, caller, h)
		return out, err
	}
	preparation := SearchGenrePreparing{Kind: "genre-preparing", State: "preparing", QueryID: id, RetryAfterMs: 1000}
	var bases []FacetBaseSnapshot
	if result.State == readprep.Preparing {
		if r.now().Sub(result.CreatedAt) >= 10*time.Second {
			out.Fallback = &SearchGenreFallbackPage{Kind: "genre-fallback", TotalIsLowerBound: true, SearchGenrePage: SearchGenrePage{QueryID: id, Revision: "preparing", Genres: []SearchGenre{}}, HasGenresKnown: false, Preparation: preparation}
		} else {
			out.Preparing = &preparation
		}
	} else {
		var summary genreSummary
		if json.Unmarshal(result.Payload, &summary) != nil {
			return out, readprep.ErrStale
		}
		bases = summary.Bases
		if err = r.authorize(ctx, caller, d, bases); err != nil {
			return out, err
		}
		page, err := r.hydrate(ctx, id, h, result, d, summary)
		if err != nil {
			if errors.Is(err, readprep.ErrStale) {
				r.store.Invalidate(result.Key)
			}
			return out, err
		}
		out.Page = &page
	}
	if err = r.authorize(ctx, caller, d, bases); err != nil {
		_ = r.store.Cancel(ctx, caller, h)
		return SearchGenreResult{}, err
	}
	out.release = result.Release
	transferred = true
	return out, nil
}
func (r *GenreRuntime) Continue(ctx context.Context, caller readprep.Caller, v Viewer, req SearchGenreRequest, token string) (SearchGenreResult, error) {
	if r == nil || req.Preview != "" || req.Cursor != "" && req.Cursor != token {
		return SearchGenreResult{}, ErrFacetBaseInput
	}
	t, err := r.decode(token)
	if err != nil {
		return SearchGenreResult{}, err
	}
	result, err := r.store.Poll(ctx, caller, t.handle)
	if err != nil {
		return SearchGenreResult{}, err
	}
	defer result.Release()
	if result.State != readprep.Ready || result.Key.Namespace != "search-genres-v1" {
		return SearchGenreResult{}, readprep.ErrStale
	}
	if err = result.ResizeDelivery(int64(65536 + 8*len(result.Key.Canonical))); err != nil {
		return SearchGenreResult{}, err
	}
	var d genreDescriptor
	var summary genreSummary
	if json.Unmarshal([]byte(result.Key.Canonical), &d) != nil || json.Unmarshal(result.Payload, &summary) != nil {
		return SearchGenreResult{}, readprep.ErrStale
	}
	if d.Spec.Preview != "" || req.LimitPresent && req.Limit != d.Spec.Limit {
		return SearchGenreResult{}, readprep.ErrStale
	}
	scopes, err := captureGenreScopes(ctx, dbwork.ReadHandle(ctx, r.db), v)
	if err != nil {
		return SearchGenreResult{}, err
	}
	if !reflect.DeepEqual(scopes, d.Scopes) {
		return SearchGenreResult{}, readprep.ErrStale
	}
	if err = r.authorize(ctx, caller, d, summary.Bases); err != nil {
		return SearchGenreResult{}, err
	}
	d.Bases = summary.Bases
	d.Spec.Direction = t.direction
	d.Spec.Anchor = genreRow{Base: t.base, Value: t.value, Count: t.count}
	return r.submit(ctx, caller, d, time.Unix(t.expires, 0))
}
func (r *GenreRuntime) Cancel(ctx context.Context, caller readprep.Caller, id string) error {
	if r == nil {
		return nil
	}
	h, ok := genreHandle(id)
	if !ok {
		return nil
	}
	return r.store.Cancel(ctx, caller, h)
}

type genreToken struct {
	handle                      readprep.Handle
	base, value, count, expires int64
	direction                   byte
}

func (r *GenreRuntime) encode(t genreToken) string {
	raw := make([]byte, 66, 98)
	raw[0] = 1
	raw[1] = t.direction
	for i, v := range []int64{t.base, t.value, t.count, t.expires} {
		binary.BigEndian.PutUint64(raw[2+i*8:10+i*8], uint64(v))
	}
	h, _ := base64.RawURLEncoding.DecodeString(string(t.handle))
	copy(raw[34:], h)
	mac := hmac.New(sha256.New, r.key[:])
	mac.Write(raw)
	return "g" + base64.RawURLEncoding.EncodeToString(append(raw, mac.Sum(nil)...))
}
func (r *GenreRuntime) decode(token string) (genreToken, error) {
	var t genreToken
	if len(token) != 132 || token[0] != 'g' {
		return t, readprep.ErrStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[1:])
	if err != nil || len(raw) != 98 || raw[0] != 1 {
		return t, readprep.ErrStale
	}
	mac := hmac.New(sha256.New, r.key[:])
	mac.Write(raw[:66])
	if !hmac.Equal(raw[66:], mac.Sum(nil)) {
		return t, readprep.ErrStale
	}
	t.direction = raw[1]
	t.base = int64(binary.BigEndian.Uint64(raw[2:10]))
	t.value = int64(binary.BigEndian.Uint64(raw[10:18]))
	t.count = int64(binary.BigEndian.Uint64(raw[18:26]))
	t.expires = int64(binary.BigEndian.Uint64(raw[26:34]))
	t.handle = readprep.Handle(base64.RawURLEncoding.EncodeToString(raw[34:66]))
	if t.direction < 1 || t.direction > 3 || t.base <= 0 || t.value <= 0 || t.count <= 0 || r.now().Unix() >= t.expires {
		return t, readprep.ErrStale
	}
	return t, nil
}
