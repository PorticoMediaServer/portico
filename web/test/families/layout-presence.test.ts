import {test, type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';
import {easings, reducedMotion} from '@design';
import type {LayoutRetentionPolicy} from '../../src/kit/layout/retention.ts';

type Ref = {current: unknown};
// React treats an omitted dependency list as "run after every commit", while an
// empty or populated list compares by Object.is. The fixture must model both, or
// a real deps-less layout pass cannot be exercised at all.
type Effect = {deps: readonly unknown[] | undefined; cleanup?: () => void};
const bridgeKey = '__porticoLayoutPresenceHooks';
const bridge = {current: undefined as HookFrame | undefined};
(globalThis as Record<string, unknown>)[bridgeKey] = bridge;
const hookModule = 'data:text/javascript,' + encodeURIComponent(`const bridge = globalThis.${bridgeKey}; ${['useRef', 'useState', 'useLayoutEffect'].map(name => `export const ${name} = (...args) => bridge.current.${name}(...args);`).join('\n')}`);
const loader = registerHooks({load(url, context, next) {
  if (url.endsWith('/kit/layout/presence.ts')) return {format: 'module-typescript', shortCircuit: true,
    source: readFileSync(new URL(url), 'utf8').replace("from 'react'", `from ${JSON.stringify(hookModule)}`)};
  return next(url, context);
}});
const {useLayoutPresence} = await import('../../src/kit/layout/presence.ts');
test.after(() => { loader.deregister(); delete (globalThis as Record<string, unknown>)[bridgeKey]; });

// Run the actual hook with dependency equality, commit-before-layout effects,
// cleanup-before-setup, and bounded state rerenders. DOM geometry, time, and WAAPI
// are controlled; map references are observed through refs without changing source.
// This is lifecycle/ownership evidence, not React DOM or heap measurement.
class HookFrame {
  private slots: any[] = [];
  private cursor = 0;
  private dirty = false;
  private mounted = true;
  private queued: {effect: Effect; run: () => void | (() => void)}[] = [];
  refs: Ref[] = [];
  useRef(value: unknown) {
    const i = this.cursor++;
    if (!this.slots[i]) { this.slots[i] = {current: value}; this.refs.push(this.slots[i]); }
    return this.slots[i];
  }
  useState(initial: unknown) {
    const i = this.cursor++;
    this.slots[i] ??= {value: initial};
    return [this.slots[i].value, (value: unknown) => {
      assert.ok(this.mounted, 'no state updates after unmount');
      if (!Object.is(value, this.slots[i].value)) { this.slots[i].value = value; this.dirty = true; }
    }];
  }
  useLayoutEffect(run: () => void | (() => void), deps?: readonly unknown[]) {
    const i = this.cursor++, previous: Effect | undefined = this.slots[i], prior = previous?.deps;
    if (!previous || deps === undefined || prior === undefined
      || deps.length !== prior.length || deps.some((value, index) => !Object.is(value, prior[index]))) {
      const effect = {deps, cleanup: previous?.cleanup}; this.slots[i] = effect; this.queued.push({effect, run});
    }
  }
  render<T>(run: () => T, commit: (result: T) => void): T {
    for (let attempts = 0; attempts < 8; attempts++) {
      bridge.current = this; this.cursor = 0; this.dirty = false; this.queued = [];
      const result = run(); commit(result);
      this.queued.forEach(job => job.effect.cleanup?.());
      this.queued.forEach(job => { job.effect.cleanup = job.run() ?? undefined; });
      if (!this.dirty) return result;
    }
    throw new Error('Presence effects did not settle within eight renders');
  }
  get maps(): Map<unknown, unknown>[] { return this.refs.map(ref => ref.current).filter(value => value instanceof Map); }
  // Ownership is per element. The hook now keeps two element-keyed refs (the
  // animation and the last animated target), so count each owned element once
  // rather than once per map that happens to mention it.
  get ownedNodes(): LayoutNode[] {
    const owned = new Set<LayoutNode>();
    for (const map of this.maps) for (const key of map.keys()) if (key instanceof LayoutNode) owned.add(key);
    return [...owned];
  }
  animation(node: LayoutNode): ControlledAnimation | undefined { return this.maps.map(map => map.get(node)).find(value => value instanceof ControlledAnimation); }
  unmount() {
    if (!this.mounted) return;
    this.mounted = false;
    for (const slot of this.slots) slot?.cleanup?.();
  }
}
class ControlledAnimation {
  finished: Promise<Animation>;
  cancelCalls = 0;
  finish!: () => void;
  reject!: () => void;
  constructor(readonly frames: Keyframe[], readonly options: KeyframeAnimationOptions) {
    this.finished = new Promise((resolve, reject) => {
      this.finish = () => resolve(this as unknown as Animation);
      this.reject = () => reject(new DOMException('Canceled', 'AbortError'));
    });
  }
  cancel() { this.cancelCalls++; this.reject(); }
}
class LayoutNode {
  dataset: {layoutItem: string};
  left = 0;
  inert = false;
  animations: ControlledAnimation[] = [];
  constructor(key: string) { this.dataset = {layoutItem: key}; }
  // The move pass compares offset geometry between commits; report the same
  // layout the rect getter exposes so an unchanged row is recognised and a
  // reordered row always differs.
  get offsetLeft() { return this.left; }
  get offsetTop() { return 0; }
  get offsetWidth() { return 100; }
  get offsetHeight() { return 100; }
  getBoundingClientRect() { return {left: this.left, top: 0, right: this.left + 100, bottom: 100, width: 100, height: 100} as DOMRect; }
  animate(frames: Keyframe[], options: KeyframeAnimationOptions) {
    const animation = new ControlledAnimation(frames, options); this.animations.push(animation); return animation;
  }
}
const getKey = (key: string) => key;
function fixture(t: TestContext) {
  t.mock.timers.enable({apis: ['setTimeout']});
  let now = 0, items: readonly string[] = [], reduced = false, policy: LayoutRetentionPolicy | undefined;
  t.mock.method(performance, 'now', () => now);
  const hooks = new HookFrame(), root = {children: [] as LayoutNode[]};
  const render = (next = items, reduce = reduced, nextPolicy = policy) => {
    items = next; reduced = reduce; policy = nextPolicy;
    return hooks.render(() => useLayoutPresence(items, getKey, reduced, 180, 320, policy), presence => {
      const previous = new Map(root.children.map(node => [node.dataset.layoutItem, node]));
      root.children = presence.present.map((key, index) => { const node = previous.get(key) ?? new LayoutNode(key); node.left = index * 100; node.inert = presence.leaving.has(key); return node; });
      presence.root.current = root as unknown as HTMLDivElement;
    });
  };
  const advance = (ms: number) => { now += ms; t.mock.timers.tick(ms); return render(); };
  const node = (key: string) => { const value = root.children.find(node => node.dataset.layoutItem === key); assert.ok(value); return value; };
  t.after(() => hooks.unmount());
  return {hooks, root, render, advance, node};
}

test('move then remove releases ownership before exit timeout removes the DOM child', async t => {
  const f = fixture(t); f.render(['a', 'b']); f.render(['b', 'a']);
  const node = f.node('a'), animation = f.hooks.animation(node)!;
  assert.ok(animation);
  assert.deepEqual(animation.frames, [{transform: 'translate(-100px,0px)'}, {transform: 'translate(0,0)'}]);
  assert.deepEqual(animation.options, {duration: 320, easing: easings.inOut});
  const leaving = f.render(['b']);
  assert.deepEqual(leaving.present, ['b', 'a']); assert.deepEqual([...leaving.leaving], ['a']);
  assert.equal(f.node('a'), node, 'the leaving DOM child keeps its identity');
  assert.equal(animation.cancelCalls, 1);
  assert.equal(f.hooks.animation(node), undefined, 'cancellation releases ownership before promise reactions');
  await Promise.resolve();
  // The withdrawn row's element/animation pair is released. The current row 'b'
  // keeps its own in-flight move: retirement must not restart an unchanged move.
  const current = f.node('b'), move = f.hooks.animation(current);
  assert.deepEqual(f.hooks.ownedNodes.map(owned => owned.dataset.layoutItem), ['b']);
  assert.ok(move, 'the current row keeps its in-flight move');
  assert.deepEqual(f.advance(179).present, ['b', 'a']);
  assert.deepEqual(f.advance(1).present, ['b']);
  assert.deepEqual(f.root.children.map(node => node.dataset.layoutItem), ['b']);
  assert.deepEqual(f.hooks.ownedNodes.map(owned => owned.dataset.layoutItem), ['b'], 'no removed Element/Animation pair remains in hook refs');
  assert.equal(f.hooks.animation(current), move, 'retirement does not restart the current move');
  assert.equal(move.cancelCalls, 0);
  move.finish();
  await Promise.resolve();
  assert.equal(f.hooks.ownedNodes.length, 0, 'the current move releases itself when it finishes');
});

test('repeated unique move/remove cycles keep owned animation references bounded', async t => {
  const f = fixture(t);
  for (let cycle = 0; cycle < 24; cycle++) {
    const a = `a-${cycle}`, b = `b-${cycle}`;
    f.render([a, b]); f.render([b, a]);
    assert.equal(f.hooks.ownedNodes.length, 2);
    f.render([]);
    f.advance(180);
    assert.equal(f.root.children.length, 0);
    assert.equal(f.hooks.ownedNodes.length, 0, `cycle ${cycle} releases removed nodes without unmount`);
  }
  await Promise.resolve();
  assert.equal(f.hooks.ownedNodes.length, 0);
});

test('an old canceled animation rejection cannot release its current replacement', async t => {
  const f = fixture(t); f.render(['a', 'b']); f.render(['b', 'a']);
  const node = f.node('a'), old = f.hooks.animation(node)!;
  f.render(['a', 'b']);
  const replacement = f.hooks.animation(node)!;
  assert.ok(replacement); assert.notEqual(replacement, old); assert.equal(old.cancelCalls, 1);
  await Promise.resolve();
  assert.equal(f.hooks.animation(node), replacement, 'old rejection must respect replacement identity');
  replacement.finish();
  await Promise.resolve();
  assert.equal(f.hooks.animation(node), undefined, 'the current animation releases itself on completion');
});

test('a current animation releases ownership when finished rejects independently', async t => {
  const f = fixture(t); f.render(['a', 'b']); f.render(['b', 'a']);
  const node = f.node('a'), animation = f.hooks.animation(node)!;
  animation.reject();
  await Promise.resolve();
  assert.equal(f.hooks.animation(node), undefined);
  assert.equal(f.hooks.ownedNodes.length, 1, 'the other in-flight animation stays owned');
});

test('unmount cancels active replacements and clears ownership and pending exit work', async t => {
  const f = fixture(t); f.render(['a', 'b', 'c']); f.render(['b', 'a', 'c']);
  f.render(['b', 'a']); f.render(['a', 'b']);
  const animations = f.hooks.ownedNodes.map(node => f.hooks.animation(node)!);
  assert.equal(animations.length, 2);
  assert.deepEqual(f.root.children.map(node => node.dataset.layoutItem), ['a', 'b', 'c']);
  f.hooks.unmount();
  assert.ok(animations.every(animation => animation.cancelCalls === 1));
  assert.equal(f.hooks.ownedNodes.length, 0);
  t.mock.timers.tick(1000); // A retained exit timer would try to update unmounted state.
  await Promise.resolve();
  assert.equal(f.hooks.ownedNodes.length, 0);
});

test('reduced motion cancels moves and preserves its own exit deadline', async t => {
  const f = fixture(t); f.render(['a', 'b']); f.render(['b', 'a']);
  const node = f.node('a'), animation = f.hooks.animation(node)!;
  f.render(['b'], true);
  assert.equal(animation.cancelCalls, 1); assert.equal(f.hooks.ownedNodes.length, 0);
  assert.deepEqual(f.advance(reducedMotion.durationMs - 1).present, ['b', 'a']);
  assert.deepEqual(f.advance(1).present, ['b']);
  const counts = new Map(f.root.children.map(node => [node, node.animations.length]));
  f.render(['a', 'b'], true); f.render(['b', 'a'], true);
  assert.ok(f.root.children.every(node => node.animations.length === (counts.get(node) ?? 0)), 'reduced motion creates no new moves');
  await Promise.resolve();
});

test('an item returning before its exit deadline retains its DOM identity and incoming order', t => {
  const f = fixture(t); f.render(['a', 'b']); const original = f.node('a');
  f.render(['b']); f.advance(100);
  const returned = f.render(['a', 'b']);
  assert.deepEqual(returned.present, ['a', 'b']); assert.equal(returned.leaving.size, 0);
  assert.equal(f.node('a'), original);
  assert.deepEqual(f.advance(100).present, ['a', 'b']);
});


test('overlapping removals share forty outgoing slots without clipping any current live row', t => {
  const f = fixture(t), first = Array.from({length: 80}, (_, i) => `old-${i}`), second = Array.from({length: 80}, (_, i) => `next-${i}`);
  const policy = {maxRetained: 40, staggerMs: 40};f.render(first, false, policy);
  const one = f.render(second);assert.equal(one.leaving.size, 40);assert.equal(one.present.length, 120);
  assert.ok(second.every(key => one.present.includes(key)));
  const originalLast = one.exits.get('old-39')!;assert.equal(originalLast.delay, 1560);assert.equal(originalLast.deadline, 1740);
  const two = f.render(['current']);assert.equal(two.leaving.size, 40);assert.equal(two.present.length, 41);
  assert.equal(two.exits.get('old-39'), originalLast, 'a new cohort cannot renew an admitted row');
  assert.equal(two.exits.has('next-0'), false, 'a full outgoing budget admits no extra paint');
  f.advance(180);const three = f.render([]);assert.equal(three.leaving.size, 40);
  assert.equal(three.exits.has('current'), false);assert.equal(three.exits.get('old-39'), originalLast);
  assert.deepEqual(f.advance(1560).present, []);
});

test('cohort layout stays put until its final stagger and later admissions cannot renew it', t => {
  const f = fixture(t), policy = {maxRetained: 4, staggerMs: 40};f.render(['a', 'b'], false, policy);
  const first = f.render(['x', 'y']), a = first.exits.get('a')!, b = first.exits.get('b')!;
  assert.equal(a.deadline, 180);assert.equal(b.delay, 40);assert.equal(b.deadline, 220);
  assert.equal(a.retireAt, 220);assert.equal(b.retireAt, 220);
  f.advance(20);const later = f.render(['new']);assert.equal(later.exits.get('a'), a);assert.equal(later.exits.get('b'), b);
  assert.equal(later.leaving.size, 4);assert.equal(later.exits.get('x')!.deadline, 200);assert.equal(later.exits.get('x')!.retireAt, 240);
  const held = f.advance(160);assert.equal(held.exits.get('a'), a, 'the already transparent first row holds its cohort layout slot');
  assert.equal(f.advance(40).exits.has('b'), false);assert.equal(f.render().exits.has('x'), true);
  assert.deepEqual(f.advance(20).present, ['new']);
});

test('replacement original scope drops retained paint and its old timer cannot recreate it', t => {
  const f = fixture(t), original = {}, replacement = {};f.render(['old'], false, {maxRetained: 40, scope: original});f.render([]);
  const fresh = f.render(['new'], false, {maxRetained: 40, scope: replacement});assert.deepEqual(fresh.present, ['new']);assert.equal(fresh.leaving.size, 0);
  assert.deepEqual(f.advance(1000).present, ['new']);assert.equal(f.hooks.ownedNodes.length, 0);
});

test('active reduction shortens retention without renewing original admission or delay', t => {
  const f = fixture(t), policy = {maxRetained: 40, staggerMs: 40};f.render(['a', 'b', 'c'], false, policy);
  const original = f.render([]), c = original.exits.get('c')!;f.advance(20);
  const reduced = f.render([], true), next = reduced.exits.get('c')!;
  assert.equal(next.token, c.token);assert.equal(next.delay, 80);assert.equal(next.deadline, 260);assert.equal(next.retireAt, 180);assert.equal(next.cohortDeadline, 260);
  assert.deepEqual(f.advance(160).present, []);
});


test('a captured owner handoff runs before removed controls become inert', t => {
  const f = fixture(t);let calls = 0;
  const policy = {maxRetained: 40, beforeLeave: (remaining: ReadonlySet<string>) => {
    calls++;assert.equal(f.node('a').inert, false);assert.ok(remaining.has('b'));assert.equal(remaining.has('a'), false);
  }};
  f.render(['a', 'b'], false, policy);assert.equal(calls, 0);
  const next = f.render(['b']);assert.equal(calls, 1);assert.equal(f.node('a').inert, true);assert.ok(next.leaving.has('a'));
  f.advance(180);assert.equal(calls, 1, 'a retirement tick cannot repeat the original focus handoff');
});
