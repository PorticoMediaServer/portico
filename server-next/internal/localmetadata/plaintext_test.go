package localmetadata

import "testing"

// Descriptions written in HTML become plain text an app can show; text with no markup is not
// touched.
func TestDescriptionsAreKeptAsPlainText(t *testing.T) {
	for in, want := range map[string]string{
		"the prelude to <i>The Lord of The Rings</i>, now the subject":         "the prelude to The Lord of The Rings, now the subject",
		"<p>First part.</p>\n<p>Second\n  part &amp; more&#8217;s.</p>":        "First part.\n\nSecond part & more’s.",
		"One line<br/>Next line<script>alert(1)</script><style>p{}</style>":    "One line\nNext line",
		"<div><b>Bold</b> start</div><ul><li>one</li><li>two</li></ul>":        "Bold start\n\none\n\ntwo",
		"Tom & Jerry, where a < b and\n\nthe second paragraph keeps its break": "Tom & Jerry, where a < b and\n\nthe second paragraph keeps its break",
	} {
		if got := PlainText(in); got != want {
			t.Errorf("PlainText(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}
