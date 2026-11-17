package tmdb

import (
	"context"
	"net/url"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// Trending reads the first 50 pages of TMDB's daily combined order, films and shows only.
// The bounded daily walk keeps the provider input near the top of its ranking.
func (c *Client) Trending(ctx context.Context) ([]metadata.Ref, error) {
	out := []metadata.Ref{}
	seen := map[metadata.Ref]bool{}
	for page := 1; page <= 50; page++ {
		var answer struct {
			Page       int `json:"page"`
			TotalPages int `json:"total_pages"`
			Results    []struct {
				ID        int64  `json:"id"`
				MediaType string `json:"media_type"`
			} `json:"results"`
		}
		if err := c.get(ctx, "/trending/all/day", url.Values{"page": {strconv.Itoa(page)}}, &answer); err != nil {
			return nil, err
		}
		for _, r := range answer.Results {
			kind := r.MediaType
			if kind == "tv" {
				kind = "show"
			}
			if r.ID <= 0 || (kind != "movie" && kind != "show") {
				continue
			}
			ref := metadata.Ref{Provider: "tmdb", Kind: kind, ID: strconv.FormatInt(r.ID, 10)}
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
		if len(answer.Results) == 0 || page >= answer.TotalPages {
			break
		}
	}
	return out, nil
}
