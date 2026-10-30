import test from 'node:test';
import assert from 'node:assert/strict';
import type {QueueEntry} from '@core/playback-v1/types.ts';
import type {V1QueueEditIntent, V1QueueEditPage, V1QueueEditReader} from '@core/playback-v1/v1-queue-player.ts';
import {createQueueEditOpening, type WebQueueEditInput} from '../src/platform/playback/queue-edit-adapter.ts';

function entry(i: number, extra: Partial<QueueEntry> = {}): QueueEntry {
  return {entryId: `e${i}`, position: i, itemId: `item${i}`, kind: 'track', title: `Track ${i}`, available: true, ...extra} as QueueEntry;
}
function page(start: number, count: number, total = 120): V1QueueEditPage {
  return Object.freeze({start, total, revision: 'rev-1', items: Object.freeze(Array.from({length: count}, (_, i) => entry(start + i))), canPrevious: start > 0,
    ...(start + count < total ? {nextCursor: `from:${start + count}`} : {})});
}

function harness(options: Readonly<{current?: () => boolean; defer?: () => Promise<void>}> = {}) {
  const reads: string[] = [];
  let release: (() => void) | undefined;
  const gate = () => options.defer ? options.defer() : Promise.resolve();
  let current = true;
  const reader: V1QueueEditReader = {
    read: async () => {reads.push('read'); await gate(); return page(0, 50);},
    next: async shown => {reads.push('next'); await gate(); return page(shown.start + shown.items.length, 50);},
    previous: async shown => {reads.push('previous'); await gate(); return page(shown.start - 50, 50);},
    edit: async (_shown, intent: V1QueueEditIntent) => {reads.push(intent.action === 'remove' ? 'remove' : 'reorder'); await gate(); return page(0, 50);},
    dispose: () => {reads.push('dispose');},
  };
  let lifetimeCurrent = true;
  const input: WebQueueEditInput = {reader, current: () => options.current ? options.current() : current, lifetime: {signal: new AbortController().signal, isCurrent: () => lifetimeCurrent},
    currentEntryId: 'e0', hidden: id => id === 'e3'};
  const opening = createQueueEditOpening(input);
  return {opening, reads, retire: () => {current = false;}, retireLifetime: () => {lifetimeCurrent = false;}, release,
    setDefer: (fn: () => Promise<void>) => {release = undefined; void fn;}};
}

test('one opening reads once and adopts the canonical page with truthful rows', async () => {
  const h = harness();
  await h.opening.open();
  const snapshot = h.opening.getSnapshot();
  assert.equal(snapshot.phase, 'ready');
  assert.equal(snapshot.page?.rows.length, 50);
  assert.equal(snapshot.page?.rows[0].current, true);
  assert.equal(snapshot.page?.rows[3].hidden, true);
  assert.equal(snapshot.page?.canNext, true);
  assert.deepEqual(h.reads, ['read']);
});

test('a duplicate action is refused before the canonical reader is called', async () => {
  let release!: () => void;
  const h = harness({defer: () => new Promise<void>(resolve => {release = resolve;})});
  const first = h.opening.open();
  assert.equal(h.opening.getSnapshot().busy, true);
  await assert.rejects(() => h.opening.open(), /queue_edit_busy/);
  assert.deepEqual(h.reads, ['read']);
  release();
  await first;
});

test('an old page cannot be used after a newer page is adopted', async () => {
  const h = harness();
  await h.opening.open();
  const first = h.opening.getSnapshot().page!;
  await h.opening.next();
  const second = h.opening.getSnapshot().page!;
  await assert.rejects(() => h.opening.move(first, first.rows[1].entryId, 1), /queue_edit_stale/);
  assert.equal(second.start, 50);
});

test('an observed drift after the awaited read retires the opening', async () => {
  let release!: () => void;
  const h = harness({defer: () => new Promise<void>(resolve => {release = resolve;})});
  const loading = h.opening.open();
  h.retire();
  release();
  await assert.rejects(() => loading, /queue_edit_stale/);
  assert.equal(h.opening.getSnapshot().phase, 'retired');
  assert.deepEqual(h.reads, ['read', 'dispose']);
});

test('hidden, current and non-adjacent rows are never editable, and a mutation consumes the drawn page', async () => {
  const h = harness();
  await h.opening.open();
  const shown = h.opening.getSnapshot().page!;
    await assert.rejects(() => h.opening.move(shown, shown.rows[3].entryId, 1), /queue_edit_stale/); // hidden
  await assert.rejects(() => h.opening.remove(shown, shown.rows[0].entryId), /queue_edit_stale/);  // current
  await h.opening.move(shown, shown.rows[1].entryId, 1);
  assert.deepEqual(h.reads.filter(r => r === 'reorder').length, 1);
  await assert.rejects(() => h.opening.remove(shown, shown.rows[2].entryId), /queue_edit_stale/);  // drawn page consumed
  assert.equal(h.opening.getSnapshot().phase, 'ready');
});

test('retired cleanup cannot dispose a successor opening', async () => {
  const first = harness(), second = harness();
  await first.opening.open();
  await second.opening.open();
  first.opening.dispose();
  first.opening.dispose();
  await second.opening.next();
  assert.equal(second.opening.getSnapshot().phase, 'ready');
  assert.deepEqual(first.reads.filter(r => r === 'dispose').length, 1);
});


test('canonical artwork, artist and duration survive the bounded row projection without invented metadata', async () => {
  const canonical: V1QueueEditPage = {...page(0, 2), items: [entry(0, {posterUrl: '/art/real', artist: 'Actual artist', durationMs: 184500}), entry(1)]};
  const reader: V1QueueEditReader = {read: async () => canonical, next: async () => canonical, previous: async () => canonical, edit: async () => canonical, dispose() {}};
  const opening = createQueueEditOpening({reader, current: () => true, lifetime: {signal: new AbortController().signal, isCurrent: () => true}, currentEntryId: 'e0', hidden: () => false});
  try {
    await opening.open();
    const rows = opening.getSnapshot().page!.rows;
    assert.equal(rows[0].artworkUrl, '/art/real');assert.equal(rows[0].artist, 'Actual artist');assert.equal(rows[0].durationMs, 184500);
    assert.equal(rows[1].artist, undefined);assert.equal(rows[1].durationMs, undefined);assert.equal(rows[1].artworkUrl, undefined);
    assert.ok(Object.isFrozen(rows[0]));
  } finally {opening.dispose();}
});
