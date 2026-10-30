package reading

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestLocalSrcsetUsesWholeCandidateGrammar(t *testing.T) {
	assets := map[string]string{"EPUB/small.jpg": "image/jpeg", "EPUB/large.png": "image/png", "EPUB/font.ttf": "font/ttf"}
	for _, raw := range []string{
		`small.jpg`, `small.jpg 1x, large.png 2x`, `small.jpg, large.png 2x`,
		`small.jpg 320w, large.png 640w`, " small.jpg .5x,\n large.png 2.5x ",
	} {
		if err := validateSrcset("EPUB/chapter.xhtml", raw, assets); err != nil {
			t.Fatalf("local candidates refused: %v", err)
		}
	}
	for _, raw := range []string{
		``, `,small.jpg`, `small.jpg,`, `small.jpg 1x,`, `small.jpg,, large.png`,
		`small.jpg,large.png`, `small.jpg 1x,, large.png 2x`,
		`small.jpg 1x, https://origin.invalid/large.png 2x`,
		`small.jpg 1x, //origin.invalid/large.png 2x`,
		`data:image/png;base64,AAAA 1x`, `blob:local 1x`, `/EPUB/small.jpg 1x`,
		`../../../small.jpg 1x`, `missing.jpg 1x`, `font.ttf 1x`,
		`small.jpg?other=1 1x`, `'small.jpg' 1x`, `small.jpg 1x, large.png 320w`,
		`small.jpg, large.png 320w`, `small.jpg 1x, large.png 1.0x`,
		`small.jpg 320w, large.png 0320w`, `small.jpg 1x, ./small.jpg 2x`,
		`small.jpg 0x`, `small.jpg -1x`, `small.jpg NaNx`, `small.jpg Infinityx`,
		`small.jpg 1e2x`, `small.jpg 0w`, `small.jpg 1.5w`, `small.jpg 1x 2x`,
		`small.jpg 1x(ignored)`, `small.jpg 1x garbage, large.png 2x`,
		`small.jpg ` + strings.Repeat("1", 40) + `x`,
	} {
		if err := validateSrcset("EPUB/chapter.xhtml", raw, assets); err == nil {
			t.Fatalf("malformed or active candidate accepted: %q", raw)
		}
	}
	if err := validateSrcset("EPUB/chapter.xhtml", strings.Repeat(" ", maxSrcsetBytes)+"small.jpg", assets); err == nil {
		t.Fatal("attribute bound ignored")
	}
	var candidates []string
	for i := 0; i < maxSrcsetCandidates+1; i++ {
		name := "image" + strings.Repeat("a", i) + ".jpg"
		assets["EPUB/"+name] = "image/jpeg"
		candidates = append(candidates, name+" "+strconv.Itoa(i+1)+"w")
	}
	if err := validateSrcset("EPUB/chapter.xhtml", strings.Join(candidates, ", "), assets); !errors.Is(err, ErrLimit) {
		t.Fatal("candidate bound ignored")
	}
}

func TestSrcsetAdmissionIsOnlyExactXHTMLImageAttribute(t *testing.T) {
	assets := map[string]string{"EPUB/small.jpg": "image/jpeg", "EPUB/large.png": "image/png"}
	document := func(body string) []byte {
		return []byte(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:x="urn:other"><body>` + body + `</body></html>`)
	}
	good := document(`<img src="small.jpg" srcset="small.jpg 1x, large.png 2x"/>`)
	original := string(good)
	if err := validateXHTML("EPUB/chapter.xhtml", good, assets); err != nil {
		t.Fatal(err)
	}
	if string(good) != original {
		t.Fatal("original representation changed")
	}
	for _, body := range []string{
		`<source srcset="small.jpg 1x"/>`, `<link srcset="small.jpg 1x"/>`,
		`<img x:srcset="small.jpg 1x"/>`, `<img SRCSET="small.jpg 1x"/>`,
		`<img xmlns="" srcset="small.jpg 1x"/>`,
		`<img src="https://origin.invalid/fallback.jpg" srcset="small.jpg 1x"/>`,
		`<img src="small.jpg" srcset="small.jpg 1x, https://origin.invalid/x 2x"/>`,
	} {
		if err := validateXHTML("EPUB/chapter.xhtml", document(body), assets); err == nil {
			t.Fatalf("wrong slot or fallback accepted: %s", body)
		}
	}
}
