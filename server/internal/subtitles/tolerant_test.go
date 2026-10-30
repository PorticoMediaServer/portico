package subtitles

import (
	"encoding/json"
	"strings"
	"testing"
)

// The files people actually have. Each of these used to be refused as text and
// sent to the burn-in renderer, or refused outright.
func TestParseTolerantReadsRealWorldSubRip(t *testing.T) {
	cases := map[string]string{
		"italic tags":             "1\n00:00:01,000 --> 00:00:02,000\n<i>Whispered.</i>\n",
		"font and bold":           "1\n00:00:01,000 --> 00:00:02,000\n<font color=\"#ffff00\"><b>Whispered.</b></font>\n",
		"no counter line":         "00:00:01,000 --> 00:00:02,000\nWhispered.\n",
		"one digit hours, dot ms": "1\n0:00:01.0 --> 0:00:02.00\nWhispered.\n",
		"carriage returns only":   "1\r00:00:01,000 --> 00:00:02,000\rWhispered.\r",
		"windows line endings":    "1\r\n00:00:01,000 --> 00:00:02,000\r\nWhispered.\r\n\r\n",
		"byte order mark":         "\ufeff1\n00:00:01,000 --> 00:00:02,000\nWhispered.\n",
		"trailing position data":  "1\n00:00:01,000 --> 00:00:02,000 X1:100 X2:200 Y1:50 Y2:80\nWhispered.\n",
		"blank lines with spaces": "1\n00:00:01,000 --> 00:00:02,000\nWhispered.\n   \n\n",
		"an empty cue beside it":  "1\n00:00:00,500 --> 00:00:00,900\n\n\n2\n00:00:01,000 --> 00:00:02,000\nWhispered.\n",
		"ass override in srt":     "1\n00:00:01,000 --> 00:00:02,000\n{\\c&H00FFFF&}Whispered.\n",
	}
	for name, body := range cases {
		cues, err := ParseTolerant([]byte(body), "srt", 0)
		if err != nil || len(cues) != 1 || cues[0].Text != "Whispered." || cues[0].StartMS != 1000 || cues[0].EndMS != 2000 {
			t.Fatalf("%s: %+v %v", name, cues, err)
		}
	}
	// Presentation facts that survive.
	cues, err := ParseTolerant([]byte("1\n00:00:01,000 --> 00:00:02,000\n{\\an8}<i>SIGN</i>\n\n2\n00:00:03,000 --> 00:00:04,000\nplain <i>partly</i>\n"), "srt", 0)
	if err != nil || len(cues) != 2 || !cues[0].Top || !cues[0].Italic || cues[1].Top || cues[1].Italic || cues[1].Text != "plain partly" {
		t.Fatalf("%+v %v", cues, err)
	}
	// Out of order on disk, in order on screen.
	cues, err = ParseTolerant([]byte("2\n00:00:05,000 --> 00:00:06,000\nSecond\n\n1\n00:00:01,000 --> 00:00:02,000\nFirst\n"), "srt", 0)
	if err != nil || len(cues) != 2 || cues[0].Text != "First" {
		t.Fatalf("%+v %v", cues, err)
	}
	// Windows-1252, which is what a non-Unicode Western subtitle nearly always is.
	cues, err = ParseTolerant([]byte("1\n00:00:01,000 --> 00:00:02,000\nCaf\xe9 \x93quoted\x94\n"), "srt", 0)
	if err != nil || cues[0].Text != "Café “quoted”" {
		t.Fatalf("%+v %v", cues, err)
	}
	// WebVTT with a header comment, notes, a style block and cue identifiers.
	vtt := "WEBVTT - made by hand\n\nNOTE a comment\nover two lines\n\nSTYLE\n::cue { color: lime }\n\nintro\n00:01.000 --> 00:02.000 line:0 align:center\n<v Anna>Hello &amp; welcome</v>\n\n00:00:03.000 --> 00:00:04.000\n<c.yellow>Yellow</c>\n"
	cues, err = ParseTolerant([]byte(vtt), "vtt", 0)
	if err != nil || len(cues) != 2 || cues[0].Text != "Hello & welcome" || !cues[0].Top || cues[1].Text != "Yellow" || cues[1].Top {
		t.Fatalf("%+v %v", cues, err)
	}
	// Nothing readable is still an error, and capacity is still capacity.
	if _, err = ParseTolerant([]byte("this is not a subtitle"), "srt", 0); err == nil {
		t.Fatal("prose accepted as subtitles")
	}
	if _, err = ParseTolerant([]byte(strings.Repeat("00:00:01,000 --> 00:00:02,000\nx\n\n", MaxCues+1)), "srt", 0); err != ErrCapacity {
		t.Fatal(err)
	}
	// Markup never reaches a player: what is stored is words.
	raw, err := Canonical([]byte("1\n00:00:01,000 --> 00:00:02,000\n<script>alert(1)</script>{\\pos(1,1)}Safe\n"), "srt", 0)
	var d Document
	if err != nil || json.Unmarshal(raw, &d) != nil || d.Cues[0].Text != "alert(1)Safe" || strings.ContainsAny(d.Cues[0].Text, "<>{}") {
		t.Fatalf("%s %v", raw, err)
	}
}

func TestParseASSAsText(t *testing.T) {
	script := "[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname\nStyle: Default,Arial\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:05.00,0:00:07.50,Default,,0,0,0,,{\\an8\\fad(200,200)}A sign, with a comma\n" +
		"Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\\i1}First line{\\i0}\\Nsecond line\n" +
		"Dialogue: 1,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\\blur2}First line\\Nsecond line\n" +
		"Dialogue: 0,0:00:08.00,0:00:09.00,Default,,0,0,0,,{\\p1}m 0 0 l 100 0 100 100 0 100{\\p0}\n" +
		"Comment: 0,0:00:08.00,0:00:09.00,Default,,0,0,0,,not shown\n"
	cues, err := ParseASSAsText([]byte(script), 0)
	if err != nil || len(cues) != 2 {
		t.Fatalf("%+v %v", cues, err)
	}
	if cues[0].Text != "First line\nsecond line" || !cues[0].Italic || cues[0].StartMS != 1000 || cues[0].EndMS != 3000 {
		t.Fatalf("%+v", cues[0])
	}
	if cues[1].Text != "A sign, with a comma" || !cues[1].Top || cues[1].StartMS != 5000 {
		t.Fatalf("%+v", cues[1])
	}
}

// A styled script is kept as text unless the viewer asks for it as authored.
func TestRenderChoiceKeepsStyledScriptsAsTextByDefault(t *testing.T) {
	script := assHeader + "Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\\pos(100,120)}Styled words\n"
	canonical, err := CanonicalAsset([]byte(script), nil, "ass", 10000000)
	if err != nil || assetRenderer(canonical) != "burn_in" {
		t.Fatal(err)
	}
	text := renderChoice(canonical, "", 10000000)
	var d Document
	if assetRenderer(text) != "external_text" || json.Unmarshal(text, &d) != nil || len(d.Cues) != 1 || d.Cues[0].Text != "Styled words" {
		t.Fatalf("%s", text)
	}
	if styled := renderChoice(canonical, "styled", 10000000); assetRenderer(styled) != "burn_in" {
		t.Fatal("an explicit request for the authored script was converted to text")
	}
	// A bitmap track has no words to keep.
	pgs := []byte(`{"version":2,"timeDomain":"source-relative","renderer":"burn_in","format":"pgs","data":"UEc="}`)
	if out := renderChoice(pgs, "text", 0); assetRenderer(out) != "burn_in" {
		t.Fatal("a bitmap track was treated as text")
	}
	if !validRender("") || !validRender("text") || !validRender("styled") || validRender("burn") {
		t.Fatal("render domain")
	}
}
