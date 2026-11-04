import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeRelatedRow} from '../src-next/screens/watch/title-related.ts';
import {RelationRowPager} from '../../../packages/client-core/src/relation-rows.ts';

// MOB-054/TV-019/TV-005: a related row's pages in the shelf's paging contract.
const entry = (id: string) => ({id, kind: 'movie', title: id}) as any;

test('a related row fills its waiting host from the page that answered it, then ends', async () => {
  let changes = 0;
  const pages: Record<string, {entries: any[]; nextCursor?: string}> = {c1: {entries: [entry('m2'), entry('m1')], nextCursor: 'c2'}, c2: {entries: []}};
  const row = new NativeRelatedRow('title', new RelationRowPager({entries: [entry('m1')], nextCursor: 'c1'}, async cursor => pages[cursor]!), () => {changes++;});
  const first = row.getSnapshot();
  assert.equal(first.hasMore, true);
  row.next({...first});
  assert.equal(row.getSnapshot(), first, 'a stale snapshot asks for nothing');
  row.next(first);
  await new Promise(resolve => setTimeout(resolve, 0));
  const second = row.getSnapshot();
  assert.deepEqual(second.entries.map(value => value.id), ['m1', 'm2']);
  assert.deepEqual(second.fulfilled, {waitingId: first.waitingId, firstItemId: 'm2'});
  assert.notEqual(second.waitingId, first.waitingId);
  row.next(second);
  await new Promise(resolve => setTimeout(resolve, 0));
  const last = row.getSnapshot();
  assert.equal(last.hasMore, false);
  assert.deepEqual(last.fulfilled, {waitingId: second.waitingId}, 'an empty last page fills nothing');
  assert.ok(changes >= 4);
  row.dispose();
});

test('a refused continuation reads the row again once in place of what was shown; a second refusal or a moved row ends it', async () => {
  const tick = () => new Promise(resolve => setTimeout(resolve, 0));
  const refused = () => Object.assign(new Error('Expired'), {code: 'invalid_cursor'});
  const reads: string[] = [];
  const row = new NativeRelatedRow('title', new RelationRowPager({entries: [entry('m1'), entry('m2')], nextCursor: 'c1'}, async cursor => {
    reads.push(cursor);
    if (cursor === '') return {entries: [entry('m9'), entry('m1')], nextCursor: 'c9'};
    throw refused();
  }), () => {});
  row.next(row.getSnapshot()); await tick();
  const reread = row.getSnapshot();
  assert.deepEqual(reads, ['c1', ''], 'the first page is read once, without a cursor');
  assert.deepEqual(reread.entries.map(value => value.id), ['m9', 'm1'], 'the first page replaces the row: m2, read under the old fence, is gone');
  assert.equal(reread.hasMore, true); assert.equal(reread.failed, false);
  assert.equal(reread.fulfilled?.firstItemId, undefined, 'a replaced row names no new title for focus');
  row.next(reread); await tick();
  const ended = row.getSnapshot();
  assert.deepEqual(reads, ['c1', '', 'c9'], 'a second refusal is not read again');
  assert.equal(ended.hasMore, false); assert.equal(ended.failed, true);
  assert.deepEqual(ended.entries.map(value => value.id), ['m9', 'm1']);
  row.dispose();
  // A real move of the row (stale_continuation) ends it with no re-read.
  const moved: string[] = [];
  const stale = new NativeRelatedRow('title', new RelationRowPager({entries: [entry('m1')], nextCursor: 'c1'}, async cursor => {
    moved.push(cursor); throw Object.assign(new Error('Moved'), {code: 'stale_continuation'});
  }), () => {});
  stale.next(stale.getSnapshot()); await tick();
  assert.deepEqual(moved, ['c1']); assert.equal(stale.getSnapshot().hasMore, false); assert.equal(stale.getSnapshot().failed, true);
  stale.dispose();
});
