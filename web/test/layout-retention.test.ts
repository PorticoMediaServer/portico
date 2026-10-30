import assert from 'node:assert/strict';
import test from 'node:test';
import {boundedOutgoingPaint, retainLayout, type LayoutRetention} from '../src/kit/layout/retention.ts';
const keyOf = (item: {id: string}) => item.id;
const rows = (...ids: string[]) => ids.map(id => Object.freeze({id}));
const initial = (live = rows()): LayoutRetention<{id: string}> => ({live, present: live, exits: new Map()});
const policy = {maxRetained: 40, staggerMs: 40};
// Source vectors only, UNRUN. These exercise the consumed retention model;
// actual focus and DOM paint remain separate owner/worker qualification.
test('only visible original rows acquire outgoing slots and every live row remains', () => {
  const before = initial(rows('a', 'b', 'c')), incoming = rows(...Array.from({length: 100}, (_, i) => `new-${i}`));
  const next = retainLayout(before, incoming, keyOf, 0, false, 180, 160, {...policy, visibleOnly: true}, new Set(['b']));
  assert.deepEqual([...next.exits.keys()], ['b']);assert.equal(next.present.length, 101);assert.equal(next.exits.get('b')!.item, before.live[1]);
  assert.ok(incoming.every(item => next.present.includes(item)));
});
test('expired paint cannot be admitted again by a timer or unrelated update', () => {
  const before = initial(rows('a')), first = retainLayout(before, [], keyOf, 0, false, 180, 160, policy);
  const expired = retainLayout(first, [], keyOf, 180, false, 180, 160, policy);
  assert.equal(expired.exits.size, 0);assert.equal(retainLayout(expired, [], keyOf, 1000, false, 180, 160, policy).present.length, 0);
});
test('a returned key withdraws its old exit and a later real removal gets a new token', () => {
  const before = initial(rows('a')), first = retainLayout(before, [], keyOf, 0, false, 180, 160, policy), current = rows('a');
  const returned = retainLayout(first, current, keyOf, 20, false, 180, 160, policy);
  assert.equal(returned.exits.size, 0);assert.equal(returned.present[0], current[0]);
  const again = retainLayout(returned, [], keyOf, 30, false, 180, 160, policy);
  assert.notEqual(again.exits.get('a')!.token, first.exits.get('a')!.token);assert.equal(again.exits.get('a')!.item, current[0]);
});
test('non-finite retention limits refuse extra paint rather than silently making it unbounded', () => {
  const next = retainLayout(initial(rows('a')), [], keyOf, 0, false, 180, 160, {maxRetained: NaN});assert.equal(next.present.length, 0);
});
test('overlapping batches never replace an older retained source with a successor value', () => {
  const before = initial(rows('a', 'b')), first = retainLayout(before, rows('new'), keyOf, 0, false, 180, 160, policy);
  const original = first.exits.get('b')!, next = retainLayout(first, rows('later'), keyOf, 20, false, 180, 160, policy);
  assert.equal(next.exits.get('b'), original);assert.equal(original.item, before.live[1]);assert.equal(original.delay, 40);
});
test('paging cannot admit new exits or renew an already admitted cohort', () => {
  const first = retainLayout(initial(rows('old')), rows('kept', 'page-only'), keyOf, 0, false, 180, 160, policy);
  const original = first.exits.get('old')!;
  const paged = retainLayout(first, rows('kept'), keyOf, 40, false, 180, 160, {...policy, admitNew: false});
  assert.equal(paged.exits.size, 1);assert.equal(paged.exits.get('old'), original);
  assert.equal(paged.exits.has('page-only'), false);assert.equal(original.retireAt, 180);
});
test('the sparse outgoing cap never caps the accepted live answer', () => {
  const old = rows(...Array.from({length: 100}, (_, i) => `old-${i}`)), live = rows(...Array.from({length: 200}, (_, i) => `live-${i}`));
  const next = retainLayout(initial(old), live, keyOf, 0, false, 180, 160, {...policy, visibleOnly: true}, new Set(old.slice(0, 60).map(keyOf)));
  assert.equal(next.exits.size, 40);assert.equal(next.live.length, 200);assert.equal(next.present.length, 240);
  assert.equal(next.exits.get('old-39')!.delay, 1560);assert.equal(next.exits.has('old-40'), false);
});

test('first replacement render caps original outgoing DOM while accepted live rows stay uncapped', () => {
  const old = rows(...Array.from({length: 100}, (_, i) => `old-${i}`));
  const incoming = rows(...Array.from({length: 120}, (_, i) => `live-${i}`)), keys = new Set(incoming.map(keyOf));
  const visible = new Set(old.slice(20, 80).map(keyOf));
  const present = boundedOutgoingPaint([...old, ...incoming], keys, keyOf, new Map(), visible, 40);
  assert.equal(present.filter(item => !keys.has(item.id)).length, 40);
  assert.ok(present.includes(old[20]));assert.equal(present.includes(old[0]), false);
  assert.ok(incoming.every(item => present.includes(item)));
});

test('existing outgoing cohorts keep priority at the render bound without borrowing live capacity', () => {
  const old = rows(...Array.from({length: 60}, (_, i) => `old-${i}`)), incoming = rows('live');
  const exits = new Map(old.slice(40, 60).map(item => [item.id, {}]));
  const present = boundedOutgoingPaint([...old, ...incoming], new Set(['live']), keyOf, exits, new Set(old.map(keyOf)), 40);
  assert.equal(present.length, 41);assert.ok(old.slice(40, 60).every(item => present.includes(item)));
  assert.equal(present.includes(old[30]), false);
});
