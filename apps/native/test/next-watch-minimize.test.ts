import test from 'node:test';
import assert from 'node:assert/strict';
import {minimizeTarget} from '../src-next/screens/watch/minimize.ts';

// E-mobile-14b: Minimize did nothing on the iPhone player after the session stopped. The button's
// fence no longer matched the dock, so the collapse folded nothing. Minimize folds what is open.
test('E-mobile-14b: Minimize folds the expanded local entry even when the pressed fence is stale', () => {
  const stale = {kind: 'queue', sessionId: 'old', generation: 1} as never, now = {kind: 'queue', sessionId: 'new', generation: 2} as never;
  assert.equal(minimizeTarget({local: {fence: now, expanded: true} as never}, stale), now);
  assert.equal(minimizeTarget({local: {fence: now, expanded: false} as never}, stale), stale, 'nothing expanded: the pressed fence, a no-op collapse');
  assert.equal(minimizeTarget({local: null} as never, stale), stale);
});
