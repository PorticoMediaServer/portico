package scan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/jpeg" // pictures beside the media are measured, not changed
	_ "image/png"
	"path"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/naming"
)

// Pictures is where the scan puts a copy of each picture it finds beside the media, under the
// name the database then knows it by. The server's picture store is one.
type Pictures interface {
	Put(id string, data []byte) error
}

// maxPicture is the largest picture file a scan copies.
const maxPicture = 20 << 20

// pictureNames are the names a picture beside the media has, alone ("poster.jpg") or after
// the media file's name ("Heat (1995)-fanart.jpg"), and what each is for.
var pictureNames = map[string]db.ImageRole{
	"poster": db.ImagePoster, "folder": db.ImagePoster, "cover": db.ImagePoster, "movie": db.ImagePoster, "show": db.ImagePoster,
	"fanart": db.ImageBackdrop, "backdrop": db.ImageBackdrop, "background": db.ImageBackdrop, "art": db.ImageBackdrop,
	"logo": db.ImageLogo, "clearlogo": db.ImageLogo, "banner": db.ImageBanner, "thumb": db.ImageThumb, "landscape": db.ImageThumb,
}

// ownSidecar reports whether a sidecar file's name starts with a media file's name
// ("Heat (1995).nfo", "Heat (1995)-poster.jpg"), and what follows it.
func ownSidecar(name, mediaStem string) (rest string, ok bool) {
	s := stem(name)
	if s == mediaStem {
		return "", true
	}
	if strings.HasPrefix(s, mediaStem+"-") || strings.HasPrefix(s, mediaStem+".") {
		return s[len(mediaStem)+1:], true
	}
	return "", false
}

// seasonPicture reads the season a picture in a show's folder is for: "season01-poster.jpg",
// "season-specials-poster.jpg", "season02.jpg".
func seasonPicture(name string) (season int, role db.ImageRole, ok bool) {
	s, found := strings.CutPrefix(stem(name), "season")
	if !found {
		return 0, 0, false
	}
	number, kind, _ := strings.Cut(s, "-")
	if number == "" && strings.HasPrefix(kind, "specials") {
		number, kind = "0", strings.TrimPrefix(strings.TrimPrefix(kind, "specials"), "-")
	}
	for _, c := range number {
		if c < '0' || c > '9' {
			return 0, 0, false
		}
		season = season*10 + int(c-'0')
	}
	if number == "" || len(number) > 4 {
		return 0, 0, false
	}
	if kind == "" {
		return season, db.ImagePoster, true
	}
	role, ok = pictureNames[kind]
	return season, role, ok
}

// picture copies one picture file into the picture store and describes it for the database.
// A file that cannot be read, or is too large, is passed over.
func (r *run) picture(file *found, role db.ImageRole) (db.ImportImage, bool) {
	if r.s.Pictures == nil || r.lib.Options.Depth == db.DepthFiles {
		return db.ImportImage{}, false
	}
	raw, err := readSmall(r.ctx, r.src, file.rel, maxPicture)
	if err != nil || len(raw) == 0 {
		return db.ImportImage{}, false
	}
	sum := sha256.Sum256(raw)
	id := hex.EncodeToString(sum[:])
	if err := r.s.Pictures.Put(id, raw); err != nil {
		r.s.log().Warn("picture not copied", "file", r.src.LocalPath(file.rel), "error", err)
		return db.ImportImage{}, false
	}
	ref := file.ref()
	out := db.ImportImage{Role: role, Source: db.ImageFromFile, FileHash: id, File: &ref}
	if config, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
		out.Width, out.Height = config.Width, config.Height
	}
	return out, true
}

// pictures are the pictures among a folder's files that belong to one title. mediaStem is the
// name of the title's media file, whose own pictures ("<name>.jpg", "<name>-fanart.jpg") are
// taken, a bare "<name>.jpg" being plain; "" takes none. alone says the title has the folder
// to itself, so the folder's own pictures ("poster.jpg") are its too. changedOnly leaves out
// files that are as the last scan stored them.
func (r *run) pictures(files []*found, mediaStem string, plain db.ImageRole, alone, changedOnly bool) []db.ImportImage {
	var out []db.ImportImage
	for _, file := range files {
		if file.role != db.FileArtwork || changedOnly && file.state == same {
			continue
		}
		role, ok := db.ImageRole(0), false
		if rest, own := ownSidecar(file.entry.Name, mediaStem); own && mediaStem != "" {
			if role, ok = pictureNames[rest]; rest == "" {
				role, ok = plain, true
			}
		} else if alone {
			role, ok = pictureNames[stem(file.entry.Name)]
		}
		if !ok {
			continue
		}
		if image, ok := r.picture(file, role); ok {
			out = append(out, image)
		}
	}
	return out
}

// showLocal is what a show's own folder holds for the show and its seasons.
type showLocal struct {
	show    []db.ImportImage
	seasons map[int][]db.ImportImage
}

// showPictures reads the pictures in a show's own folder. files is the folder's listing when
// the scan is in that folder now; otherwise the folder is listed, once a scan.
func (r *run) showPictures(folder string, files []*found, changedOnly bool) showLocal {
	listed := files == nil
	if listed {
		if cached, ok := r.shows[folder]; ok {
			return cached
		}
		if r.s.Pictures != nil {
			entries, _ := r.src.List(r.ctx, folder)
			for _, e := range entries {
				if !e.Dir && role(r.lib.Kind, e.Name) == db.FileArtwork {
					files = append(files, &found{entry: e, rel: path.Join(folder, e.Name), role: db.FileArtwork, state: added})
				}
			}
		}
	}
	local := showLocal{show: r.pictures(files, "", 0, true, changedOnly), seasons: map[int][]db.ImportImage{}}
	for _, file := range files {
		if file.role != db.FileArtwork || changedOnly && file.state == same {
			continue
		}
		if season, role, ok := seasonPicture(file.entry.Name); ok {
			if image, ok := r.picture(file, role); ok {
				local.seasons[season] = append(local.seasons[season], image)
			}
		}
	}
	if listed {
		r.shows[folder] = local
	}
	return local
}

func seasonKey(season int) string { return "s" + strconv.Itoa(season) }

// sidecarChanged reports whether a sidecar of the folder that could describe this title (its
// NFO file, a picture) was added, changed or removed since the last scan. alone says the
// title has the folder to itself, so the folder's own sidecars are its too.
func (f *folder) sidecarChanged(mediaStem string, alone bool) bool {
	for _, name := range f.changed {
		if _, own := ownSidecar(name, mediaStem); own || alone {
			return true
		}
	}
	return false
}

// rereadUnits are the titles whose details live in this folder while their files are deeper:
// a show's tvshow.nfo and pictures, and the pictures of its seasons. They re-read a title
// that is already stored and never make one.
func (r *run) rereadUnits(f *folder) []unit {
	// A folder seen for the first time holds no stored title: its files make theirs below.
	if len(f.changed) == 0 || f.rel == "" || f.fresh {
		return nil
	}
	var items []db.ImportItem
	if number, isSeason := naming.SeasonFolder(path.Base(f.rel)); isSeason {
		// A season's own folder: its poster.
		show := showFolder(path.Join(f.rel, "x"))
		if images := r.pictures(f.files, "", 0, true, true); len(images) > 0 && show != "" {
			key := db.FolderKey(r.path.ID, show)
			items = append(items, db.ImportItem{Kind: db.KindSeason, Parent: &key, ChildKey: seasonKey(number), Reread: true, Images: images})
		}
		return []unit{{items: items}}
	}
	local := r.showPictures(f.rel, f.files, true)
	show := db.ImportItem{Kind: db.KindShow, Keys: []db.ItemKey{db.FolderKey(r.path.ID, f.rel)}, Reread: true, Images: local.show}
	describe(&show, r.nfo("show", path.Join(f.rel, "tvshow.nfo")), "show")
	// Only the folder key finds it: the show is the one that owns this folder, whatever ids
	// the file now carries.
	show.Keys = []db.ItemKey{db.FolderKey(r.path.ID, f.rel)}
	items = append(items, show)
	for season, images := range local.seasons {
		key := db.FolderKey(r.path.ID, f.rel)
		items = append(items, db.ImportItem{Kind: db.KindSeason, Parent: &key, ChildKey: seasonKey(season), Reread: true, Images: images})
	}
	return []unit{{items: items}}
}
