package enrich

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestTMDBCandidateBufferConsumesOnlySelectedOutput(t *testing.T) {
	body := tmdbCreditsFixture(101, 51)
	original := bytes.Clone(body)
	layout := tmdbMovieCounts{cast: 101, crew: 51}
	buffer := tmdbCreditBuffer{rows: make([]ScreenCredit, 152)}
	backing := buffer.rows
	selected, err := buffer.prepare(ProviderRef{"tmdb", Movie, "42"}, body, layout)
	if err != nil || selected.Count() != 150 {
		t.Fatal("W selection refused", err)
	}
	rows := selected.Rows()
	for i, row := range rows {
		wantOrdinal := int64(i)
		wantClass := Cast
		if i >= 100 {
			wantOrdinal++
			wantClass = Crew
		}
		if row.SourceOrdinal != wantOrdinal || row.Class != wantClass {
			t.Fatal("selected-only native order changed", i, row)
		}
	}
	assertTMDBBufferCleared(t, &buffer, backing)
	backing[0] = ScreenCredit{Name: "old buffer mutation"}
	if !reflect.DeepEqual(rows, selected.Rows()) || !bytes.Equal(original, body) {
		t.Fatal("output retained temporary input or altered immutable entity")
	}
	if again, err := buffer.prepare(ProviderRef{"tmdb", Movie, "42"}, body, layout); err == nil || again.Count() != 0 {
		t.Fatal("consumed buffer was reused", err)
	}
}

func TestTMDBCandidateBufferRetiresOnRefusal(t *testing.T) {
	original := string(tmdbCreditsFixture(101, 51))
	for name, body := range map[string][]byte{
		// Fails late, after many array slots have been filled.
		"partial_decode": []byte(strings.Replace(original, `"id":10050`, `"id":0`, 1)),
		// W validates even the row its policy would discard.
		"discarded_invalid_name": []byte(strings.Replace(original, `"name":"Cast 100"`, `"name":""`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			buffer := tmdbCreditBuffer{rows: make([]ScreenCredit, 152)}
			backing := buffer.rows
			selected, err := buffer.prepare(ProviderRef{"tmdb", Movie, "42"}, body, tmdbMovieCounts{101, 51})
			if err == nil || !reflect.DeepEqual(selected, PreparedScreenCredits{}) {
				t.Fatal("malformed input produced partial selected output", err)
			}
			assertTMDBBufferCleared(t, &buffer, backing)
		})
	}
}

func assertTMDBBufferCleared(t *testing.T, buffer *tmdbCreditBuffer, backing []ScreenCredit) {
	t.Helper()
	if buffer.rows != nil {
		t.Fatal("temporary buffer retained its backing")
	}
	for i, row := range backing {
		if row != (ScreenCredit{}) {
			t.Fatal("temporary candidate reference survived", i)
		}
	}
}

func TestTMDBCandidateBufferEmptyIsConsumed(t *testing.T) {
	buffer := tmdbCreditBuffer{rows: make([]ScreenCredit, 0)}
	got, err := buffer.prepare(ProviderRef{"tmdb", Movie, "42"}, []byte(`{"id":42,"title":"empty","credits":{"cast":[],"crew":[]}}`), tmdbMovieCounts{})
	if err != nil || got.Count() != 0 || buffer.rows != nil {
		t.Fatal("complete empty input changed", err)
	}
	var absent *tmdbCreditBuffer
	if _, err := absent.prepare(ProviderRef{}, nil, tmdbMovieCounts{}); !errors.Is(err, ErrTMDBMovie) {
		t.Fatal("nil buffer admitted", err)
	}
}
