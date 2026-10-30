import test from 'node:test';
import assert from 'node:assert/strict';
import {planSelectionPlayback, selectionStartSeconds, SELECTION_SEGMENTS_MAX} from '../src/selection-playback-plan.ts';
import type {HomeSelectionPlaybackItem} from '../src/home-selection/types.ts';

type Kind = HomeSelectionPlaybackItem['identity']['kind'];
const identity = (id: string, kind: Kind, libraryId = 'lib') => ({id, kind, libraryId} as HomeSelectionPlaybackItem['identity']);
const leaf = (id: string, kind: Kind = 'movie', extra: {startSeconds?: number; libraryId?: string; available?: boolean; missing?: boolean; targetId?: string} = {}): HomeSelectionPlaybackItem => ({
  identity: identity(id, kind, extra.libraryId),
  entry: {id, kind: kind as never, libraryId: extra.libraryId ?? 'lib', title: id, available: extra.available, missing: extra.missing, playback: {itemId: extra.targetId ?? id, ...(extra.startSeconds === undefined ? {} : {startSeconds: extra.startSeconds})}},
  target: {itemId: extra.targetId ?? id, ...(extra.startSeconds === undefined ? {} : {startSeconds: extra.startSeconds})},
  startIntent: 'resume',
});
const container = (id: string, kind: Kind, extra: {targetId?: string; libraryId?: string; available?: boolean; missing?: boolean} = {}): HomeSelectionPlaybackItem => ({
  identity: identity(id, kind, extra.libraryId),
  entry: {id, kind: kind as never, libraryId: extra.libraryId ?? 'lib', title: id, available: extra.available, missing: extra.missing, playback: {itemId: extra.targetId ?? `${id}-resume`}},
  target: {itemId: extra.targetId ?? `${id}-resume`},
  startIntent: 'resume',
});
const collection = (id: string): HomeSelectionPlaybackItem => ({
  identity: identity(id, 'collection'),
  collection: {collection: {id, libraryId: 'lib', name: id, itemCount: 3, revision: 1}, mediaKind: 'movie', members: [{id: 'm1', kind: 'movie'} as never]},
});
const invalidTarget = 'invalid_selection_playback_target';
const throws = (fn: () => unknown, message: string) => assert.throws(fn, (error: unknown) => error instanceof Error && error.message === message);

test('display order, aggregates as selectors and leaves in one segment', () => {
  const plan = planSelectionPlayback([container('show1', 'show'), leaf('m1'), leaf('m2'), collection('col1'), leaf('show2', 'episode')], 'replace');
  assert.deepEqual(plan.segments, [
    {source: {container: {kind: 'show', id: 'show1'}}, anchor: {itemId: 'show1-resume'}},
    {source: {items: {ids: ['m1', 'm2']}}},
    {source: {container: {kind: 'collection', id: 'col1'}}},
    {source: {items: {ids: ['show2']}}},
  ]);
});

test('a bounded preview is never flattened into its members', () => {
  const plan = planSelectionPlayback([collection('col1')], 'replace');
  assert.deepEqual(plan.segments, [{source: {container: {kind: 'collection', id: 'col1'}}}]);
});

test('only Play anchors the first container, and only the first item', () => {
  assert.deepEqual(planSelectionPlayback([container('s1', 'show')], 'replace').segments, [{source: {container: {kind: 'show', id: 's1'}}, anchor: {itemId: 's1-resume'}}]);
  assert.deepEqual(planSelectionPlayback([container('s1', 'show')], 'insert').segments, [{source: {container: {kind: 'show', id: 's1'}}}]);
  assert.deepEqual(planSelectionPlayback([leaf('m1'), container('s2', 'season')], 'replace').segments, [{source: {items: {ids: ['m1']}}}, {source: {container: {kind: 'season', id: 's2'}}}]);
});

test('supported containers include a disc; channel and person entries are refused', () => {
  assert.deepEqual(planSelectionPlayback([container('d1', 'disc')], 'replace').segments, [{source: {container: {kind: 'disc', id: 'd1'}}, anchor: {itemId: 'd1-resume'}}]);
  for (const kind of ['channel', 'person'] as const) {
    throws(() => planSelectionPlayback([{identity: identity('x', kind), entry: {}, target: {itemId: 'x'}, startIntent: 'resume'} as never], 'replace'), invalidTarget);
  }
  throws(() => planSelectionPlayback([], 'replace'), invalidTarget);
});

test('leaves are chunked at 500 ids in each items segment', () => {
  const ids = (count: number) => Array.from({length: count}, (_, index) => `m${index}`);
  const plan = planSelectionPlayback(ids(500).map(id => leaf(id)), 'replace');
  assert.equal(plan.segments.length, 1);
  assert.deepEqual(plan.segments[0]!.source, {items: {ids: ids(500)}});
  const split = planSelectionPlayback(ids(501).map(id => leaf(id)), 'replace');
  assert.equal(split.segments.length, 2);
  assert.deepEqual(split.segments.map(segment => 'items' in segment.source && segment.source.items.ids.length), [500, 1]);
  // The 500-id split is a segment fact, not a queue-size fact.
  assert.deepEqual(planSelectionPlayback(ids(501).map(id => leaf(id)), 'insert').segments.map(segment => 'items' in segment.source && segment.source.items.ids.length), [500, 1]);
});

test('the 16-segment ceiling belongs to one create/replace request', () => {
  assert.equal(SELECTION_SEGMENTS_MAX, 16);
  const alternating = (count: number) => Array.from({length: count}, (_, index) => index % 2 ? leaf(`m${index}`) : container(`s${index}`, 'show'));
  assert.equal(planSelectionPlayback(alternating(16), 'replace').segments.length, 16);
  throws(() => planSelectionPlayback(alternating(17), 'replace'), 'queue_too_large');
  // Insertion is one request per segment: no invented batch limit and no order change.
  const inserted = planSelectionPlayback(alternating(17), 'insert');
  assert.equal(inserted.segments.length, 17);
  assert.deepEqual(inserted.segments.map(segment => 'container' in segment.source ? segment.source.container.id : (segment.source as {items: {ids: string[]}}).items.ids[0]),
    alternating(17).map(item => item.identity.id));
});

test('every identity and availability check is kept', () => {
  throws(() => planSelectionPlayback([leaf('m1', 'movie', {libraryId: 'lib'})].map(item => ({...item, identity: {...item.identity, id: 'other'}})), 'replace'), invalidTarget);
  throws(() => planSelectionPlayback([{...leaf('m1'), identity: {...identity('m1', 'movie'), libraryId: 'other'}}], 'replace'), invalidTarget);
  throws(() => planSelectionPlayback([{...leaf('m1'), entry: {...(leaf('m1') as {entry: object}).entry, kind: 'episode' as never}}], 'replace'), invalidTarget);
  throws(() => planSelectionPlayback([leaf('m1', 'movie', {available: false})], 'replace'), invalidTarget);
  throws(() => planSelectionPlayback([leaf('m1', 'movie', {missing: true})], 'replace'), invalidTarget);
  throws(() => planSelectionPlayback([leaf('m1', 'movie', {targetId: 'm2'})], 'replace'), invalidTarget);
  throws(() => planSelectionPlayback([container('s1', 'radio')], 'replace'), invalidTarget);
  // An unusable identity is refused by the wire-id check before any segment exists.
  assert.throws(() => planSelectionPlayback([{...leaf('m1'), identity: {...identity('m1', 'movie'), id: ''}}], 'replace'), (error: unknown) => error instanceof Error && error.name === 'ContractError');
  // A container's target need not be itself; a leaf's must be.
  assert.equal(planSelectionPlayback([container('s1', 'show', {targetId: 'ep1'})], 'replace').segments.length, 1);
});

test('the start bound stays finite and within 0..1,000,000,000', () => {
  for (const value of [-1, 1e9 + 1, Number.NaN, Number.POSITIVE_INFINITY]) {
    throws(() => planSelectionPlayback([leaf('m1', 'movie', {startSeconds: value})], 'replace'), invalidTarget);
  }
  for (const value of [0, 1e9, 12.5]) {
    assert.equal(planSelectionPlayback([leaf('m1', 'movie', {startSeconds: value})], 'replace').startSeconds, value);
  }
});

test('the first item’s own start is the plan’s start, and a collection has none', () => {
  assert.equal(planSelectionPlayback([leaf('m1', 'movie', {startSeconds: 42}), leaf('m2', 'movie', {startSeconds: 7})], 'replace').startSeconds, 42);
  assert.equal(planSelectionPlayback([collection('col1'), leaf('m1', 'movie', {startSeconds: 7})], 'replace').startSeconds, undefined);
  assert.equal(selectionStartSeconds([]), undefined);
});

test('the plan is frozen: neither the result nor its segment list can be changed', () => {
  const plan = planSelectionPlayback([leaf('m1'), leaf('m2')], 'replace');
  assert.equal(Object.isFrozen(plan), true);
  assert.equal(Object.isFrozen(plan.segments), true);
  assert.throws(() => {(plan as {segments: unknown[]}).segments.push({source: {items: {ids: ['m3']}}});});
  assert.throws(() => {(plan as {startSeconds?: number}).startSeconds = 1;});
  // The caller's reverse for `next` still works on a copy.
  assert.deepEqual([...plan.segments].reverse().length, 1);
});
