package subtitles

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

type ProviderQuery struct {
	Title, Language, IMDbID, TMDBID, ParentIMDbID, ParentTMDBID string
	Year, Season, Episode, Page                                 int
}
type MediaSearchProvider interface {
	SearchMedia(context.Context, ProviderQuery) ([]ProviderCandidate, error)
}

func providerQuery(ctx context.Context, tx *sql.Tx, item, title, language string, page int) (ProviderQuery, error) {
	q := ProviderQuery{Title: title, Language: language, Page: page, Season: -1, Episode: -1}
	var entity, show int64
	var catalogTitle, showTitle string
	e := tx.QueryRowContext(ctx, `SELECT e.id,e.title,e.year,COALESCE(ep.show_id,0),COALESCE(sh.title,''),COALESCE(se.number,-1),COALESCE(ep.number,-1) FROM catalog_entities e LEFT JOIN catalog_episodes ep ON ep.entity_id=e.id LEFT JOIN catalog_entities sh ON sh.id=ep.show_id LEFT JOIN catalog_seasons se ON se.entity_id=ep.season_id WHERE e.public_id=pid_blob(?)`, item).Scan(&entity, &catalogTitle, &q.Year, &show, &showTitle, &q.Season, &q.Episode)
	if e != nil {
		return q, e
	}
	if title != "" {
		return q, nil
	}
	q.Title = catalogTitle
	if show != 0 {
		q.Title = showTitle
	}
	readIDs := func(id int64) (string, string, error) {
		rows, e := tx.QueryContext(ctx, `SELECT provider,provider_id FROM catalog_external_ids WHERE entity_id=? AND provider IN('imdb','tmdb') ORDER BY provider,provider_id LIMIT 4`, id)
		if e != nil {
			return "", "", e
		}
		defer rows.Close()
		imdb, tmdb := "", ""
		for rows.Next() {
			var p, v string
			if e = rows.Scan(&p, &v); e != nil {
				return "", "", e
			}
			if p == "imdb" {
				v = strings.TrimPrefix(v, "tt")
			}
			n, e := strconv.ParseInt(v, 10, 64)
			if e != nil || n <= 0 {
				continue
			}
			v = strconv.FormatInt(n, 10)
			if p == "imdb" && imdb == "" {
				imdb = v
			}
			if p == "tmdb" && tmdb == "" {
				tmdb = v
			}
		}
		return imdb, tmdb, rows.Err()
	}
	q.IMDbID, q.TMDBID, e = readIDs(entity)
	if e != nil {
		return q, e
	}
	if show != 0 && q.IMDbID == "" && q.TMDBID == "" {
		q.ParentIMDbID, q.ParentTMDBID, e = readIDs(show)
	}
	return q, e
}
func providerLanguage(language string) string {
	key := strings.ToLower(language)
	switch {
	case key == "pt-br" || key == "pob":
		return "pt-br"
	case key == "zh-tw" || key == "zh-hant" || key == "cht":
		return "zh-tw"
	case key == "zh-cn" || key == "zh-hans" || key == "chs":
		return "zh-cn"
	}
	return subtitleLanguageKey(language)
}
