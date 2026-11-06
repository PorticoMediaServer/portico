// Build-aware top-level test discovery for the manual lane workflow.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type lanePackage struct {
	ImportPath, Dir           string
	TestGoFiles, XTestGoFiles []string
}
type inventoryInput struct {
	Packages []lanePackage
	Run      string
	Tests    []string
}
type laneTest struct{ Package, Name string }

func testPrefix(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(strings.TrimPrefix(name, prefix))
	return !unicode.IsLower(r)
}

func discover(p lanePackage) ([]string, error) {
	set := map[string]bool{}
	var files []*ast.File
	for _, name := range append(p.TestGoFiles, p.XTestGoFiles...) {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(p.Dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name != "TestMain" && (testPrefix(fn.Name.Name, "Test") || testPrefix(fn.Name.Name, "Fuzz")) {
				set[fn.Name.Name] = true
			}
		}
	}
	for _, e := range doc.Examples(files...) {
		if e.Output != "" || e.EmptyOutput {
			set["Example"+e.Name] = true
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func inventory(in inventoryInput) ([]laneTest, error) {
	filter, err := regexp.Compile(in.Run)
	if err != nil {
		return nil, err
	}
	exact, found := map[string]bool{}, map[string]bool{}
	for _, name := range in.Tests {
		exact[name] = true
	}
	var out []laneTest
	for _, p := range in.Packages {
		names, err := discover(p)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 && len(exact) == 0 {
			out = append(out, laneTest{p.ImportPath, ""}) // explicit compile-only package
		}
		for _, name := range names {
			if len(exact) > 0 && !exact[name] {
				continue
			}
			// Go's slash/alternation matcher selects subtests dynamically. Keep
			// all candidate parents for those filters and pass the ORIGINAL -run
			// unchanged at execution; never approximate away a matching parent.
			if !strings.Contains(in.Run, "/") && !filter.MatchString(name) {
				continue
			}
			found[name] = true
			out = append(out, laneTest{p.ImportPath, name})
		}
	}
	for name := range exact {
		if !found[name] {
			return nil, fmt.Errorf("requested test absent or excluded by -run: %s", name)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tests matched the requested packages/filter/list")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package == out[j].Package {
			return out[i].Name < out[j].Name
		}
		return out[i].Package < out[j].Package
	})
	return out, nil
}

func main() {
	var in inventoryInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		panic(err)
	}
	out, err := inventory(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	parents := map[string][]string{}
	for _, p := range in.Packages {
		names, err := discover(p)
		if err != nil {
			panic(err)
		}
		parents[p.ImportPath] = names
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Units   []laneTest
		Parents map[string][]string
	}{out, parents}); err != nil {
		panic(err)
	}
}
