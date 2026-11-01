package compactcatalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestRatingClassificationBoundsEachWrite(t *testing.T) {
	db := cc1OpenDB(t, "ratings-bounded.sqlite")
	for i := 0; i < 25; i++ {
		cc1Exec(t, db, `INSERT INTO content_rating_pending(value_key,value) VALUES(?,?)`, fmt.Sprintf("rating-%02d", i), fmt.Sprintf("Rating %02d", i))
	}
	dbwork.ResetPlans()
	age := func(value string) (int, bool) { return 13, value != "Rating 24" }
	more, err := ClassifyPendingRatings(context.Background(), db, age)
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Fatal("one turn exhausted a 25-spelling queue")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM content_rating_ages`); got != 8 {
		t.Fatalf("classified %d, want 8", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM content_rating_pending`); got != 17 {
		t.Fatalf("pending %d, want 17", got)
	}
	for turns := 0; more && turns < 10; turns++ {
		more, err = ClassifyPendingRatings(context.Background(), db, age)
		if err != nil {
			t.Fatal(err)
		}
	}
	if more {
		t.Fatal("queue did not drain")
	}
	if got := cc1Scalar(t, db, `SELECT minimum_age FROM content_rating_ages WHERE value_key='rating-24'`); got != -1 {
		t.Fatalf("unknown age=%d, want -1", got)
	}
	plans := dbwork.Plans()
	if plans.Overruns != 0 {
		t.Fatalf("classification exceeded its existing plans: %+v", plans)
	}
}
