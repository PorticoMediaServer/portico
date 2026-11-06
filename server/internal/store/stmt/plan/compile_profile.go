package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// Approved by the same-head Linux/amd64 + Darwin/arm64 comparison in
// run42265422658 (issue1154), comparison SHA256
// be279a84e6fdb0e7c423ed7569b4811b64d305f6d0e3e195f7f2e026603434ec.
// These are matching whole profiles, never independent option allow-lists.
type compileProfile struct{ platform, compiler, mutex string }

// SHA256 of the exact ordered JSON array of all54 other reviewed options.
const approvedOtherOptionsSHA256 = "d0fa9d3823cc55e94b4a15d0536cab79db9c4521f9449c92f88d3391a54297b2"

type compileSpan struct {
	path       string
	start, end int
}

// The standard JSON decoder locates only environment.compileOptions values.
// SQL strings or similarly named fields elsewhere cannot obtain equivalence.
func compileSpans(data []byte) ([]compileSpan, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	var spans []compileSpan
	var walk func(string, string) error
	walk = func(path, parent string) error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			for d.More() {
				token, err := d.Token()
				if err != nil {
					return err
				}
				key := token.(string)
				child := path + "." + key
				if parent == "environment" && key == "compileOptions" {
					var raw json.RawMessage
					if err := d.Decode(&raw); err != nil {
						return err
					}
					end := int(d.InputOffset())
					spans = append(spans, compileSpan{child, end - len(raw), end})
				} else if err := walk(child, key); err != nil {
					return err
				}
			}
			_, err = d.Token()
		case json.Delim('['):
			for i := 0; d.More(); i++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, i), ""); err != nil {
					return err
				}
			}
			_, err = d.Token()
		}
		return err
	}
	if err := walk("$", ""); err != nil {
		return nil, err
	}
	return spans, nil
}

func identifyCompileProfile(raw []byte) (compileProfile, error) {
	var rows Rows
	if err := json.Unmarshal(raw, &rows); err != nil {
		return compileProfile{}, err
	}
	if len(rows.Columns) != 1 || rows.Columns[0] != "compile_options" || len(rows.Values) != 56 {
		return compileProfile{}, fmt.Errorf("expected compile_options with exactly56 options")
	}
	var compiler, mutex string
	threadsafe := false
	var otherOptions []string
	for _, row := range rows.Values {
		if len(row) != 1 {
			return compileProfile{}, fmt.Errorf("invalid compile option row")
		}
		value, ok := row[0].(string)
		if !ok {
			return compileProfile{}, fmt.Errorf("non-text compile option")
		}
		if strings.HasPrefix(value, "COMPILER=") {
			if compiler != "" {
				return compileProfile{}, fmt.Errorf("duplicate compiler option")
			}
			compiler = value
		}
		if strings.HasPrefix(value, "MUTEX_") {
			if mutex != "" {
				return compileProfile{}, fmt.Errorf("extra mutex option")
			}
			mutex = value
		}
		if !strings.HasPrefix(value, "COMPILER=") && !strings.HasPrefix(value, "MUTEX_") {
			otherOptions = append(otherOptions, value)
		}
		threadsafe = threadsafe || value == "THREADSAFE=1"
	}
	otherJSON, _ := json.Marshal(otherOptions)
	if fmt.Sprintf("%x", sha256.Sum256(otherJSON)) != approvedOtherOptionsSHA256 {
		return compileProfile{}, fmt.Errorf("the54 other compile options differ from the approved ordered vector")
	}
	for _, profile := range [...]compileProfile{
		{"linux/amd64", "COMPILER=gcc-12.2.0", "MUTEX_PTHREADS"},
		{"darwin/arm64", "COMPILER=clang-21.0.0", "MUTEX_NOOP"},
	} {
		if compiler == profile.compiler && mutex == profile.mutex && threadsafe {
			return profile, nil
		}
	}
	return compileProfile{}, fmt.Errorf("unapproved compiler/mutex pair %q/%q or THREADSAFE", compiler, mutex)
}

// Match just the two approved option strings in a comparison copy. Both
// original inputs/goldens stay intact. Every other byte, including the other54
// options, their order, engine/sourceID and all plans, remains an exact gate.
func compileOptionEquivalence(expected, actual []byte, platform string) ([]byte, error) {
	want, err := compileSpans(expected)
	if err != nil {
		return nil, err
	}
	got, err := compileSpans(actual)
	if err != nil {
		return nil, err
	}
	if len(want) != len(got) {
		return nil, fmt.Errorf("compile-option environment inventory differs")
	}
	var result bytes.Buffer
	position := 0
	for i, span := range want {
		other := got[i]
		if span.path != other.path {
			return nil, fmt.Errorf("compile-option environment path differs")
		}
		before, after := expected[span.start:span.end], actual[other.start:other.end]
		old, err := identifyCompileProfile(before)
		if err != nil {
			return nil, fmt.Errorf("%s reviewed profile: %w", span.path, err)
		}
		current, err := identifyCompileProfile(after)
		if err != nil {
			return nil, fmt.Errorf("%s actual profile: %w", span.path, err)
		}
		if current.platform != platform {
			return nil, fmt.Errorf("%s actual profile %s does not match platform %s", span.path, current.platform, platform)
		}
		matched := append([]byte(nil), before...)
		for _, pair := range [][2]string{{old.compiler, current.compiler}, {old.mutex, current.mutex}} {
			from, _ := json.Marshal(pair[0])
			to, _ := json.Marshal(pair[1])
			if bytes.Count(matched, from) != 1 {
				return nil, fmt.Errorf("%s duplicate or noncanonical option encoding", span.path)
			}
			matched = bytes.Replace(matched, from, to, 1)
		}
		if !bytes.Equal(matched, after) {
			return nil, fmt.Errorf("%s differs beyond the two approved options", span.path)
		}
		result.Write(expected[position:span.start])
		result.Write(matched)
		position = span.end
	}
	result.Write(expected[position:])
	return result.Bytes(), nil
}
