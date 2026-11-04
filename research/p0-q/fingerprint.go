package main

import (
	"crypto/sha256"
	"fmt"
	"runtime/debug"
	"strings"
	"unicode"
)

var engineFingerprint string

func frozenFingerprint(sourceID string) string {
	inputs := []string{"sqlite_source_id=" + sourceID, "go_unicode=" + unicode.Version, "facettext_source_sha256=98d3b979746c988b22d2b48e9a0a5b59cd40bf52afa9ac6a6cb5acaacac3ceee", "token_recipe=A-CJK-uni-bi-v1:scalarhex6:word12:numeric6:ascii-prefix6..72"}
	if b, ok := debug.ReadBuildInfo(); ok {
		for _, d := range b.Deps {
			if d.Path == "modernc.org/sqlite" || d.Path == "golang.org/x/text" {
				inputs = append(inputs, d.Path+"="+d.Version+":"+d.Sum)
			}
		}
	}
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(inputs, "\n"))))
	emit("unicode_recipe_fingerprint", map[string]any{"sha256": sum, "inputs": inputs, "scope": "engine module includes compiled Unicode/tokenizer tables; Go Unicode and x/text normalization pinned separately"})
	return sum
}
