package read

import (
	"errors"
	"testing"
	"time"
)

func TestMPPreparedRestrictedOwner(t *testing.T) {
	f, r := mpPreparedFixture(t)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=1 WHERE id=1`)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',1,0)`)
	in := PreparedJobInput{View: f.input, Job: "mp-job", Item: 1}
	got, _, err := r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || got.Item.ID != 1 || got.ID != "mp-job" {
		t.Fatal("restricted owner positive", got, err)
	}
	f.exec(t, `UPDATE profile_restrictions SET maximum_age=0,revision=revision+1 WHERE profile_id='p'`)
	got, _, err = r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrDenied) || got != (PreparedJobStatus{}) {
		t.Fatal("restricted owner received hidden job/item", got, err)
	}
}
