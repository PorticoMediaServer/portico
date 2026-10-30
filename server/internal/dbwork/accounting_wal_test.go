package dbwork

import "testing"

func TestAccountingWALFramesExcludeOnlyTheObservedHeader(t *testing.T) {
	for _, size := range []int64{512, 4096, 65536} {
		for _, headers := range []uint64{0, 1, 2} {
			frames, known := observedWALFrames(32*headers+9*(uint64(size)+24), headers, size)
			if !known || frames != 9 {
				t.Fatal(size, headers, frames, known)
			}
		}
	}
	for _, args := range [][3]uint64{{1, 1, 4096}, {33, 1, 4096}, {4120, 0, 0}} {
		if _, ok := observedWALFrames(args[0], args[1], int64(args[2])); ok {
			t.Fatal("invalid geometry represented as known", args)
		}
	}
}
func TestAccountingWALRollingMaxRetiresOldSamplesAndKeepsUnknown(t *testing.T) {
	resetAccountingWAL()
	t.Cleanup(resetAccountingWAL)
	noteAccountingWAL(AccountingFlip, accountingWALSample{bytes: 8192, frames: 2, pages: 2, writtenKnown: true, framesKnown: true, pagesKnown: true})
	noteAccountingWAL(AccountingFold, accountingWALSample{bytes: 4096, frames: 1, pages: 1, writtenKnown: true, framesKnown: true, pagesKnown: true})
	for i := 0; i < accountingWALWindow; i++ {
		noteAccountingWAL(AccountingFlip, accountingWALSample{bytes: 100, frames: 1, pages: 1, writtenKnown: true, framesKnown: true, pagesKnown: true})
	}
	got := AccountingWAL()
	if got.Flip.Samples != 65 || got.Flip.WindowSamples != 64 || got.Flip.MaxWALBytes != 100 || got.Flip.MaxFrames != 1 || got.Flip.MaxCachePages != 1 || got.Fold.Samples != 1 || got.Fold.MaxWALBytes != 4096 || got.Flip.PeakWALBytes != 8192 || got.Flip.PeakFrames != 2 || got.Flip.PeakCachePages != 2 {
		t.Fatal(got)
	}
	noteAccountingWAL(AccountingFlip, accountingWALSample{})
	got = AccountingWAL()
	if got.Flip.UnknownWAL != 1 || got.Flip.UnknownFrames != 1 || got.Flip.UnknownCachePages != 1 {
		t.Fatal("unknown became zero proof", got)
	}
}
