package decoder

import (
	"strings"
	"testing"
)

func TestPreparedRecipesUseBoundedFullMediaCommands(t *testing.T) {
	for _, r := range []PreparedRecipe{{Width: 1280, Height: 720, VideoKbps: 4000, AudioKbps: 192}, {Width: 1920, Height: 1080, VideoKbps: 8000, AudioKbps: 192}, {AudioOnly: true, AudioKbps: 192}} {
		args, e := preparedArgs("http://127.0.0.1:1/opaque", r, false)
		if e != nil {
			t.Fatal(e)
		}
		s := strings.Join(args, " ")
		for _, required := range []string{"-xerror", "-nostdin", "-map 0:a", "-sn -dn", "-c:a aac", "-ac 2", "-f mp4 pipe:1"} {
			if !strings.Contains(s, required) {
				t.Fatal("missing fixed argument", required, s)
			}
		}
		for _, forbidden := range []string{"-ss ", "-t ", "-to ", "concat:", "/source/"} {
			if strings.Contains(s, forbidden) {
				t.Fatal("not a fixed full-media recipe", s)
			}
		}
	}
	if _, e := preparedArgs("input", PreparedRecipe{Width: 4000, Height: 4000, VideoKbps: 99000, AudioKbps: 192}, false); e == nil {
		t.Fatal("arbitrary profile accepted")
	}
	args, e := preparedArgs("input", PreparedRecipe{}, true)
	if e != nil || !strings.Contains(strings.Join(args, " "), "-map 0:v? -map 0:a? -sn -dn -f null -") {
		t.Fatal("validator does not consume complete output", args, e)
	}
}

func TestPreparedChapterPolicyIsExplicitAndVersioned(t *testing.T) {
	legacy := PreparedRecipe{AudioOnly: true, AudioKbps: 192}
	args, err := preparedArgs("input", legacy, false)
	if err != nil || strings.Contains(strings.Join(args, " "), "-map_chapters") {
		t.Fatal("changed legacy recipe", args, err)
	}
	legacy.DropChapters = true
	args, err = preparedArgs("input", legacy, false)
	if err != nil || !strings.Contains(strings.Join(args, " "), "-map_chapters -1") {
		t.Fatal("new recipe copied unremapped chapter data", args, err)
	}
}

func TestPreparedFragmentBoundPreservesLegacyBytes(t *testing.T) {
	for _, audio := range []bool{false, true} {
		r := PreparedRecipe{AudioOnly: audio, DropChapters: true, AudioKbps: 192, Width: 1280, Height: 720, VideoKbps: 4000}
		old, err := preparedArgs("input", r, false)
		if err != nil || strings.Contains(strings.Join(old, " "), "-frag_duration") {
			t.Fatal("legacy fragmentation changed", old, err)
		}
		r.BoundedFragments = true
		current, err := preparedArgs("input", r, false)
		if err != nil || !strings.Contains(strings.Join(current, " "), "-frag_duration 1000000") {
			t.Fatal("streaming fragments not bounded", current, err)
		}
		if !strings.Contains(strings.Join(current, " "), "-map_chapters -1") {
			t.Fatal("new recipe restored unmapped chapters")
		}
	}
}
