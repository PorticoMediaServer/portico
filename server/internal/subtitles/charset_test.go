package subtitles

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestLegacySubtitleDecodingUsesLanguageAndScript(t *testing.T) {
	phrases := map[string]string{"ru": "Это русский текст. Что происходит? Привет!", "uk": "Це український текст і привіт.", "ja": "これは日本語です。こんにちは。", "zh": "这是中文字幕。你好世界。", "ko": "안녕하세요. 한국어 자막입니다.", "el": "Ελληνικά υπότιτλοι, καλημέρα.", "ar": "مرحبا بالعالم ترجمة عربية", "he": "שלום עולם כתוביות בעברית", "pl": "Zażółć gęślą jaźń. Łódź.", "vi": "Tiếng Việt rất đẹp.", "tr": "Türkçe altyazı, şarkı güzel."}
	for _, c := range subtitleCharsets {
		lang := strings.Fields(c.languages)[0]
		phrase := phrases[lang]
		if phrase == "" {
			continue
		}
		raw, e := c.encoding.NewEncoder().Bytes([]byte("1\n00:00:01,000 --> 00:00:03,000\n" + phrase + "\n"))
		if e != nil {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			got, e := DecodeSubtitleText(raw, lang)
			if e != nil || !strings.Contains(string(got), phrase) {
				t.Fatalf("got %q: %v", got, e)
			}
		})
	}
}
func TestSubtitleUTF16WithoutBOM(t *testing.T) {
	text := "1\n00:00:01,000 --> 00:00:02,000\nHello 世界\n"
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		units := utf16.Encode([]rune(text))
		raw := make([]byte, len(units)*2)
		for i, v := range units {
			order.PutUint16(raw[i*2:], v)
		}
		got, e := DecodeSubtitleText(raw, "zh")
		if e != nil || string(got) != text {
			t.Fatalf("%q %v", got, e)
		}
	}
}
func TestTolerantASSAndMarkupKeepGoodCues(t *testing.T) {
	ass := assHeader + "Dialogue: 0,bad,0:00:02.00,Default,,0,0,0,,bad\nDialogue: 0,0:00:01.000,0:00:03.500,Default,,0,0,0,,Good\nDialogue: 0,0:00:04.00,0:00:04.00,Default,,0,0,0,,marker\n"
	raw, e := CanonicalAsset([]byte(ass), nil, "ass", 10000000)
	if e != nil {
		t.Fatal(e)
	}
	got := renderChoice(raw, "text", 10000000)
	if !strings.Contains(string(got), "Good") {
		t.Fatal(string(got))
	}
	for _, v := range []struct{ format, text string }{{"smi", `<SAMI><BODY><SYNC Start=1000><P>Good<SYNC Start=bad><P>bad<SYNC Start=3000><P>&nbsp;</BODY></SAMI>`}, {"ttml", `<tt><body><p begin="bad" end="3s">bad</p><p begin="1s" end="3s">Good<br/>line</p></body></tt>`}} {
		cues, e := ParseTolerant([]byte(v.text), v.format, 5000000)
		if e != nil || len(cues) == 0 || !strings.Contains(cues[0].Text, "Good") {
			t.Fatalf("%s %+v %v", v.format, cues, e)
		}
	}
	cues, e := ParseTolerant([]byte("1\n00:00:01,000 --> 00:00:02,000\n{LAUGHS}\n"), "srt", 5000000)
	if e != nil || cues[0].Text != "{LAUGHS}" {
		t.Fatal(cues, e)
	}
}

func TestLegacySubtitlesWithoutLanguageHint(t *testing.T) {
	for _, v := range []struct{ name, body string }{{"windows-1251", "Это русский текст. Что происходит? Привет!"}, {"koi8-r", "Это русский текст. Что происходит? Привет!"}, {"gbk", "这是中文字幕。你好世界。"}, {"shift-jis", "これは日本語です。こんにちは。"}, {"euc-kr", "안녕하세요. 한국어 자막입니다."}} {
		for _, c := range subtitleCharsets {
			if c.name != v.name {
				continue
			}
			raw, e := c.encoding.NewEncoder().Bytes([]byte("1\n00:00:01,000 --> 00:00:03,000\n" + v.body))
			if e != nil {
				t.Fatal(e)
			}
			got, e := DecodeSubtitleText(raw, "")
			if e != nil || !strings.Contains(string(got), v.body) {
				t.Fatalf("%s: %q %v", v.name, got, e)
			}
		}
	}
}
