package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// Sidecar extras are recognised from the owning media folder, never from
// provider data. Classification is a pure function of the path so inventory can
// fence one small additional publication without a second filesystem pass.

type ExtraClassification struct {
	Kind       string
	Title      string
	ParentPath string
}

func ExtraKinds() []string {
	return []string{"trailer", "featurette", "deleted_scene", "behind_the_scenes", "interview", "scene", "short", "other"}
}

func extraKindLabel(kind string) string {
	switch kind {
	case "trailer":
		return "Trailers"
	case "featurette":
		return "Featurettes"
	case "deleted_scene":
		return "Deleted Scenes"
	case "behind_the_scenes":
		return "Behind the Scenes"
	case "interview":
		return "Interviews"
	case "scene":
		return "Scenes"
	case "short":
		return "Shorts"
	}
	return "Extras"
}

// ClassifyExtra is the catalogue adapter for pure extras evidence.
func ClassifyExtra(root, path string) (ExtraClassification, bool) {
	return ClassifyExtraFor("movie", root, path)
}
func ClassifyExtraFor(kind, root, path string) (ExtraClassification, bool) {
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return ExtraClassification{}, false
	}
	extras := naming.ReadExtras(filepath.ToSlash(relative), kind)
	if len(extras) == 0 {
		return ExtraClassification{}, false
	}
	e := extras[0].Value
	return ExtraClassification{Kind: e.Kind, Title: naming.ExtraTitle(path), ParentPath: filepath.Join(root, filepath.FromSlash(e.ParentPath))}, true
}

// publishExtraTx records an extra as an ordinary item so it plays through the
// normal item path, plus one relationship row naming its owning media folder.
func (s *Service) publishExtraTx(ctx context.Context, tx *sql.Tx, source LibrarySource, asset, path string, extra ExtraClassification) error {
	preferences, err := LibraryPreferencesTx(ctx, tx, source.LibraryID)
	if err != nil {
		return err
	}
	if !preferences.FindExtras {
		return nil
	}
	relative, err := filepath.Rel(source.ResolvedPath, path)
	if err != nil {
		return err
	}
	handle, err := compactcatalog.LibraryTx(ctx, tx, source.LibraryID)
	if err != nil {
		return err
	}
	assetID, err := compactcatalog.AssetByTokenTx(ctx, tx, asset)
	if err != nil {
		return err
	}
	owner, err := ResolveExtraOwnerTx(ctx, tx, source.LibraryID, path, extra.ParentPath)
	if err != nil {
		return err
	}
	var ownerID any
	if owner != 0 {
		ownerID = owner
	}
	root, err := compactcatalog.LibraryRootTx(ctx, tx, handle)
	if err != nil {
		return err
	}
	item, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Extra, Key: compactcatalog.ExtraKey(root, path), Title: extra.Title, Added: catalogedNow()})
	if err != nil {
		return err
	}
	if err = compactcatalog.LinkAssetTx(ctx, tx, item, assetID, compactcatalog.Link{}); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO item_extras(item_id,library_id,parent_path,owner_id,kind,title,asset_id,relative_path) VALUES(?,?,?,?,?,?,?,?)
 ON CONFLICT(item_id) DO UPDATE SET library_id=excluded.library_id,parent_path=excluded.parent_path,owner_id=excluded.owner_id,kind=excluded.kind,title=excluded.title,asset_id=excluded.asset_id,relative_path=excluded.relative_path`,
		item, source.LibraryID, extra.ParentPath, ownerID, extra.Kind, extra.Title, asset, filepath.ToSlash(relative))
	return err
}

type DetailExtra struct {
	Type  string         `json:"type"`
	Label string         `json:"label"`
	Items []ContentEntry `json:"items"`
}

// itemExtras resolves the extras that sit beside an item's own media — all of
// them: they are the title's own. The owning folder is the join key, so extras
// survive the parent being re-identified.
func (s *Service) itemExtras(profile string, item Item) ([]DetailExtra, error) {
	out := []DetailExtra{}
	if item.Kind == "extra" {
		return out, nil
	}
	grouped := map[string][]string{}
	rows, e := s.read().Query(`SELECT pid(i.public_id),e.kind FROM item_extras e JOIN catalog_entities i ON i.id=e.item_id JOIN catalog_entities owner ON owner.id=e.owner_id WHERE owner.public_id=pid_blob(?) AND e.library_id=? AND `+homeVisible("i.id")+` ORDER BY e.kind,e.title COLLATE NOCASE,e.item_id`, item.ID, item.LibraryID)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var id, kind string
		if e = rows.Scan(&id, &kind); e != nil {
			rows.Close()
			return nil, e
		}
		grouped[kind] = append(grouped[kind], id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, kind := range ExtraKinds() {
		ids := grouped[kind]
		if len(ids) == 0 {
			continue
		}
		entries, e := s.homeEntries(profile, ids)
		if e != nil {
			return nil, e
		}
		out = append(out, DetailExtra{Type: kind, Label: extraKindLabel(kind), Items: entries})
	}
	return out, nil
}
