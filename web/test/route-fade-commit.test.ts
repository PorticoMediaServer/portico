import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {bindRouteFade, type RouteFadeCommits} from '../src/platform/route-motion.ts';
import {easings} from '@design';

// UNRUN production-owner vectors. Controlled WAAPI/style/commit events are not a browser
// restoration measurement or a second router implementation. Root's real adapter is separate.
class Observer {
  static latest: Observer;
  disconnected = false;
  constructor(readonly callback: () => void) {Observer.latest = this;}
  observe() {}
  disconnect() {this.disconnected = true;}
}
class Job {
  onfinish: (() => void) | null = null;
  canceled = 0;
  constructor(readonly frames: Keyframe[], readonly options: KeyframeAnimationOptions) {}
  cancel() {this.canceled++;}
}
function fixture(t: TestContext, initialReduced = false) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'MutationObserver');
  Object.defineProperty(globalThis, 'MutationObserver', {configurable: true, value: Observer});
  t.after(() => {if (descriptor) Object.defineProperty(globalThis, 'MutationObserver', descriptor);else Reflect.deleteProperty(globalThis, 'MutationObserver');});
  const media = Object.assign(new EventTarget(), {matches: initialReduced});
  const state = {opacity: .4};
  const document = {documentElement: {dataset: {} as Record<string, string>}, body: {dataset: {} as Record<string, string>},
    defaultView: {matchMedia: () => media, getComputedStyle: () => ({opacity: String(state.opacity)})}, startViewTransition: undefined as (() => void) | undefined};
  const jobs: Job[] = [];
  const element = {ownerDocument: document, isConnected: true, closest: () => null,
    animate(frames: Keyframe[], options: KeyframeAnimationOptions) {const job = new Job(frames, options);jobs.push(job);return job as unknown as Animation;}};
  const target = {current: element as unknown as HTMLElement | null};
  const listeners = new Set<(key: string) => void>();
  const commits: RouteFadeCommits = {subscribe: listener => {listeners.add(listener);return () => {listeners.delete(listener);};}};
  const bind = () => {const cleanup = bindRouteFade(target, 'original-entry', commits);t.after(cleanup);return cleanup;};
  const rendered = (key = 'original-entry') => {for (const listener of [...listeners]) listener(key);};
  return {document, media, state, jobs, element, target, listeners, bind, rendered};
}

test('fallback waits for the original rendered entry, then starts only once', t => {
  const f = fixture(t);f.bind();assert.equal(f.jobs.length, 0);
  f.rendered('another-entry');assert.equal(f.jobs.length, 0);
  f.rendered();assert.equal(f.jobs.length, 1);assert.equal(f.jobs[0].frames[0].transform, 'translateY(4px)');
  assert.equal(f.jobs[0].options.duration, 180);assert.equal(f.jobs[0].options.easing, easings.out);
  f.rendered();assert.equal(f.jobs.length, 1);
});
test('supported View Transitions and browser-owned null targets never create a fallback subscription', t => {
  const f = fixture(t);f.document.startViewTransition = () => {};f.bind();assert.equal(f.listeners.size, 0);
  f.document.startViewTransition = undefined;f.target.current = null;f.bind();assert.equal(f.listeners.size, 0);assert.equal(f.jobs.length, 0);
});
test('an absent original history-entry witness never creates a fallback owner', t => {
  const f = fixture(t);const cleanup = bindRouteFade(f.target, undefined, {subscribe: () => {throw new Error('no fabricated entry subscription');}});cleanup();
  assert.equal(f.jobs.length, 0);
});
test('a detached or replaced actual content node cannot receive the captured route arrival', t => {
  const f = fixture(t);f.bind();f.target.current = {} as HTMLElement;f.rendered();assert.equal(f.jobs.length, 0);
  f.target.current = f.element as unknown as HTMLElement;f.element.isConnected = false;f.rendered();assert.equal(f.jobs.length, 0);
});
test('cleanup blocks a queued rendered callback and cancels only its own animation', t => {
  const f = fixture(t);const cleanup = f.bind();const stale = [...f.listeners][0];cleanup();stale('original-entry');assert.equal(f.jobs.length, 0);
  const nextCleanup = f.bind();f.rendered();const next = f.jobs[0];cleanup();assert.equal(next.canceled, 0);nextCleanup();assert.equal(next.canceled, 1);
});
test('a current app reduction change continues painted opacity with no transform travel', t => {
  const f = fixture(t);f.bind();f.rendered();const old = f.jobs[0];const lateFinish = old.onfinish;
  f.state.opacity = .37;f.document.body.dataset.reduceMotion = 'true';Observer.latest.callback();
  assert.equal(f.jobs.length, 2);assert.equal(f.jobs[1].frames[0].opacity, .37);assert.equal(f.jobs[1].frames[0].transform, 'none');
  assert.equal(f.jobs[1].options.duration, 160);assert.equal(f.jobs[1].options.easing, 'linear');
  lateFinish?.();assert.equal(f.jobs[1].canceled, 0, 'old completion cannot cancel the replacement paint');
});
test('system reduction is read synchronously at the actual accepted commit', t => {
  const f = fixture(t, true);f.bind();f.rendered();assert.equal(f.jobs[0].frames[0].transform, 'none');
  assert.equal(f.jobs[0].options.duration, 160);assert.equal(f.jobs[0].options.easing, 'linear');
});
test('settled route entries do not replay when the preference changes', t => {
  const f = fixture(t);f.bind();f.rendered();f.jobs[0].onfinish?.();f.media.matches = true;f.media.dispatchEvent(new Event('change'));
  assert.equal(f.jobs.length, 1);
});
