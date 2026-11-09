package read

// Typed values of the large Home preparation. Every window is reduced into
// these explicit fields before the snapshot closes; no []any row, statement
// handle or transaction survives a quantum. The fixed composition is then run
// over the reconstructed typed frame, so the merged delivery, target and
// activity semantics are not re-implemented here.

// rbHomePrepareEntity mirrors exactly the closed rb.home.fixed.entity
// projection. Presence flags keep SQL NULL distinct from zero.
type rbHomePrepareEntity struct {
	ID, Library, Kind, Retired, Revision int64
	Public                               [16]byte
	AdmissionAge                         int64
	HasAdmissionAge                      bool
	Parent, Show, Season, Album, Book    int64
	HasParent, HasShow, HasSeason        bool
	HasAlbum, HasBook                    bool
	SeasonNumber, EpisodeNumber          int64
	DiscNumber, TrackNumber, PartNumber  int64
	HasSeasonNumber, HasEpisodeNumber    bool
	HasDiscNumber, HasTrackNumber        bool
	HasPartNumber                        bool
	Added, ContentAdded                  string
	HasAdded, HasContentAdded            bool
	NumberingLength                      int64
	MetadataRevision                     int64
	SourceRevision                       int64
	IdentityRevision                     int64
	Personal                             rbHomePreparePersonal
	Binding                              homeBindingResult
	HasBinding, Available                bool
}

// rbHomePreparePersonal mirrors exactly the closed rb.home.fixed.personal
// projection. ActivityStateLive is length(NULLIF(activity_state,'ended'));
// ActivityStateLength is length(activity_state).
type rbHomePreparePersonal struct {
	Present                                              bool
	Revision                                             int64
	ItemPublic                                           [16]byte
	ResumePosition, ResumeUnit, ResumeCompleted, Watched int64
	PlayCount, Favorite, Watchlisted, NotInterested      int64
	Rating                                               int64
	HasRating                                            bool
	ActivityAt                                           string
	HasActivityAt                                        bool
	ActivityStateLive                                    int64
	HasActivityStateLive                                 bool
	ActivityStateLength                                  int64
	HasActivityStateLength                               bool
	ActivityVisible                                      int64
	ResumePlaybackID                                     string
	HasResumePlaybackID                                  bool
	ResumeItemPublic                                     [16]byte
	HasResumeItemPublic                                  bool
	ContainerWatermark, ContainerClearedThrough          string
	HasContainerWatermark, HasContainerClearedThrough    bool
	ContainerRevision                                    int64
	HasContainerRevision                                 bool
	DismissedChildPublic                                 [16]byte
	HasDismissedChildPublic                              bool
	DismissedActivityAt, DismissedPlaybackID             string
	HasDismissedActivityAt, HasDismissedPlaybackID       bool
}

func rbHomePrepareNullableInt(v any) int64 {
	if n, ok := v.(int64); ok {
		return n
	}
	return 0
}

func rbHomePrepareMaybeInt(v int64, has bool) any {
	if !has {
		return nil
	}
	return v
}

func rbHomePrepareMaybeString(v string, has bool) any {
	if !has {
		return nil
	}
	return v
}

func rbHomePrepareMaybePublic(v [16]byte, has bool) any {
	if !has {
		return nil
	}
	public := v
	return public[:]
}

func rbHomePrepareAdmitted(retired int64, age any, f browseFence) bool {
	if retired != 0 {
		return false
	}
	if age == nil {
		return f.allowUnrated != 0
	}
	n, ok := age.(int64)
	if !ok {
		return false
	}
	return f.maximumAge < 0 || n <= f.maximumAge
}

func rbHomePrepareUnrestricted(f browseFence) bool {
	return f.maximumAge < 0 && f.allowUnrated != 0
}

func rbHomePrepareBlob(v any, dst *[16]byte) bool {
	raw, ok := v.([]byte)
	if !ok || len(raw) != 16 {
		return false
	}
	copy(dst[:], raw)
	return true
}

func rbHomePrepareEntityFrom(row []any) (*rbHomePrepareEntity, error) {
	if len(row) != 23 {
		return nil, ErrUnqualified
	}
	e := &rbHomePrepareEntity{}
	var ok bool
	if e.ID, ok = row[0].(int64); !ok || e.ID <= 0 {
		return nil, ErrUnqualified
	}
	if !rbHomePrepareBlob(row[1], &e.Public) {
		return nil, ErrUnqualified
	}
	if e.Kind, ok = row[2].(int64); !ok || e.Kind <= 0 {
		return nil, ErrUnqualified
	}
	if e.Retired, ok = row[3].(int64); !ok {
		return nil, ErrUnqualified
	}
	if e.Revision, ok = row[4].(int64); !ok {
		return nil, ErrUnqualified
	}
	if row[5] != nil {
		if e.AdmissionAge, ok = row[5].(int64); !ok {
			return nil, ErrUnqualified
		}
		e.HasAdmissionAge = true
	}
	if e.Parent, e.HasParent, ok = rbHomePrepareNullable(row[6]); !ok {
		return nil, ErrUnqualified
	}
	if e.Show, e.HasShow, ok = rbHomePrepareNullable(row[7]); !ok {
		return nil, ErrUnqualified
	}
	if e.Season, e.HasSeason, ok = rbHomePrepareNullable(row[8]); !ok {
		return nil, ErrUnqualified
	}
	if e.Album, e.HasAlbum, ok = rbHomePrepareNullable(row[9]); !ok {
		return nil, ErrUnqualified
	}
	if e.Book, e.HasBook, ok = rbHomePrepareNullable(row[10]); !ok {
		return nil, ErrUnqualified
	}
	if e.SeasonNumber, e.HasSeasonNumber, ok = rbHomePrepareNullable(row[11]); !ok {
		return nil, ErrUnqualified
	}
	if e.EpisodeNumber, e.HasEpisodeNumber, ok = rbHomePrepareNullable(row[12]); !ok {
		return nil, ErrUnqualified
	}
	if e.DiscNumber, e.HasDiscNumber, ok = rbHomePrepareNullable(row[13]); !ok {
		return nil, ErrUnqualified
	}
	if e.TrackNumber, e.HasTrackNumber, ok = rbHomePrepareNullable(row[14]); !ok {
		return nil, ErrUnqualified
	}
	if e.PartNumber, e.HasPartNumber, ok = rbHomePrepareNullable(row[15]); !ok {
		return nil, ErrUnqualified
	}
	if row[16] != nil {
		if e.Added, ok = row[16].(string); !ok || len(e.Added) != 30 {
			return nil, ErrUnqualified
		}
		e.HasAdded = true
	}
	if row[17] != nil {
		if e.ContentAdded, ok = row[17].(string); !ok || len(e.ContentAdded) != 30 {
			return nil, ErrUnqualified
		}
		e.HasContentAdded = true
	}
	if e.NumberingLength, ok = row[18].(int64); !ok || e.NumberingLength < 0 {
		return nil, ErrUnqualified
	}
	if _, ok = row[19].(int64); !ok {
		return nil, ErrUnqualified
	}
	if e.MetadataRevision, ok = row[20].(int64); !ok {
		return nil, ErrUnqualified
	}
	if e.SourceRevision, ok = row[21].(int64); !ok {
		return nil, ErrUnqualified
	}
	if e.IdentityRevision, ok = row[22].(int64); !ok {
		return nil, ErrUnqualified
	}
	return e, nil
}

func rbHomePrepareNullable(v any) (int64, bool, bool) {
	if v == nil {
		return 0, false, true
	}
	n, ok := v.(int64)
	return n, true, ok
}

func (e *rbHomePrepareEntity) fixed() homeFixedEntity {
	row := make([]any, 23)
	public := e.Public
	row[0], row[1], row[2], row[3], row[4] = e.ID, public[:], e.Kind, e.Retired, e.Revision
	row[5] = rbHomePrepareMaybeInt(e.AdmissionAge, e.HasAdmissionAge)
	row[6] = rbHomePrepareMaybeInt(e.Parent, e.HasParent)
	row[7] = rbHomePrepareMaybeInt(e.Show, e.HasShow)
	row[8] = rbHomePrepareMaybeInt(e.Season, e.HasSeason)
	row[9] = rbHomePrepareMaybeInt(e.Album, e.HasAlbum)
	row[10] = rbHomePrepareMaybeInt(e.Book, e.HasBook)
	row[11] = rbHomePrepareMaybeInt(e.SeasonNumber, e.HasSeasonNumber)
	row[12] = rbHomePrepareMaybeInt(e.EpisodeNumber, e.HasEpisodeNumber)
	row[13] = rbHomePrepareMaybeInt(e.DiscNumber, e.HasDiscNumber)
	row[14] = rbHomePrepareMaybeInt(e.TrackNumber, e.HasTrackNumber)
	row[15] = rbHomePrepareMaybeInt(e.PartNumber, e.HasPartNumber)
	row[16] = rbHomePrepareMaybeString(e.Added, e.HasAdded)
	row[17] = rbHomePrepareMaybeString(e.ContentAdded, e.HasContentAdded)
	row[18], row[19] = e.NumberingLength, int64(0)
	row[20], row[21], row[22] = e.MetadataRevision, e.SourceRevision, e.IdentityRevision
	entity := homeFixedEntity{
		Identity:  HomeFixedIdentity{ID: e.ID, Library: e.Library, Kind: e.Kind, Revision: e.Revision, PublicID: e.Public},
		Row:       [][]any{row},
		Binding:   e.Binding,
		Available: e.Available,
	}
	if e.Personal.Present {
		entity.Personal = [][]any{e.Personal.fixed()}
	}
	return entity
}

func rbHomePreparePersonalFrom(row []any) (rbHomePreparePersonal, error) {
	p := rbHomePreparePersonal{}
	if len(row) != 24 {
		return p, ErrUnqualified
	}
	var ok bool
	if p.Revision, ok = row[0].(int64); !ok {
		return p, ErrUnqualified
	}
	if !rbHomePrepareBlob(row[1], &p.ItemPublic) {
		return p, ErrUnqualified
	}
	if p.ResumePosition, ok = row[2].(int64); !ok {
		return p, ErrUnqualified
	}
	if p.ResumeUnit, ok = row[3].(int64); !ok {
		return p, ErrUnqualified
	}
	if p.ResumeCompleted, ok = row[4].(int64); !ok {
		return p, ErrUnqualified
	}
	if p.Watched, ok = row[5].(int64); !ok {
		return p, ErrUnqualified
	}
	if p.PlayCount, ok = row[6].(int64); !ok {
		return p, ErrUnqualified
	}
	if p.Favorite, ok = row[7].(int64); !ok {
		return p, ErrUnqualified
	}
	if p.Watchlisted, ok = row[8].(int64); !ok {
		return p, ErrUnqualified
	}
	if p.NotInterested, ok = row[9].(int64); !ok {
		return p, ErrUnqualified
	}
	if row[10] != nil {
		if p.Rating, ok = row[10].(int64); !ok {
			return p, ErrUnqualified
		}
		p.HasRating = true
	}
	if row[11] != nil {
		if p.ActivityAt, ok = row[11].(string); !ok || len(p.ActivityAt) != 30 {
			return p, ErrUnqualified
		}
		p.HasActivityAt = true
	}
	if row[12] != nil {
		if p.ActivityStateLive, ok = row[12].(int64); !ok || p.ActivityStateLive < 0 {
			return p, ErrUnqualified
		}
		p.HasActivityStateLive = true
	}
	if p.ActivityVisible, ok = row[13].(int64); !ok {
		return p, ErrUnqualified
	}
	if row[14] != nil {
		if p.ResumePlaybackID, ok = row[14].(string); !ok {
			return p, ErrUnqualified
		}
		p.HasResumePlaybackID = true
	}
	if row[15] != nil {
		if !rbHomePrepareBlob(row[15], &p.ResumeItemPublic) {
			return p, ErrUnqualified
		}
		p.HasResumeItemPublic = true
	}
	if row[16] != nil {
		if p.ContainerWatermark, ok = row[16].(string); !ok {
			return p, ErrUnqualified
		}
		p.HasContainerWatermark = true
	}
	if row[17] != nil {
		if p.ContainerClearedThrough, ok = row[17].(string); !ok {
			return p, ErrUnqualified
		}
		p.HasContainerClearedThrough = true
	}
	if row[18] != nil {
		if _, ok = row[18].(int64); !ok {
			return p, ErrUnqualified
		}
	}
	if row[19] != nil {
		if p.ContainerRevision, ok = row[19].(int64); !ok {
			return p, ErrUnqualified
		}
		p.HasContainerRevision = true
	}
	if row[20] != nil {
		if !rbHomePrepareBlob(row[20], &p.DismissedChildPublic) {
			return p, ErrUnqualified
		}
		p.HasDismissedChildPublic = true
	}
	if row[21] != nil {
		if p.DismissedActivityAt, ok = row[21].(string); !ok {
			return p, ErrUnqualified
		}
		p.HasDismissedActivityAt = true
	}
	if row[22] != nil {
		if p.DismissedPlaybackID, ok = row[22].(string); !ok {
			return p, ErrUnqualified
		}
		p.HasDismissedPlaybackID = true
	}
	if row[23] != nil {
		if p.ActivityStateLength, ok = row[23].(int64); !ok || p.ActivityStateLength < 0 {
			return p, ErrUnqualified
		}
		p.HasActivityStateLength = true
	}
	p.Present = true
	return p, nil
}

func (p rbHomePreparePersonal) fixed() []any {
	row := make([]any, 24)
	public := p.ItemPublic
	row[0], row[1] = p.Revision, public[:]
	row[2], row[3], row[4], row[5], row[6], row[7], row[8], row[9] = p.ResumePosition, p.ResumeUnit, p.ResumeCompleted, p.Watched, p.PlayCount, p.Favorite, p.Watchlisted, p.NotInterested
	row[10] = rbHomePrepareMaybeInt(p.Rating, p.HasRating)
	row[11] = rbHomePrepareMaybeString(p.ActivityAt, p.HasActivityAt)
	row[12] = rbHomePrepareMaybeInt(p.ActivityStateLive, p.HasActivityStateLive)
	row[13] = p.ActivityVisible
	row[14] = rbHomePrepareMaybeString(p.ResumePlaybackID, p.HasResumePlaybackID)
	row[15] = rbHomePrepareMaybePublic(p.ResumeItemPublic, p.HasResumeItemPublic)
	row[16] = rbHomePrepareMaybeString(p.ContainerWatermark, p.HasContainerWatermark)
	row[17] = rbHomePrepareMaybeString(p.ContainerClearedThrough, p.HasContainerClearedThrough)
	if p.HasActivityAt {
		row[18] = int64(len(p.ActivityAt))
	}
	row[19] = rbHomePrepareMaybeInt(p.ContainerRevision, p.HasContainerRevision)
	row[20] = rbHomePrepareMaybePublic(p.DismissedChildPublic, p.HasDismissedChildPublic)
	row[21] = rbHomePrepareMaybeString(p.DismissedActivityAt, p.HasDismissedActivityAt)
	row[22] = rbHomePrepareMaybeString(p.DismissedPlaybackID, p.HasDismissedPlaybackID)
	row[23] = rbHomePrepareMaybeInt(p.ActivityStateLength, p.HasActivityStateLength)
	return row
}
