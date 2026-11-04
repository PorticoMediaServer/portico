package main

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"strings"
	"unicode/utf8"
)

var cjkShare = -1
var oldCJKRecipe bool
var cjkTitlesBuilt int
var cjkTitleNames = []string{"東京物語", "流浪地球", "花樣年華", "一代宗師", "千と千尋の神隠し", "王菲演唱會", "君の名は", "四月物語", "春夏秋冬", "霸王別姬", "少年時代", "海街日記"}
var cjkPersonNames = []string{"王菲", "李华", "山田太郎", "陳奕迅", "宇多田光", "张伟", "山口百恵", "박지은"}

func scriptShare(id int) bool {
	x := uint64(id) + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	return cjkShare >= 0 && int(x%100) < cjkShare
}
func titleIsCJK(id int) bool {
	if id < 9 {
		return id == 4
	}
	if cjkShare < 0 {
		return id%47 == 0
	}
	return scriptShare(id)
}
func personName(id int) string {
	if scriptShare(id) {
		return fmt.Sprintf("%s %d", cjkPersonNames[id%len(cjkPersonNames)], id)
	}
	return fmt.Sprintf("%s %s %d", []string{"Ada", "John", "Jo", "Al", "Alice", "Nolan", "Akira", "Lee"}[id%8], []string{"Lovelace", "Smith", "Williams", "Li", "Cooper", "Johnson"}[id%6], id)
}
func collectionName(id int) string {
	if scriptShare(id) {
		return fmt.Sprintf("%s珍藏 %d", cjkTitleNames[id%len(cjkTitleNames)], id)
	}
	return fmt.Sprintf("%s Collection %d", []string{"Night", "Criterion", "Al", "Studio"}[id%4], id)
}
func renamedTitle(id, version int) string {
	if titleIsCJK(id) {
		return fmt.Sprintf("%s 第%d話", cjkTitleNames[id%len(cjkTitleNames)], version)
	}
	return fmt.Sprintf("Harbor Churn %d", version)
}
func renamedPerson(id, version int) string {
	if scriptShare(id) {
		return fmt.Sprintf("%s 改訂%d", cjkPersonNames[id%len(cjkPersonNames)], version)
	}
	return fmt.Sprintf("Ada Churn Common %d", version)
}
func cjkSeed(term string) string {
	r := []rune(term)
	for i := 0; i+1 < len(r); i++ {
		if facettext.CJK(r[i]) && facettext.CJK(r[i+1]) {
			return string(r[i : i+2])
		}
	}
	for _, x := range r {
		if facettext.CJK(x) {
			return string(x)
		}
	}
	return term
}
func policyTerms(q string) []string { return strings.Fields(facettext.SearchFold(q)) }
func shortInput(q string) bool {
	q = strings.TrimSpace(q)
	if strings.ContainsFunc(q, facettext.CJK) {
		return utf8.RuneCountInString(q) < 2
	}
	return utf8.RuneCountInString(q) < 3
}
