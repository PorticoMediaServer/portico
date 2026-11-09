package read

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestRBGroupAuthorBooksUnicodePending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbGroupFixture(t, false)
		f.exec(t, `UPDATE catalog_entities SET title='Émile Zola' WHERE id=2005`)
		f.exec(t, `UPDATE catalog_entities SET author='Émile Zola',book_author_identity_key='author:émile zola' WHERE id=2006`)
		in := RBGroupInput{View: f.input, Family: RBGroupAuthorBooks, Subject: 2005}
		in.View.Kind = 8
		got, _, err := f.reader.rbGroupWindow(t.Context(), rbGroupRequest(), time.Second, f.output, in)
		if !errors.Is(err, ErrNotReady) || !reflect.DeepEqual(got, RBGroupPage{}) {
			t.Fatal("unproved Unicode book membership became a complete page", got, err)
		}
	})
}

func TestRBAuthorIndexedNameScope(t *testing.T) {
	for _, name := range []string{"", "Émile Zola", "émile Zola", "A\x00B", "\xff"} {
		if !errors.Is(rbAuthorIndexedName(name), ErrNotReady) {
			t.Fatal("unproved identity accepted", name)
		}
	}
	for _, name := range []string{"Author", "AUTHOR", "James Author"} {
		if err := rbAuthorIndexedName(name); err != nil {
			t.Fatal("ASCII scope refused", err)
		}
	}
	// The approved interim does not find unmatched non-ASCII variants of an
	// ASCII name. The post-lock stored key must unify this Kelvin-sign pair.
	if strings.ToLower("Kelvin") != strings.ToLower("Kelvin") {
		t.Fatal("Unicode identity premise changed")
	}
}
