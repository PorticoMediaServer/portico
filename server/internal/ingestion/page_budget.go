package ingestion

import (
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Scanner M2 (planning/v3/rebuilds/SCANNER-REVIEW.md): an inventory step
// lists up to inventorySourceFolders folders under one shared raw-name budget
// and publishes them in one transaction. The budget is sized by the
// transaction's measured writer hold, not a fixed count: one new file costs
// 3-6 ms of writer time on a fast host and several times that on a small VPS
// (catalogue triggers), and SERVER-LOAD-REVIEW gives a background transaction
// about 50 ms before it should yield to user writes. So each source keeps its
// own budget, per scan: cut toward the target when a page holds too long, doubled
// when a full page holds well under it. Unchanged files publish almost
// nothing, so a rescan's pages grow to the largest the storage batch allows.
const (
	pageHoldTarget = 50 * time.Millisecond
	pageNamesStart = 16
)

type pageBudgets struct {
	mu    sync.Mutex
	names map[string]int
}

// pageNames is the raw-name budget for job's next inventory step.
func (s *Service) pageNames(job string) int {
	s.pages.mu.Lock()
	defer s.pages.mu.Unlock()
	if n := s.pages.names[job]; n > 0 {
		return n
	}
	return pageNamesStart
}

// notePageHold adapts job's budget from a committed step that was offered
// budget names, published files of them and held the writer for held. Only
// files count toward growth: a page of subfolder names is cheap and says
// nothing about what the files inside them will cost (a movie library's root
// lists every film folder before the first film).
func (s *Service) notePageHold(job string, budget, files int, held time.Duration) {
	s.pages.mu.Lock()
	defer s.pages.mu.Unlock()
	if s.pages.names == nil {
		s.pages.names = map[string]int{}
	}
	s.pages.names[job] = nextPageNames(budget, files, held)
}

// nextPageNames: over the target, scale down in proportion (never below one
// name); a page that used its whole budget well under the target doubles,
// up to the storage batch limit. Anything else keeps its budget.
func nextPageNames(budget, used int, held time.Duration) int {
	budget = max(1, min(budget, inventorySourceNames))
	switch {
	case held > pageHoldTarget:
		return max(1, int(int64(budget)*int64(pageHoldTarget)/int64(held)))
	case used >= budget && held < pageHoldTarget/2:
		return min(inventorySourceNames, budget*2)
	}
	return budget
}

// forgetPageBudget drops a scan's budget when it ends. Each scan starts from
// pageNamesStart: a rescan's large unchanged pages say nothing about the cost
// of the new files the next scan may find.
func (s *Service) forgetPageBudget(job string) {
	s.pages.mu.Lock()
	delete(s.pages.names, job)
	s.pages.mu.Unlock()
}

// pageFiles counts a listed page's file entries.
func pageFiles(page storage.InventoryPage) int {
	n := 0
	for _, entry := range page.Entries {
		if !entry.Directory {
			n++
		}
	}
	return n
}
