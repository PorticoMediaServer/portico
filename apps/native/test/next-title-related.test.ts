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
