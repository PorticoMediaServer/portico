package writer

import (
	"errors"
	"strings"
	"testing"
)

func iaConfigurationRequestFixture() (CommandIdentity, iaConfigurationRequest) {
	id, retire := iaRetirementRequestFixture()
	s := iaConfigurationSource{id: "source", name: "Source", incarnation: "original", generation: 1, configuredRoot: "/configured/é", resolvedRoot: "/resolved/é", rootIdentity: "native", kind: "local", classification: "local", enabled: true, identityConfirmed: true}
	return id, iaConfigurationRequest{actor: retire.actor, libraryKey: "library", libraryName: "Library", libraryKind: "movie", action: "create_library", sources: []iaConfigurationSource{s}, scanAfter: true}
}

func TestIAConfigurationOwnsCompleteRequest(t *testing.T) {
	id, r := iaConfigurationRequestFixture()
	c, err := newIAConfigurationCommand(IAConfigureLibrary, id, r)
	if err != nil || c.checkOriginal() != nil {
		t.Fatal(err)
	}
	r.sources[0].name = "caller mutation"
	if c.request.sources[0].name != "Source" || c.checkOriginal() != nil {
		t.Fatal("input slice aliases command")
	}
	for _, mutate := range []func(*iaConfigurationRequest){
		func(v *iaConfigurationRequest) { v.sources[0].resolvedRoot += "x" },
		func(v *iaConfigurationRequest) { v.sources[0].rootIdentity += "x" },
		func(v *iaConfigurationRequest) { v.sources[0].name += "x" },
		func(v *iaConfigurationRequest) { v.actor.accountEpoch++ },
		func(v *iaConfigurationRequest) { v.scanAfter = !v.scanAfter },
		func(v *iaConfigurationRequest) {
			v.sources[0].remoteSourceID = "remote"
			v.sources[0].remoteSourceGeneration = 3
		},
	} {
		changed := c.request
		changed.sources = append([]iaConfigurationSource(nil), changed.sources...)
		mutate(&changed)
		other, err := newIAConfigurationCommand(IAConfigureLibrary, id, changed)
		if err != nil || other.digest == c.digest {
			t.Fatal("complete input fact omitted", err)
		}
	}
	c.request.sources[0].generation++
	if !errors.Is(c.checkOriginal(), ErrConflict) {
		t.Fatal("mutated original accepted")
	}
}

func TestIAConfigurationSourceUpdatesPreserveOriginal(t *testing.T) {
	id, r := iaConfigurationRequestFixture()
	r.libraryID, r.expectedConfigurationRevision, r.libraryName, r.libraryKind, r.action = 7, 5, "", "", "update_source"
	before := iaConfigurationPriorSource{iaConfigurationSource: r.sources[0], health: "ready"}
	r.before = &before
	r.sources[0].generation++
	c, err := newIAConfigurationCommand(IAConfigureSource, id, r)
	if err != nil {
		t.Fatal("ordinary settings update must retain source incarnation", err)
	}
	before.rootIdentity = "mutated caller witness"
	if c.checkOriginal() != nil || c.request.before.rootIdentity != "native" {
		t.Fatal("original pointer aliases caller")
	}
	r = c.request
	r.sources = append([]iaConfigurationSource(nil), c.request.sources...)
	r.replacement = true
	r.sources[0].incarnation = "replacement"
	if _, err := newIAConfigurationCommand(IAConfigureSource, id, r); !errors.Is(err, ErrInvalid) {
		t.Fatal("replacement accepted without explicit confirmation", err)
	}
	r.acceptReplacement = true
	if _, err := newIAConfigurationCommand(IAConfigureSource, id, r); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"disable_source", "enable_source"} {
		r = c.request
		r.sources = append([]iaConfigurationSource(nil), c.request.sources...)
		r.action = action
		r.sources[0].enabled = action == "enable_source"
		if _, err := newIAConfigurationCommand(IAConfigureSource, id, r); err != nil {
			t.Fatal(action, err)
		}
	}
	d, err := Describe(IAConfigureSource)
	if err != nil || d.Durability != Full {
		t.Fatal("source denial can select NORMAL", d, err)
	}
}

func TestIAConfigurationRefusesMalformedBeforeClone(t *testing.T) {
	id, r := iaConfigurationRequestFixture()
	for _, mutate := range []func(*iaConfigurationRequest){
		func(v *iaConfigurationRequest) { v.sources = nil },
		func(v *iaConfigurationRequest) { v.sources = append(v.sources, v.sources[0]) },
		func(v *iaConfigurationRequest) { v.sources[0].resolvedRoot = "\xff" },
		func(v *iaConfigurationRequest) { v.sources[0].rootIdentity = "bad\x00identity" },
		func(v *iaConfigurationRequest) { v.sources[0].intervalSeconds = 899 },
		func(v *iaConfigurationRequest) { v.sources[0].remoteSourceGeneration = 1 },
	} {
		bad := r
		bad.sources = append([]iaConfigurationSource(nil), r.sources...)
		mutate(&bad)
		if c, err := newIAConfigurationCommand(IAConfigureLibrary, id, bad); !errors.Is(err, ErrInvalid) || c != nil {
			t.Fatal("malformed request frozen", err)
		}
	}
	r.sources[0].name = strings.Repeat("a", maxCanonicalRequestBytes+1)
	if c, err := newIAConfigurationCommand(IAConfigureLibrary, id, r); !errors.Is(err, ErrCap) || c != nil {
		t.Fatal("independent payload cap", err)
	}
}
