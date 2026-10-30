import test from 'node:test';
import assert from 'node:assert/strict';
import {retainLayout, type LayoutExit, type LayoutRetention} from '../src/kit/layout/retention.ts';
import {resolveRemovalFocus, type RemovalFocusContext} from '../src/kit/layout/focus-context.ts';

// UNRUN source regression against the public History composition 4af76987.
// Helper coverage only: the real retention helper and the real removal-focus
// resolver. Nothing here is a second History state machine, a clock, a portal, a
// product hook or a render/inert/pointer/overlay integration.
//
// A retention call only keeps carrying exits when the previous result and this
// call share the exact same scope object; a different object resets the paint.

type Row = Readonly<{id: string; label: string}>;
const row = (id: string, label = id): Row => ({id, label});
const keyOf = (item: Row) => item.id;
const scope = Object.freeze({surface: 'history'});
const base = (live: readonly Row[], exits: ReadonlyMap<string, LayoutExit<Row>> = new Map()): LayoutRetention<Row> =>
  ({live, present: live, exits, scope});

test('retention keeps every live row even when more than forty are outgoing', () => {
  const live = Array.from({length: 60}, (_, index) => row('live-' + index));
  const next = retainLayout(base(live), live, keyOf, 0, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, scope});
  assert.equal(next.live.length, 60, 'live rows are never capped');
});

test('one shared bound admits at most forty actually visible outgoing rows', () => {
  const before = Array.from({length: 50}, (_, index) => row('r' + index));
  const visible = new Set(before.map(item => item.id));
  const first = retainLayout(base(before), [], keyOf, 0, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, visibleOnly: true, scope}, visible);
  assert.equal(first.exits.size, 40, 'no more than forty visible rows leave at once');
  const noneVisible = retainLayout(base(before), [], keyOf, 0, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, visibleOnly: true, scope}, new Set<string>());
  assert.equal(noneVisible.exits.size, 0, 'only actually visible rows are admitted');
});

test('an admitted exit keeps its original delay and deadline before its expiry, and the bound stays shared', () => {
  const firstBatch = Array.from({length: 30}, (_, index) => row('a' + index));
  const secondBatch = Array.from({length: 20}, (_, index) => row('b' + index));
  const afterFirst = retainLayout(base([...firstBatch, ...secondBatch]), secondBatch, keyOf, 1000, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, scope});
  assert.equal(afterFirst.exits.size, 30);
  const admitted = afterFirst.exits.get('a0')!;
  assert.equal(admitted.admittedAt, 1000);
  assert.equal(admitted.delay, 0);
  assert.equal(admitted.deadline, 1180);
  // A second cohort arrives while the first cohort is still legitimately alive.
  const afterSecond = retainLayout(afterFirst, [], keyOf, 1100, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, scope});
  assert.deepEqual(afterSecond.exits.get('a0'), admitted, 'a later update cannot extend or rewrite the admitted exit');
  assert.equal(afterSecond.exits.size, 40, 'overlapping cohorts share the same forty-row bound');
});

test('reduction only shortens the effective retirement', () => {
  const first = retainLayout(base([row('a')]), [], keyOf, 1000, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, scope});
  const reduced = retainLayout(first, [], keyOf, 1000, true, 180, 160,
    {maxRetained: 40, staggerMs: 40, scope});
  assert.equal(reduced.exits.get('a')!.retireAt, 1160, 'reduced retention never exceeds the original cohort');
  assert.equal(reduced.exits.get('a')!.reduced, true);
});

test('a cohort keeps its layout slot until the final stagger finishes', () => {
  const rows = [row('a'), row('b')];
  const next = retainLayout(base(rows), [], keyOf, 0, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, scope});
  assert.equal(next.exits.get('b')!.delay, 40);
  assert.equal(next.exits.get('b')!.cohortDeadline, 220, '180 plus one 40 ms stagger');
  assert.deepEqual(next.present.map(item => item.id), ['a', 'b'], 'faded predecessors keep their slots');
  const kept = retainLayout(next, [], keyOf, 200, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, scope});
  assert.deepEqual(kept.present.map(item => item.id), ['a', 'b'], 'the slot survives past the first row deadline');
});

test('a new source scope drops the old paint instead of inheriting it', () => {
  const first = retainLayout(base([row('a')]), [], keyOf, 0, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, scope});
  assert.equal(first.exits.size, 1);
  const replaced = retainLayout(first, [row('b')], keyOf, 10, false, 180, 160,
    {maxRetained: 40, staggerMs: 40, scope: Object.freeze({surface: 'history', generation: 2})});
  assert.equal(replaced.exits.size, 0, 'a replaced source drops the previous row paint');
});

// Element fixture. The object is structural and is cast exactly once at the
// HTMLElement boundary; the helper API keeps its real types above this line.
const primary = '[data-layout-primary],button:not(:disabled),a[href],[tabindex="0"]';
type Fixture = Readonly<{element: HTMLElement; state: {focused: boolean; isConnected: boolean; visibility: string}}>;
const members = new Set<object>();
let rootElement: HTMLElement;

function node(id: string, options: {rects?: number; visibility?: string; disabled?: boolean; tabindex?: boolean; connected?: boolean; inRoot?: boolean} = {}): Fixture {
  const state = {focused: false, isConnected: options.connected ?? true, visibility: options.visibility ?? 'visible'};
  const value = {
    id, state,
    get isConnected() {return state.isConnected;},
    matches: (selector: string) => selector === primary && options.disabled !== true,
    hasAttribute: (name: string) => name === 'tabindex' && options.tabindex !== false,
    querySelectorAll: () => [] as unknown[],
    closest: () => null,
    getClientRects: () => Array(options.rects ?? 1).fill({}),
    getAttribute: (name: string) => name === 'aria-disabled' && options.disabled ? 'true' : null,
    focus() {state.focused = true;},
  };
  if (options.inRoot !== false) members.add(value);
  const element = value as unknown as HTMLElement;
  (value as unknown as {element: HTMLElement}).element = element;
  return {element, state};
}

const root = node('root');
rootElement = root.element;
Object.defineProperty(root.element, 'contains', {value: (candidate: object) => members.has(candidate as object)});

const originalStyle = (globalThis as unknown as {getComputedStyle?: unknown}).getComputedStyle;
test.after(() => {
  if (originalStyle === undefined) delete (globalThis as unknown as {getComputedStyle?: unknown}).getComputedStyle;
  else (globalThis as unknown as {getComputedStyle: unknown}).getComputedStyle = originalStyle;
});
(globalThis as unknown as {getComputedStyle: (value: {state?: {visibility?: string}}) => {visibility: string}}).getComputedStyle =
  value => ({visibility: value?.state?.visibility ?? 'visible'});

const focusContext = (order: Fixture[], surviving: Fixture[], sectionStarts: Fixture[] = []): RemovalFocusContext =>
  ({root: rootElement, order: order.map(item => item.element), surviving: surviving.map(item => item.element),
    sectionStarts: sectionStarts.map(item => item.element)});

test('removal focus goes to the next surviving row, then the previous one', () => {
  const [a, b, c] = [node('a'), node('b'), node('c')];
  assert.equal(resolveRemovalFocus(focusContext([a, b, c], [a, c]), b.element).element, c.element, 'next surviving row');
  assert.equal(resolveRemovalFocus(focusContext([a, b, c], [a, b]), c.element).element, b.element, 'previous when the last row goes');
});

test('an outgoing row is never a legal target, and a same-key successor is not confused with it', () => {
  const [a, b] = [node('a'), node('b')];
  const successor = node('b');
  const target = resolveRemovalFocus(focusContext([a, b, successor], [a, successor]), b.element);
  assert.equal(target.element, successor.element, 'the captured identity decides, not the key');
  assert.notEqual(target.element, b.element);
});

test('a connected node outside the captured root is refused by the root bound', () => {
  const a = node('a');
  const foreign = node('foreign', {inRoot: false});
  assert.equal(resolveRemovalFocus(focusContext([a, foreign], [foreign]), a.element).kind, 'region');
});

test('a surviving section start is used before any region fallback, and an empty context returns none', () => {
  const a = node('a');
  const section = node('section');
  assert.equal(resolveRemovalFocus(focusContext([a], [], [section]), a.element).kind, 'section');
  assert.equal(resolveRemovalFocus(focusContext([a], []), a.element).kind, 'region');
  assert.equal(resolveRemovalFocus({root: rootElement, order: [], surviving: [], sectionStarts: []}, node('detached').element).kind, 'none');
});

test('a disconnected or hidden survivor is skipped instead of stealing focus', () => {
  const a = node('a');
  const gone = node('gone', {connected: false});
  const invisible = node('invisible', {visibility: 'hidden'});
  assert.equal(resolveRemovalFocus(focusContext([a, gone], [gone]), a.element).kind, 'region');
  assert.equal(resolveRemovalFocus(focusContext([a, invisible], [invisible]), a.element).kind, 'region');
});
