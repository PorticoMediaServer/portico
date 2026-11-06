package writer

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
)

func searchImageBeginRequestFixture(t *testing.T) (searchImageStageRequest, CommandIdentity) {
	t.Helper()
	identity := CommandIdentity{Incarnation: [16]byte{17}, Sequence: 8, OperationID: [16]byte{19}}
	r := searchImageStageRequest{variant: "image_stage",
		bank:   searchImageBeginBank{id: 7, key: "v1", role: "active", profile: searchpolicy.Compiled()},
		source: searchImageBeginSource{entityID: 41, libraryID: 3, kind: 1, revision: 11, incarnation: [16]byte{23}, title: "東京"},
		owner: searchImageBeginOwner{id: 29, entityID: 41, bankID: 7, incarnation: identity.Incarnation,
			revision: 19, inputGeneration: 5, publishedGeneration: 3, publishedImage: sql.NullInt64{Int64: 13, Valid: true},
			sourceIncarnation: [16]byte{23}, sourceRevision: 11, recipe: searchpolicy.RecipeV1, phase: "capture",
			leaseOwner: 1, leaseGeneration: 2, leaseExpiresMS: 2100000000000, nextAttempt: sql.NullInt64{Int64: 1000, Valid: true},
			lastSequence: 7, acknowledgedSequence: 7},
		header: searchImageBeginHeader{titleFold: "東京", titlePrefixTokens: "006771004eac", initialPrefixTokens: "006771004eac"},
	}
	m, err := searchmanifest.Prepare(searchImageBeginManifestInput(r), []searchmanifest.Row{{Field: 1, Prefix: "a", Lexical: "b", Normalized: "東京"}})
	if err != nil {
		t.Fatal(err)
	}
	r.header.expectedFragments, r.header.expectedBytes, r.header.expectedDigest = m.Count(), m.Bytes(), m.Digest()
	return r, identity
}

func TestSearchImageBeginRequestAndReference(t *testing.T) {
	r, identity := searchImageBeginRequestFixture(t)
	c, err := newSearchImageStageCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalRequestDigest(SearchImageStage, 1, identity, r)
	if err != nil || c.digest != want || !c.valid {
		t.Fatal("complete original request was not frozen/digested", err)
	}
	seed, err := searchmanifest.Seed(searchImageBeginManifestInput(r), r.header.expectedFragments, r.header.expectedBytes)
	if err != nil || c.seed != seed {
		t.Fatal("declared manifest seed differs", err)
	}
	ref, err := SearchImageStageReference(c)
	if err != nil || ref.Identity != identity || ref.RequestDigest != want {
		t.Fatal("reference is not original identity/digest", err)
	}
	ref.Identity.Sequence++
	ref.RequestDigest[0] ^= 1
	copyAgain, _ := SearchImageStageReference(c)
	if copyAgain.Identity != identity || copyAgain.RequestDigest != want {
		t.Fatal("reference retained caller-mutable authority")
	}
	// A concrete prepared domain command still cannot bypass the facade stubs.
	got, err := (&Writer{}).StageSearchImage(t.Context(), &PublisherDisclosure{}, c)
	if !errors.Is(err, ErrNotImplemented) || !reflect.DeepEqual(got, SearchImageStageResult{}) {
		t.Fatal("domain packet bypassed the contract stub", got, err)
	}
	if _, err := (&Writer{}).ResolveSearchImage(t.Context(), nil, PublisherReceiptRequest{JobID: r.owner.id, LibraryID: r.source.libraryID, EntityID: r.source.entityID, Receipt: copyAgain}); !errors.Is(err, ErrNotImplemented) {
		t.Fatal("reference fabricated a historical result")
	}
	// Generated control nonce is not PRE-allocation request content.
	c.retirementIncarnation[0] ^= 1
	unchanged, err := canonicalRequestDigest(SearchImageStage, 1, c.identity, c.request)
	if err != nil || unchanged != want {
		t.Fatal("retirement output rewrote original request", err)
	}
}

func TestSearchImageBeginManifestUsesNativeScope(t *testing.T) {
	r, _ := searchImageBeginRequestFixture(t)
	want := searchmanifest.Input{Subject: searchmanifest.Entity, SubjectID: 41, LibraryID: 3, Kind: 1,
		SearchGenerationID: 7, BankKey: "v1", EngineVersion: r.bank.profile.EngineVersion,
		UnicodeFingerprint: r.bank.profile.UnicodeFingerprint, Recipe: searchpolicy.RecipeV1,
		InputGeneration: 5, SourceRevision: 11, SourceIncarnation: [16]byte{23}}
	if got := searchImageBeginManifestInput(r); got != want {
		t.Fatalf("manifest substituted a different native counter/scope: %+v", got)
	}
	if want.SourceRevision == r.owner.revision || want.SourceRevision == r.owner.inputGeneration {
		t.Fatal("fixture must distinguish source, subject job and input counters")
	}
}

type searchImageForeignCommand struct{}

func (searchImageForeignCommand) isSearchImageStageCommand() { panic("marker invoked") }

type searchImageCommandWrapper struct{ SearchImageStageCommand }

func TestSearchImageBeginReferenceRejectsWrappersWithoutInvocation(t *testing.T) {
	r, identity := searchImageBeginRequestFixture(t)
	c, err := newSearchImageStageCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []SearchImageStageCommand{nil, (*searchImageStageCommand)(nil), &searchImageStageCommand{},
		searchImageForeignCommand{}, searchImageCommandWrapper{c}, searchImageCommandWrapper{searchImageForeignCommand{}}} {
		if ref, err := SearchImageStageReference(cmd); !errors.Is(err, ErrInvalid) || ref != (ReceiptReference{}) {
			t.Fatal("unrecognized/nonvalid packet exposed a reference", ref, err)
		}
	}
}

// Test-only field mutation, not a second production request encoder.
func searchImageBeginTestLeaves(v reflect.Value, visit func(reflect.Value)) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			searchImageBeginTestLeaves(v.Field(i), visit)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			searchImageBeginTestLeaves(v.Index(i), visit)
		}
	default:
		visit(reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem())
	}
}

func TestSearchImageBeginDigestBindsEveryNativeField(t *testing.T) {
	r, identity := searchImageBeginRequestFixture(t)
	c, err := newSearchImageStageCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		count++
		before := reflect.New(v.Type()).Elem()
		before.Set(v)
		switch v.Kind() {
		case reflect.Int64:
			v.SetInt(v.Int() + 1)
		case reflect.Uint8:
			v.SetUint(v.Uint() ^ 1)
		case reflect.Bool:
			v.SetBool(!v.Bool())
		case reflect.String:
			v.SetString(v.String() + "changed")
		default:
			t.Fatalf("uncovered native request type: %v", v.Type())
		}
		digest, err := canonicalRequestDigest(SearchImageStage, 1, identity, r)
		if err != nil || digest == c.digest {
			t.Fatalf("native request field %d omitted: %v", count, err)
		}
		prepared, err := newSearchImageStageCommand(identity, r)
		if err == nil && prepared.digest != digest {
			t.Fatalf("constructor hashes a partial request, field %d", count)
		}
		v.Set(before)
	})
	if count < 100 {
		t.Fatal("sparse native-field coverage", count)
	}
	for _, mutate := range []func(*CommandIdentity){func(i *CommandIdentity) { i.Incarnation[0] ^= 1 }, func(i *CommandIdentity) { i.Sequence++ }, func(i *CommandIdentity) { i.OperationID[0] ^= 1 }} {
		changed := identity
		mutate(&changed)
		digest, err := canonicalRequestDigest(SearchImageStage, 1, changed, r)
		if err != nil || digest == c.digest {
			t.Fatal("original command identity omitted", err)
		}
	}
	t.Logf("independently mutated %d complete native request leaves", count)
}

func TestSearchImageBeginOwnsRetainedStrings(t *testing.T) {
	r, identity := searchImageBeginRequestFixture(t)
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String {
			s := v.String()
			v.SetString((s + strings.Repeat("retention", 32768))[:len(s)])
		}
	})
	c, err := newSearchImageStageCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	var oldPointers, newPointers []*byte
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String {
			oldPointers = append(oldPointers, unsafe.StringData(v.String()))
		}
	})
	searchImageBeginTestLeaves(reflect.ValueOf(&c.request).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String {
			newPointers = append(newPointers, unsafe.StringData(v.String()))
		}
	})
	if len(oldPointers) != 13 || len(newPointers) != len(oldPointers) {
		t.Fatal("string ownership inventory changed")
	}
	for i := range oldPointers {
		if newPointers[i] != nil && oldPointers[i] == newPointers[i] {
			t.Fatal("retained caller backing string", i)
		}
	}
}

func TestSearchImageBeginStructuralRefusals(t *testing.T) {
	r, identity := searchImageBeginRequestFixture(t)
	for _, mutate := range []func(*searchImageStageRequest){
		func(r *searchImageStageRequest) { r.variant = "publish" },
		func(r *searchImageStageRequest) { r.owner.phase = "stage" },
		func(r *searchImageStageRequest) { r.owner.document = sql.NullInt64{Int64: 37, Valid: true} },
		func(r *searchImageStageRequest) { r.owner.contentDigest = "old-stage" },
		func(r *searchImageStageRequest) { r.owner.stagedBytes = 1 },
		func(r *searchImageStageRequest) { r.owner.afterFragmentID = 1 },
		func(r *searchImageStageRequest) { r.owner.acknowledgedSequence-- },
		func(r *searchImageStageRequest) { r.owner.revision = math.MaxInt64 },
		func(r *searchImageStageRequest) { r.owner.sourceRevision++ },
		func(r *searchImageStageRequest) { r.source.incarnation[0] ^= 1 },
		func(r *searchImageStageRequest) { r.source.kind = 2 },
		func(r *searchImageStageRequest) { r.owner.publishedImage = sql.NullInt64{} },
		func(r *searchImageStageRequest) { r.owner.inputGeneration = r.owner.publishedGeneration },
		func(r *searchImageStageRequest) { r.owner.leaseOwner = 0 },
		func(r *searchImageStageRequest) { r.owner.nextAttempt = sql.NullInt64{Int64: -1, Valid: true} },
		func(r *searchImageStageRequest) { r.bank.key = "unsupported" },
		func(r *searchImageStageRequest) { r.bank.role = "retired" },
		func(r *searchImageStageRequest) { r.bank.profile.UnicodeFingerprint = "bad" },
		func(r *searchImageStageRequest) { r.header.expectedFragments = -1 },
		func(r *searchImageStageRequest) { r.header.expectedBytes = -1 },
		func(r *searchImageStageRequest) { r.header.expectedFragments = 0 },
		func(r *searchImageStageRequest) { r.header.initialPrefixTokens = string([]byte{255}) },
		func(r *searchImageStageRequest) { r.source.title = strings.Repeat("a", MaxTitleCharacters+1) },
	} {
		bad := r
		mutate(&bad)
		if c, err := newSearchImageStageCommand(identity, bad); !errors.Is(err, ErrInvalid) || c != nil {
			t.Fatal("unsupported or stale-shaped packet admitted", err)
		}
	}
}

func TestSearchImageBeginZeroManifestAndFreshEligibilitySeparation(t *testing.T) {
	r, identity := searchImageBeginRequestFixture(t)
	r.header.expectedFragments, r.header.expectedBytes = 0, 0
	r.header.expectedDigest, _ = searchmanifest.Seed(searchImageBeginManifestInput(r), 0, 0)
	if _, err := newSearchImageStageCommand(identity, r); err != nil {
		t.Fatal("valid empty manifest refused", err)
	}
	r.bank.profile.UnicodeFingerprint = searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("0", 64)
	r.header.expectedDigest, _ = searchmanifest.Seed(searchImageBeginManifestInput(r), 0, 0)
	if _, err := newSearchImageStageCommand(identity, r); err != nil {
		t.Fatal("preparation incorrectly proved fresh profile eligibility", err)
	}
	// This packet is STILL subject to fresh native engine/profile/lease/source
	// checks after historical receipt resolution. No execution is tested here.
}

func TestSearchImageBeginOversizedTextRefusesBeforeAllocation(t *testing.T) {
	r, identity := searchImageBeginRequestFixture(t)
	r.header.titlePrefixTokens = ""
	otherTextBytes := 0
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String {
			otherTextBytes += v.Len()
		}
	})
	backing := strings.Repeat("x", maxCanonicalRequestBytes+1)
	for _, offset := range []int{-1, 0, 1} {
		t.Run(map[int]string{-1: "limit-minus-one", 0: "limit", 1: "limit-plus-one"}[offset], func(t *testing.T) {
			// TEXT alone fits at -1/0, but the COMPLETE typed wire does not.
			r.header.titlePrefixTokens = backing[:maxCanonicalRequestBytes+offset-otherTextBytes]
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			c, err := newSearchImageStageCommand(identity, r)
			runtime.ReadMemStats(&after)
			allocated := after.TotalAlloc - before.TotalAlloc
			if !errors.Is(err, ErrInvalid) || c != nil || allocated > 64<<10 {
				t.Fatalf("TEXT total=%d cloned/hashed before overhead refusal: %v, allocated=%d", maxCanonicalRequestBytes+offset, err, allocated)
			}
			t.Logf("TEXT total=%d, allocated=%d", maxCanonicalRequestBytes+offset, allocated)
		})
	}
}

func TestSearchImageBeginCanonicalEnvelopeBoundary(t *testing.T) {
	// Use the SHARED encoder, not a duplicate production size/encoding routine.
	e := requestEncoder{h: sha256.New()}
	e.value(reflect.ValueOf(searchImageStageRequest{}), 0)
	// Fixed codec header: domain + purpose/version + incarnation + sequence +
	// operation ID. All TEXT values in this request are empty for this probe.
	wireFixed := e.written + uint64(len(canonicalRequestCodec)+4+16+8+16)
	if e.err != nil || wireFixed != searchImageStageFixedBytes {
		t.Fatalf("update reviewed request preflight for changed wire overhead: %d, %v", wireFixed, e.err)
	}
	r, identity := searchImageBeginRequestFixture(t)
	r.header.titlePrefixTokens = ""
	otherTextBytes := 0
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String {
			otherTextBytes += v.Len()
		}
	})
	backing := strings.Repeat("x", maxCanonicalRequestBytes)
	for _, offset := range []int{-1, 0, 1} {
		t.Run(map[int]string{-1: "wire-limit-minus-one", 0: "wire-limit", 1: "wire-limit-plus-one"}[offset], func(t *testing.T) {
			r.header.titlePrefixTokens = backing[:maxCanonicalRequestBytes-searchImageStageFixedBytes+offset-otherTextBytes]
			c, err := newSearchImageStageCommand(identity, r)
			if offset <= 0 {
				if err != nil || c == nil || !c.valid {
					t.Fatalf("valid complete wire boundary refused: %v", err)
				}
			} else if !errors.Is(err, ErrInvalid) || c != nil {
				t.Fatalf("complete wire boundary exceeded: %v", err)
			}
		})
	}
}
