package writer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

func directImageFixture() USDirectSnapshotImage {
	return USDirectSnapshotImage{
		Authority: "local", ServerID: "server",
		Account:   USDirectAccountImage{ID: "account", Username: "native user", PrimaryProfileID: "primary", Role: "member", Revision: 7, AllowedLibraries: []string{"b", "a"}, DisabledAt: "2026-11-07T00:00:00Z", DisabledReason: "owner", HostedAccountID: "hosted"},
		Profiles:  []USDirectProfileImage{{ID: "primary", Name: " Main ", Primary: true, Position: 2, Art: "blue", Revision: 9, PINRevision: 4, PINRequired: true, AllowedLibraries: []string{"b", "a"}}, {ID: "child", Name: "Kids", Art: "mint", Revision: 1}},
		CanManage: true, Capabilities: USDirectCapabilitiesImage{true, true, true, true}, RememberProfiles: true,
	}
}
func imageSHA(t *testing.T, v USDirectSnapshotImage) [32]byte {
	t.Helper()
	d, e := v.ManifestSHA256()
	if e != nil {
		t.Fatal(e)
	}
	return d
}

// Walk every native leaf, including nested capability flags and each ordered
// cohort element. These are data mutations, never permission evaluation.
func TestUSDirectSnapshotImageEveryField(t *testing.T) {
	base := directImageFixture()
	var visit func(reflect.Value, []int, string)
	visit = func(v reflect.Value, path []int, name string) {
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i), append(append([]int{}, path...), i), name+"."+v.Type().Field(i).Name)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i), append(append([]int{}, path...), i), fmt.Sprintf("%s[%d]", name, i))
			}
		default:
			t.Run(name, func(t *testing.T) {
				next := base.Clone()
				p := reflect.ValueOf(&next).Elem()
				for _, i := range path {
					if p.Kind() == reflect.Struct {
						p = p.Field(i)
					} else {
						p = p.Index(i)
					}
				}
				switch p.Kind() {
				case reflect.String:
					p.SetString(p.String() + "x")
				case reflect.Bool:
					p.SetBool(!p.Bool())
				case reflect.Int, reflect.Int64:
					p.SetInt(p.Int() + 1)
				default:
					t.Fatal(p.Kind())
				}
				if imageSHA(t, base) == imageSHA(t, next) {
					t.Fatal("field omitted")
				}
			})
		}
	}
	visit(reflect.ValueOf(base), nil, "snapshot")
}
func TestUSDirectSnapshotImageOwnershipAndShapes(t *testing.T) {
	base := directImageFixture()
	copy := base.Clone()
	if !reflect.DeepEqual(base, copy) {
		t.Fatal("clone normalized")
	}
	copy.Account.AllowedLibraries[0] = "changed"
	copy.Profiles[0].AllowedLibraries[0] = "changed"
	copy.Profiles[0].Name = "changed"
	if base.Account.AllowedLibraries[0] != "b" || base.Profiles[0].AllowedLibraries[0] != "b" || base.Profiles[0].Name != " Main " {
		t.Fatal("alias")
	}
	controls := []struct {
		name string
		set  func(*USDirectSnapshotImage)
	}{
		{"profiles", func(v *USDirectSnapshotImage) { v.Profiles = []USDirectProfileImage{} }},
		{"account libraries", func(v *USDirectSnapshotImage) { v.Account.AllowedLibraries = []string{} }},
		{"profile libraries", func(v *USDirectSnapshotImage) { v.Profiles[0].AllowedLibraries = []string{} }},
	}
	for _, c := range controls {
		t.Run(c.name, func(t *testing.T) {
			a := base.Clone()
			c.set(&a)
			b := a.Clone()
			switch c.name {
			case "profiles":
				b.Profiles = nil
			case "account libraries":
				b.Account.AllowedLibraries = nil
			default:
				b.Profiles[0].AllowedLibraries = nil
			}
			if imageSHA(t, a) == imageSHA(t, b) {
				t.Fatal("nil/empty collapsed")
			}
			if !reflect.DeepEqual(a, a.Clone()) || !reflect.DeepEqual(b, b.Clone()) {
				t.Fatal("shape lost")
			}
		})
	}
	for _, change := range []func(*USDirectSnapshotImage){
		func(v *USDirectSnapshotImage) { v.Profiles[0], v.Profiles[1] = v.Profiles[1], v.Profiles[0] },
		func(v *USDirectSnapshotImage) {
			v.Account.AllowedLibraries[0], v.Account.AllowedLibraries[1] = v.Account.AllowedLibraries[1], v.Account.AllowedLibraries[0]
		},
		func(v *USDirectSnapshotImage) {
			v.Profiles[0].AllowedLibraries[0], v.Profiles[0].AllowedLibraries[1] = v.Profiles[0].AllowedLibraries[1], v.Profiles[0].AllowedLibraries[0]
		},
		func(v *USDirectSnapshotImage) { v.Profiles[0].Name = strings.TrimSpace(v.Profiles[0].Name) },
		func(v *USDirectSnapshotImage) { v.Account.HostedAccountID = "" },
		func(v *USDirectSnapshotImage) { v.Account.DisabledAt = "" },
		func(v *USDirectSnapshotImage) { v.Account.DisabledReason = "" },
	} {
		v := base.Clone()
		change(&v)
		if imageSHA(t, v) == imageSHA(t, base) {
			t.Fatal("native distinction lost")
		}
	}
}
func TestUSDirectSnapshotImageRedaction(t *testing.T) {
	v := directImageFixture()
	for _, item := range []any{v, &v, v.Account, &v.Account, v.Profiles[0], &v.Profiles[0], v.Capabilities, &v.Capabilities, v.Profiles} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%20.3v"} {
			got := fmt.Sprintf(format, item)
			if !strings.Contains(got, "redacted") || strings.Contains(got, "native user") || strings.Contains(got, "primary") {
				t.Fatalf("leak: %s", got)
			}
		}
	}
}
func TestUSDirectSnapshotImageCodecBudget(t *testing.T) {
	v := directImageFixture()
	v.ServerID = strings.Repeat("a", maxCanonicalRequestBytes)
	if _, err := v.ManifestSHA256(); err == nil {
		t.Fatal("over budget accepted")
	}
	// Input edit caps do not become source-image cohort ceilings.
	v = directImageFixture()
	v.Profiles = make([]USDirectProfileImage, 1001)
	v.Account.AllowedLibraries = make([]string, 1001)
	if _, err := v.ManifestSHA256(); err != nil {
		t.Fatal(err)
	}
}

// AST inspection keeps the legacy identity dependency out of the writer while
// failing on added, removed, reordered or retyped public DTO fields.
func TestUSDirectSnapshotImageSourceShape(t *testing.T) {
	for _, c := range []struct {
		file, dto string
		image     any
	}{
		{"direct_accounts.go", "DirectSnapshot", USDirectSnapshotImage{}},
		{"direct_accounts.go", "DirectAccount", USDirectAccountImage{}},
		{"direct_profiles.go", "DirectProfile", USDirectProfileImage{}},
		{"sign_in_kind.go", "ManagementCapabilities", USDirectCapabilitiesImage{}},
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "../../identity/"+c.file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var fields *ast.FieldList
		ast.Inspect(f, func(n ast.Node) bool {
			if ts, ok := n.(*ast.TypeSpec); ok && ts.Name.Name == c.dto {
				fields = ts.Type.(*ast.StructType).Fields
				return false
			}
			return true
		})
		if fields == nil {
			t.Fatal(c.dto)
		}
		typ := reflect.TypeOf(c.image)
		if len(fields.List) != typ.NumField() {
			t.Fatalf("%s field count drift", c.dto)
		}
		for i, f := range fields.List {
			if len(f.Names) != 1 || f.Names[0].Name != typ.Field(i).Name {
				t.Fatalf("%s field %d drift", c.dto, i)
			}
			var native func(ast.Expr) string
			native = func(e ast.Expr) string {
				switch n := e.(type) {
				case *ast.Ident:
					switch n.Name {
					case "DirectAccount":
						return "USDirectAccountImage"
					case "DirectProfile":
						return "USDirectProfileImage"
					case "ManagementCapabilities":
						return "USDirectCapabilitiesImage"
					}
					return n.Name
				case *ast.ArrayType:
					if n.Len == nil {
						return "[]" + native(n.Elt)
					}
				}
				return "unsupported"
			}
			got := typ.Field(i).Type.String()
			got = strings.ReplaceAll(got, "writer.", "")
			if native(f.Type) != got {
				t.Fatalf("%s.%s type drift: %s versus %s", c.dto, typ.Field(i).Name, native(f.Type), got)
			}
		}
	}
}

func TestUSDirectSnapshotImagePreservesNativeStringBytes(t *testing.T) {
	a := directImageFixture()
	a.Account.Username = string([]byte{0xff})
	a.Profiles[0].AllowedLibraries[0] = string([]byte{0xfe})
	want := imageSHA(t, a)
	for _, mutate := range []func(*USDirectSnapshotImage){
		func(v *USDirectSnapshotImage) { v.Account.Username = string([]byte{0xfe}) },
		func(v *USDirectSnapshotImage) { v.Profiles[0].AllowedLibraries[0] = string([]byte{0xff}) },
	} {
		b := a.Clone()
		mutate(&b)
		if imageSHA(t, b) == want {
			t.Fatal("native bytes collapsed before JSON encoding")
		}
	}
}
