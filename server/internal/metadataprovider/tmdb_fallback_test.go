package metadataprovider

import "testing"

func fallbackFixture() (ScreenRecord, tmdbScreen) {
	var v tmdbScreen
	v.Language = "ja"
	v.Translations.Translations = []tmdbTranslation{{Language: "en"}, {Language: "ja"}, {Language: "fr"}}
	v.Translations.Translations[0].Data.Title, v.Translations.Translations[0].Data.Overview, v.Translations.Translations[0].Data.Tagline = "Spirited Away", "A girl enters a spirit world.", "English tagline"
	v.Translations.Translations[1].Data.Title, v.Translations.Translations[1].Data.Overview = "千と千尋の神隠し", "Japanese overview"
	// French exists as a translation but has no title or overview of its own.
	return ScreenRecord{Title: "Spirited Away", OriginalTitle: "千と千尋の神隠し"}, v
}

// Server › Metadata › Fallback language fills what the metadata language has
// no translation for, from English or the original language, or leaves it.
func TestTMDBFallbackLanguage(t *testing.T) {
	r, v := fallbackFixture()
	fillFallback(&r, v, "fr-FR", "en")
	if r.Title != "Spirited Away" || r.Overview != "A girl enters a spirit world." || r.Tagline != "English tagline" {
		t.Fatalf("english fallback: %+v", r)
	}
	r, v = fallbackFixture()
	fillFallback(&r, v, "fr-FR", "original")
	if r.Title != "千と千尋の神隠し" || r.Overview != "Japanese overview" || r.Tagline != "" {
		t.Fatalf("original fallback: %+v", r)
	}
	r, v = fallbackFixture()
	fillFallback(&r, v, "fr-FR", "blank")
	if r.Overview != "" || r.Title != "Spirited Away" {
		t.Fatalf("blank leaves gaps: %+v", r)
	}
	// A field the requested language has is never replaced.
	r, v = fallbackFixture()
	r.Overview = "Résumé"
	v.Translations.Translations[2].Data.Title = "Le Voyage de Chihiro"
	r.Title = "Le Voyage de Chihiro"
	fillFallback(&r, v, "fr-FR", "en")
	if r.Overview != "Résumé" || r.Title != "Le Voyage de Chihiro" {
		t.Fatalf("translated fields kept: %+v", r)
	}
	// Asking for the fallback language itself changes nothing.
	r, v = fallbackFixture()
	fillFallback(&r, v, "en-US", "en")
	if r.Overview != "" {
		t.Fatalf("same language: %+v", r)
	}
}
