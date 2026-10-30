package linearinput

import (
	"strings"
	"testing"
)

func TestAttributesPreserveQuotedCommaWithoutCreatingAnotherFetch(t *testing.T) {
	v, e := parseAttributes(`METHOD=AES-128,URI="keys/key?a=1,b=2",KEYFORMAT="identity"`)
	if e != nil || attribute(v, "URI") != "keys/key?a=1,b=2" {
		t.Fatal(v, e)
	}
	if !strings.Contains(formatAttributes(v), `URI="keys/key?a=1,b=2"`) {
		t.Fatal("lost URI quoting")
	}
}
func TestMalformedAttributesFailClosed(t *testing.T) {
	for _, input := range []string{"", `URI="a",URI="b"`, `URI="a",`, `URI="a"junk`, `URI="a`, `URI=a b`, `uRI=a`, `URI="a
secret"`, `URI="a",,METHOD=NONE`} {
		if _, e := parseAttributes(input); e != ErrFormat {
			t.Fatalf("accepted %q: %v", input, e)
		}
	}
}
func TestAttributeCountBound(t *testing.T) {
	var parts []string
	for n := 0; n < 65; n++ {
		parts = append(parts, strings.Repeat("A", n+1)+"=x")
	}
	if _, e := parseAttributes(strings.Join(parts, ",")); e != ErrFormat {
		t.Fatal("unbounded attributes", e)
	}
}
