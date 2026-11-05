package scan

import "testing"

func basicInput() BasicInput {
	return BasicInput{
		File: observation().Witness, Available: true, PolicyRevision: 1, ProbeRecipe: "basic-v1",
		NFO:       LocalWitness{Observed: true, Revision: "nfo:01"},
		Artwork:   LocalWitness{Observed: true, Revision: "art:01"},
		Subtitles: LocalWitness{Observed: true, Revision: "sub:01"},
	}
}

func TestBasicUnchangedRescan(t *testing.T) {
	accepted := basicInput()
	if got := PlanBasic(accepted, &accepted); got != ReuseBasic {
		t.Fatalf("unchanged input starts another helper: %v", got)
	}
	if got := PlanBasic(accepted, nil); got != ProbeRequired {
		t.Fatalf("unaccepted input reused: %v", got)
	}
	// Known absence is reusable; a directory that was never observed is not.
	absent := accepted
	absent.NFO, absent.Artwork, absent.Subtitles = LocalWitness{Observed: true}, LocalWitness{Observed: true}, LocalWitness{Observed: true}
	if got := PlanBasic(absent, &absent); got != ReuseBasic {
		t.Fatalf("known local absence not reusable: %v", got)
	}
	for _, change := range []struct {
		name string
		edit func(*BasicInput)
	}{
		{"library", func(b *BasicInput) { b.File.Source.LibraryID++ }},
		{"source", func(b *BasicInput) { b.File.Source.SourceID += ":other" }},
		{"incarnation", func(b *BasicInput) { b.File.Source.Incarnation += ":new" }},
		{"source revision", func(b *BasicInput) { b.File.Source.Revision += ":new" }},
		{"path", func(b *BasicInput) { b.File.RelativePath = "Another.mkv" }},
		{"object identity", func(b *BasicInput) { b.File.ObjectIdentity += ":new" }},
		{"opaque revision", func(b *BasicInput) { b.File.Revision = "object:7" }},
		{"size", func(b *BasicInput) { b.File.Size++ }},
		{"mtime", func(b *BasicInput) { b.File.ModifiedNS++ }},
		{"policy", func(b *BasicInput) { b.PolicyRevision++ }},
		{"recipe", func(b *BasicInput) { b.ProbeRecipe += ":new" }},
		{"nfo", func(b *BasicInput) { b.NFO.Revision += ":new" }},
		{"art", func(b *BasicInput) { b.Artwork.Revision += ":new" }},
		{"subtitles", func(b *BasicInput) { b.Subtitles.Revision += ":new" }},
		{"unobserved nfo", func(b *BasicInput) { b.NFO.Observed = false }},
		{"unobserved art", func(b *BasicInput) { b.Artwork.Observed = false }},
		{"unobserved subtitles", func(b *BasicInput) { b.Subtitles.Observed = false }},
		{"missing recipe", func(b *BasicInput) { b.ProbeRecipe = "" }},
		{"missing policy", func(b *BasicInput) { b.PolicyRevision = 0 }},
		{"missing revision", func(b *BasicInput) { b.File.Revision = "" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			current := accepted
			change.edit(&current)
			if got := PlanBasic(current, &accepted); got != ProbeRequired {
				t.Fatalf("changed or unknown input reused: %v", got)
			}
			// Equally incomplete witnesses must never prove an unchanged scan.
			if !current.reusable() && PlanBasic(current, &current) != ProbeRequired {
				t.Fatal("two incomplete observations proved reuse")
			}
		})
	}
}

func TestBasicUnavailableRetainsEntry(t *testing.T) {
	if got := PlanBasic(BasicInput{}, nil); got != ProbeRequired {
		t.Fatalf("unknown input inferred unavailable: %v", got)
	}
	accepted := basicInput()
	current := accepted
	current.Available = false
	if got := PlanBasic(current, &accepted); got != SourceUnavailable {
		t.Fatalf("unavailable entry scheduled a probe: %v", got)
	}
	// An unavailable or missing canonical acceptance is not Basic proof on
	// reappearance, even when size/mtime look identical.
	if got := PlanBasic(accepted, &current); got != ProbeRequired {
		t.Fatalf("unavailable acceptance reused on reappearance: %v", got)
	}
}
