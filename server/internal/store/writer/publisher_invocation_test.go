package writer

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"reflect"
	"testing"
)

func publisherInvocationFixture(t *testing.T) (*Writer, *PublisherDisclosure, *searchImageStageCommand, PublisherReceiptRequest) {
	t.Helper()
	r, identity := searchImageBeginRequestFixture(t)
	command, err := newSearchImageStageCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	w := &Writer{effective: &effectiveRuntime{}}
	disclosure := &PublisherDisclosure{issuer: w, scope: &publisherScope{jobID: r.owner.id, libraryID: r.source.libraryID,
		entityID: r.source.entityID, libraryKey: "component-library", incarnation: identity.Incarnation, lifetime: t.Context()}}
	ref, err := SearchImageStageReference(command)
	if err != nil {
		t.Fatal(err)
	}
	request := PublisherReceiptRequest{JobID: r.owner.id, LibraryID: r.source.libraryID, EntityID: r.source.entityID, Receipt: ref}
	return w, disclosure, command, request
}

func TestPublisherInvocationPreservesOriginalAndCannotExecute(t *testing.T) {
	w, disclosure, command, request := publisherInvocationFixture(t)
	for _, call := range []publisherCall{publisherStageImage, publisherResolveImage, publisherAckImage} {
		var input SearchImageStageCommand
		q := request
		if call == publisherStageImage {
			q = PublisherReceiptRequest{}
			input = command
		}
		v, err := preparePublisherInvocation(t.Context(), w, disclosure, call, q, input)
		if err != nil || v.request != request || v.disclosure != disclosure || v.call != call || (v.input == command) != (call == publisherStageImage) {
			t.Fatal("changed original reference/scope", v, err)
		}
		q.Receipt.RequestDigest[0] ^= 1
		q.Receipt.Identity.Sequence++
		if v.request != request {
			t.Fatal("invocation retained mutable request")
		}
	}
	// This protocol foundation supplies no profile, grant, native receipt or
	// private adapter substitute. Even a valid envelope cannot become Applied.
	result, err := w.StageSearchImage(t.Context(), disclosure, command)
	if !errors.Is(err, ErrNotImplemented) || !reflect.DeepEqual(result, SearchImageStageResult{}) {
		t.Fatal("protocol envelope enabled execution", result, err)
	}
	result, err = w.ResolveSearchImage(t.Context(), disclosure, request)
	if !errors.Is(err, ErrNotImplemented) || !reflect.DeepEqual(result, SearchImageStageResult{}) {
		t.Fatal("protocol envelope invented history", result, err)
	}
	ack, err := w.AckSearchImage(t.Context(), disclosure, request)
	if !errors.Is(err, ErrNotImplemented) || !reflect.DeepEqual(ack, AckResult{}) {
		t.Fatal("protocol envelope advanced floor", ack, err)
	}
}

func TestPublisherInvocationRejectsTamperedAndMixedDispatcherInputs(t *testing.T) {
	w, disclosure, command, _ := publisherInvocationFixture(t)
	v, err := preparePublisherInvocation(t.Context(), w, disclosure, publisherStageImage, PublisherReceiptRequest{}, command)
	if err != nil {
		t.Fatal(err)
	}
	bad := v
	bad.request.Receipt.RequestDigest[0] ^= 1
	if checked, err := validatePublisherInvocation(t.Context(), w, &bad); !errors.Is(err, ErrInvalid) || checked != (publisherInvocation{}) {
		t.Fatal("changed original reference accepted", checked, err)
	}
	for _, c := range []struct {
		input *effectiveStageBeginCommand
		ack   bool
		scan  *inventoryInvocation
	}{
		{input: &effectiveStageBeginCommand{}}, {ack: true}, {scan: &inventoryInvocation{}},
	} {
		out, floor, err := w.effective.runCommand(t.Context(), w, nil, EnrichmentReceiptRequest{}, c.input, c.ack, c.scan, commandInvocation{publisher: &v})
		if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(out, commandOutput{}) || !reflect.DeepEqual(floor, AckResult{}) {
			t.Fatal("mixed domain invocation entered execution", out, floor, err)
		}
	}
}

type publisherInvocationWrapper struct{ SearchImageStageCommand }

func TestPublisherInvocationRejectsTypesAndMixedCalls(t *testing.T) {
	w, disclosure, command, request := publisherInvocationFixture(t)
	for _, input := range []SearchImageStageCommand{nil, (*searchImageStageCommand)(nil), &searchImageStageCommand{}, contractPublisher{}, publisherInvocationWrapper{command}} {
		v, err := preparePublisherInvocation(t.Context(), w, disclosure, publisherStageImage, PublisherReceiptRequest{}, input)
		if !errors.Is(err, ErrInvalid) || v != (publisherInvocation{}) {
			t.Fatal("foreign payload invoked or accepted", v, err)
		}
	}
	for _, c := range []struct {
		call  publisherCall
		q     PublisherReceiptRequest
		input SearchImageStageCommand
	}{
		{0, request, nil}, {255, request, nil}, {publisherStageImage, request, command},
		{publisherResolveImage, request, command}, {publisherAckImage, request, (*searchImageStageCommand)(nil)},
	} {
		if v, err := preparePublisherInvocation(t.Context(), w, disclosure, c.call, c.q, c.input); !errors.Is(err, ErrInvalid) || v != (publisherInvocation{}) {
			t.Fatal("mixed/unknown invocation accepted", c.call, v, err)
		}
	}
}

func TestPublisherInvocationCurrentScopeAndLifetimes(t *testing.T) {
	for _, mode := range []string{"missing_writer", "missing_runtime", "missing_cap", "foreign_issuer", "missing_scope", "missing_lifetime", "revoked", "authority_canceled", "empty_library_key", "invalid_library_key", "job", "library", "entity", "incarnation", "zero_sequence", "caller_canceled", "nil_caller", "cancel_then_revoke", "revoke_then_cancel"} {
		t.Run(mode, func(t *testing.T) {
			w, disclosure, _, request := publisherInvocationFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := ErrDisclosureDenied
			var caller context.Context = ctx
			switch mode {
			case "missing_writer":
				w = nil
			case "missing_runtime":
				w.effective = nil
			case "missing_cap":
				disclosure = nil
			case "foreign_issuer":
				disclosure.issuer = &Writer{effective: w.effective}
			case "missing_scope":
				disclosure.scope = nil
			case "missing_lifetime":
				disclosure.scope.lifetime = nil
			case "revoked":
				disclosure.scope.revoked.Store(true)
			case "authority_canceled":
				life, stop := context.WithCancel(t.Context())
				stop()
				disclosure.scope.lifetime = life
			case "empty_library_key":
				disclosure.scope.libraryKey = ""
			case "invalid_library_key":
				disclosure.scope.libraryKey = "\xff"
			case "job":
				request.JobID++
			case "library":
				request.LibraryID++
			case "entity":
				request.EntityID++
			case "incarnation":
				request.Receipt.Identity.Incarnation[0] ^= 1
			case "zero_sequence":
				request.Receipt.Identity.Sequence = 0
			case "caller_canceled":
				cancel()
				want = context.Canceled
			case "nil_caller":
				caller = nil
				want = ErrInvalid
			case "cancel_then_revoke":
				cancel()
				disclosure.scope.revoked.Store(true)
			case "revoke_then_cancel":
				disclosure.scope.revoked.Store(true)
				cancel()
			}
			v, err := preparePublisherInvocation(caller, w, disclosure, publisherResolveImage, request, nil)
			if !errors.Is(err, want) || v != (publisherInvocation{}) {
				t.Fatal("scope/lifetime failure returned invocation", v, err, want)
			}
		})
	}
}

func TestPublisherProtocolRecordsRequireSharedFinish(t *testing.T) {
	for _, typ := range []string{"publisherInvocation", "publisherReceiptLookup"} {
		body := "package writer\nfunc bypass(ctx context.Context)(out " + typ + ",err error){return out,nil}"
		file, err := parser.ParseFile(token.NewFileSet(), "publisher_control.go", body, 0)
		if err != nil {
			t.Fatal(err)
		}
		if bad := unguardedCommandFinishes(file); len(bad) != 1 || bad[0] != "bypass" {
			t.Fatal("publisher record opted out of finish", typ, bad)
		}
	}
}
