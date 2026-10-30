import test from 'node:test';
import assert from 'node:assert/strict';
import {parseBrowseResult} from '../src/browse.ts';

function entry(id: string) {
  return {id, kind: 'movie', title: `Title ${id}`};
}

function payload(opts: {
  start: number; total: number; hasMore: boolean; nextCursor?: string;
  entryCount?: number; positionIndex?: unknown;
}): any {
  const count = opts.entryCount ?? 60;
  const startId = opts.start;
  return {
    pivot: 'movies',
    applied: {query: {variant:'predicate',field: 'genre', operator: 'contains', value: 'Action'}, sort: [{field: 'title', direction: 'asc'}], seek: null},
    entries: Array.from({length: count}, (_, i) => entry(`e${startId + i}`)),
    pageInfo: {
      start: opts.start, total: opts.total, revision: 'rev', hasMore: opts.hasMore,
      nextCursor: opts.nextCursor ?? (opts.hasMore ? 'cursor-next' : ''),
    },
    positionIndex: opts.positionIndex ?? [],
  };
}

test('browse totals are exact (start/total checks hold)', () => {
  const page = parseBrowseResult(payload({start: 0, total: 9999, hasMore: true, entryCount: 60}));
  assert.equal(page.pageInfo.total, 9999);
  assert.equal(page.pageInfo.hasMore, true);
  // A page asked for past the end is an empty page with the server's total, so the list shrinks to it.
  const past = parseBrowseResult(payload({start: 10000, total: 9999, hasMore: false, entryCount: 0}));
  assert.deepEqual([past.entries.length, past.pageInfo.total, past.pageInfo.hasMore], [0, 9999, false]);
  // Whether rows remain follows from where the page starts and the total, whatever the flag says.
  assert.equal(parseBrowseResult(payload({start: 0, total: 9999, hasMore: false, entryCount: 60})).pageInfo.hasMore, true);
  assert.throws(() => parseBrowseResult(payload({start: -1, total: 9999, hasMore: true, entryCount: 60})), {code: 'invalid_browse'});
  assert.throws(() => parseBrowseResult({...payload({start: 0, total: 9999, hasMore: true, entryCount: 60}), pageInfo: {start: 0, total: 'many', revision: 'rev', hasMore: true, nextCursor: ''}}), {code: 'invalid_browse'});
});

test('exact totals above 10,000 keep paging', () => {
  const page = parseBrowseResult(payload({start: 0, total: 15020, hasMore: true, entryCount: 60}));
  assert.equal(page.pageInfo.total, 15020);
  assert.equal(page.pageInfo.hasMore, true);
  const past = parseBrowseResult(payload({start: 10020, total: 15020, hasMore: true, entryCount: 60}));
  assert.equal(past.pageInfo.start, 10020);
  assert.equal(past.pageInfo.total, 15020);
  assert.equal(past.pageInfo.hasMore, true);
  const last = parseBrowseResult(payload({start: 15000, total: 15020, hasMore: false, entryCount: 20, nextCursor: ''}));
  assert.equal(last.pageInfo.hasMore, false);
});
