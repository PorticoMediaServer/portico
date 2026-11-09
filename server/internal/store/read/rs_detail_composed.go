package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// RSDetailFactState distinguishes a proven absence/presence from an owner fact
// that this composition cannot truthfully supply yet. A pending section never
// carries invented rows or values.
type RSDetailFactState uint8

const (
	// RSDetailFactAbsent is a successful proof that this item/kind supplies no
	// such facts (including the deliberate related=none first composition).
	RSDetailFactAbsent RSDetailFactState = iota
	// RSDetailFactPresent means the section below carries current source rows.
	RSDetailFactPresent
	// RSDetailFactPending names an accepted owner ABI that is not composed yet.
	RSDetailFactPending
)

// RSDetailPersonal is the current profile's raw personal projection. It is not
// a typed resume child/source decision; that owner mapping remains pending.
type RSDetailPersonal struct {
	UserState
	NotInterested bool
	Rating        *int64
}

// RSDetailArtwork carries the selected public artwork slot. Pending means an
// accepted selection exists but its renditions are not ready; no invented
// digest, URL, provider or rendition is published in that state.
type RSDetailArtwork struct {
	State RSDetailFactState
	Fact  *ArtworkFact
}

// RSDetailComposed is the complete common detail DTO for one authorized item.
// It composes the merged typed facts, RD2 personal/child/art/credit/version
// windows, selected artwork inheritance and accepted Reading facts under ONE
// publication custody and deadline. Related rows are deliberately absent for
// this first slice. Selected-file technical facts (task 03) and accepted
// provider facts (task 11) are explicit pending states, as are the owner
// effective-series and typed-resume projections (task 13).
type RSDetailComposed struct {
	Facts             TitleFacts
	Personal          RSDetailPersonal
	Children          []BrowseItem
	ChildrenExhausted bool
	Images            []Image
	Cast, Crew        []Credit
	CreditsExhausted  bool
	Versions          []Version
	Art               RSDetailArtwork
	Reading           *ReadingFacts
	Technical         RSDetailFactState
	Provider          RSDetailFactState
	Related           RSDetailFactState
	EffectiveSeries   RSDetailFactState
	Resume            RSDetailFactState
	Next              *TitleCursor
	RawRows           int
}

type RSDetailComposedInput struct {
	View  BrowseInput
	Item  int64
	After *TitleCursor
}

// RSDetailComposedReader composes one bounded detail. It never calls another
// public reader, so no sub-read can reset the original deadline, output grant
// or LIVE custodian.
type RSDetailComposedReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewRSDetailComposedReader(s *governor.Snapshots) (*RSDetailComposedReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &RSDetailComposedReader{snapshots: s, pool: s}, nil
}

func validRSDetailComposed(in RSDetailComposedInput) bool {
	return in.Item > 0 && in.View.After == nil && !in.View.Descending && !in.View.KeepArticles
}

// Detail requires ConsumeForeground custody through the final consumer.
func (r *RSDetailComposedReader) Detail(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RSDetailComposedInput) (RSDetailComposed, BrowseReadResult, error) {
	if r == nil {
		return RSDetailComposed{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RSDetailComposed, BrowseReadResult, error) {
		in.View.Now = at
		return r.detail(ctx, request, budget, output, in)
	})
}

func (r *RSDetailComposedReader) detail(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RSDetailComposedInput) (RSDetailComposed, BrowseReadResult, error) {
	if r == nil || !validRSDetailComposed(in) {
		return RSDetailComposed{}, BrowseReadResult{}, ErrInput
	}
	kind := in.View.Kind
	reading := kind == 20 || kind == 21
	switch {
	case kind >= 1 && kind <= 10:
		in.View.policy = readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: in.Item}
	case reading:
		// Reading keeps its own closed native publication/format recipe.
		in.View.policy = readPolicyBinding{recipe: rsReadingNativePolicy, subject: in.Item}
	default:
		return RSDetailComposed{}, BrowseReadResult{}, ErrUnqualified
	}

	// One exact-byte window selects every business payload. Preflight calls stay
	// inside the finite statement recipe; no handle or grant leaves a quantum.
	windows := &rsByteWindow{source: r.snapshots, preflightCalls: 768, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}

	child, cast, crew := int64(0), int64(-1), int64(-1)
	if in.After != nil {
		child, cast, crew = in.After.child, in.After.cast, in.After.crew
	}
	var source []any
	var selected []any
	var choices []rsArtCandidate
	next := 0
	var pending *rsArtCandidate
	creditImage := int64(0)
	var inventory *rsReadingInventory
	var publications [][]any
	var library string
	readingDone := false

	root := func(q browseQuery) ([]any, error) {
		rows, err := q("rs.detail_composed.source", rsArtSourceArgs(in.Item, in.View.Library, in.View.Kind)...)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		current := rows[0]
		if reading {
			publication, err := q("rs.reading.source", in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return nil, err
			}
			if len(publication) != 1 {
				return nil, ErrDenied
			}
			current = append(current, publication[0]...)
		}
		if in.After != nil && !equalSource(in.After.source, current) {
			return nil, ErrStale
		}
		// A selection from an earlier attempt is a gate, never a retained draft
		// card: it must still match its exact accepted provenance before retry.
		if selected != nil && !equalSource(source, current) {
			selected = nil
		}
		if selected != nil {
			name, args := "rs.art.verify", selected[:20]
			if selected[4] == nil {
				// A locked NULL digest has no artwork_sources row to join; its
				// exact witness is the selection key itself, exactly as the
				// merged artwork reader verifies its empty choice.
				name, args = "rs.art.verify_empty", []any{selected[0], selected[1], selected[2], selected[5], selected[6], selected[19]}
			} else if selected[20] != nil {
				name = "rs.art.verify_local"
				args = selected[:34]
			}
			currentArt, err := q(name, args...)
			if err != nil {
				return nil, err
			}
			if len(currentArt) != 1 {
				selected = nil
				return nil, ErrStale
			}
		}
		if reading && readingDone {
			readingDone = false
			if err := inventory.verify(q, ReadingFactsInput{View: in.View, Item: in.Item}, library); err != nil {
				return nil, err
			}
		}
		source = current
		return current, nil
	}

	steps := []readStep[RSDetailComposed]{
		func(ctx context.Context, _ browseQuery, f browseFence, d *RSDetailComposed) error {
			child, cast, crew = 0, -1, -1
			if in.After != nil {
				child, cast, crew = in.After.child, in.After.cast, in.After.crew
				if in.After.fence != f || in.After.item != in.Item {
					return ErrStale
				}
			}
			// The source row keeps the RD2 early scalar age/retirement proof before
			// any typed payload or child/credit window is read.
			if source[2].(int64) != 0 || f.container == 0 && ((source[3] == nil && f.allowUnrated == 0) || (source[3] != nil && f.maximumAge >= 0 && source[3].(int64) > f.maximumAge)) {
				return ErrDenied
			}
			selected = nil
			pending = nil
			next = 0
			creditImage = 0
			choices = rsArtChoices(source, ArtworkInput{View: in.View, Item: in.Item, Role: rsDetailComposedArtRole(in.View.Kind), PageSlot: true})
			*d = RSDetailComposed{Technical: RSDetailFactPending, Provider: RSDetailFactPending, Related: RSDetailFactAbsent, EffectiveSeries: RSDetailFactAbsent, Resume: RSDetailFactAbsent, Art: RSDetailArtwork{State: RSDetailFactAbsent}}
			if kind == 8 || kind == 9 {
				d.EffectiveSeries = RSDetailFactPending
			}
			if kind == 8 || kind == 9 || reading {
				d.Resume = RSDetailFactPending
			}
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, _ *RSDetailComposed) error {
			itemSizes, err := q("rs.detail_composed.item_octets", f.profile, in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return err
			}
			if len(itemSizes) != 1 {
				return ErrDenied
			}
			itemBytes, err := rsPointBytes(itemSizes[0])
			if err != nil {
				return err
			}
			factSizes, err := q("rs.detail.facts_octets", in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return err
			}
			if len(factSizes) != 1 {
				return ErrDenied
			}
			factBytes, err := rsPointBytes(factSizes[0])
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return windows.arm(itemBytes+factBytes, 4)
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *RSDetailComposed) error {
			items, err := q("rs.detail_composed.item", f.profile, in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return err
			}
			if len(items) != 1 || !admittedItem(items[0], f) {
				return ErrDenied
			}
			item := items[0]
			rows, err := q("rs.detail.facts", in.Item, in.View.Library, in.View.Kind)
			if err != nil {
				return err
			}
			if len(rows) != 1 || f.container == 0 && !admittedItem(rows[0], f) {
				return ErrDenied
			}
			if err := rsDetailComposedFacts(rows[0], kind, &d.Facts); err != nil {
				return err
			}
			d.Personal = rsDetailComposedPersonal(item)
			if item[8] != nil {
				creditImage = item[8].(int64)
			}
			d.RawRows += 2
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, _ browseFence, d *RSDetailComposed) error {
			if pending != nil {
				return ctx.Err()
			}
			for calls := 0; calls < 8 && next < len(choices); calls++ {
				candidate := choices[next]
				next++
				sizes, err := q("rs.art.octets", candidate.id, candidate.role)
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
				pending = &candidate
				d.Art.State = RSDetailFactPending
				return windows.arm(bytes, 2)
			}
			d.Art.State = RSDetailFactAbsent
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, _ browseFence, d *RSDetailComposed) error {
			if pending == nil {
				return ctx.Err()
			}
			rows, err := q("rs.art.point", pending.id, pending.role)
			if err != nil {
				return err
			}
			if len(rows) != 1 {
				return ErrStale
			}
			selected = rows[0]
			pending = nil
			if rsArtAbsent(selected) {
				// A locked NULL digest is explicit absence, not pending work: it
				// stops alias/ancestor fallback and keeps the zero artwork fact.
				d.Art.State = RSDetailFactAbsent
				return ctx.Err()
			}
			if !rsArtReady(selected) {
				d.Art.State = RSDetailFactPending
				return ctx.Err()
			}
			var fact ArtworkFact
			if err := rsArtFact(selected, &fact); err != nil {
				return err
			}
			d.Art = RSDetailArtwork{State: RSDetailFactPresent, Fact: &fact}
			d.RawRows++
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *RSDetailComposed) error {
			rows, err := q("foreground.children", in.Item, child, int64(MaxRaw))
			if err != nil {
				return err
			}
			d.RawRows += len(rows)
			n := 0
			for _, v := range rows {
				child = v[0].(int64)
				n++
				if v[8].(int64) != 0 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
					return ErrUnqualified
				}
				if !admittedItem(v, f) {
					continue
				}
				item, err := itemValue(v)
				if err != nil {
					return err
				}
				d.Children = append(d.Children, item)
				if len(d.Children) == in.View.Limit {
					break
				}
			}
			d.ChildrenExhausted = n == len(rows) && len(rows) < MaxRaw
			rows, err = q("foreground.images", in.Item, int64(129))
			if err != nil {
				return err
			}
			d.RawRows += len(rows)
			if len(rows) > 128 {
				return ErrUnqualified
			}
			// Same precedence as the foreground reader: PK order puts locked
			// before matched for each role, so the first row for a role is its
			// precedence choice and a later matched row never revives a consumed
			// role. A locked selection with a NULL digest is a present empty
			// choice: it consumes the role and omits the image rather than being
			// asserted as a string or replaced by matched art. The logic is
			// duplicated here because the merged foreground loop takes no shared
			// helper and that file is outside this task's scope.
			lastRole := ""
			for _, v := range rows {
				role := v[0].(string)
				if role == lastRole {
					continue
				}
				lastRole = role
				if v[2] == nil {
					continue
				}
				d.Images = append(d.Images, Image{Role: role, Digest: v[2].(string), Provider: v[3].(string), Origin: v[4].(string), Revision: v[5].(int64)})
			}

			rows, err = q("foreground.credits.class", "Acting")
			if err != nil {
				return err
			}
			d.RawRows += len(rows)
			var department any
			if len(rows) == 1 {
				department = rows[0][0]
			}
			d.CreditsExhausted = true
			image := creditImage
			for _, class := range []struct {
				name   string
				after  *int64
				limit  int
				values *[]Credit
			}{
				{"cast", &cast, 100, &d.Cast}, {"crew", &crew, 50, &d.Crew},
			} {
				args := []any{in.Item, image, *class.after}
				name := "foreground.credits." + class.name
				if department == nil {
					if class.name == "cast" {
						continue
					}
					name += ".all"
				} else {
					args = append(args, department)
				}
				rows, err = q(name, args...)
				if err != nil {
					return err
				}
				d.RawRows += len(rows)
				if len(rows) == class.limit {
					d.CreditsExhausted = false
				}
				for _, v := range rows {
					c := Credit{Ord: v[0].(int64), Name: v[2].(string), Role: v[3].(string), Department: v[4].(string)}
					if v[1] != nil {
						c.Person = v[1].(int64)
					}
					*class.values = append(*class.values, c)
					*class.after = c.Ord
				}
			}

			rows, err = q("foreground.versions", in.Item, int64(17))
			if err != nil {
				return err
			}
			d.RawRows += len(rows)
			if len(rows) > 16 {
				return ErrUnqualified
			}
			for _, v := range rows {
				d.Versions = append(d.Versions, Version{Asset: v[0].(int64), Part: v[1].(int64), Edition: v[2].(string), Available: v[3].(int64) != 0})
			}
			if !d.ChildrenExhausted || !d.CreditsExhausted {
				d.Next = &TitleCursor{fence: f, item: in.Item, child: child, cast: cast, crew: crew, source: source}
			}
			return ctx.Err()
		},
	}

	if reading {
		steps = append(steps,
			func(ctx context.Context, q browseQuery, f browseFence, d *RSDetailComposed) error {
				library = f.library
				readingDone = false
				inventory = &rsReadingInventory{sources: make(map[string][]any), assets: make(map[int64][]any), states: make(map[int64][2]bool)}
				bytes, err := rsReadingOctets(q, "rs.reading.octets", in.Item, in.View.Library, in.View.Kind)
				if err != nil {
					return err
				}
				maxBytes, total := max(bytes, 1), bytes
				publications, err = rsReadingPublications(q, in.Item, in.View.Library, library)
				if err != nil {
					return err
				}
				inventory.ids, err = q("rs.reading.source_ids", library, int64(rsReadingPublicationCap+1))
				if err != nil {
					return err
				}
				if len(inventory.ids) > rsReadingPublicationCap {
					return ErrUnqualified
				}
				for _, v := range publications {
					bytes, err := rsReadingOctets(q, "rs.reading.asset_octets", v[0], in.View.Library)
					if err != nil {
						return err
					}
					total, err = rsReadingProspectiveBytes(total, bytes, output.Request.Need[governor.MemoryBytes]/8)
					if err != nil {
						return err
					}
					maxBytes = max(maxBytes, bytes)
				}
				for _, v := range inventory.ids {
					if len(v[0].(string)) > 256 {
						return ErrUnqualified
					}
					bytes, err := rsReadingOctets(q, "rs.reading.library_source_octets", v[0], library)
					if err != nil {
						return err
					}
					total, err = rsReadingProspectiveBytes(total, bytes, output.Request.Need[governor.MemoryBytes]/8)
					if err != nil {
						return err
					}
					maxBytes = max(maxBytes, bytes)
				}
				if int64(len(publications)+len(inventory.ids))*256 > output.Request.Need[governor.MemoryBytes]/8 {
					return governor.ErrSnapshotBudget
				}
				return windows.arm(maxBytes, 512)
			},
			func(ctx context.Context, q browseQuery, _ browseFence, d *RSDetailComposed) error {
				gone, err := q("rs.reading.retired", in.Item)
				if err != nil {
					return err
				}
				inventory.retired = len(gone) > 0
				for _, idrow := range inventory.ids {
					id := idrow[0].(string)
					rows, err := q("rs.reading.library_source", id, library)
					if err != nil {
						return err
					}
					if len(rows) != 1 {
						return ErrStale
					}
					inventory.sources[id] = rows[0]
				}
				facts := ReadingFacts{ID: in.Item}
				available := false
				for _, v := range publications {
					id := v[0].(int64)
					rows, err := q("rs.reading.asset", id, in.View.Library)
					if err != nil {
						return err
					}
					if len(rows) != 1 {
						return ErrStale
					}
					inventory.assets[id] = rows[0]
					ok, err := inventory.available(q, id, library)
					if err != nil {
						return err
					}
					if rsReadingFormat(in.View.Kind, v[1].(string), v[2].(int64) != 0, false) && ok {
						available = true
					}
				}
				rows, err := q("rs.reading.facts", in.Item, in.View.Library, in.View.Kind)
				if err != nil {
					return err
				}
				if len(rows) != 1 {
					return ErrStale
				}
				if err := rsDetailComposedReading(rows[0], &facts); err != nil {
					return err
				}
				facts.Available = available
				d.Reading = &facts
				readingDone = true
				d.RawRows += len(publications)
				return ctx.Err()
			},
		)
	}

	return publishRead(ctx, windows, request, budget, output, in.View, steps, root)
}

func rsDetailComposedArtRole(kind int64) string {
	switch kind {
	case 1, 2, 3, 10:
		return "poster"
	case 4:
		return "still"
	case 5:
		return "portrait"
	default:
		return "cover"
	}
}

func rsDetailComposedPersonal(row []any) RSDetailPersonal {
	personal := RSDetailPersonal{}
	if row[9] == nil {
		return personal
	}
	personal.UserState = UserState{Revision: row[9].(int64), ResumePosition: row[10].(int64), ResumeUnit: row[11].(int64), PlayCount: row[12].(int64), Favorite: row[13].(int64) != 0, Watchlisted: row[14].(int64) != 0, Watched: row[15].(int64) != 0, Completed: row[16].(int64) != 0}
	personal.NotInterested = row[17].(int64) != 0
	personal.Rating = rsInteger(row[18])
	return personal
}

// rsDetailComposedFacts decodes the accepted common/typed entity facts. Ebook
// and comic kinds deliberately supply no timed-media typed facts here; their
// accepted Reading attribution is a separate current publication point.
func rsDetailComposedFacts(v []any, kind int64, d *TitleFacts) error {
	item, err := itemValue(v)
	if err != nil {
		return err
	}
	*d = TitleFacts{Item: TitleItem{BrowseItem: item, Kind: kind, Overview: v[7].(string)}, Year: v[8].(int64), OriginalTitle: v[9].(string), Tagline: v[10].(string), Edition: v[11].(string), ReleaseDate: v[12].(string), ContentRating: v[13].(string), Studio: v[14].(string), Network: v[15].(string), Country: v[16].(string), AddedAt: rsText(v[17]), LastContentAddedAt: rsText(v[18])}
	required := func(n int) (int64, error) {
		if v[n] == nil {
			return 0, ErrUnqualified
		}
		return v[n].(int64), nil
	}
	switch kind {
	case 1, 5:
		// Movie and artist roots use the common accepted descriptive fields.
	case 3:
		show, err := required(24)
		if err != nil {
			return err
		}
		number, err := required(26)
		if err != nil {
			return err
		}
		d.Season = &SeasonFacts{Show: show, Number: number}
	case 2:
		d.Show = &ShowFacts{HideSeasons: v[19].(int64) != 0, AbsoluteNumbering: v[20].(int64) != 0, NewestFirst: v[21].(int64) != 0, SettingsRevision: v[22].(int64), Ranges: v[23].(string)}
	case 4:
		show, err := required(24)
		if err != nil {
			return err
		}
		number, err := required(27)
		if err != nil {
			return err
		}
		if v[28] == nil {
			return ErrUnqualified
		}
		d.Episode = &EpisodeFacts{Show: show, Season: rsInteger(v[25]), Number: number, Numbering: v[28].(string), AirDate: rsInteger(v[29]), IdentityStatus: rsText(v[30]), OrderingBasis: rsText(v[31])}
	case 6:
		artist, err := required(32)
		if err != nil {
			return err
		}
		d.Album = &AlbumFacts{Artist: artist, RecordLabel: v[33].(string), ReleaseCountry: v[34].(string), EditionKey: v[35].(string)}
	case 7:
		album, err := required(36)
		if err != nil {
			return err
		}
		d.Song = &SongFacts{Album: album, Disc: rsInteger(v[37]), Track: rsInteger(v[38])}
	case 8:
		d.Book = &BookFacts{Author: v[39].(string), Narrator: v[40].(string), Series: v[41].(string), SeriesPosition: v[42].(string)}
	case 9:
		book, err := required(43)
		if err != nil {
			return err
		}
		d.AudioFile = &AudioFileFacts{Book: book, Disc: rsInteger(v[37]), Part: rsInteger(v[44])}
	case 10:
		d.Collection = &CollectionFacts{NameKey: rsText(v[45]), Revision: v[46].(int64)}
	case 20, 21:
		// Reading facts are composed from their accepted publication point.
	default:
		return ErrUnqualified
	}
	return nil
}

// rsDetailComposedReading decodes the accepted Reading attribution pair. NULL
// stays a source absence; it is never replaced by another profile or a default.
func rsDetailComposedReading(v []any, p *ReadingFacts) error {
	p.ID = v[0].(int64)
	id, ok := v[1].([]byte)
	if !ok || len(id) != 16 {
		return ErrUnqualified
	}
	copy(p.PublicID[:], id)
	p.Title = v[2].(string)
	p.Year = v[3].(int64)
	text := func(n int) *string {
		if v[n] == nil {
			return nil
		}
		s := v[n].(string)
		return &s
	}
	p.AddedAt = text(4)
	p.PublicationType = text(5)
	p.PublicationTypeSource = text(6)
	p.IssueDate = text(7)
	p.IssueDateSource = text(8)
	p.Author = ReadingAttribution{text(9), text(10), text(11)}
	p.Series = ReadingAttribution{text(12), text(13), text(15)}
	p.SeriesID = text(14)
	p.NumberedOrder = rsInteger(v[16])
	if v[17] != nil {
		var x float64
		switch n := v[17].(type) {
		case float64:
			x = n
		case int64:
			x = float64(n)
		default:
			return ErrUnqualified
		}
		p.NumberValue = &x
	}
	return nil
}
