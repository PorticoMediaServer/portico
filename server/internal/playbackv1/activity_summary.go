package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ActivitySession is a read-only display projection. It never includes the
// presentation URL, file paths, transport controls, or capability tokens.
type ActivitySession struct {
	ID             string                 `json:"id"`
	User           ActivityUser           `json:"user"`
	Profile        AdminName              `json:"profile"`
	Device         AdminName              `json:"device"`
	Item           *ActivityItem          `json:"item,omitempty"`
	Kind           string                 `json:"kind"`
	State          string                 `json:"state"`
	StartedAt      string                 `json:"startedAt"`
	Problem        string                 `json:"problem,omitempty"`
	BufferingSince string                 `json:"bufferingSince,omitempty"`
	Channel        *ActivityChannel       `json:"channel,omitempty"`
	Programme      *ActivityProgramme     `json:"programme,omitempty"`
	Transcode      *ConversionObservation `json:"transcode,omitempty"`
	Converting     bool                   `json:"converting"`
	PositionMs     int64                  `json:"positionMs"`
	DurationMs     int64                  `json:"durationMs,omitempty"`
	Decision       Decisions              `json:"decision"`
	Quality        *Quality               `json:"quality,omitempty"`
	VideoHeight    int                    `json:"videoHeight,omitempty"`
}
type ActivityChannel struct {
	Name   string `json:"name"`
	Number string `json:"number,omitempty"`
}
type ActivityProgramme struct {
	Title  string `json:"title"`
	EndsAt string `json:"endsAt"`
}
type ActivityUser struct {
	ID string `json:"id"`
	// Username is absent without a validated canonical membership username.
	// Neither the account ID nor its profile is a substitute.
	Username string `json:"username,omitempty"`
}

type ActivityItem struct {
	ID               string        `json:"id"`
	Title            string        `json:"title"`
	Kind             string        `json:"kind"`
	ShowID           string        `json:"showId,omitempty"`
	EpisodeNumbering string        `json:"episodeNumbering,omitempty"`
	AirDate          string        `json:"airDate,omitempty"`
	ShowTitle        string        `json:"showTitle,omitempty"`
	SeasonNumber     *int          `json:"seasonNumber,omitempty"`
	EpisodeNumber    *int          `json:"episodeNumber,omitempty"`
	Year             int           `json:"year,omitempty"`
	Artist           string        `json:"artist,omitempty"`
	BookTitle        string        `json:"bookTitle,omitempty"`
	Author           string        `json:"author,omitempty"`
	Album            string        `json:"album,omitempty"`
	Art              []ActivityArt `json:"art"`
}
type ActivityArt struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Role     string `json:"role"`
	Revision string `json:"revision"`
}
type ActivitySessions struct {
	Count           int64                   `json:"count"`
	ConvertingCount int64                   `json:"convertingCount"`
	Overflow        ActivitySessionOverflow `json:"overflow"`
	Items           []ActivitySession       `json:"items"`
}

type ActivitySessionOverflow struct {
	Count           int64 `json:"count"`
	ConvertingCount int64 `json:"convertingCount"`
}

// Only committed presentation decisions establish conversion. Invalid or absent
// JSON cannot turn a requested quality ceiling into a conversion fact.
const activityConverting = `CASE WHEN json_valid(v.presentation) THEN CASE WHEN
 json_extract(v.presentation,'$.decision.video.action') IN ('transcode','burn') OR
 json_extract(v.presentation,'$.decision.audio.action')='transcode' OR
 json_extract(v.presentation,'$.decision.subtitles.action')='burn'
 THEN 1 ELSE 0 END ELSE 0 END`

// ActivitySummarySessions and Dashboard share qualification, totals and order.
func (s *Service) ActivitySummarySessions(ctx context.Context) (ActivitySessions, error) {
	page, err := s.activitySessionPage(ctx, 3, "", true)
	out := ActivitySessions{Count: page.Totals.Streams, ConvertingCount: page.Totals.Converting, Items: []ActivitySession{}}
	if err != nil {
		return out, err
	}
	out.Overflow = ActivitySessionOverflow{Count: out.Count - int64(len(page.Items)), ConvertingCount: out.ConvertingCount}
	for _, a := range page.Items {
		v := ActivitySession{ID: a.ID, User: ActivityUser{ID: a.User.ID, Username: a.User.Name}, Profile: a.Profile, Device: a.Device, Item: a.Item, Kind: a.Kind, State: a.State, StartedAt: a.StartedAt, Problem: a.Problem, BufferingSince: a.BufferingSince, Channel: a.Channel, Programme: a.Programme, Transcode: a.Transcode, Converting: convertingDecision(a.Decision), PositionMs: a.PositionMs, DurationMs: a.DurationMs, Decision: a.Decision, Quality: a.Quality, VideoHeight: a.VideoHeight}
		out.Items = append(out.Items, v)
		if v.Converting {
			out.Overflow.ConvertingCount--
		}
	}
	return out, nil
}

func enrichActivityItems(ctx context.Context, tx *sql.Tx, items []AdminSession, artwork bool) error {
	ids := []string{}
	for _, a := range items {
		if a.Item != nil && a.Kind != "channel" {
			ids = append(ids, a.Item.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	raw, _ := json.Marshal(ids)
	rows, err := tx.QueryContext(ctx, `SELECT pid(i.public_id),k.name,i.year,COALESCE(pid(sh.public_id),''),COALESCE(sh.title,''),se.number,ep.number,COALESCE(ep.numbering,''),COALESCE(strftime('%Y-%m-%d',ep.air_date*86400,'unixepoch'),''),COALESCE(ar.title,''),COALESCE(al.title,''),COALESCE(b.author,''),COALESCE(book.title,'')
 FROM json_each(?) wanted CROSS JOIN catalog_entities i ON i.public_id=pid_blob(wanted.value)
 CROSS JOIN catalog_kinds k ON k.id=i.kind
 LEFT JOIN catalog_episodes ep ON ep.entity_id=i.id LEFT JOIN catalog_entities sh ON sh.id=ep.show_id
 LEFT JOIN catalog_seasons se ON se.entity_id=ep.season_id
 LEFT JOIN catalog_songs so ON so.entity_id=i.id LEFT JOIN catalog_entities al ON al.id=so.album_id
 LEFT JOIN catalog_albums ad ON ad.entity_id=al.id LEFT JOIN catalog_entities ar ON ar.id=ad.artist_id
 LEFT JOIN catalog_book_files bf ON bf.entity_id=i.id LEFT JOIN catalog_books b ON b.entity_id=bf.book_id LEFT JOIN catalog_entities book ON book.id=bf.book_id`, string(raw))
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, kind, showID, show, numbering, airDate, artist, album, author, bookTitle string
		var year int
		var season, episode *int
		if err = rows.Scan(&id, &kind, &year, &showID, &show, &season, &episode, &numbering, &airDate, &artist, &album, &author, &bookTitle); err != nil {
			break
		}
		for n := range items {
			if it := items[n].Item; it != nil && it.ID == id {
				it.Kind, it.ShowID, it.EpisodeNumbering, it.AirDate = kind, showID, numbering, airDate
				it.Author = author
				it.BookTitle = bookTitle
				it.Year, it.ShowTitle, it.SeasonNumber, it.EpisodeNumber, it.Artist, it.Album = year, show, season, episode, artist, album
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if !artwork {
		return nil
	}
	// Keyed entity/parent joins and ready-object checks keep artwork work bounded
	// by this page, including an episode's show and a song's album fallback.
	rows, err = tx.QueryContext(ctx, `WITH requested AS MATERIALIZED (
 SELECT i.id,pid(i.public_id) public_id FROM json_each(?) wanted CROSS JOIN catalog_entities i ON i.public_id=pid_blob(wanted.value)
 ), candidates AS (
 SELECT r.public_id,'item' kind,r.id entity_id,0 priority FROM requested r
 UNION ALL SELECT r.public_id,'show',ep.show_id,1 FROM requested r CROSS JOIN catalog_episodes ep ON ep.entity_id=r.id
 UNION ALL SELECT r.public_id,'album',so.album_id,1 FROM requested r CROSS JOIN catalog_songs so ON so.entity_id=r.id
 UNION ALL SELECT r.public_id,'book',bf.book_id,1 FROM requested r CROSS JOIN catalog_book_files bf ON bf.entity_id=r.id
 ), ranked AS (
 SELECT c.public_id,a.kind,pid(e.public_id) art_id,a.role,a.digest,
 row_number() OVER(PARTITION BY c.public_id,CASE a.role WHEN 'backdrop' THEN 'backdrop' WHEN 'cover' THEN 'square' WHEN 'square' THEN 'square' ELSE 'poster' END ORDER BY c.priority,a.role) rank
 FROM candidates c CROSS JOIN artwork_selections a ON a.kind=c.kind AND a.entity_id=c.entity_id AND a.subject='' AND a.role IN('poster','backdrop','cover','square')
 CROSS JOIN artwork_sources o ON o.digest=a.digest CROSS JOIN catalog_entities e ON e.id=a.entity_id
 ) SELECT public_id,kind,art_id,role,digest FROM ranked WHERE rank=1 ORDER BY public_id,role`, string(raw))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var a ActivityArt
		if err = rows.Scan(&id, &a.Kind, &a.ID, &a.Role, &a.Revision); err != nil {
			return err
		}
		for n := range items {
			if it := items[n].Item; it != nil && it.ID == id {
				it.Art = append(it.Art, a)
			}
		}
	}
	return rows.Err()
}
