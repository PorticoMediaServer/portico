package persistence

import (
	"encoding/json"
	"flag"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"sort"
)

var derivedClearanceOutput = flag.String("derived-clearance-report", "", "write the measured wildcard classification without narrowing any pin")

// Diagnostics share the census's already-loaded type profiles. A file has one
// primary bucket in the summary and may appear under several shared helpers;
// helper counts are explicitly non-additive. No bucket exempts a source file.
type derivedClearanceReport struct {
	patterns map[string]map[string]bool
	helpers  map[string]map[string]bool
}

func newDerivedClearanceReport() *derivedClearanceReport {
	return &derivedClearanceReport{patterns: map[string]map[string]bool{}, helpers: map[string]map[string]bool{}}
}

func (r *derivedClearanceReport) add(file, pattern string) {
	if r == nil {
		return
	}
	if r.patterns[file] == nil {
		r.patterns[file] = map[string]bool{}
	}
	r.patterns[file][pattern] = true
}

func (r *derivedClearanceReport) helper(file, name string) {
	if r == nil {
		return
	}
	if r.helpers[name] == nil {
		r.helpers[name] = map[string]bool{}
	}
	r.helpers[name][file] = true
}

func (r *derivedClearanceReport) sqlArgument(file string, expr ast.Expr, info *types.Info) {
	if r == nil {
		return
	}
	pattern := "SQL expression/fragment"
	switch expr := expr.(type) {
	case *ast.CallExpr:
		pattern = "SQL from builder call"
		if fn, ok := derivedFlowObject(expr.Fun, info).(*types.Func); ok {
			r.helper(file, fn.FullName())
		}
	case *ast.Ident:
		pattern = "SQL from variable/parameter"
	case *ast.SelectorExpr, *ast.IndexExpr:
		pattern = "SQL from field/collection"
	}
	r.add(file, pattern)
}

func (r *derivedClearanceReport) callable(file string, call *ast.CallExpr, info *types.Info, origins *derivedCallableOrigins) {
	if r == nil {
		return
	}
	pattern := "indirect function value"
	r.helper(file, "indirect type: "+types.TypeString(info.TypeOf(call.Fun), func(pkg *types.Package) string { return pkg.Path() }))
	if _, parameter := types.Unalias(info.TypeOf(call.Fun)).(*types.TypeParam); parameter {
		pattern = "function-constrained type parameter"
	}
	if variable, ok := derivedFlowObject(call.Fun, info).(*types.Var); ok {
		for provider := range origins.providerHints[variable] {
			r.helper(file, "tuple provider: "+provider)
		}
		switch {
		case variable.IsField():
			pattern = "function field"
		case variable.Pkg() != nil && variable.Parent() == variable.Pkg().Scope():
			pattern = "global function slot"
		case origins.unstable[variable]:
			pattern = "incoming/captured/addressed callback"
		default:
			for _, binding := range origins.writes[variable] {
				if factory, ok := binding.expr.(*ast.CallExpr); ok {
					pattern = "function returned by provider"
					if fn, ok := derivedFlowObject(factory.Fun, binding.info).(*types.Func); ok {
						r.helper(file, fn.FullName())
					}
				}
			}
		}
	}
	r.add(file, pattern)
}

func (r *derivedClearanceReport) write(path string, c writerCensus, owner string) error {
	type count struct {
		Pattern string   `json:"pattern"`
		Files   []string `json:"files"`
		Count   int      `json:"count"`
	}
	priority := []string{"reflect import", "typed reflection", "function-constrained type parameter", "incoming/captured/addressed callback", "function field", "global function slot", "function returned by provider", "indirect function value", "SQL from builder call", "SQL from variable/parameter", "SQL from field/collection", "unresolved SQL signature", "SQL expression/fragment", "unmodelled schema/temporary target", "escaped SQL executor"}
	buckets := map[string][]string{}
	foreign, local := map[string]bool{}, 0
	for file := range c.Dynamic {
		if filepath.ToSlash(filepath.Dir(file)) == owner {
			local++
			continue
		}
		foreign[file] = true
		primary := "unclassified (must investigate)"
		for _, label := range priority {
			if r.patterns[file][label] {
				primary = label
				break
			}
		}
		buckets[primary] = append(buckets[primary], file)
	}
	counts := func(groups map[string][]string) []count {
		result := []count{}
		for label, files := range groups {
			sort.Strings(files)
			result = append(result, count{label, files, len(files)})
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].Count != result[j].Count {
				return result[i].Count > result[j].Count
			}
			return result[i].Pattern < result[j].Pattern
		})
		return result
	}
	helpers := map[string][]string{}
	for helper, files := range r.helpers {
		for file := range files {
			if foreign[file] {
				helpers[helper] = append(helpers[helper], file)
			}
		}
	}
	output := struct {
		Total           int     `json:"wildcard_files"`
		OwnerLocal      int     `json:"owner_local_files"`
		Foreign         int     `json:"foreign_wildcard_files"`
		PrimaryPatterns []count `json:"primary_patterns"`
		SharedHelpers   []count `json:"shared_helpers_non_additive"`
	}{len(c.Dynamic), local, len(foreign), counts(buckets), counts(helpers)}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}
