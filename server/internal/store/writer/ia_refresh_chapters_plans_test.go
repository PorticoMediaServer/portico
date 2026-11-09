package writer

import "testing"

func TestIARefreshChapterStatementPlans(t *testing.T) {
	iaRefreshAssertWalkPlans(t, iaRefreshChapterDefinitions())
}
