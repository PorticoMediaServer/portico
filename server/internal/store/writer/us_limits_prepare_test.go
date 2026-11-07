package writer

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func usLimitsFixture() (*usLimitsCommand, error) {
	f := usMemberCommandFixture()
	v := usLimits{MaxProfiles: 8, Conversion: "server", MaxStreamsMode: "inherit", MaximumAge: -1, AllowUnrated: true}
	target := usLimitsTarget{member: f.Target.target(), limitsRevision: 1, role: "member", current: usLimits{AwayQuality: "720p-low"}}
	return prepareUSLimits(f.Identity, f.Owner, f.Issuer.issuer(), target, v)
}
func TestUSLimitsNormalizationAndOwnership(t *testing.T) {
	c, e := usLimitsFixture()
	if e != nil {
		t.Fatal(e)
	}
	r := *c.request.usLimitsPayload
	r.input.Schedule.Windows = []usLimitsWindow{{Days: []int{6, 0}, StartMinute: 1300, EndMinute: 10}}
	r.input.Tags.DeniedLabels = []string{"  café  "}
	r.input.Channels.Channels = []string{"live:source:channel", "library:channel"}
	r.input.RatingSystem = " mpaa "
	r.input.MaxContentRating = " PG-13 "
	n, e := prepareUSLimits(r.identity, r.ownerID, r.issuer, r.target, r.input)
	if e != nil {
		t.Fatal(e)
	}
	r.input.Schedule.Windows[0].Days[0] = 2
	r.input.Tags.DeniedLabels[0] = "mutated"
	r.input.Channels.Channels[0] = "mutated"
	if n.request.normalized.Schedule.Windows[0].Days[0] != 0 || n.request.normalized.Tags.DeniedLabels[0] != "café" || n.request.input.Tags.DeniedLabels[0] != "  café  " || n.request.normalized.MaximumAge != 13 || n.request.normalized.AwayQuality != "720p-low" {
		t.Fatal("normalization or ownership")
	}
	if n.request.input.Schedule.Windows[0].Days[0] != 6 {
		t.Fatal("raw days sorted")
	}
	if c.request.input.Schedule.Windows != nil || c.request.normalized.Schedule.Windows == nil || c.request.normalized.Channels.Channels == nil || c.request.normalized.Tags.DeniedLabels == nil {
		t.Fatal("nil semantics")
	}
}
func TestUSLimitsSourceBounds(t *testing.T) {
	c, _ := usLimitsFixture()
	cases := []struct {
		name string
		edit func(*usLimits)
	}{
		{"profiles", func(v *usLimits) { v.MaxProfiles = 0 }},
		{"conversion", func(v *usLimits) { v.Conversion = "invalid" }},
		{"away", func(v *usLimits) { v.AwayQuality = "invalid" }},
		{"streams", func(v *usLimits) { v.MaxStreamsMode = "limit"; v.MaxStreams = 1001 }},
		{"bitrate", func(v *usLimits) { v.RemoteBitrateKbps = 200001 }},
		{"rating", func(v *usLimits) { v.MaxContentRating = "no-such-rating" }},
		{"timezone", func(v *usLimits) { v.Schedule.Timezone = "not/a/timezone" }},
		{"windows", func(v *usLimits) { v.Schedule.Windows = make([]usLimitsWindow, 65) }},
		{"days", func(v *usLimits) { v.Schedule.Windows = []usLimitsWindow{{Days: []int{1, 1}, EndMinute: 1}} }},
		{"channels", func(v *usLimits) { v.Channels.Channels = make([]string, 513) }},
		{"canonical", func(v *usLimits) { v.Channels.Channels = []string{"live:a:b:c"} }},
		{"tags", func(v *usLimits) { v.Tags.DeniedLabels = make([]string, 257) }},
		{"invalidUTF8Tag", func(v *usLimits) { v.Tags.DeniedLabels = []string{"\xff"} }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			v := cloneUSLimits(c.request.input)
			tt.edit(&v)
			if normalizeUSLimits(&v) == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
	v := cloneUSLimits(c.request.input)
	v.Tags.DeniedLabels = make([]string, 256)
	for i := range v.Tags.DeniedLabels {
		v.Tags.DeniedLabels[i] = strings.Repeat("é", 120)
	}
	if e := normalizeUSLimits(&v); e != nil {
		t.Fatal("source rune and 256-tag envelope", e)
	}
}
func TestUSLimitsDigestRegistrationAndRedaction(t *testing.T) {
	c, e := usLimitsFixture()
	if e != nil {
		t.Fatal(e)
	}
	if d, err := c.canonicalDigest(); err != nil || d != c.digest {
		t.Fatal("canonical digest", err)
	}
	for _, v := range []any{*c, c, c.request} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%p"} {
			out := fmt.Sprintf(verb, v)
			if strings.Contains(out, c.request.ownerID) || strings.Contains(out, c.request.issuer.tokenDigest) {
				t.Fatalf("leaked %s: %s", verb, out)
			}
		}
	}
}

// Mutate each private frozen leaf independently, including normalized caches,
// every issuer witness and identity byte. The stored digest must reject it.
func TestUSLimitsEveryFrozenWitness(t *testing.T) {
	c, err := usLimitsFixture()
	if err != nil {
		t.Fatal(err)
	}
	c.request.input.Schedule.Windows = []usLimitsWindow{{Days: []int{0, 6}, EndMinute: 10}}
	c.request.input.Channels.Channels = []string{"live:a:b"}
	c.request.input.Tags.DeniedLabels = []string{"tag"}
	x := 5
	c.request.target.serverMaxStreams = &x
	c.request.normalized = cloneUSLimits(c.request.input)
	c.digest, err = c.canonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if v.Kind() == reflect.Pointer {
			if !v.IsNil() {
				walk(v.Elem())
			}
			return
		}
		if v.Kind() == reflect.Struct {
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
			return
		}
		if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
			return
		}
		w := reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
		old := reflect.New(v.Type()).Elem()
		old.Set(w)
		switch v.Kind() {
		case reflect.String:
			w.SetString(v.String() + "\xff")
		case reflect.Bool:
			w.SetBool(!v.Bool())
		case reflect.Int, reflect.Int64:
			w.SetInt(v.Int() + 1)
		case reflect.Uint8:
			w.SetUint((v.Uint() + 1) % 256)
		default:
			t.Fatalf("uncovered %s", v.Kind())
		}
		d, e := c.canonicalDigest()
		if e != nil {
			t.Fatal(e)
		}
		if d == c.digest {
			t.Fatal("unbound frozen witness")
		}
		if _, err := c.checkedDigest(); err == nil {
			t.Fatal("mutation accepted")
		}
		w.Set(old)
		count++
	}
	walk(reflect.ValueOf(&c.request).Elem())
	if count < 100 {
		t.Fatalf("only %d witnesses", count)
	}
}
