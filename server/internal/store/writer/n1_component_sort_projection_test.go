package writer

import (
	"errors"
	"testing"
)

func TestN1DomainCapUsesCanonicalProjection(t *testing.T) {
	original := inventoryFreshRequest(t)
	command, err := n1DomainCapCommand(original)
	if err != nil {
		t.Fatal("domain-cap source is not constructible", err)
	}
	file := command.request.files[0]
	projection, err := ProjectFilenameMetadata(file.title, file.sortTitle, file.metadataLanguage)
	if err != nil {
		t.Fatal(err)
	}
	if file.sortKey != projection.SortKey || file.sortHead != projection.SortHead || file.sortKeyKeepArticles != projection.SortKeyKeepArticles || file.sortHeadKeepArticles != projection.SortHeadKeepArticles {
		t.Fatal("fixture retained stale derived sort fields")
	}
	if len(file.title) != MaxTitleCharacters || len(file.objectIdentity) != 8192 || len(file.changeToken) != 4096 {
		t.Fatal("fix changed source caps")
	}
	// The real guard must still refuse the old cloned field, not silently repair
	// an inconsistent source input. Use the actual immutable constructor.
	invalid := inventoryTestClone(command.request)
	invalid.files[0].sortKey = original.request.files[0].sortKey
	rejected, err := newInventoryAcceptCommand(command.identity, invalid)
	var policy *PolicyError
	if rejected != nil || !errors.As(err, &policy) || policy.Field != "inventory_sort_projection" {
		t.Fatal("projection guard was bypassed", err)
	}
}
