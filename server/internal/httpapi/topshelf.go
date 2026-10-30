package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// The tvOS Top Shelf extension runs outside the app, in a process that cannot
// hold the app's session and is woken by the system at times the person is not
// present. It therefore reads one narrow, read-only feed with its own token.
//
// The token is issued to a registered device by a signed-in session, is bound to
// the account, the profile and that device, lives thirty days (the app renews it
// whenever it is opened), and can fetch nothing
// but this feed and the artwork it names. It is not a session: it cannot browse,
// cannot play, and cannot be exchanged for one.

const topShelfSections = 4
const topShelfEntries = 10
const topShelfLifetime = identity.MaxTopShelfLifetime

// TopShelfEntry is one card. The URLs are absolute paths on this server and carry
// the feed token, so the extension needs no credential of its own to draw them.
type TopShelfEntry struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	ShowTitle string `json:"showTitle,omitempty"`
	// These are the Home card's supplied facts. Nil numbers remain omitted;
	// a genuine season zero is preserved. Clients never parse Subtitle.
	SeasonNumber   *int   `json:"seasonNumber,omitempty"`
	EpisodeNumber  *int   `json:"episodeNumber,omitempty"`
	AbsoluteNumber *int   `json:"absoluteNumber,omitempty"`
	DateBased      bool   `json:"dateBased,omitempty"`
	Special        bool   `json:"special,omitempty"`
	AirDate        string `json:"airDate,omitempty"`
	Subtitle       string `json:"subtitle,omitempty"`
	ImageURL       string `json:"imageUrl,omitempty"`
	DisplayURL     string `json:"displayUrl"`
	PlayURL        string `json:"playUrl,omitempty"`
	ProgressPct    int    `json:"progressPercent,omitempty"`
}

// TopShelfSection is one shelf. Shape mirrors the home row it came from so the
// extension can pick a carousel or an inset banner without guessing.
type TopShelfSection struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	TitleText catalog.ServerText `json:"titleText"`
	Shape     string             `json:"shape"`
	Entries   []TopShelfEntry    `json:"entries"`
}

// TopShelfFeed is the whole document.
type TopShelfFeed struct {
	ServerName string            `json:"serverName"`
	ServerID   string            `json:"serverId"`
	ProfileID  string            `json:"profileId"`
	ExpiresAt  string            `json:"expiresAt"`
	Sections   []TopShelfSection `json:"sections"`
}

// TopShelfToken is what a signed-in session hands the extension.
type TopShelfToken struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
	FeedURL   string `json:"feedUrl"`
}

func (d Dependencies) topShelfRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "POST /v1/topshelf/token", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			InstallationID string `json:"installationId"`
		}
		if e = decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.IssueTopShelfToken(r.Context(), p, body.InstallationID, topShelfLifetime)
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 201, TopShelfToken{Token: out.Token, ExpiresAt: out.ExpiresAt, FeedURL: "/v1/topshelf"})
	})
	d.httpHandle(mux, "GET /v1/topshelf", func(w http.ResponseWriter, r *http.Request) {
		grant, e := d.Identity.TopShelfGrant(r.Context(), topShelfBearer(r))
		if e != nil {
			failure(w, e)
			return
		}
		p := identity.Principal{Viewer: grant.Viewer, Hash: grant.Hash, Epoch: grant.Epoch}
		libraries, e := d.topShelfLibraries(r.Context(), p)
		if e != nil {
			failure(w, e)
			return
		}
		restrictions, fence, e := d.viewerRestrictions(r, p)
		if e != nil {
			failure(w, e)
			return
		}
		viewer := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: fence, Libraries: libraries, Restrictions: restrictions}
		document, e := d.Catalog.WithContext(r.Context()).HomeRows(catalog.HomeRequest{Viewer: viewer, ServerID: d.Identity.ID(), Profile: identity.PersonalKey(p.Viewer), ViewerFence: fence, Libraries: libraries, Restrictions: restrictions, Limit: topShelfEntries, Rows: topShelfSections})
		if e != nil {
			failure(w, e)
			return
		}
		out := TopShelfFeed{ServerName: d.Identity.Name(), ServerID: d.Identity.ID(), ProfileID: p.ProfileID, ExpiresAt: grant.ExpiresAt, Sections: []TopShelfSection{}}
		for _, row := range document.Rows {
			if len(row.Entries) == 0 || len(out.Sections) >= topShelfSections {
				continue
			}
			section := topShelfSection(row)
			for _, entry := range row.Entries {
				if len(section.Entries) >= topShelfEntries {
					break
				}
				capability, err := d.Identity.TopShelfArtworkCapability(grant.Token, entry.ID)
				if err != nil {
					failure(w, err)
					return
				}
				section.Entries = append(section.Entries, topShelfEntry(entry, capability))
			}
			out.Sections = append(out.Sections, section)
		}
		// The feed is personal and short-lived; a shared cache must never hold it.
		w.Header().Set("Cache-Control", "private, no-store")
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/topshelf/art/{id}", func(w http.ResponseWriter, r *http.Request) {
		grant, e := d.Identity.ResolveTopShelfArtwork(r.Context(), r.URL.Query().Get("grant"), r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		p := identity.Principal{Viewer: grant.Viewer, Hash: grant.Hash, Epoch: grant.Epoch}
		library, e := d.Catalog.WithContext(r.Context()).LibraryForItem(r.PathValue("id"))
		if e == nil {
			e = d.allowedLibrary(r.Context(), p, library)
		}
		if e == nil {
			e = d.restrictedItem(r, p, r.PathValue("id"))
		}
		if e != nil {
			if errors.Is(e, identity.ErrUnauthorized) {
				e = sql.ErrNoRows
			}
			failure(w, e)
			return
		}
		if d.Metadata == nil {
			failure(w, errors.New("artwork is unavailable on this server"))
			return
		}
		size := artworkSize(r)
		if size != metadata.ArtworkSmall && size != metadata.ArtworkLarge {
			failure(w, metadata.ErrRepairInput)
			return
		}
		file, mime, e := d.topShelfArtwork(r.Context(), r.PathValue("id"), size)
		if e != nil {
			if errors.Is(e, metadata.ErrArtworkPending) {
				w.Header().Set("Retry-After", "5")
			}
			failure(w, e)
			return
		}
		defer file.Close()
		info, e := file.Stat()
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=300")
		digest := strings.TrimSuffix(filepath.Base(file.Name()), ".img")
		if len(digest) == 64 && strings.Trim(digest, "0123456789abcdef") == "" {
			w.Header().Set("ETag", `"`+digest+`"`)
		}
		http.ServeContent(w, r, "artwork", info.ModTime(), file)
	})
}

// Episode cards are posters: prefer season artwork, then the show, then the
// item's own poster. The grant already authorizes the specific episode.
func (d Dependencies) topShelfArtwork(ctx context.Context, item string, size metadata.ArtworkSize) (*os.File, string, error) {
	var season, show string
	err := dbwork.ReadHandle(ctx, d.DB).QueryRowContext(ctx, `SELECT COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=ep.season_id),''),(SELECT pid(public_id) FROM catalog_entities WHERE id=ep.show_id) FROM catalog_entities e JOIN catalog_episodes ep ON ep.entity_id=e.id WHERE e.public_id=pid_blob(?)`, item).Scan(&season, &show)
	if err != nil && err != sql.ErrNoRows {
		return nil, "", err
	}
	if err == nil {
		for _, target := range []metadata.RepairTarget{{Kind: "season", ID: season}, {Kind: "show", ID: show}} {
			if target.ID == "" {
				continue
			}
			f, mime, e := d.Metadata.EntityArtworkVariant(ctx, target, "poster", "", size, "")
			if e == nil {
				return f, mime, nil
			}
			if !errors.Is(e, metadata.ErrArtworkPending) && !errors.Is(e, sql.ErrNoRows) {
				return nil, "", e
			}
		}
	}
	return d.Metadata.EntityArtworkVariant(ctx, metadata.RepairTarget{Kind: "item", ID: item}, "poster", "", size, "")
}

func topShelfSection(row catalog.HomeRow) TopShelfSection {
	return TopShelfSection{ID: row.ID, Title: row.Title, TitleText: row.TitleText, Shape: row.ArtworkShape, Entries: []TopShelfEntry{}}
}

func topShelfEntry(entry catalog.ContentEntry, token string) TopShelfEntry {
	out := TopShelfEntry{
		ID: entry.ID, Kind: entry.Kind, Title: entry.Title, Subtitle: entry.Subtitle,
		SeasonNumber: entry.SeasonNumber, EpisodeNumber: entry.EpisodeNumber, AbsoluteNumber: entry.AbsoluteNumber,
		DateBased: entry.DateBased, Special: entry.Special, AirDate: entry.AirDate,
		DisplayURL: "portico://item/" + entry.ID,
	}
	if entry.Show != nil {
		out.ShowTitle = entry.Show.Name
	}
	if entry.PosterURL != "" || entry.Kind == "episode" {
		out.ImageURL = "/v1/topshelf/art/" + entry.ID + "?grant=" + token + "&w=400"
	}
	if entry.Playback != nil {
		out.PlayURL = "portico://play/" + entry.ID
	}
	if entry.ProgressSeconds != nil && entry.Duration != nil && *entry.Duration > 0 {
		percent := int(*entry.ProgressSeconds / *entry.Duration * 100)
		if percent > 0 && percent < 100 {
			out.ProgressPct = percent
		}
	}
	return out
}

// topShelfLibraries resolves what the feed's profile may see. It re-runs the same
// per-library decision every other read makes; the feed token widens nothing.
func (d Dependencies) topShelfLibraries(ctx context.Context, p identity.Principal) ([]string, error) {
	libraries, e := d.Catalog.WithContext(ctx).Libraries()
	if e != nil {
		return nil, e
	}
	ids := []string{}
	names := make([]string, 0, len(libraries))
	for _, library := range libraries {
		names = append(names, library.ID)
	}
	allowed, e := d.allowedLibrarySet(ctx, p, names)
	if e != nil {
		return nil, e
	}
	for _, library := range libraries {
		if allowed[library.ID] {
			ids = append(ids, library.ID)
		}
	}
	return ids, nil
}

// The JSON feed accepts its credential only in the Authorization header.
func topShelfBearer(r *http.Request) string { return directBearer(r) }
