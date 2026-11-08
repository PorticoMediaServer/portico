package writer

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

func usMemberProfileImageFixture() USMemberProfileImage {
	return USMemberProfileImage{
		ID: "secret-id", Name: "secret-name", Primary: true, Position: 7, Art: "secret-art",
		Revision: 9, PINRevision: 8, PINRequired: true,
		AllowedLibraries: []string{"secret-lib", "second"},
		Restrictions: USMemberProfileRestrictionsImage{
			ProfileID: "secret-profile", RatingSystem: "secret-system", MaximumAgeRating: "secret-rating",
			MaximumAge: 13, AllowUnrated: true, BlockedLabels: []string{"secret-label", "secret-label-2", "last"},
			AllowDownloads: true, AllowLiveTV: true, AllowDVR: true, AllowWatchTogether: true, Revision: 4,
		},
		Avatar: &USMemberProfileAvatarImage{
			ProfileID: "secret-avatar", Version: 3, UpdatedAt: "secret-time", SourceMIME: "secret-mime", URL: "secret-url",
		},
	}
}

func usMemberProfileImageSHA(t *testing.T, v USMemberProfileImage) [32]byte {
	t.Helper()
	d, err := v.ManifestSHA256()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Walk every native leaf in the image, including nested restriction flags,
// library and label ordinals, and the optional avatar. These are data
// mutations, never permission evaluation.
func TestUSMemberProfileEveryField(t *testing.T) {
	base := usMemberProfileImageFixture()
	navigate := func(root *USMemberProfileImage, path []int) reflect.Value {
		at := reflect.ValueOf(root).Elem()
		for _, i := range path {
			for at.Kind() == reflect.Ptr {
				at = at.Elem()
			}
			if at.Kind() == reflect.Struct {
				at = at.Field(i)
			} else {
				at = at.Index(i)
			}
		}
		return at
	}
	var visit func(reflect.Value, []int, string)
	visit = func(v reflect.Value, path []int, name string) {
		switch v.Kind() {
		case reflect.Ptr:
			if v.IsNil() {
				return
			}
			visit(v.Elem(), path, name)
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i), append(append([]int{}, path...), i), name+"."+v.Type().Field(i).Name)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i), append(append([]int{}, path...), i), fmt.Sprintf("%s[%d]", name, i))
			}
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int64:
			t.Run(name, func(t *testing.T) {
				next := base.Clone()
				leaf := navigate(&next, path)
				switch leaf.Kind() {
				case reflect.String:
					leaf.SetString(leaf.String() + "x")
				case reflect.Bool:
					leaf.SetBool(!leaf.Bool())
				case reflect.Int, reflect.Int64:
					leaf.SetInt(leaf.Int() + 1)
				default:
					t.Fatal(leaf.Kind())
				}
				if usMemberProfileImageSHA(t, base) == usMemberProfileImageSHA(t, next) {
					t.Fatal("field omitted")
				}
			})
		default:
			t.Fatalf("%s unsupported kind %v", name, v.Kind())
		}
	}
	visit(reflect.ValueOf(base), nil, "profile")
}

func TestUSMemberProfileOwnershipAndShapes(t *testing.T) {
	base := usMemberProfileImageFixture()
	clone := base.Clone()
	if !reflect.DeepEqual(base, clone) {
		t.Fatal("clone normalized")
	}
	clone.ID = "changed"
	clone.Name = "changed"
	clone.Art = "changed"
	clone.AllowedLibraries[0] = "changed"
	clone.Restrictions.ProfileID = "changed"
	clone.Restrictions.RatingSystem = "changed"
	clone.Restrictions.MaximumAgeRating = "changed"
	clone.Restrictions.BlockedLabels[0] = "changed"
	clone.Avatar.ProfileID = "changed"
	clone.Avatar.UpdatedAt = "changed"
	clone.Avatar.SourceMIME = "changed"
	clone.Avatar.URL = "changed"
	if base.ID != "secret-id" || base.Name != "secret-name" || base.Art != "secret-art" ||
		base.AllowedLibraries[0] != "secret-lib" || base.Restrictions.ProfileID != "secret-profile" ||
		base.Restrictions.RatingSystem != "secret-system" || base.Restrictions.MaximumAgeRating != "secret-rating" ||
		base.Restrictions.BlockedLabels[0] != "secret-label" || base.Avatar.ProfileID != "secret-avatar" ||
		base.Avatar.UpdatedAt != "secret-time" || base.Avatar.SourceMIME != "secret-mime" || base.Avatar.URL != "secret-url" {
		t.Fatal("caller mutation reached the source image")
	}
	// Element replacement is also independent of the caller's container.
	clone = base.Clone()
	clone.Avatar = &USMemberProfileAvatarImage{ProfileID: "other"}
	if base.Avatar.ProfileID != "secret-avatar" {
		t.Fatal("avatar replaced through the source image")
	}

	for _, change := range []struct {
		name string
		set  func(*USMemberProfileImage)
	}{
		{"library order", func(v *USMemberProfileImage) {
			v.AllowedLibraries[0], v.AllowedLibraries[1] = v.AllowedLibraries[1], v.AllowedLibraries[0]
		}},
		{"label order", func(v *USMemberProfileImage) {
			v.Restrictions.BlockedLabels[0], v.Restrictions.BlockedLabels[2] = v.Restrictions.BlockedLabels[2], v.Restrictions.BlockedLabels[0]
		}},
		{"label duplicate", func(v *USMemberProfileImage) { v.Restrictions.BlockedLabels = v.Restrictions.BlockedLabels[1:] }},
		{"legal trailing space", func(v *USMemberProfileImage) { v.Name = " " + v.Name }},
		{"repaired label", func(v *USMemberProfileImage) { v.Restrictions.BlockedLabels[0] = "?" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			next := base.Clone()
			change.set(&next)
			if usMemberProfileImageSHA(t, next) == usMemberProfileImageSHA(t, base) {
				t.Fatal("native distinction lost")
			}
		})
	}
}

func TestUSMemberProfileNilEmptyZero(t *testing.T) {
	base := usMemberProfileImageFixture()
	zero := USMemberProfileImage{}
	if usMemberProfileImageSHA(t, zero) == usMemberProfileImageSHA(t, base) {
		t.Fatal("zero image collapsed onto a populated image")
	}
	// Every leaf is encoded even when it is the zero value.
	navigate := func(root *USMemberProfileImage, path []int) reflect.Value {
		at := reflect.ValueOf(root).Elem()
		for _, i := range path {
			for at.Kind() == reflect.Ptr {
				at = at.Elem()
			}
			if at.Kind() == reflect.Struct {
				at = at.Field(i)
			} else {
				at = at.Index(i)
			}
		}
		return at
	}
	var zeroLeaves func(reflect.Value, []int, string)
	zeroLeaves = func(v reflect.Value, path []int, name string) {
		switch v.Kind() {
		case reflect.Ptr:
			if v.IsNil() {
				return
			}
			zeroLeaves(v.Elem(), path, name)
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				zeroLeaves(v.Field(i), append(append([]int{}, path...), i), name+"."+v.Type().Field(i).Name)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				zeroLeaves(v.Index(i), append(append([]int{}, path...), i), fmt.Sprintf("%s[%d]", name, i))
			}
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int64:
			t.Run(name, func(t *testing.T) {
				next := base.Clone()
				leaf := navigate(&next, path)
				leaf.Set(reflect.Zero(leaf.Type()))
				if usMemberProfileImageSHA(t, next) == usMemberProfileImageSHA(t, base) {
					t.Fatal("zero value omitted")
				}
			})
		default:
			t.Fatalf("%s unsupported kind %v", name, v.Kind())
		}
	}
	zeroLeaves(reflect.ValueOf(base), nil, "profile")

	for _, c := range []struct {
		name string
		edit func(*USMemberProfileImage)
	}{
		{"libraries nil", func(v *USMemberProfileImage) { v.AllowedLibraries = nil }},
		{"libraries empty", func(v *USMemberProfileImage) { v.AllowedLibraries = []string{} }},
		{"labels nil", func(v *USMemberProfileImage) { v.Restrictions.BlockedLabels = nil }},
		{"labels empty", func(v *USMemberProfileImage) { v.Restrictions.BlockedLabels = []string{} }},
		{"avatar nil", func(v *USMemberProfileImage) { v.Avatar = nil }},
		{"avatar zero", func(v *USMemberProfileImage) { v.Avatar = &USMemberProfileAvatarImage{} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := base.Clone()
			c.edit(&a)
			b := a.Clone()
			switch c.name {
			case "libraries nil":
				b.AllowedLibraries = []string{}
			case "libraries empty":
				b.AllowedLibraries = nil
			case "labels nil":
				b.Restrictions.BlockedLabels = []string{}
			case "labels empty":
				b.Restrictions.BlockedLabels = nil
			case "avatar nil":
				b.Avatar = &USMemberProfileAvatarImage{}
			default:
				b.Avatar = nil
			}
			if usMemberProfileImageSHA(t, a) == usMemberProfileImageSHA(t, b) {
				t.Fatal("nil and empty collapsed")
			}
			if !reflect.DeepEqual(a, a.Clone()) || !reflect.DeepEqual(b, b.Clone()) {
				t.Fatal("shape lost through clone")
			}
		})
	}
}

func TestUSMemberProfileRedaction(t *testing.T) {
	v := usMemberProfileImageFixture()
	// Only values owning a redacted String/GoString/Format are listed: a raw
	// []string carrier would print its elements.
	items := []any{
		v, &v, v.Restrictions, &v.Restrictions, *v.Avatar, v.Avatar,
	}
	for _, item := range items {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%20.4v"} {
			got := fmt.Sprintf(verb, item)
			if !strings.Contains(got, "redacted") || strings.Contains(got, "secret") {
				t.Fatalf("format %s leaked: %s", verb, got)
			}
		}
	}
}

func TestUSMemberProfileCodecBudget(t *testing.T) {
	// Native returned facts may hold invalid UTF-8; the image binds raw bytes
	// rather than rejecting or repairing them.
	v := usMemberProfileImageFixture()
	v.Name = string([]byte{255})
	v.Restrictions.BlockedLabels[0] = string([]byte{0xfe, 0xff})
	if _, err := v.ManifestSHA256(); err != nil {
		t.Fatal(err)
	}
	// Signed native integers, including negatives, are bound exactly.
	v = usMemberProfileImageFixture()
	v.Position = -1
	v.Restrictions.MaximumAge = -1
	if _, err := v.ManifestSHA256(); err != nil {
		t.Fatal(err)
	}
	// Input edit caps are not stored-source cohort ceilings.
	v = usMemberProfileImageFixture()
	v.AllowedLibraries = make([]string, 1001)
	v.Restrictions.BlockedLabels = make([]string, 1001)
	if _, err := v.ManifestSHA256(); err != nil {
		t.Fatal(err)
	}
	// The shared encoder budget refuses rather than truncating.
	v = usMemberProfileImageFixture()
	v.Name = strings.Repeat("x", maxCanonicalRequestBytes)
	if _, err := v.ManifestSHA256(); err == nil {
		t.Fatal("over-budget image accepted")
	}
}

func TestUSMemberProfileCohortManifest(t *testing.T) {
	base := usMemberProfileImageFixture()
	identity := CommandIdentity{Sequence: 1}
	identity.Incarnation[0] = 1
	identity.OperationID[0] = 2
	sha := func(id CommandIdentity, ordinal uint64, v USMemberProfileImage) [32]byte {
		t.Helper()
		d, err := usMemberProfileImageManifest(id, ordinal, v)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	want := sha(identity, 3, base)
	if sha(identity, 3, base) != want {
		t.Fatal("cohort manifest not stable")
	}
	for i := 0; i < 4; i++ {
		id := identity
		ordinal := uint64(3)
		switch i {
		case 0:
			id.Incarnation[0]++
		case 1:
			id.OperationID[0]++
		case 2:
			id.Sequence++
		case 3:
			ordinal++
		}
		if sha(id, ordinal, base) == want {
			t.Fatal("identity or ordinal not bound")
		}
	}
	// The image-only manifest stays independent of the cohort identity.
	if usMemberProfileImageSHA(t, base) == want {
		t.Fatal("cohort manifest does not bind its prefix")
	}
	mutated := base.Clone()
	mutated.Revision++
	if sha(identity, 3, mutated) == want {
		t.Fatal("cohort manifest not bound to the image")
	}
}

// AST inspection keeps the legacy identity dependency out of the writer test
// binary while failing on added, removed, reordered or retyped public DTO
// fields. The image must carry every native MemberProfile field, so a new
// public response field cannot silently disappear from saved output.
func TestUSMemberProfileSourceShape(t *testing.T) {
	native := func(file, name string) []string {
		t.Helper()
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "../../identity/"+file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s ceased to be a struct", name)
			}
			for _, field := range st.Fields.List {
				var typ bytes.Buffer
				if err := format.Node(&typ, fset, field.Type); err != nil {
					t.Fatal(err)
				}
				if len(field.Names) == 0 {
					got = append(got, typ.String())
				}
				for _, fieldName := range field.Names {
					got = append(got, fieldName.Name+":"+typ.String())
				}
			}
			return false
		})
		if len(got) == 0 {
			t.Fatalf("%s native DTO not found", name)
		}
		return got
	}
	for _, c := range []struct {
		file, name string
		want       []string
	}{
		{"direct_profiles.go", "DirectProfile", []string{
			"ID:string", "Name:string", "Primary:bool", "Position:int", "Art:string", "Revision:int64",
			"PINRevision:int64", "PINRequired:bool", "AllowedLibraries:[]string",
		}},
		{"restrictions.go", "ProfileRestrictions", []string{
			"ProfileID:string", "RatingSystem:string", "MaximumAgeRating:string", "MaximumAge:int",
			"AllowUnrated:bool", "BlockedLabels:[]string", "AllowDownloads:bool", "AllowLiveTV:bool",
			"AllowDVR:bool", "AllowWatchTogether:bool", "Revision:int64",
		}},
		{"avatars.go", "ProfileAvatar", []string{
			"ProfileID:string", "Version:int64", "UpdatedAt:string", "SourceMIME:string", "URL:string",
		}},
		{"member_profiles.go", "MemberProfile", []string{
			"DirectProfile", "Restrictions:ProfileRestrictions", "Avatar:*ProfileAvatar",
		}},
	} {
		if got := native(c.file, c.name); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s native response drift: %v", c.name, got)
		}
	}

	// The flattened image must mirror MemberProfile exactly, in order.
	want := []string{"ID", "Name", "Primary", "Position", "Art", "Revision", "PINRevision", "PINRequired", "AllowedLibraries"}
	for _, field := range native("member_profiles.go", "MemberProfile")[1:] {
		want = append(want, strings.SplitN(field, ":", 2)[0])
	}
	typ := reflect.TypeOf(USMemberProfileImage{})
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		got = append(got, typ.Field(i).Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("image field drift: %v", got)
	}
	names := func(typ reflect.Type) []string {
		out := make([]string, 0, typ.NumField())
		for i := 0; i < typ.NumField(); i++ {
			out = append(out, typ.Field(i).Name)
		}
		return out
	}
	fieldNames := func(dto []string) []string {
		out := make([]string, 0, len(dto))
		for _, field := range dto {
			out = append(out, strings.SplitN(field, ":", 2)[0])
		}
		return out
	}
	if got, expected := names(reflect.TypeOf(USMemberProfileRestrictionsImage{})), fieldNames(native("restrictions.go", "ProfileRestrictions")); !reflect.DeepEqual(got, expected) {
		t.Fatalf("restrictions image field drift: %v", got)
	}
	if got, expected := names(reflect.TypeOf(USMemberProfileAvatarImage{})), fieldNames(native("avatars.go", "ProfileAvatar")); !reflect.DeepEqual(got, expected) {
		t.Fatalf("avatar image field drift: %v", got)
	}
}
