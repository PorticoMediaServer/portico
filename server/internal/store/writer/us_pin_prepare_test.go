package writer

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func usPINTestRequest() usPINRequest {
	return usPINRequest{account: "us-pin-account", profile: "us-pin-profile",
		profileLifetime: strings.Repeat("f", 32), expectedRevision: 1, expectedPINRevision: 1,
		hash: []byte(usPINFixtureHash)}
}

func TestUSPINPreparationFreezeAndPurposes(t *testing.T) {
	in := usPINTestRequest()
	prepared, err := prepareUSPIN(usPINSet, in)
	if err != nil {
		t.Fatal(err)
	}
	in.hash[7] = 'A'
	in.profile = "another-profile"
	if !bytes.Equal(prepared.request.hash, []byte(usPINFixtureHash)) || prepared.request.profile != "us-pin-profile" {
		t.Fatal("prepared payload aliases caller input")
	}
	for _, kind := range []CommandID{usPINSet, usPINClear} {
		d, ok := describeUSPINCommand(kind)
		if !ok || d.ID != kind || d.Durability != Full || d.Name == "" {
			t.Fatal("missing FULL draft descriptor")
		}
	}
	for _, kind := range []CommandID{SecurityAccept, USMemberLibraries, 531, 534} {
		if _, ok := describeUSPINCommand(kind); ok {
			t.Fatal("unrelated purpose described as PIN")
		}
		if p, err := prepareUSPIN(kind, usPINTestRequest()); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("unrelated purpose prepared")
		}
	}
	for _, hash := range [][]byte{nil, {}} {
		in := usPINTestRequest()
		in.hash = hash
		p, err := prepareUSPIN(usPINClear, in)
		if err != nil || p == nil || len(p.request.hash) != 0 {
			t.Fatal("explicit clear rejected", err)
		}
	}
}

func TestUSPINPreparationBoundsAndHashEnvelope(t *testing.T) {
	// No hashing/verification in this test: Cost is a metadata parser only.
	for _, version := range []byte{'a', 'b', 'y'} {
		hash := []byte(usPINFixtureHash)
		hash[2] = version
		if cost, err := bcrypt.Cost(hash); err != nil || cost != 8 || !usPINHashEnvelope(hash) {
			t.Fatal("valid PIN-cost envelope refused", err)
		}
	}
	cases := []struct {
		name string
		edit func(*usPINRequest)
	}{
		{"account", func(q *usPINRequest) { q.account = "" }},
		{"profile", func(q *usPINRequest) { q.profile = strings.Repeat("p", 257) }},
		{"lifetime", func(q *usPINRequest) { q.profileLifetime = strings.Repeat("F", 32) }},
		{"revision_zero", func(q *usPINRequest) { q.expectedRevision = 0 }},
		{"revision_overflow", func(q *usPINRequest) { q.expectedRevision = usMaxRevision }},
		{"pin_revision_zero", func(q *usPINRequest) { q.expectedPINRevision = 0 }},
		{"pin_revision_overflow", func(q *usPINRequest) { q.expectedPINRevision = usMaxRevision }},
		{"empty", func(q *usPINRequest) { q.hash = nil }},
		{"short", func(q *usPINRequest) { q.hash = q.hash[:59] }},
		{"long", func(q *usPINRequest) { q.hash = append(q.hash, '.') }},
		{"password_cost", func(q *usPINRequest) { q.hash[4], q.hash[5] = '1', '0' }},
		{"low_cost", func(q *usPINRequest) { q.hash[5] = '4' }},
		{"version", func(q *usPINRequest) { q.hash[2] = 'x' }},
		{"separator", func(q *usPINRequest) { q.hash[6] = '.' }},
		{"encoding", func(q *usPINRequest) { q.hash[30] = ':' }},
		{"salt_padding", func(q *usPINRequest) { q.hash[28] = '/' }},
		{"digest_padding", func(q *usPINRequest) { q.hash[59] = '/' }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := usPINTestRequest()
			tc.edit(&q)
			p, err := prepareUSPIN(usPINSet, q)
			if p != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid preparation accepted")
			}
			if strings.Contains(err.Error(), "$2") || strings.Contains(err.Error(), string(q.hash)) && len(q.hash) != 0 {
				t.Fatal("hash leaked in error")
			}
		})
	}
	if p, err := prepareUSPIN(usPINClear, usPINTestRequest()); p != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("clear accepted a hash")
	}
	q := usPINTestRequest()
	q.expectedRevision, q.expectedPINRevision = usMaxRevision-1, usMaxRevision-1
	if _, err := prepareUSPIN(usPINSet, q); err != nil {
		t.Fatal("last safe revision refused", err)
	}
}
