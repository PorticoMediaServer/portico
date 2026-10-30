import test from 'node:test';
import assert from 'node:assert/strict';
import {readBackCommit, readBackDestination, readRetireAttempt, readRouteEntry, readSameRouteEntry, readVersionAlternatives,
  readVersionCanChoose, readVersionPick, readVersionPostRetire, readVersionPreRetire, readVersionRecovery,
  type ReadRouteEntry, type ReadVersion} from '../src/screens/read/read-version-choice.ts';

const epub = {sourceId: 'epub-source', format: 'epub' as const, size: 900, inventoryAvailable: true};
const pdf = {sourceId: 'pdf-source', format: 'pdf' as const, size: 1200, inventoryAvailable: true};
const stale = {sourceId: 'stale-source', format: 'pdf' as const, size: 800, inventoryAvailable: false};
const route = {} as never;
const entry = (key: string, href = `/item/book?entry=${key}`): ReadRouteEntry => ({href, key});
const location = (value: ReadRouteEntry) => ({href: value.href, state: {__TSR_key: value.key}});

test('a rejected default EPUB leaves the available PDF choosable', () => {
  const versions = [epub, pdf];
  assert.equal(readVersionCanChoose(versions), true);
  const picked = readVersionPick(versions, epub.sourceId, pdf.sourceId);
  assert.equal(picked?.sourceId, pdf.sourceId);
  assert.deepEqual(readVersionAlternatives(versions, epub.sourceId).map(v => v.sourceId), [pdf.sourceId]);
});

test('unavailable, duplicate and unchanged versions cannot dispatch', () => {
  assert.equal(readVersionPick([epub, stale], epub.sourceId, stale.sourceId), undefined);
  assert.equal(readVersionPick([epub, pdf], epub.sourceId, epub.sourceId), undefined);
  assert.equal(readVersionPick([epub, pdf], epub.sourceId, undefined), undefined);
  assert.equal(readVersionPick([epub, pdf], epub.sourceId, 'absent-source'), undefined);
  assert.equal(readVersionPick([epub, pdf, {...pdf, sourceId: pdf.sourceId}], epub.sourceId, pdf.sourceId), undefined, 'duplicate ids refuse');
});

test('a one-version item shows no chooser', () => {
  assert.equal(readVersionCanChoose([epub]), false);
  assert.equal(readVersionCanChoose([]), false);
  assert.deepEqual(readVersionAlternatives([epub], epub.sourceId), []);
});

type Reentry = {
  /** Start a successor attempt from inside the abort listener. */
  successor(value: unknown): void;
  /** Replace the current route from inside the abort listener. */
  route(value: ReadRouteEntry): void;
  /** Replace the committed eligibility list with an equal-contents new list. */
  versions(value: readonly ReadVersion[]): void;
  readonly attemptB: {live: boolean; ready: boolean; rereading: boolean};
};

/** Drive the exact caller sequence the component uses: pre-retire admission, the
 * component's dead/detach/abort retirement, then the post-retire witness, and only
 * then the source write. `reenter` runs from the abort listener. */
function recover(reenter?: (set: Reentry) => void) {
  const versions = [epub, pdf];
  const opened = entry('entry-a');
  const attemptA = {live: true, ready: true, rereading: true, target: (() => 1) as unknown};
  const attemptB = {live: true, ready: true, rereading: false};
  let currentAttempt: unknown = attemptA, currentRoute: ReadRouteEntry | undefined = opened, sourceId: string = epub.sourceId;
  let committed: {context: unknown; sourceId: string; versions: readonly ReadVersion[]} = {context: route, sourceId: epub.sourceId, versions};
  const rendered = {context: route, sourceId: epub.sourceId, versions, attempt: attemptA};
  const admitted = readVersionRecovery({
    liveRoute: true, route: opened, committedRoute: opened, currentRoute,
    rendered, committed, currentAttempt, attemptLive: attemptA.live, candidate: pdf,
    retire: value => {
      assert.equal(value, attemptA, 'retirement may only target the attempt the row was rendered with');
      readRetireAttempt(attemptA, () => {if (currentAttempt === attemptA) currentAttempt = undefined;}, () => {
        // The abort listener must see the attempt already dead and detached.
        assert.equal(attemptA.live, false, 'dead is published before abort');
        assert.equal(currentAttempt, undefined, 'the attempt ref is cleared before abort');
        reenter?.({successor: value => {currentAttempt = value;}, route: value => {currentRoute = value;},
          versions: value => {committed = {...committed, versions: value};}, attemptB});
      });
    },
    afterRetire: () => ({liveRoute: true, committedRoute: opened, currentRoute, successor: currentAttempt, committed}),
  });
  if (admitted) sourceId = pdf.sourceId;
  return {admitted, sourceId, attemptA, attemptB};
}

test('caller-level failed A really selects B after the original is retired', () => {
  const result = recover();
  assert.equal(result.admitted, true);
  assert.equal(result.sourceId, pdf.sourceId, 'the caller wrote the chosen source');
  assert.equal(result.attemptA.live, false, 'the original attempt was retired');
});

test('an abort-listener successor blocks the pending source write', () => {
  const result = recover(set => {set.successor(set.attemptB);});
  assert.equal(result.admitted, false);
  assert.equal(result.sourceId, epub.sourceId);
  assert.equal(result.attemptB.live, true, 'the old callback never retired the successor');
});

test('an abort-listener route replacement blocks the pending source write', () => {
  const result = recover(set => {set.route(entry('entry-b'));});
  assert.equal(result.admitted, false);
  assert.equal(result.sourceId, epub.sourceId);
});

test('an abort-listener list replacement blocks the pending source write', () => {
  const result = recover(set => {set.versions([epub, pdf]);});
  assert.equal(result.admitted, false);
  assert.equal(result.sourceId, epub.sourceId);
});

test('a reused component accepts the new entry and refuses the old callback', () => {
  const versions = [epub, pdf];
  const oldEntry = entry('entry-old'), newEntry = entry('entry-new');
  const attemptA = {live: true, aborted: false};
  const rendered = {context: route, sourceId: epub.sourceId, versions, attempt: attemptA};
  const committed = {context: route, sourceId: epub.sourceId, versions};
  const base = {liveRoute: true, route: oldEntry, committedRoute: newEntry, currentRoute: newEntry,
    rendered, committed, currentAttempt: attemptA, attemptLive: true, candidate: pdf};
  // The old entry's callback is refused after the new entry committed, even though the
  // same mounted component and the same attempt continue.
  assert.equal(readVersionPreRetire(base), false);
  // The new entry's callback is the fresh committed lifetime and works.
  assert.equal(readVersionPreRetire({...base, route: newEntry}), true);
  // Back: old callback does nothing; the new callback decides normally.
  assert.equal(readBackCommit(oldEntry, newEntry, newEntry, true), undefined);
  assert.equal(readBackCommit(newEntry, newEntry, newEntry, true), 'back');
  assert.equal(readBackCommit(newEntry, newEntry, newEntry, false), 'home');
});

test('a retained row is refused after a successor, replacement or navigation', () => {
  const versions = [epub, pdf];
  const opened = entry('entry-a'), elsewhere = entry('entry-b');
  const attemptA = {live: true, aborted: false}, attemptB = {live: true, aborted: false};
  const rendered = {context: route, sourceId: epub.sourceId, versions, attempt: attemptA};
  const committed = {context: route, sourceId: epub.sourceId, versions};
  const base = {liveRoute: true, route: opened, committedRoute: opened, currentRoute: opened,
    rendered, committed, currentAttempt: attemptA, attemptLive: true, candidate: pdf};
  assert.equal(readVersionPreRetire(base), true);
  // A successor attempt exists: the old callback must not retire it.
  assert.equal(readVersionPreRetire({...base, currentAttempt: attemptB}), false);
  // The original was already retired.
  assert.equal(readVersionPreRetire({...base, attemptLive: false}), false);
  // The committed source or list was replaced (even with equal contents).
  assert.equal(readVersionPreRetire({...base, committed: {...committed, sourceId: pdf.sourceId}}), false);
  assert.equal(readVersionPreRetire({...base, committed: {...committed, versions: [...versions]}}), false);
  // Another route entry replaced the original, or the new entry committed.
  assert.equal(readVersionPreRetire({...base, currentRoute: elsewhere}), false);
  assert.equal(readVersionPreRetire({...base, committedRoute: elsewhere}), false);
  assert.equal(readVersionPreRetire({...base, liveRoute: false}), false);
  // Both attempts undefined never admits.
  assert.equal(readVersionPreRetire({...base, rendered: {...rendered, attempt: undefined}, currentAttempt: undefined}), false);
  // The row itself must still be the unique, available, non-current row.
  assert.equal(readVersionPreRetire({...base, candidate: epub}), false);
  assert.equal(readVersionPreRetire({...base, rendered: {...rendered, versions: [epub, stale]}}), false);
});

test('post-retire still refuses a successor or a replaced witness', () => {
  const versions = [epub, pdf];
  const opened = entry('entry-a'), elsewhere = entry('entry-b');
  const attemptA = {live: false, aborted: true};
  const rendered = {context: route, sourceId: epub.sourceId, versions, attempt: attemptA};
  const committed = {context: route, sourceId: epub.sourceId, versions};
  const base = {liveRoute: true, route: opened, committedRoute: opened, currentRoute: opened,
    rendered, committed, successor: undefined, candidate: pdf};
  assert.equal(readVersionPostRetire(base), true);
  assert.equal(readVersionPostRetire({...base, successor: {}}), false);
  assert.equal(readVersionPostRetire({...base, currentRoute: elsewhere}), false);
  assert.equal(readVersionPostRetire({...base, committedRoute: elsewhere}), false);
  assert.equal(readVersionPostRetire({...base, committed: {...committed, versions: [...versions]}}), false);
});

test('the route witness is the real __TSR_key entry, not history.location.key', () => {
  assert.deepEqual(readRouteEntry(location(entry('entry-a'))), entry('entry-a'));
  assert.equal(readRouteEntry({href: '/item/book', state: {}}), undefined);
  assert.equal(readRouteEntry({href: '/item/book', state: {__TSR_key: ''}}), undefined);
  assert.equal(readRouteEntry({href: '/item/book', state: {__TSR_key: 7 as never}}), undefined);
  assert.equal(readSameRouteEntry(entry('entry-a'), entry('entry-a')), true);
  assert.equal(readSameRouteEntry(entry('entry-a'), entry('entry-b')), false);
  assert.equal(readSameRouteEntry(entry('entry-a'), {...entry('entry-a'), href: '/other'}), false);
  assert.equal(readSameRouteEntry(undefined, entry('entry-a')), false);
});

test('Back returns in-app only when the captured route really has a predecessor', () => {
  assert.equal(readBackDestination(true), 'back');
  assert.equal(readBackDestination(false), 'home');
});
