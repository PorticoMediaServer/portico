package plan

import (
	"bytes"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

func compileFixture(t *testing.T, profile string, change func(map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		t.Fatal(err)
	}
	env := value["environment"].(map[string]any)
	env["schemaBinding"] = &SchemaBinding{Version: 1, SQLSHA256: schemaSQLSHA()}
	options := env["compileOptions"].(map[string]any)["values"].([]any)
	for _, option := range options {
		row := option.([]any)
		if profile == "darwin/arm64" {
			switch row[0] {
			case "COMPILER=gcc-12.2.0":
				row[0] = "COMPILER=clang-21.0.0"
			case "MUTEX_PTHREADS":
				row[0] = "MUTEX_NOOP"
			}
		}
	}
	if change != nil {
		change(value)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func changeCompileOption(value map[string]any, prefix, next string) {
	for _, row := range value["environment"].(map[string]any)["compileOptions"].(map[string]any)["values"].([]any) {
		option := row.([]any)
		if strings.HasPrefix(option[0].(string), prefix) {
			option[0] = next
			return
		}
	}
	panic("test option missing")
}

func TestPlanCompileProfileClosedEquivalence(t *testing.T) {
	linux := compileFixture(t, "linux/amd64", nil)
	darwin := compileFixture(t, "darwin/arm64", nil)
	for _, c := range []struct {
		platform string
		before   []byte
		after    []byte
	}{
		{"linux/amd64", linux, linux}, {"darwin/arm64", linux, darwin},
		{"linux/amd64", darwin, linux}, {"darwin/arm64", darwin, darwin},
	} {
		if err := compareJSONForPlatform(c.before, c.after, c.platform); err != nil {
			t.Fatal(c.platform, err)
		}
	}
	// The exported path binds the actual profile to this process's platform.
	actual := linux
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		actual = darwin
	}
	err := CompareJSON(linux, actual)
	if (runtime.GOOS+"/"+runtime.GOARCH == "linux/amd64" || runtime.GOOS+"/"+runtime.GOARCH == "darwin/arm64") != (err == nil) {
		t.Fatal("runtime profile binding", err)
	}
	// Actual golden consumers also wrap environments in arrays/Golden objects.
	wrapped := func(raw []byte) []byte { return append(append([]byte(`[{"Golden":`), raw...), []byte(`}]`)...) }
	if err := compareJSONForPlatform(wrapped(linux), wrapped(darwin), "darwin/arm64"); err != nil {
		t.Fatal("nested environment profile", err)
	}
}

func TestPlanCompileProfileRejectsUnapprovedPairs(t *testing.T) {
	linux := compileFixture(t, "linux/amd64", nil)
	cases := []struct {
		name   string
		change func(map[string]any)
	}{
		{"mixed", func(v map[string]any) { changeCompileOption(v, "MUTEX_", "MUTEX_PTHREADS") }},
		{"unknown-compiler", func(v map[string]any) { changeCompileOption(v, "COMPILER=", "COMPILER=clang-22.0.0") }},
		{"extra-option", func(v map[string]any) {
			opts := v["environment"].(map[string]any)["compileOptions"].(map[string]any)
			opts["values"] = append(opts["values"].([]any), []any{"UNKNOWN=1"})
		}},
		{"extra-mutex", func(v map[string]any) { changeCompileOption(v, "MAX_ATTACHED=", "MUTEX_PTHREADS") }},
		{"missing-compiler", func(v map[string]any) { changeCompileOption(v, "COMPILER=", "MISSING=1") }},
		{"threadsafe", func(v map[string]any) { changeCompileOption(v, "THREADSAFE=", "THREADSAFE=0") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			actual := compileFixture(t, "darwin/arm64", c.change)
			if err := compareJSONForPlatform(linux, actual, "darwin/arm64"); err == nil {
				t.Fatal("unapproved compile profile accepted")
			}
			if err := compareJSONForPlatform(actual, actual, "darwin/arm64"); err == nil {
				t.Fatal("identical malformed profile bypassed validation")
			}
		})
	}
	if err := compareJSONForPlatform(linux, compileFixture(t, "darwin/arm64", nil), "linux/amd64"); err == nil {
		t.Fatal("Darwin pair accepted on Linux")
	}
}

func TestPlanCompileProfilePreservesOtherBytes(t *testing.T) {
	linux := compileFixture(t, "linux/amd64", nil)
	for _, c := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"other-option", func(v map[string]any) { changeCompileOption(v, "MAX_ATTACHED=", "MAX_ATTACHED=999") }},
		{"sourceID", func(v map[string]any) {
			engine := v["environment"].(map[string]any)["engine"].(map[string]any)
			engine["values"].([]any)[0].([]any)[1] = "different sourceID"
		}},
		{"opcode", func(v map[string]any) {
			plan := v["plans"].([]any)[0].(map[string]any)
			plan["explain"].(map[string]any)["values"].([]any)[0].([]any)[1] = "ChangedOpcode"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := compareJSONForPlatform(linux, compileFixture(t, "darwin/arm64", c.change), "darwin/arm64"); err == nil {
				t.Fatal("non-equivalent field accepted")
			}
		})
	}
	// No similarly named property outside an actual environment is rewritten.
	if err := compareJSONForPlatform([]byte(`{"sql":"COMPILER=gcc-12.2.0"}`), []byte(`{"sql":"COMPILER=clang-21.0.0"}`), "darwin/arm64"); err == nil {
		t.Fatal("SQL text received compile-option equivalence")
	}
	if bytes.Equal(linux, compileFixture(t, "darwin/arm64", nil)) {
		t.Fatal("fixture/golden inputs were rewritten")
	}
}

// Reordering BOTH inputs must refuse even though removing the two pair rows
// leaves the same ordered54-option digest and the profiles still match.
func TestPlanCompileProfileRejectsReorderedPairs(t *testing.T) {
	for _, prefix := range []string{"COMPILER=", "MUTEX_"} {
		for _, reviewed := range []string{"linux/amd64", "darwin/arm64"} {
			t.Run(prefix+reviewed, func(t *testing.T) {
				reorder := func(v map[string]any) {
					rows := v["environment"].(map[string]any)["compileOptions"].(map[string]any)
					options := rows["values"].([]any)
					for i, row := range options {
						if strings.HasPrefix(row.([]any)[0].(string), prefix) {
							moved := append([]any(nil), options[:i]...)
							moved = append(moved, options[i+1:]...)
							rows["values"] = append(moved, row)
							return
						}
					}
					t.Fatal("test pair row absent")
				}
				want := compileFixture(t, reviewed, reorder)
				got := compileFixture(t, "linux/amd64", reorder)
				if err := compareJSONForPlatform(want, got, "linux/amd64"); err == nil || !strings.Contains(err.Error(), "option at row") {
					t.Fatalf("reordered whole profile accepted or wrong refusal: %v", err)
				}
			})
		}
	}
}

func TestPlanCompileProfileRejectsCompilerRowSwap(t *testing.T) {
	for _, reviewed := range []string{"linux/amd64", "darwin/arm64"} {
		t.Run(reviewed, func(t *testing.T) {
			swap := func(v map[string]any) {
				rows := v["environment"].(map[string]any)["compileOptions"].(map[string]any)["values"].([]any)
				rows[0], rows[1] = rows[1], rows[0]
				if !strings.HasPrefix(rows[51].([]any)[0].(string), "MUTEX_") {
					t.Fatal("compiler control moved the mutex row")
				}
			}
			// Removing COMPILER from either vector still yields the exact54
			// other options, and MUTEX stays51. Only the compiler pin refuses.
			want := compileFixture(t, reviewed, swap)
			got := compileFixture(t, "linux/amd64", swap)
			err := compareJSONForPlatform(want, got, "linux/amd64")
			if err == nil || !strings.Contains(err.Error(), "compiler option at row0, want1") {
				t.Fatalf("compiler row swap accepted or wrong refusal: %v", err)
			}
		})
	}
}
