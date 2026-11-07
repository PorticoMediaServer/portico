package writer

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func restrictionsResultFixture() (CommandIdentity, RequestDigest, usRestrictionsSavedResult) {
	return CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, RequestDigest{3}, usRestrictionsSavedResult{kind: usProfileRestrictionsEdit, version: 1, ownerID: "owner", account: "original-account", memberIncarnation: strings.Repeat("a", 32), profile: "profile", profileLifetime: strings.Repeat("b", 32), appliedMS: 100, revision: 2, ratingSystem: "mpaa", maximumAgeRating: "PG", maximumAge: 8, allowUnrated: true, allowDownloads: true, allowLiveTV: true, allowDVR: true, allowWatchTogether: true, labels: []string{"private-label", "second"}}
}

func TestUSRestrictionsResultDigestFields(t *testing.T) {
	i, r, v := restrictionsResultFixture()
	base, err := usRestrictionsResultDigest(i, r, v)
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*usRestrictionsSavedResult){
		"owner": func(v *usRestrictionsSavedResult) { v.ownerID = "other" }, "account": func(v *usRestrictionsSavedResult) { v.account = "other" }, "member": func(v *usRestrictionsSavedResult) { v.memberIncarnation = strings.Repeat("c", 32) }, "profile": func(v *usRestrictionsSavedResult) { v.profile = "other" }, "lifetime": func(v *usRestrictionsSavedResult) { v.profileLifetime = strings.Repeat("d", 32) }, "applied": func(v *usRestrictionsSavedResult) { v.appliedMS++ }, "revision": func(v *usRestrictionsSavedResult) { v.revision++ }, "system": func(v *usRestrictionsSavedResult) { v.ratingSystem = "retired" }, "rating": func(v *usRestrictionsSavedResult) { v.maximumAgeRating = "retired" }, "age": func(v *usRestrictionsSavedResult) { v.maximumAge = 99 }, "unrated": func(v *usRestrictionsSavedResult) { v.allowUnrated = false }, "downloads": func(v *usRestrictionsSavedResult) { v.allowDownloads = false }, "live": func(v *usRestrictionsSavedResult) { v.allowLiveTV = false }, "dvr": func(v *usRestrictionsSavedResult) { v.allowDVR = false }, "together": func(v *usRestrictionsSavedResult) { v.allowWatchTogether = false }, "label": func(v *usRestrictionsSavedResult) { v.labels[0] = "other" }, "order": func(v *usRestrictionsSavedResult) { v.labels[0], v.labels[1] = v.labels[1], v.labels[0] }, "count": func(v *usRestrictionsSavedResult) { v.labels = v.labels[:1] },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			q := cloneUSRestrictionsResult(v)
			change(&q)
			d, e := usRestrictionsResultDigest(i, r, q)
			if e != nil || d == base {
				t.Fatal("unbound saved field", e)
			}
		})
	}
	for name, change := range map[string]func(*CommandIdentity){"incarnation": func(i *CommandIdentity) { i.Incarnation[0]++ }, "operation": func(i *CommandIdentity) { i.OperationID[0]++ }, "sequence": func(i *CommandIdentity) { i.Sequence++ }} {
		t.Run(name, func(t *testing.T) {
			j := i
			change(&j)
			d, e := usRestrictionsResultDigest(j, r, v)
			if e != nil || d == base {
				t.Fatal("unbound identity", e)
			}
		})
	}
	d, e := usRestrictionsResultDigest(i, RequestDigest{4}, v)
	if e != nil || d == base {
		t.Fatal("unbound request", e)
	}
	q := v
	q.labels = nil
	a, e := usRestrictionsResultDigest(i, r, q)
	if e != nil {
		t.Fatal(e)
	}
	q.labels = []string{}
	b, e := usRestrictionsResultDigest(i, r, q)
	if e != nil || a == b {
		t.Fatal("nil collapsed", e)
	}
}

func TestUSRestrictionsResultValidation(t *testing.T) {
	i, r, v := restrictionsResultFixture()
	bad := map[string]func(*usRestrictionsSavedResult){"kind": func(v *usRestrictionsSavedResult) { v.kind = USProfileLibraries }, "version": func(v *usRestrictionsSavedResult) { v.version = 2 }, "owner": func(v *usRestrictionsSavedResult) { v.ownerID = "" }, "account": func(v *usRestrictionsSavedResult) { v.account = "" }, "member": func(v *usRestrictionsSavedResult) { v.memberIncarnation = "bad" }, "profile": func(v *usRestrictionsSavedResult) { v.profile = "" }, "lifetime": func(v *usRestrictionsSavedResult) { v.profileLifetime = "bad" }, "applied": func(v *usRestrictionsSavedResult) { v.appliedMS = -1 }, "revision": func(v *usRestrictionsSavedResult) { v.revision = 1 }, "age": func(v *usRestrictionsSavedResult) { v.maximumAge = -2 }, "emptyLabel": func(v *usRestrictionsSavedResult) { v.labels = []string{""} }, "largeLabel": func(v *usRestrictionsSavedResult) { v.labels = []string{strings.Repeat("x", 361)} }, "invalidUTF8": func(v *usRestrictionsSavedResult) { v.labels = []string{string([]byte{255})} }, "many": func(v *usRestrictionsSavedResult) { v.labels = make([]string, 65) }, "systemUTF8": func(v *usRestrictionsSavedResult) { v.ratingSystem = string([]byte{255}) }, "ratingUTF8": func(v *usRestrictionsSavedResult) { v.maximumAgeRating = string([]byte{255}) }}
	for name, change := range bad {
		t.Run(name, func(t *testing.T) {
			q := cloneUSRestrictionsResult(v)
			change(&q)
			if _, e := usRestrictionsResultDigest(i, r, q); e == nil {
				t.Fatal("accepted malformed")
			}
			if _, e := usRestrictionsResultVariableBytes(q); e == nil {
				t.Fatal("charged malformed")
			}
		})
	}
	for name, change := range map[string]func(*CommandIdentity){"incarnation": func(i *CommandIdentity) { i.Incarnation = [16]byte{} }, "operation": func(i *CommandIdentity) { i.OperationID = [16]byte{} }, "sequence": func(i *CommandIdentity) { i.Sequence = 0 }} {
		t.Run(name, func(t *testing.T) {
			j := i
			change(&j)
			if _, e := usRestrictionsResultDigest(j, r, v); e == nil {
				t.Fatal("accepted missing witness")
			}
		})
	}
	if _, e := usRestrictionsResultDigest(i, RequestDigest{}, v); e == nil {
		t.Fatal("accepted empty request")
	}
	v.maximumAge = -1
	v.ratingSystem = "retired"
	v.maximumAgeRating = "retired"
	if _, e := usRestrictionsResultDigest(i, r, v); e != nil {
		t.Fatal(e)
	}
}

func TestUSRestrictionsResultOwnershipAndRedaction(t *testing.T) {
	_, _, v := restrictionsResultFixture()
	q := cloneUSRestrictionsResult(v)
	q.labels[0] = "changed"
	if v.labels[0] != "private-label" {
		t.Fatal("aliased labels")
	}
	for _, labels := range [][]string{nil, {}} {
		v.labels = labels
		q = cloneUSRestrictionsResult(v)
		if (q.labels == nil) != (labels == nil) {
			t.Fatal("clone changed nil semantics")
		}
	}
	_, _, v = restrictionsResultFixture()
	for _, value := range []any{v, &v} {
		for _, verb := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x", "%X", "%d", "%100.3s"} {
			s := fmt.Sprintf(verb, value)
			if !strings.Contains(s, "redacted") || strings.Contains(s, "private-label") || strings.Contains(s, "original-account") {
				t.Fatal("leaked result", verb)
			}
		}
	}
}

func TestUSRestrictionsResultNativeBytesAndEncodingBounds(t *testing.T) {
	w := usSchemaDraftFixture(t)
	i, r, v := restrictionsResultFixture()
	raw, e := json.Marshal([]string{strings.Repeat(string([]byte{255}), 120), strings.Repeat(string([]byte{254}), 120)})
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &v.labels); e != nil {
		t.Fatal(e)
	}
	if len(v.labels[0]) != 360 || v.labels[0] != v.labels[1] {
		t.Fatal("repair control")
	}
	for len(v.labels) < 64 {
		v.labels = append(v.labels, v.labels[0])
	}
	d, e := usRestrictionsResultDigest(i, r, v)
	if e != nil {
		t.Fatal(e)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := w.db.ExecContext(t.Context(), query, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,created_ms) VALUES(?,?,'system','server',0)`, v.ownerID, i.Incarnation[:])
	exec(`INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES(?,?,?,?,?,542,1,'profile_restrictions_v1',?,?,?)`, v.ownerID, i.Incarnation[:], i.Sequence, i.OperationID[:], r[:], v.appliedMS, d[:], len(v.labels))
	exec(`INSERT INTO security_restriction_results(owner_id,command_incarnation,command_sequence,account_id,member_incarnation,profile_id,profile_lifetime,revision,rating_system,maximum_age_rating,maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ownerID, i.Incarnation[:], i.Sequence, v.account, v.memberIncarnation, v.profile, v.profileLifetime, v.revision, v.ratingSystem, v.maximumAgeRating, v.maximumAge, v.allowUnrated, v.allowDownloads, v.allowLiveTV, v.allowDVR, v.allowWatchTogether)
	insert := `INSERT INTO security_restriction_result_labels(owner_id,command_incarnation,command_sequence,ordinal,label) VALUES(?,?,?,?,?)`
	for ordinal, label := range v.labels {
		exec(insert, v.ownerID, i.Incarnation[:], i.Sequence, ordinal, label)
	}
	var actual int64
	if e = w.db.QueryRowContext(t.Context(), `SELECT (SELECT sum(variable_bytes) FROM security_command_receipts)+(SELECT sum(variable_bytes) FROM security_restriction_results)+(SELECT sum(variable_bytes) FROM security_restriction_result_labels)`).Scan(&actual); e != nil {
		t.Fatal(e)
	}
	want, e := usRestrictionsResultVariableBytes(v)
	if e != nil || actual != want {
		t.Fatal("native bytes", actual, want, e)
	}
	rows, e := w.db.QueryContext(t.Context(), `SELECT label FROM security_restriction_result_labels WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>-1 AND ordinal<64 ORDER BY ordinal LIMIT 64`, v.ownerID, i.Incarnation[:], i.Sequence)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for rows.Next() {
		var label string
		if e = rows.Scan(&label); e != nil || label != v.labels[count] {
			t.Fatal("label roundtrip", e)
		}
		count++
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	if count != 64 {
		t.Fatal(count)
	}
	exec(`DELETE FROM security_restriction_result_labels`)
	for _, bad := range []struct {
		ordinal int
		label   string
	}{{-1, "x"}, {64, "x"}, {0, ""}, {0, strings.Repeat("x", 361)}} {
		if _, e = w.db.ExecContext(t.Context(), insert, v.ownerID, i.Incarnation[:], i.Sequence, bad.ordinal, bad.label); e == nil {
			t.Fatal("native accepted encoding/range violation", bad.ordinal, len(bad.label))
		}
	}
}

func TestUSRestrictionsResultRetainsFullNativeRevision(t *testing.T) {
	i, r, v := restrictionsResultFixture()
	v.revision = math.MaxInt64
	if _, err := usRestrictionsResultDigest(i, r, v); err != nil {
		t.Fatal("native result revision was capped", err)
	}
}
