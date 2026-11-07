package writer

import (
	"errors"
	"strings"
	"testing"
)

func usMemberTestTarget() usMemberTarget {
	return usMemberTarget{account: "us-member", incarnation: strings.Repeat("e", 32), expectedRevision: 1, expectedEpoch: 1}
}

func TestUSMemberPreparationPurposesAndFreeze(t *testing.T) {
	in := usMemberRequest{target: usMemberTestTarget(), role: "admin"}
	p, err := prepareUSMember(usMemberRoleChange, in)
	if err != nil {
		t.Fatal(err)
	}
	in.target.account, in.role = "other", "owner"
	if p.request.target != usMemberTestTarget() || p.request.role != "admin" {
		t.Fatal("prepared input changed with caller scalars")
	}
	for _, id := range []CommandID{501, 502, 503} {
		d, ok := describeUSMemberCommand(id)
		if !ok || d.ID != id || d.Durability != Full || d.Name == "" {
			t.Fatal("missing explicit FULL purpose", id)
		}
	}
	for _, id := range []CommandID{SecurityAccept, 500, 504, USMemberLibraries} {
		if _, ok := describeUSMemberCommand(id); ok {
			t.Fatal("unrelated purpose described", id)
		}
		if p, err := prepareUSMember(id, usMemberRequest{target: usMemberTestTarget(), role: "admin"}); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("unrelated purpose prepared", id)
		}
	}
	if p, err := prepareUSMember(usAccountEpochAdvance, usMemberRequest{target: usMemberTestTarget()}); p != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("epoch borrowed a membership effect")
	}
	if _, err := prepareUSMember(usMembershipDisable, usMemberRequest{target: usMemberTestTarget()}); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareUSAccountEpoch(usMemberTestTarget()); err != nil {
		t.Fatal(err)
	}
}

func TestUSMemberPreparationBounds(t *testing.T) {
	for _, edit := range []func(*usMemberTarget){
		func(q *usMemberTarget) { q.account = "" },
		func(q *usMemberTarget) { q.account = strings.Repeat("a", 257) },
		func(q *usMemberTarget) { q.incarnation = strings.Repeat("E", 32) },
		func(q *usMemberTarget) { q.expectedRevision = 0 },
		func(q *usMemberTarget) { q.expectedRevision = usMaxRevision + 1 },
		func(q *usMemberTarget) { q.expectedEpoch = 0 },
		func(q *usMemberTarget) { q.expectedEpoch = usMaxRevision },
	} {
		q := usMemberTestTarget()
		edit(&q)
		if p, err := prepareUSMember(usMembershipDisable, usMemberRequest{target: q}); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid membership comparison prepared", q)
		}
		if p, err := prepareUSAccountEpoch(q); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid epoch comparison prepared", q)
		}
	}
	for _, role := range []string{"", "owner", "OWNER", "admin ", "viewer"} {
		if p, err := prepareUSMember(usMemberRoleChange, usMemberRequest{target: usMemberTestTarget(), role: role}); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("unsupported target tier prepared", role)
		}
	}
	if p, err := prepareUSMember(usMembershipDisable, usMemberRequest{target: usMemberTestTarget(), role: "member"}); p != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("disable accepted a tier edit")
	}
	q := usMemberTestTarget()
	q.expectedRevision, q.expectedEpoch = usMaxRevision-1, usMaxRevision-1
	if _, err := prepareUSMember(usMemberRoleChange, usMemberRequest{target: q, role: "member"}); err != nil {
		t.Fatal("last safe edit refused", err)
	}
	q.expectedRevision++ // the post-disable membership revision may be exhausted
	if _, err := prepareUSAccountEpoch(q); err != nil {
		t.Fatal("epoch could not compose after final membership edit", err)
	}
	if p, err := prepareUSMember(usMembershipDisable, usMemberRequest{target: q}); p != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("exhausted membership revision prepared")
	}
}
