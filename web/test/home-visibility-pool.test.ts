import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {observeHomeIntersection, subscribePageVisibility} from '../src/screens/home/visibility.ts';

test('Home visibility pool shares one IntersectionObserver per options key across hundreds of targets', () => {
  type FakeObserver = {
    options: IntersectionObserverInit | undefined;
    observed: Set<Element>;
    disconnected: boolean;
    callback: IntersectionObserverCallback;
  };
  const created: FakeObserver[] = [];
  class MockIntersectionObserver {
    observed = new Set<Element>();
    disconnected = false;
    constructor(public callback: IntersectionObserverCallback, public options?: IntersectionObserverInit) {
      created.push(this);
    }
    observe(target: Element) { this.observed.add(target); }
    unobserve(target: Element) { this.observed.delete(target); }
    disconnect() { this.disconnected = true; this.observed.clear(); }
  }
  const doc = {defaultView: {IntersectionObserver: MockIntersectionObserver}} as unknown as Document;
  const makeEl = () => ({ownerDocument: doc}) as unknown as Element;

  const states = new Map<number, boolean>();
  const cleanups: (() => void)[] = [];
  const cards = Array.from({length: 220}, (_, index) => {
    const el = makeEl();
    cleanups.push(observeHomeIntersection(el, visible => states.set(index, visible)));
    return el;
  });
  const deferredRows = Array.from({length: 12}, () => {
    const el = makeEl();
    cleanups.push(observeHomeIntersection(el, () => {}, {rootMargin: '800px 0px'}));
    return el;
  });

  assert.equal(created.length, 2, '232 targets share just 2 observers (default + near-margin)');
  assert.equal(created[0].observed.size, 220);
  assert.equal(created[1].observed.size, 12);

  // Batch delivery with multiple entries for the same element delivers the latest state.
  created[0].callback([
    {target: cards[5], isIntersecting: false} as IntersectionObserverEntry,
    {target: cards[5], isIntersecting: true} as IntersectionObserverEntry,
    {target: cards[99], isIntersecting: true} as IntersectionObserverEntry,
  ], created[0] as unknown as IntersectionObserver);
  assert.equal(states.get(5), true);
  assert.equal(states.get(99), true);

  // Unsubscribing all elements disconnects and cleans up both observers.
  for (const stop of cleanups) stop();
  assert.equal(created[0].disconnected, true);
  assert.equal(created[1].disconnected, true);
  assert.equal(deferredRows.length, 12);
});

test('Home page visibility shares a single document visibilitychange listener across subscribers', () => {
  const listeners = new Set<() => void>();
  let addCalls = 0, removeCalls = 0;
  const doc = {
    addEventListener: (type: string, fn: () => void) => { assert.equal(type, 'visibilitychange'); addCalls++; listeners.add(fn); },
    removeEventListener: (type: string, fn: () => void) => { assert.equal(type, 'visibilitychange'); removeCalls++; listeners.delete(fn); },
  } as unknown as Document;

  let notified = 0;
  const offs = Array.from({length: 200}, () => subscribePageVisibility(() => { notified++; }, doc));
  assert.equal(addCalls, 1, 'only one document visibilitychange listener is registered');
  assert.equal(listeners.size, 1);

  for (const fn of listeners) fn();
  assert.equal(notified, 200);

  for (const off of offs.slice(0, 199)) off();
  assert.equal(removeCalls, 0, 'listener stays active while any subscriber remains');
  offs[199]();
  assert.equal(removeCalls, 1, 'listener removed when final subscriber unsubscribes');
  assert.equal(listeners.size, 0);
});

test('HomeScreen does not trigger live clock updates on window resize', () => {
  const source = readFileSync(new URL('../src/screens/home/Home.tsx', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /window\.addEventListener\('resize',\s*update\)/);
  assert.doesNotMatch(source, /new IntersectionObserver\(/);
});
