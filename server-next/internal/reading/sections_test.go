package reading

import "testing"

// A section's text is what the reader's indexed-text.ts makes of the same page: the body only,
// script, style and template left out, a space around each block, and white space (Go's
// unicode.IsSpace, which counts a no-break space) collapsed and trimmed.
func TestSectionTextMatchesTheReader(t *testing.T) {
	for _, c := range []struct{ page, want string }{
		{`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Not this</title><style>p{}</style></head><body>` +
			`<h1>Chapter&#160;1</h1><p>One&nbsp;two<br/>three</p><script>no</script><div>four<span>five</span></div>` +
			`<template>gone</template><p>  six  </p></body></html>`, "Chapter 1 One two three fourfive six"},
		{`<html xmlns="http://www.w3.org/1999/xhtml"><body><p>a<![CDATA[b]]>c</p><SCRIPT>kept</SCRIPT></body></html>`, "abc kept"},
		{`<package><body><p>not a page</p></body></package>`, ""},
	} {
		got, _ := sectionText([]byte(c.page))
		if got != c.want {
			t.Errorf("%q\n got %q\nwant %q", c.page, got, c.want)
		}
	}
}
