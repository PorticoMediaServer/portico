import test from 'node:test';
import assert from 'node:assert/strict';
import {SegmentIntrosSource, type SegmentIntrosReads} from '../src/screens/settings/server-intros/source.ts';
import type {SegmentTitleReview, SegmentStatus} from '@core/segment-review.ts';

const title = (id: string): SegmentTitleReview => ({id, title: id, kind: 'show', libraryId: 'video', mode: 'standard', seasons: [{number: 1, title: '', entries: []}]});
const status: SegmentStatus = {mode: 'standard', libraries: [], files: {complete: 0, pending: 0, failed: 0, unsupported: 0}, showsWaiting: 0, catchUpComplete: true, lastFullPassMS: 0, failures: []};
function deferred<T>() {let resolve!: (value: T) => void; const promise = new Promise<T>(r => {resolve = r;}); return {promise, resolve};}
function fixture(overrides: Partial<SegmentIntrosReads> = {}) {
  let current = true;
  const reads: SegmentIntrosReads = {status: async () => status, titles: async libraryId => ({libraryId, titles: [], nextCursor: ''}), title: async id => title(id), approveSeason: async () => 0, redetect: async () => 0, approveMarker: async () => {}, editMarker: async () => {}, ...overrides};
  const source = new SegmentIntrosSource({api: {request: async () => {throw new Error('Unexpected API request');}}, isCurrent: () => current, reads, pollMs: 0});
  return {source, retire: () => {current = false;}};
}

test('a late title read cannot replace a newer title', async () => {
  const old = deferred<SegmentTitleReview>();
  const {source} = fixture({title: async id => id === 'old' ? old.promise : title(id)});
  try {
    const pending = source.openTitle('old');
    await source.openTitle('new');
    old.resolve(title('old')); await pending;
    assert.equal(source.getSnapshot().title?.id, 'new');
  } finally {source.dispose();}
});

test('changing titles retires the old write busy state and its completion', async () => {
  const write = deferred<number>();
  const {source} = fixture({approveSeason: () => write.promise});
  try {
    await source.openTitle('old');
    const pending = source.approve();
    assert.equal(source.getSnapshot().busy, 'approve');
    await source.openTitle('new');
    assert.equal(source.getSnapshot().busy, undefined);
    write.resolve(5); await pending;
    assert.equal(source.getSnapshot().title?.id, 'new');
    assert.equal(source.getSnapshot().note, undefined);
  } finally {source.dispose();}
});

test('retired authority cannot publish an outstanding status answer', async () => {
  const read = deferred<SegmentStatus>();
  const f = fixture({status: () => read.promise});
  const pending = f.source.load();
  f.retire(); read.resolve(status); await pending;
  assert.equal(f.source.getSnapshot().status, undefined);
  f.source.dispose();
});

test('library replacement discards a late paged answer', async () => {
  const page = deferred<{libraryId: string; titles: []; nextCursor: string}>();
  const {source} = fixture({titles: async (libraryId, cursor) => cursor ? page.promise : ({libraryId, titles: [], nextCursor: 'next'})});
  try {
    await source.openLibrary('old');
    const pending = source.moreTitles();
    await source.openLibrary('new');
    page.resolve({libraryId: 'old', titles: [], nextCursor: 'stale'}); await pending;
    assert.equal(source.getSnapshot().libraryId, 'new');
    assert.equal(source.getSnapshot().titlesCursor, 'next');
  } finally {source.dispose();}
});
