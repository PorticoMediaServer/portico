package api

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
)

// localeProvider records what language the real provider adapter is asked for; all requests
// still go to newShelf's local fake, never TMDB.
type localeProvider struct {
	metadata.Provider
	french, english atomic.Int64
}

func (p *localeProvider) Details(ctx context.Context, ref metadata.Ref, locale metadata.Locale) (metadata.Record, error) {
	switch locale.Language {
	case "fr":
		p.french.Add(1)
	case "en":
		p.english.Add(1)
	}
	return p.Provider.Details(ctx, ref, locale)
}

// A bulk language choice is saved, shown and used at refresh. Undo and clearing the choice
// restore inheritance, and a language the provider cannot use is refused beside the field.
func TestMetadataLanguageIsUsedAndCanBeUndone(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv", "Heat (1995).mkv")
	provider := &localeProvider{Provider: tmdb.New("test-token", sh.tmdb.URL)}
	sh.s.UseProvider(provider)
	films := get[ItemPage](t, sh.s, sh.owner, "/v1/items?library="+sh.library+"&kind=movie")
	ids := []string{films.Items[0].ID, films.Items[1].ID}
	if job := sh.startJob(JobStart{Kind: jobEditItems, Target: JobTarget{Items: ids}, Params: JSON(`{"language":"fr"}`)}); job.State != "done" {
		t.Fatalf("bulk language: %+v", job)
	}
	read := func(id string) MetadataDocument {
		t.Helper()
		return get[MetadataDocument](t, sh.s, sh.owner, "/v1/admin/items/"+id+"/metadata")
	}
	for _, id := range ids {
		if f := read(id).Fields["language"]; f.Value != "fr" || f.Source != "edit" || !f.Locked {
			t.Fatalf("stored choice: %+v", f)
		}
	}
	if job := sh.startJob(JobStart{Kind: jobRefreshLibrary, Target: JobTarget{Library: sh.library}}); job.State != "done" || provider.french.Load() != 2 {
		t.Fatalf("refresh did not use both overrides: %+v, French %d", job, provider.french.Load())
	}
	// Undo the bulk edit on one film, as its own History entry promises.
	history := get[Page[MetadataHistoryEntry]](t, sh.s, sh.owner, "/v1/admin/items/"+ids[0]+"/metadata/history")
	sh.do("POST", "/v1/admin/items/"+ids[0]+"/metadata/history/"+history.Items[0].ID+"/undo", nil, nil, 200)
	if f := read(ids[0]).Fields["language"]; f.Value != nil || f.Locked {
		t.Fatalf("Undo did not restore inheritance: %+v", f)
	}
	doc := read(ids[1])
	sh.patch(ids[1], doc.Revision, MetadataPatch{Language: new("not-a-language")}, nil, 422)
	sh.patch(ids[1], doc.Revision, MetadataPatch{Language: new("")}, nil, 200)
	for _, id := range ids {
		sh.do("POST", "/v1/admin/items/"+id+"/metadata/refresh", nil, nil, 200)
	}
	if provider.english.Load() != 2 {
		t.Fatalf("inherited language was not used after Undo/clear: English %d", provider.english.Load())
	}
}
