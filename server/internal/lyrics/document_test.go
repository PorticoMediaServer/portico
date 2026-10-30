package lyrics

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseLRCAndOffset(t *testing.T) {
	d, e := Parse([]byte("\xef\xbb\xbf[ar:Example]\r\n[au:Original author]\n[offset:+250]\n[00:02.50][00:01.2]First\n[00:02.500]Second"), "lrc")
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Lines) != 2 || *d.Lines[0].AtMS != 1200 || *d.Lines[1].AtMS != 2500 || d.Lines[1].Text != "First\nSecond" || d.EmbeddedOffsetMS != -250 {
		t.Fatalf("unexpected normalized document: %+v", d)
	}
	if validateTiming(d, -250, 3) != nil || validateTiming(d, 0, 1) == nil {
		t.Fatal("source timeline bounds not enforced")
	}
}
func TestPlainTextAndEncoding(t *testing.T) {
	for _, raw := range [][]byte{[]byte("<b>literal text</b>\r\nnext"), {0xff, 0xfe, 'H', 0, 'i', 0}, {0xfe, 0xff, 0, 'H', 0, 'i'}} {
		d, e := Parse(raw, "text")
		if e != nil || len(d.Lines) == 0 || d.Lines[0].AtMS != nil {
			t.Fatalf("plain encoding: %+v %v", d, e)
		}
		if validateTiming(d, 1, 50) == nil {
			t.Fatal("plain lyrics accepted timing offset")
		}
	}
}
func TestRejectMalformedAndUnbounded(t *testing.T) {
	repeated := ""
	for i := 0; i < 64; i++ {
		repeated += fmt.Sprintf("[%02d:00]", i)
	}
	repeated += strings.Repeat("x", 5000)
	cases := []struct{ name, text, format string }{
		{"empty", "  ", "text"}, {"control", "a\x00b", "text"}, {"invalid utf8", string([]byte{0xff}), "text"},
		{"unpaired surrogate", string([]byte{0xff, 0xfe, 0, 0xd8}), "text"}, {"truncated utf16", string([]byte{0xff, 0xfe, 0}), "text"},
		{"seconds", "[00:60]word", "lrc"}, {"negative stamp", "[-01:03]word", "lrc"}, {"fraction", "[00:01.1234]word", "lrc"},

		{"too many lines", strings.Repeat("x\n", MaxLines+1), "text"},
		{"large text", strings.Repeat("x", MaxTextBytes+1), "text"}, {"large line", strings.Repeat("x", 8193), "text"},
		{"expanded timestamps", repeated, "lrc"}, {"html format", "<p>example</p>", "html"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, e := Parse([]byte(c.text), c.format); e == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}
func TestLanguage(t *testing.T) {
	if v, e := Language("EN-ca"); e != nil || v != "en-ca" {
		t.Fatal(v, e)
	}
	if v, e := Language(""); e != nil || v != "und" {
		t.Fatal(v, e)
	}
	for _, v := range []string{"../en", "en_CA", "en<script>", strings.Repeat("a", 64)} {
		if _, e := Language(v); e == nil {
			t.Fatal("invalid language accepted", v)
		}
	}
}

func TestEnhancedLRCAndNegativeOffset(t *testing.T) {
	d, e := Parse([]byte("[offset:-100]\n[00:12.00][01:15.00]<00:12.01>Hello <00:12.40>world"), "lrc")
	if e != nil || d.EmbeddedOffsetMS != 100 || len(d.Lines) != 2 || *d.Lines[0].AtMS != 12000 || *d.Lines[1].AtMS != 75000 || d.Lines[0].Text != "Hello world" {
		t.Fatalf("%+v %v", d, e)
	}
}

func TestLRCSkipsOddLines(t *testing.T) {
	d, e := Parse([]byte("[id:abc]\n[00:01:50]Good\nuntimed\n[00:70]Bad\n[00:02]Also good"), "lrc")
	if e != nil || len(d.Lines) != 2 || d.SkippedLines != 3 || *d.Lines[0].AtMS != 1500 {
		t.Fatalf("%+v %v", d, e)
	}
}
