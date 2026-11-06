package writer

import (
	"errors"
	"testing"
)

func TestInventoryFilenameMetadataProjection(t *testing.T) {
	for _, tc := range []struct{ name, title, explicit, language, key, head, keep, keepHead string }{
		{"article", "The Film", "", "en-US", "film", "f", "the film", "t"},
		{"explicit override", "The Film", "The Archive", "en-US", "the archive", "t", "the archive", "t"},
		{"language article", "Le Film", "", "fr-FR", "film", "f", "le film", "l"},
		{"unicode head", "日本語", "", "ja", "日本語", "日", "日本語", "日"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ProjectFilenameMetadata(tc.title, tc.explicit, tc.language)
			if err != nil || m != (InventoryFilenameMetadata{tc.title, tc.explicit, tc.key, tc.head, tc.keep, tc.keepHead, tc.language}) {
				t.Fatal(m, err)
			}
		})
	}
}

func TestInventoryAcceptPermitsAbsentSortTitle(t *testing.T) {
	for _, explicit := range []string{"", "The Archive"} {
		input, identity := inventoryPublicInput(t)
		m, err := ProjectFilenameMetadata("The Film", explicit, "en-US")
		if err != nil {
			t.Fatal(err)
		}
		f := &input.Files[0]
		f.Title, f.SortTitle, f.SortKey, f.SortHead = m.Title, m.SortTitle, m.SortKey, m.SortHead
		f.SortKeyKeepArticles, f.SortHeadKeepArticles, f.MetadataLanguage = m.SortKeyKeepArticles, m.SortHeadKeepArticles, m.MetadataLanguage
		command, err := NewInventoryAcceptCommand(identity, input)
		if err != nil {
			t.Fatal(err)
		}
		got := command.(*inventoryAcceptCommand).request.files[0]
		if got.title != m.Title || got.sortTitle != m.SortTitle || got.sortKey != m.SortKey || got.sortHead != m.SortHead ||
			got.sortKeyKeepArticles != m.SortKeyKeepArticles || got.sortHeadKeepArticles != m.SortHeadKeepArticles || got.metadataLanguage != m.MetadataLanguage {
			t.Fatal("projection changed", got, m)
		}
		if _, err = InventoryAcceptReference(command); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInventoryFilenameStillRequiresTitle(t *testing.T) {
	for _, title := range []string{"", "   "} {
		for _, explicit := range []string{"", "Override"} {
			if got, err := ProjectFilenameMetadata(title, explicit, "en-US"); !errors.Is(err, ErrInvalid) || got != (InventoryFilenameMetadata{}) {
				t.Fatal(got, err)
			}
			input, identity := inventoryPublicInput(t)
			input.Files[0].Title, input.Files[0].SortTitle = title, explicit
			if got, err := NewInventoryAcceptCommand(identity, input); !errors.Is(err, ErrInvalid) || got != nil {
				t.Fatal(got, err)
			}
		}
	}
}
