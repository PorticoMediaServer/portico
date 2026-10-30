import test from 'node:test';
import assert from 'node:assert/strict';
import {DashboardCursorWindow, type DashboardCursorPage, type DashboardCursorSnapshot} from '../src-next/screens/settings-live/dashboard-cursor-window';

test('cursor window retains the focused chunk until focus moves and refuses a repeated offered cursor', async () => {
  const calls: string[] = [];
  const window = new DashboardCursorWindow(async cursor => {
    calls.push(cursor);
    const next = cursor === '' ? 'a' : cursor === 'a' ? 'b' : cursor === 'b' ? 'c' : 'c';
    return {items: [{id: cursor || 'initial'}], nextCursor: next, metadata: {total: 999}};
  }, row => row.id, () => true, () => {});
  await window.refresh(); window.focus('initial');
  await window.next(window.snapshot()); await window.next(window.snapshot());
  await window.next(window.snapshot());
  assert.deepEqual(window.snapshot().items.map(row => row.id), ['initial', 'a', 'b']);
  // A repeated cursor is refused instead of evicting the focused row or looping.
  assert.equal(window.snapshot().refreshRequired, true);
  assert.deepEqual(calls, ['', 'a', 'b', 'c']); window.dispose();
});

test('retired cursor reads cannot publish into a refreshed query', async () => {
  let finish!: (page: DashboardCursorPage<{id: string}, number>) => void, call = 0;
  const window = new DashboardCursorWindow<{id: string}, number>(async () => {
    call++;
    if (call === 2) return new Promise(resolve => {finish = resolve;});
    return {items: [{id: call === 1 ? 'old' : 'fresh'}], nextCursor: call === 1 ? 'next' : '', metadata: call};
  }, row => row.id, () => true, () => {});
  await window.refresh(); const stale = window.next(window.snapshot()); await window.refresh();
  finish({items: [{id: 'stale'}], metadata: 1}); await stale;
  assert.deepEqual(window.snapshot().items.map(row => row.id), ['fresh']);
  window.dispose();
});

test('eviction waits for the real focused row to leave its chunk', async () => {
  const window = new DashboardCursorWindow(async cursor => ({items: [{id: cursor || 'first'}], nextCursor: cursor === '' ? 'a' : cursor === 'a' ? 'b' : cursor === 'b' ? 'c' : 'd', metadata: 1}), row => row.id, () => true, () => {});
  await window.refresh(); window.focus('first');
  await window.next(window.snapshot()); await window.next(window.snapshot()); await window.next(window.snapshot());
  assert.deepEqual(window.snapshot().items.map(row => row.id), ['first', 'a', 'b']); assert.equal(window.snapshot().pending, true);
  window.focus(undefined);
  assert.deepEqual(window.snapshot().items.map(row => row.id), ['a', 'b', 'c']); assert.equal(window.snapshot().pending, false);
  window.dispose();
});

test('previous and forward follow retained producer bookmarks without inventing cursors', async () => {
  const calls: string[] = [];
  const next: Record<string, string> = {'': 'a', a: 'b', b: 'c', c: 'd', d: ''};
  const window = new DashboardCursorWindow(async cursor => {calls.push(cursor); return {items: [{id: cursor || 'first'}], nextCursor: next[cursor], metadata: 1};}, row => row.id, () => true, () => {});
  await window.refresh(); await window.next(window.snapshot()); await window.next(window.snapshot()); await window.next(window.snapshot());
  assert.deepEqual(window.snapshot().items.map(row => row.id), ['a', 'b', 'c']);
  await window.previous(window.snapshot());
  assert.deepEqual(window.snapshot().items.map(row => row.id), ['first', 'a', 'b']);
  await window.next(window.snapshot());
  assert.deepEqual(window.snapshot().items.map(row => row.id), ['a', 'b', 'c']);
  assert.deepEqual(calls, ['', 'a', 'b', 'c', '', 'c']); window.dispose();
});

test('refresh preserves the focused old row until its original focus lease blurs', async () => {
  let read = 0;
  const window = new DashboardCursorWindow(async () => ({items: [{id: ++read === 1 ? 'old' : 'fresh'}], nextCursor: '', metadata: read}), row => row.id, () => true, () => {});
  await window.refresh(); const lease = window.focus('old')!;
  await window.refresh(); assert.deepEqual(window.snapshot().items.map(row => row.id), ['old']);
  assert.equal(window.snapshot().pending, true);
  window.blur('old', lease); assert.deepEqual(window.snapshot().items.map(row => row.id), ['fresh']);
  assert.equal(window.snapshot().pending, false); window.dispose();
});

test('a cycle longer than bookmark history eventually requires refresh without extra requests', async () => {
  const calls: string[] = [];
  const window = new DashboardCursorWindow(async cursor => {
    calls.push(cursor); const n = cursor ? Number(cursor.slice(1)) : -1;
    const successor = n < 0 ? 0 : (n + 1) % 70;
    return {items: [{id: cursor || 'first'}], nextCursor: `c${successor}`, metadata: 1};
  }, row => row.id, () => true, () => {});
  await window.refresh();
  for (let i = 0; i < 400 && !window.snapshot().refreshRequired; i++) await window.next(window.snapshot());
  assert.equal(window.snapshot().refreshRequired, true); assert.ok(calls.length > 70 && calls.length < 400);
  assert.ok(window.snapshot().chunks.length <= 3); assert.ok(window.snapshot().bookmarks.length <= 64); window.dispose();
});

test('a long acyclic producer chain remains reachable beyond bookmark capacity', async () => {
  let calls = 0;
  const window = new DashboardCursorWindow(async cursor => {
    calls++; const n = cursor ? Number(cursor.slice(1)) : 0;
    return {items: [{id: `row${n}`}], nextCursor: n < 150 ? `c${n + 1}` : '', metadata: 1};
  }, row => row.id, () => true, () => {});
  await window.refresh(); for (let i = 0; i < 150; i++) await window.next(window.snapshot());
  assert.equal(window.snapshot().refreshRequired, false); assert.equal(window.snapshot().items.at(-1)?.id, 'row150');
  assert.ok(window.snapshot().chunks.length <= 3); assert.ok(window.snapshot().bookmarks.length <= 64); assert.equal(calls, 151); window.dispose();
});

test('conflicting retained job revisions require explicit refresh', async () => {
  const window = new DashboardCursorWindow(async cursor => ({items: [{id: 'job', revision: cursor ? 2 : 1}], nextCursor: cursor ? '' : 'next', metadata: undefined}), row => row.id, () => true, () => {}, (a, b) => a.revision === b.revision);
  await window.refresh(); await window.next(window.snapshot());
  assert.equal(window.snapshot().refreshRequired, true); assert.equal(window.snapshot().items[0].revision, 1); window.dispose();
});

test('an old action lease cannot release a successor lease for the same row', async () => {
  const window = new DashboardCursorWindow(async cursor => ({items: [{id: cursor || 'first'}], nextCursor: cursor === '' ? 'a' : cursor === 'a' ? 'b' : cursor === 'b' ? 'c' : 'd', metadata: 1}), row => row.id, () => true, () => {});
  await window.refresh(); const old = window.pin('first'); const successor = window.pin('first'); window.unpin(old);
  await window.next(window.snapshot()); await window.next(window.snapshot()); await window.next(window.snapshot());
  assert.deepEqual(window.snapshot().items.map(row => row.id), ['first', 'a', 'b']); assert.equal(window.snapshot().pending, true);
  window.unpin(old); assert.equal(window.snapshot().pending, true);
  window.unpin(successor); assert.deepEqual(window.snapshot().items.map(row => row.id), ['a', 'b', 'c']); window.dispose();
});

test('refresh re-reads only the visible bounded cursor window and preserves its focused row', async () => {
  const calls: string[] = []; let revision = 'old';
  const window = new DashboardCursorWindow(async cursor => {calls.push(cursor); return {items: [{id: cursor || 'first', revision}], nextCursor: cursor === '' ? 'a' : cursor === 'a' ? 'b' : '', metadata: revision};}, row => row.id, () => true, () => {});
  await window.refresh(); await window.next(window.snapshot()); window.focus('a');
  revision = 'new'; await window.refresh();
  assert.deepEqual(calls, ['', 'a', '', 'a']);
  assert.deepEqual(window.snapshot().items.map(row => `${row.id}:${row.revision}`), ['first:new', 'a:new']);
  assert.equal(window.snapshot().nextCursor, 'b'); assert.equal(window.snapshot().pending, false); window.dispose();
});

test('an old blur receipt cannot release a newer focus lease for the same row', async () => {
  const window = new DashboardCursorWindow(async cursor => ({items: [{id: cursor || 'first'}], nextCursor: cursor === '' ? 'a' : cursor === 'a' ? 'b' : cursor === 'b' ? 'c' : 'd', metadata: 1}), row => row.id, () => true, () => {});
  await window.refresh(); const old = window.focus('first')!, current = window.focus('first')!;
  window.blur('first', old); await window.next(window.snapshot()); await window.next(window.snapshot()); await window.next(window.snapshot());
  assert.equal(window.snapshot().pending, true); assert.equal(window.snapshot().items[0]?.id, 'first');
  window.blur('first', current); assert.equal(window.snapshot().pending, false); assert.equal(window.snapshot().items[0]?.id, 'a'); window.dispose();
});

test('an aged-out previous bookmark returns to the real first page', async () => {
  const calls: string[] = [];
  const window = new DashboardCursorWindow(async cursor => {
    calls.push(cursor); const n = cursor ? Number(cursor.slice(1)) : 0;
    return {items: [{id: `r${n}`}], nextCursor: `c${n + 1}`, metadata: 1};
  }, row => row.id, () => true, () => {});
  await window.refresh(); for (let i = 0; i < 70; i++) await window.next(window.snapshot());
  for (let i = 0; i < 64 && window.snapshot().chunks[0]?.cursor !== ''; i++) await window.previous(window.snapshot());
  assert.equal(window.snapshot().chunks[0]?.cursor, ''); assert.equal(window.snapshot().items[0]?.id, 'r0');
  const length = calls.length; await window.next(window.snapshot()); assert.equal(calls[length], 'c1'); window.dispose();
});

test('synchronous duplicate sees the actual pending flight and shares its settlement', async () => {
  let state!: DashboardCursorSnapshot<{id: string}, number>, duplicate: Promise<void> | undefined, finish!: (page: DashboardCursorPage<{id: string}, number>) => void, calls = 0;
  const window = new DashboardCursorWindow<{id: string}, number>(async () => {calls++; return new Promise(resolve => {finish = resolve;});}, row => row.id, () => true, value => {state = value; if (value.pending && !duplicate) duplicate = window.next(value);});
  window.seed('', {items: [{id: 'one'}], nextCursor: 'next', metadata: 1}); const original = state;
  const pending = window.next(original); assert.equal(duplicate, pending); assert.equal(calls, 1);
  let settled = false; void duplicate!.then(() => {settled = true;}); await Promise.resolve(); assert.equal(settled, false);
  finish({items: [{id: 'two'}], metadata: 1}); await pending; assert.equal(settled, true); window.dispose();
});

test('a synchronous pending notification can retire a flight before it dispatches', async () => {
  let calls = 0;
  const window = new DashboardCursorWindow<{id: string}, number>(async () => {calls++; return {items: [], metadata: 1};}, row => row.id, () => true, value => {if (value.pending) window.dispose();});
  await window.refresh(); assert.equal(calls, 0);
});

test('abort reentry keeps the successor flight instead of erasing it', async () => {
  let calls = 0, successor: Promise<void> | undefined, finish!: (page: DashboardCursorPage<{id: string}, number>) => void;
  const window = new DashboardCursorWindow<{id: string}, number>((_cursor, signal) => {
    calls++;
    if (calls === 1) return new Promise((_, reject) => signal.addEventListener('abort', () => {successor = window.refresh(); reject(new Error('aborted'));}, {once: true}));
    return new Promise(resolve => {finish = resolve;});
  }, row => row.id, () => true, () => {});
  window.seed('', {items: [{id: 'first'}], nextCursor: 'next', metadata: 1});
  const old = window.next(window.snapshot()), outer = window.refresh();
  assert.equal(calls, 2); assert.ok(successor); finish({items: [{id: 'successor'}], metadata: 2});
  await Promise.all([old, outer, successor!]); assert.deepEqual(window.snapshot().items.map(row => row.id), ['successor']); window.dispose();
});

test('expired visible cursor requires explicit first-page recovery without retrying that cursor', async () => {
  const calls: string[] = []; let expire = false;
  const window = new DashboardCursorWindow(async cursor => {
    calls.push(cursor); if (expire && cursor === 'a') throw Object.assign(new Error('invalid_request'), {code: 'invalid_request'});
    const next: Record<string, string> = {'': 'a', a: 'b', b: 'c', c: 'd'};
    return {items: [{id: cursor || 'first'}], nextCursor: next[cursor], metadata: 1};
  }, row => row.id, () => true, () => {});
  await window.refresh(); await window.next(window.snapshot()); await window.next(window.snapshot()); await window.next(window.snapshot());
  const lease = window.focus('b')!; expire = true; await window.refresh();
  assert.equal(window.snapshot().refreshRequired, true); const count = calls.length; await window.refresh(); assert.equal(calls.length, count);
  await window.restart(window.snapshot()); assert.equal(calls.at(-1), ''); assert.equal(window.snapshot().pending, true);
  window.blur('b', lease); assert.equal(window.snapshot().items[0]?.id, 'first'); assert.equal(window.snapshot().refreshRequired, false); window.dispose();
});

test('a reentrant refresh notification prevents the retired continuation from dispatching', async () => {
  const calls: string[] = []; let reentered = false, successor: Promise<void> | undefined;
  const window = new DashboardCursorWindow(async cursor => {calls.push(cursor); return {items: [{id: 'fresh'}], metadata: 1};}, row => row.id, () => true, value => {
    if (value.pending && !reentered) {reentered = true; successor = window.refresh();}
  });
  window.seed('', {items: [{id: 'old'}], nextCursor: 'next', metadata: 1});
  const old = window.next(window.snapshot()); await Promise.all([old, successor!]);
  assert.deepEqual(calls, ['']); assert.deepEqual(window.snapshot().items.map(row => row.id), ['fresh']); window.dispose();
});


test('initial refresh notification coalesces on its reserved flight', async () => {
  let duplicate: Promise<void> | undefined, finish!: (page: DashboardCursorPage<{id: string}, number>) => void, calls = 0;
  const window = new DashboardCursorWindow<{id: string}, number>(() => {calls++; return new Promise(resolve => {finish = resolve;});}, row => row.id, () => true, value => {if (value.pending && !duplicate) duplicate = window.refresh();});
  const pending = window.refresh(); assert.equal(duplicate, pending); assert.equal(calls, 1);
  let settled = false; void duplicate!.then(() => {settled = true;}); await Promise.resolve(); assert.equal(settled, false);
  finish({items: [{id: 'first'}], metadata: 1}); await pending; assert.equal(settled, true); window.dispose();
});

for (const when of ['notification', 'response'] as const) test(`currentness refusal at ${when} retires only the original flight`, async () => {
  let current = true, refused = false, calls = 0, finish!: (page: DashboardCursorPage<{id: string}, number>) => void;
  const window = new DashboardCursorWindow<{id: string}, number>(() => {calls++; return new Promise(resolve => {finish = resolve;});}, row => row.id, () => current, value => {if (when === 'notification' && value.pending && !refused) {refused = true; current = false;}});
  const pending = window.refresh();
  if (when === 'response') {current = false; finish({items: [{id: 'retired'}], metadata: 1});}
  await pending; assert.equal(calls, when === 'notification' ? 0 : 1); assert.equal(window.snapshot().pending, false); assert.deepEqual(window.snapshot().items, []);
  current = true; const successor = window.refresh(); assert.equal(calls, when === 'notification' ? 1 : 2); finish({items: [{id: 'successor'}], metadata: 2}); await successor;
  assert.equal(window.snapshot().items[0]?.id, 'successor'); window.dispose();
});

test('cancelPending preserves a successor installed by changed and old abort callbacks', async () => {
  let finishOld!: (value: DashboardCursorPage<{id: string}, number>) => void, finishNew!: (value: DashboardCursorPage<{id: string}, number>) => void;
  let calls = 0, successor: Promise<void> | undefined, fromAbort: Promise<void> | undefined, cancelling = false;
  const window = new DashboardCursorWindow<{id: string}, number>((_cursor, signal) => {
    calls++;
    if (calls === 1) return new Promise(resolve => {finishOld = resolve; signal.addEventListener('abort', () => {fromAbort = window.next(window.snapshot());}, {once: true});});
    return new Promise(resolve => {finishNew = resolve;});
  }, row => row.id, () => true, value => {if (cancelling && !value.pending && !successor) successor = window.next(value);});
  window.seed('', {items: [{id: 'first'}], nextCursor: 'next', metadata: 1});
  const old = window.next(window.snapshot()); cancelling = true; window.cancelPending();
  assert.equal(calls, 2); assert.ok(successor); assert.equal(fromAbort, successor);
  finishOld({items: [{id: 'retired'}], metadata: 1}); await old;
  assert.equal(window.snapshot().pending, true); assert.equal(window.snapshot().items.some(row => row.id === 'retired'), false);
  finishNew({items: [{id: 'successor'}], metadata: 1}); await successor;
  assert.deepEqual(window.snapshot().items.map(row => row.id), ['first', 'successor']); window.dispose();
});
