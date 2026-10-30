package naming

import (
	"path"
	"strings"
)

var extraFolders = map[string]string{
	"extras": "other", "extra": "other", "other": "other", "featurettes": "featurette", "featurette": "featurette", "trailers": "trailer", "trailer": "trailer", "behind the scenes": "behind_the_scenes", "behind the scene": "behind_the_scenes", "deleted scenes": "deleted_scene", "deleted scene": "deleted_scene", "interviews": "interview", "interview": "interview", "scenes": "scene", "shorts": "short", "short": "short", "specials": "other", "bonus": "other", "bonus disc": "other", "deleted": "deleted_scene", "making of": "behind_the_scenes", "video extras": "other", "bonus features": "other", "supplemental": "other", "supplementals": "other", "extras and bonuses": "other",
}
var extraSuffixes = []struct{ suffix, kind string }{
	{"-trailer", "trailer"}, {".trailer", "trailer"}, {"-featurette", "featurette"}, {"-deleted", "deleted_scene"}, {"-deletedscenes", "deleted_scene"}, {"-behindthescenes", "behind_the_scenes"}, {"-interview", "interview"}, {"-scene", "scene"}, {"-short", "short"}, {"-other", "other"},
}

func extraFolder(s string, episodic bool) (string, bool) {
	s = strings.ToLower(strings.Join(strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(s)), " "))
	if episodic && s == "specials" {
		return "", false
	}
	k, ok := extraFolders[s]
	return k, ok
}

// ReadExtras requires a media owner below the source root. A top-level series
// called Extras or a category called Shorts is never swallowed. File suffix
// evidence wins an extras-folder disagreement, like all other file evidence.
func ReadExtras(relative, kind string) []Evidence[Extra] {
	p := path.Clean(slash(relative))
	parts := strings.Split(p, "/")
	if len(relative) > MaxPathBytes || strings.IndexByte(relative, 0) >= 0 || len(parts) > MaxDepth || p == ".." || strings.HasPrefix(p, "../") {
		return nil
	}
	name := strings.TrimSuffix(parts[len(parts)-1], path.Ext(parts[len(parts)-1]))
	lower := strings.ToLower(name)
	for _, s := range extraSuffixes {
		if strings.HasSuffix(lower, s.suffix) {
			return []Evidence[Extra]{ev(Extra{s.kind, path.Dir(p)}, FileName, p, .98)}
		}
	}
	if kind == "tv" || kind == "anime" {
		for _, loc := range episodeToken.FindAllStringIndex(name, 32) {
			if validMarker(name, loc) {
				return nil
			}
		}
		if dateToken.MatchString(name) {
			return nil
		}
	}
	if len(parts) >= 2 && (lower == "trailer" || lower == "trailers") {
		var owner Reading
		readComponent(&owner, parts[len(parts)-2], parts[len(parts)-2], ParentFolder, path.Dir(p), .8)
		ownerTitle, _ := owner.PreferredTitle()
		if !strings.EqualFold(ownerTitle, "trailer") && !strings.EqualFold(ownerTitle, "trailers") {
			return []Evidence[Extra]{ev(Extra{"trailer", path.Dir(p)}, FileName, p, .85)}
		}
	}
	first := -1
	k := ""
	at := -1
	for i := 1; i < len(parts)-1; i++ {
		if v, ok := extraFolder(parts[i], kind == "tv" || kind == "anime"); ok {
			// A named sub-folder interrupts the extras chain: Show/Extras/Another
			// Show/Season 1 is not an extra owned by Show.
			if first < 0 {
				first = i
			}
			k = v
			at = i
		} else if first >= 0 {
			if _, ok := SeasonFolder(parts[i]); ok {
				return nil
			}
		}
	}
	if first >= 0 {
		return []Evidence[Extra]{ev(Extra{k, strings.Join(parts[:first], "/")}, source(len(parts)-1-at), strings.Join(parts[:at+1], "/"), .9)}
	}
	return nil
}

func ExtraTitle(filename string) string {
	s := strings.TrimSuffix(path.Base(slash(filename)), path.Ext(filename))
	for _, e := range extraSuffixes {
		if strings.HasSuffix(strings.ToLower(s), e.suffix) {
			s = s[:len(s)-len(e.suffix)]
			break
		}
	}
	s = clean(s)
	if s == "" {
		return "Extra"
	}
	return s
}
