import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';

type Effect = {deps: readonly unknown[]; cleanup?: () => void};
const bridgeKey = '__porticoDockEntryHooks';
const bridge = {current: undefined as HookFrame | undefined};
(globalThis as Record<string, unknown>)[bridgeKey] = bridge;
const hookModule = 'data:text/javascript,' + encodeURIComponent(`const bridge = globalThis.${bridgeKey}; ${['useRef', 'useState', 'useLayoutEffect'].map(name => `export const ${name} = (...args) => bridge.current.${name}(...args);`).join('\n')}`);
const loader = registerHooks({load(url, context, next) {
  if (url.endsWith('/kit/dock/dock-entry.ts') || url.endsWith('/kit/dock/use-motion.ts')) {
    return {format: 'module-typescript', shortCircuit: true, source: readFileSync(new URL(url), 'utf8').replace("from 'react'", `from ${JSON.stringify(hookModule)}`)};
  }
  return next(url, context);
}});

/** Minimal DOM + WAAPI, mirroring the existing layout-presence hook seam. */
class ControlledAnimation {
  playState: 'running' | 'finished' | 'idle' = 'running';
  cancelled = false;
  private resolved = false;
  private resolveFinished!: () => void;
  readonly finished: Promise<void>;
  constructor(readonly keyframes: readonly Record<string, unknown>[], readonly options: Record<string, unknown>) {
    this.finished = new Promise<void>(resolve => {this.resolveFinished = resolve;});
  }
  cancel() {this.cancelled = true; this.playState = 'idle';}
  /** Settles the promise even after a later cancel, so a queued completion can be exercised. */
  resolve() {if (this.resolved) return; this.resolved = true; this.playState = 'finished'; this.resolveFinished();}
}
class FakeElement {
  style: Record<string, string> = {};
  computed = {opacity: '1', transform: 'none'};
  readonly animations: ControlledAnimation[] = [];
  words?: FakeElement;
  animate(keyframes: readonly Record<string, unknown>[], options: Record<string, unknown>) {
    const animation = new ControlledAnimation(keyframes, options); this.animations.push(animation); return animation as unknown as Animation;
  }
  querySelector(selector: string) {return selector === '[data-dock-identity]' ? this.words ?? null : null;}
  closest() {return null;}
  dispatchEvent() {return true;}
}
class HookFrame {
  private slots: any[] = [];
  private cursor = 0;
  private dirty = false;
  private mounted = true;
  private queued: {effect: Effect; run: () => void | (() => void)}[] = [];
  useRef(value: unknown) {
    const i = this.cursor++;
    if (!this.slots[i]) this.slots[i] = {current: value};
    return this.slots[i];
  }
  useState(initial: unknown) {
    const i = this.cursor++;
    this.slots[i] ??= {value: initial};
    return [this.slots[i].value, (value: unknown) => {
      assert.ok(this.mounted, 'no state updates after unmount');
      if (!Object.is(value, this.slots[i].value)) {this.slots[i].value = value; this.dirty = true;}
    }];
  }
  useLayoutEffect(run: () => void | (() => void), deps: readonly unknown[]) {
    const i = this.cursor++, previous: Effect | undefined = this.slots[i];
    if (!previous || deps.length !== previous.deps.length || deps.some((value, index) => !Object.is(value, previous.deps[index]))) {
      const effect = {deps, cleanup: previous?.cleanup}; this.slots[i] = effect; this.queued.push({effect, run});
    }
  }
  render<T>(run: () => T, commit: (result: T) => void): T {
    for (let attempts = 0; attempts < 8; attempts++) {
      bridge.current = this; this.cursor = 0; this.dirty = false; this.queued = [];
      const result = run(); commit(result);
      this.queued.forEach(job => job.effect.cleanup?.());
      this.queued.forEach(job => {job.effect.cleanup = job.run() ?? undefined;});
      if (!this.dirty) return result;
    }
    throw new Error('Dock entry effects did not settle within eight renders');
  }
  unmount() {
    if (!this.mounted) return;
    this.mounted = false;
    for (const slot of this.slots) slot?.cleanup?.();
  }
}

// Environment the hooks read at call time.
const listeners = new Set<() => void>();
const media = {matches: false, addEventListener: (_: string, listener: () => void) => listeners.add(listener), removeEventListener: (_: string, listener: () => void) => listeners.delete(listener)};
(globalThis as Record<string, unknown>).window = {matchMedia: () => media};
(globalThis as Record<string, unknown>).document = {documentElement: {dataset: {}}, body: {dataset: {}}};
(globalThis as Record<string, unknown>).MutationObserver = class {observe() {} disconnect() {}};
const computed = new WeakMap<FakeElement, FakeElement['computed']>();
(globalThis as Record<string, unknown>).getComputedStyle = (element: FakeElement) => computed.get(element) ?? element.computed;

const {useDockEntry} = await import('../src/kit/dock/dock-entry.ts');
const {useDockHandover} = await import('../src/kit/dock/use-motion.ts');
const {dockWordsOwner} = await import('../src/kit/dock/dock-words.ts');
test.after(() => {
  loader.deregister();
  delete (globalThis as Record<string, unknown>)[bridgeKey];
  delete (globalThis as Record<string, unknown>).window;
  delete (globalThis as Record<string, unknown>).document;
  delete (globalThis as Record<string, unknown>).MutationObserver;
  delete (globalThis as Record<string, unknown>).getComputedStyle;
});

function rig() {
  const screen = new FakeElement(), words = new FakeElement();
  screen.words = words;
  computed.set(screen, screen.computed); computed.set(words, words.computed);
  media.matches = false; listeners.clear();
  const frame = new HookFrame(), ref = {current: screen as unknown as HTMLElement};
  let identity = 'A';
  const paint = () => frame.render(() => {useDockEntry(ref, identity); useDockHandover(ref, identity);}, () => {});
  const preference = (reduced: boolean) => {media.matches = reduced; listeners.forEach(listener => listener());};
  const drain = async () => {await Promise.resolve(); await Promise.resolve();};
  const settle = () => {frame.unmount(); listeners.clear(); media.matches = false;};
  return {screen, words, frame, paint, preference, drain, settle, setIdentity: (next: string) => {identity = next;}};
}
const running = (element: FakeElement) => element.animations.filter(animation => animation.playState === 'running');

test('a preference change before the words delay retargets from the real zero paint', async () => {
  const r = rig();
  try {
    r.paint();
    const entryWords = r.words.animations.at(-1)!;
    assert.equal(entryWords.options.delay, 300);
    r.words.computed.opacity = '0';
    r.preference(true);
    r.paint();
    const retarget = r.words.animations.at(-1)!;
    assert.notEqual(retarget, entryWords);
    assert.equal(retarget.options.duration, 160);
    assert.equal(retarget.options.easing, 'linear');
    assert.equal(retarget.options.delay, undefined);
    assert.deepEqual(retarget.keyframes[0], {opacity: '0'});
    assert.equal(running(r.words).length, 1);
    assert.ok(dockWordsOwner(r.words), 'the retarget keeps the entry claim while it paints');
  } finally {r.settle();}
});

test('a partial painted opacity retargets, and a rapid reversal uses the newest paint', async () => {
  const r = rig();
  try {
    r.paint();
    r.words.computed.opacity = '0.35';
    r.preference(true); r.paint();
    assert.deepEqual(r.words.animations.at(-1)!.keyframes[0], {opacity: '0.35'});
    r.words.computed.opacity = '0.6';
    r.preference(false); r.paint();
    const newest = r.words.animations.at(-1)!;
    assert.equal(newest.options.duration, 160);
    assert.deepEqual(newest.keyframes[0], {opacity: '0.6'});
    assert.equal(running(r.words).length, 1);
  } finally {r.settle();}
});

test('a completion queued before takeover cannot settle or cancel the successor', async () => {
  const r = rig();
  try {
    r.paint();
    const oldWords = r.words.animations.at(-1)!;
    oldWords.resolve(); // completion queued while it is still the current job
    r.setIdentity('B');
    r.paint(); // takeover: entry releases the claim, handover starts its authored paint
    const successor = r.words.animations.at(-1)!;
    assert.equal(successor.options.duration, 180);
    await r.drain();
    assert.equal(successor.playState, 'running');
    assert.equal(successor.cancelled, false);
    assert.equal(dockWordsOwner(r.words), undefined);
    assert.equal(r.words.style.opacity, undefined);
  } finally {r.settle();}
});

test('a stale words completion from a retarget cannot reset a later one', async () => {
  const r = rig();
  try {
    r.paint();
    r.words.computed.opacity = '0.35';
    r.preference(true); r.paint();
    const retarget = r.words.animations.at(-1)!;
    retarget.resolve();
    r.words.computed.opacity = '0.6';
    r.preference(false); r.paint();
    const successor = r.words.animations.at(-1)!;
    await r.drain();
    assert.notEqual(successor, retarget);
    assert.equal(successor.playState, 'running');
    assert.equal(successor.cancelled, false);
    assert.equal(r.words.style.opacity, undefined);
  } finally {r.settle();}
});

test('the entry claim ends when its words paint settles', async () => {
  const r = rig();
  try {
    r.paint();
    assert.ok(dockWordsOwner(r.words), 'the entry owns the words while it paints them');
    const ink = r.words.animations.at(-1)!;
    ink.resolve();
    await r.drain();
    assert.equal(dockWordsOwner(r.words), undefined);
    assert.equal(r.words.style.opacity, '1');
  } finally {r.settle();}
});
