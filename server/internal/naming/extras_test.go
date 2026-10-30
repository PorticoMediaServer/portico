package naming

import "testing"

func TestExtrasCorpus(t *testing.T) {
	folders := []struct{ name, kind string }{
		{"Trailers", "trailer"}, {"Behind The Scenes", "behind_the_scenes"}, {"Behind-The-Scenes", "behind_the_scenes"}, {"Behind_The_Scenes", "behind_the_scenes"}, {"Deleted Scenes", "deleted_scene"}, {"Featurettes", "featurette"}, {"Interviews", "interview"}, {"Scenes", "scene"}, {"Shorts", "short"}, {"Other", "other"}, {"Extras", "other"}, {"Bonus", "other"}, {"Supplementals", "other"},
	}
	for _, folder := range folders {
		for _, prefix := range []string{"Film (2020)", "Show/Season 1", "Show"} {
			for _, file := range []string{"Set.mkv", "Design.mp4", "Interview.avi"} {
				p := prefix + "/" + folder.name + "/" + file
				e := ReadExtras(p, "movie")
				if len(e) != 1 || e[0].Value.Kind != folder.kind || e[0].Value.ParentPath != prefix {
					t.Errorf("%s: %+v", p, e)
				}
			}
		}
	}
	for _, entry := range extraSuffixes {
		p := "Film (2020)/Film" + entry.suffix + ".mkv"
		e := ReadExtras(p, "movie")
		if len(e) != 1 || e[0].Value.Kind != entry.kind || e[0].Source != FileName {
			t.Error(p, e)
		}
	}
	for _, p := range []string{"Extras/Season 1/Extras - S01E01.mkv", "Shorts/Pixar - Bao (2018).mkv", "Trailers/Film (2020).mkv", "Show/Specials/S00E01.mkv", "Extras/Extras - S01E01.mkv", "Category/Extras/Extras - S01E01.mkv", "Show/Extras/Other Show/Season 1/S01E01.mkv"} {
		if len(ReadExtras(p, "tv")) != 0 {
			t.Error(p)
		}
	}
	r := Read("Film (2020)/Featurettes/Film-trailer.mkv", "movie")
	if r.Extras[0].Source != FileName || r.Extras[0].Value.Kind != "trailer" {
		t.Fatal(r.Extras)
	}
	for _, p := range []string{"Film (2020)/trailer.mkv", "Film (2020)/Film.trailer.mkv"} {
		e := ReadExtras(p, "movie")
		if len(e) != 1 || e[0].Value.Kind != "trailer" {
			t.Error(p, e)
		}
	}
	if len(ReadExtras("Trailer.mkv", "movie")) != 0 || len(ReadExtras("Trailer (2020)/Trailer.mkv", "movie")) != 0 {
		t.Fatal("loose movie named Trailer classified as extra")
	}
}
