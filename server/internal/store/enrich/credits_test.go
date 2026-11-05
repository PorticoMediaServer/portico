package enrich

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unsafe"
)

func subject() ProviderRef { return ProviderRef{"tmdb", Movie, "1"} }
func credit(class CreditClass, ord int64) ScreenCredit {
	return ScreenCredit{Class: class, Person: ProviderRef{"tmdb", Person, fmt.Sprint(ord + 1)}, CreditID: fmt.Sprint(ord), Name: fmt.Sprint("Person ", ord), Role: "Background", Department: "Crew", SourceOrdinal: ord, Billing: ord}
}
func cohort() []ScreenCredit {
	rows := make([]ScreenCredit, 0, 285)
	for i := range int64(200) {
		c := credit(Cast, i)
		c.Billing = 199 - i
		rows = append(rows, c)
	}
	for i := int64(200); i < 280; i++ {
		rows = append(rows, credit(Crew, i))
	}
	for i, role := range []string{"Director", "Writer", "Creator", "Composer", "Producer"} {
		c := credit(Crew, 280+int64(i))
		c.Role = role
		rows = append(rows, c)
	}
	return rows
}
func TestCreditPreparationTrimsBeforeAcceptedDocument(t *testing.T) {
	raw := cohort()
	p, err := PrepareScreenCredits(subject(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Count() != 150 {
		t.Fatal(p.Count())
	}
	rows := p.Rows()
	for i, c := range rows[:100] {
		if c.Class != Cast || c.Billing != int64(i) || c.SourceOrdinal != int64(199-i) {
			t.Fatalf("bad billing %d: %+v", i, c)
		}
	}
	for i, c := range rows[100:] {
		want := int64(200 + i)
		if i >= 45 {
			want = 280 + int64(i-45)
		}
		if c.Class != Crew || c.SourceOrdinal != want {
			t.Fatalf("bad crew %d: %+v", i, c)
		}
	}
	// No rejected rows affect the accepted hash or survive in a stage chunk.
	raw[0].Name = "Rejected Changed"
	raw[270].Name = "Rejected Crew Changed"
	again, err := PrepareScreenCredits(subject(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Digest() != again.Digest() {
		t.Fatal("discarded rows entered accepted digest")
	}
	var staged []ScreenCredit
	for cursor := 0; cursor < p.Count(); cursor += 16 {
		chunk := p.Chunk(cursor)
		if len(chunk) > 16 {
			t.Fatal("unbounded chunk")
		}
		staged = append(staged, chunk...)
	}
	if !slices.Equal(rows, staged) || len(p.Chunk(144)) != 6 || len(p.Chunk(-1)) != 0 || len(p.Chunk(150)) != 0 {
		t.Fatal("stage differs from retained cohort")
	}
}
func TestCreditPreparationIsImmutableAndDeterministic(t *testing.T) {
	raw := cohort()
	p, err := PrepareScreenCredits(subject(), raw)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(raw)
	again, err := PrepareScreenCredits(subject(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Digest() != again.Digest() || !slices.Equal(p.Rows(), again.Rows()) {
		t.Fatal("input order changed normalized output")
	}
	raw[0].Name = "Changed raw"
	rows := p.Rows()
	rows[0].Name = "Changed exported"
	chunk := p.Chunk(0)
	chunk[0].Name = "Changed chunk"
	if p.Rows()[0].Name == rows[0].Name || p.Rows()[0].Name == chunk[0].Name {
		t.Fatal("mutable input escaped")
	}
	changed := p.Rows()
	changed[0].Name = "Changed retained"
	other, err := PrepareScreenCredits(subject(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if other.Digest() == p.Digest() {
		t.Fatal("retained facts omitted from digest")
	}
}
func TestCreditPreparationKeepsMultipleRolesAndOpaqueIDs(t *testing.T) {
	a, b := credit(Crew, 1), credit(Crew, 2)
	a.Person.ExternalID = "001"
	b.Person = a.Person
	a.Role = "Director"
	b.Role = "Producer"
	p, err := PrepareScreenCredits(subject(), []ScreenCredit{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if p.Count() != 2 || p.Rows()[0].Person.ExternalID != "001" || p.Rows()[1].Role != "Producer" {
		t.Fatal(p.Rows())
	}
	local := subject()
	local.Provider = "future-provider"
	a.Person.Provider = local.Provider
	if _, err := PrepareScreenCredits(local, []ScreenCredit{a}); err != nil {
		t.Fatal("provider registry became closed:", err)
	}
	empty, err := PrepareScreenCredits(local, nil)
	if err != nil || empty.Count() != 0 || empty.Digest() == ([32]byte{}) {
		t.Fatal("empty cohort lacks exact digest")
	}
}
func TestCreditPreparationRejectsNamespaceAndMalformedRows(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ScreenCredit)
	}{
		{"provider", func(c *ScreenCredit) { c.Person.Provider = "tvdb" }},
		{"kind", func(c *ScreenCredit) { c.Person.Kind = Recording }},
		{"artist", func(c *ScreenCredit) { c.Class = "artist" }},
		{"ordinal", func(c *ScreenCredit) { c.SourceOrdinal = -1 }},
		{"billing", func(c *ScreenCredit) { c.Billing = -1 }},
		{"empty name", func(c *ScreenCredit) { c.Name = " " }},
		{"invalid utf8", func(c *ScreenCredit) { c.Name = "\xff" }},
		{"control", func(c *ScreenCredit) { c.Role = "Director\x00" }},
		{"oversize", func(c *ScreenCredit) { c.Role = strings.Repeat("x", 2001) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := credit(Crew, 0)
			tc.mutate(&c)
			p, err := PrepareScreenCredits(subject(), []ScreenCredit{c})
			if err == nil || p.Count() != 0 {
				t.Fatal("invalid row accepted")
			}
		})
	}
	c := credit(Cast, 0)
	if _, err := PrepareScreenCredits(subject(), []ScreenCredit{c, c}); err == nil {
		t.Fatal("duplicate ordinal accepted")
	}
	s := subject()
	s.Kind = Release
	if _, err := PrepareScreenCredits(s, nil); err == nil {
		t.Fatal("release accepted as screen")
	}
	s.Kind = "show"
	if err := s.Validate(); err == nil {
		t.Fatal("adapter alias not normalized")
	}
	if _, err := PrepareScreenCredits(subject(), make([]ScreenCredit, 32769)); err == nil {
		t.Fatal("unbounded input")
	}
}
func TestCreditPreparationUsesExactFrozenCrewPriority(t *testing.T) {
	// Late key crew outrank all50 earlier non-key rows, while output retains the
	// original ordinal. Each priority has51 rows; all50 director rows win.
	var raw []ScreenCredit
	for i := range int64(51) {
		c := credit(Crew, i)
		c.Role = "Producer"
		raw = append(raw, c)
	}
	for i := int64(51); i < 102; i++ {
		c := credit(Crew, i)
		c.Role = "Composer"
		raw = append(raw, c)
	}
	for i := int64(102); i < 153; i++ {
		c := credit(Crew, i)
		c.Role = "Created by"
		raw = append(raw, c)
	}
	for i := int64(153); i < 204; i++ {
		c := credit(Crew, i)
		c.Department = "Writing"
		raw = append(raw, c)
	}
	for i := int64(204); i < 255; i++ {
		c := credit(Crew, i)
		c.Role = "Series Director"
		raw = append(raw, c)
	}
	for start, want := range []int64{204, 153, 102, 51, 0} {
		p, err := PrepareScreenCredits(subject(), raw[:255-start*51])
		if err != nil {
			t.Fatal(err)
		}
		if p.Count() != 50 || p.Rows()[0].SourceOrdinal != want || p.Rows()[49].SourceOrdinal != want+49 {
			t.Fatalf("priority%d: %+v", start, p.Rows())
		}
	}
}
func BenchmarkPrepareScreenCredits(b *testing.B) {
	raw := cohort()
	for b.Loop() {
		if _, err := PrepareScreenCredits(subject(), raw); err != nil {
			b.Fatal(err)
		}
	}
}

// Pointer ownership tests use small slices of one large allocation. No OOM or
// GC timing assumption is needed to detect retention of the response buffer.
func TestCreditPreparationOwnsEveryRetainedString(t *testing.T) {
	values := []string{"tmdb", "movie", "subject-1", "cast", "person", "person-1", "credit-1", "Name", "Actor", "Acting"}
	backing := strings.Join(values, "|") + strings.Repeat("x", 1<<20)
	fields := make([]string, len(values))
	offset := 0
	for i, value := range values {
		fields[i] = backing[offset : offset+len(value)]
		offset += len(value) + 1
	}
	inputSubject := ProviderRef{fields[0], SubjectKind(fields[1]), fields[2]}
	input := ScreenCredit{Class: CreditClass(fields[3]), Person: ProviderRef{fields[0], SubjectKind(fields[4]), fields[5]}, CreditID: fields[6], Name: fields[7], Role: fields[8], Department: fields[9]}
	prepared, err := PrepareScreenCredits(inputSubject, []ScreenCredit{input})
	if err != nil {
		t.Fatal(err)
	}
	gotSubject := prepared.Subject()
	row := prepared.Rows()[0]
	pairs := []struct{ name, input, got string }{
		{"subject.provider", inputSubject.Provider, gotSubject.Provider},
		{"subject.kind", string(inputSubject.Kind), string(gotSubject.Kind)},
		{"subject.id", inputSubject.ExternalID, gotSubject.ExternalID},
		{"row.class", string(input.Class), string(row.Class)},
		{"row.person.provider", input.Person.Provider, row.Person.Provider},
		{"row.person.kind", string(input.Person.Kind), string(row.Person.Kind)},
		{"row.person.id", input.Person.ExternalID, row.Person.ExternalID},
		{"row.credit", input.CreditID, row.CreditID}, {"row.name", input.Name, row.Name},
		{"row.role", input.Role, row.Role}, {"row.department", input.Department, row.Department},
	}
	for _, pair := range pairs {
		if pair.input != pair.got {
			t.Errorf("%s changed value", pair.name)
		}
		if unsafe.StringData(pair.input) == unsafe.StringData(pair.got) {
			t.Errorf("%s retains response backing storage", pair.name)
		}
	}
}
