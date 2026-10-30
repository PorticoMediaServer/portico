package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// Names are read relative to the admitted source. The machine's mount path
// never contributes a title, year, edition or provider hint.
func movieFileReadingTx(ctx context.Context, tx *sql.Tx, library string, asset int64, path string) (MovieFile, error) {
	source, relative, err := assetSourceTx(ctx, tx, library, asset)
	if err != nil {
		return MovieFile{}, err
	}
	if relative == "" {
		relative = filepath.Base(path)
	}
	var revision, token string
	if err = tx.QueryRowContext(ctx, `SELECT a.token,COALESCE((SELECT revision FROM inventory_objects WHERE asset_id=a.token AND source_id=? AND relative_path=? AND retired=0 ORDER BY id LIMIT 1),'') FROM catalog_assets a WHERE a.id=?`, source, relative, asset).Scan(&token, &revision); err != nil {
		return MovieFile{}, err
	}
	embedded, err := VideoNameTx(ctx, tx, token, revision)
	if err != nil {
		return MovieFile{}, err
	}
	prefs, err := LibraryPreferencesTx(ctx, tx, library)
	if err != nil {
		return MovieFile{}, err
	}
	if prefs.TitleSource == "names" {
		embedded = naming.Embedded{}
	}
	r := naming.ReadWithEmbedded(relative, "movie", embedded)
	title, year := r.PreferredTitle()
	f := MovieFile{Library: library, Asset: asset, Path: path, Title: title, Year: year, Provider: nameProviderIDs(r, "movie")}
	if len(r.Editions) > 0 {
		f.Edition = r.Editions[0].Value
	}
	if len(r.Parts) > 0 {
		f.Part = r.Parts[0].Value
	}
	return f, nil
}

func nameProviderIDs(r naming.Reading, kind string) []compactcatalog.ProviderIdentity {
	out := []compactcatalog.ProviderIdentity{}
	preferred, _ := r.PreferredTitle()
	fileWins := false
	for _, t := range r.Titles {
		if (t.Source == naming.FileName || t.Source == naming.EmbeddedMetadata) && t.Confidence >= .8 {
			fileWins = true
			break
		}
	}
	seen := map[string]bool{}
	for _, e := range r.ProviderIDs {
		if fileWins && e.Source != naming.FileName && e.Source != naming.EmbeddedMetadata {
			agrees := false
			for _, t := range r.Titles {
				if t.Path == e.Path && compactcatalog.IdentityText(t.Value) == compactcatalog.IdentityText(preferred) {
					agrees = true
				}
			}
			if !agrees {
				continue
			}
		}
		if seen[e.Value.Provider] {
			continue
		}
		seen[e.Value.Provider] = true
		providerKind := kind
		if e.Value.Provider == "imdb" {
			providerKind = "title"
		}
		if e.Value.Provider == "anilist" {
			providerKind = "anime"
		}
		out = append(out, compactcatalog.ProviderIdentity{Provider: e.Value.Provider, Kind: providerKind, ID: e.Value.ID})
	}
	return out
}
