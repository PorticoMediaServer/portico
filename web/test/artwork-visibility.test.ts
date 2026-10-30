import test from 'node:test';
import assert from 'node:assert/strict';
import {observeVisibleArtwork} from '../src/kit/artwork/visible-artwork.ts';

function host() {
  let now = 0;
  const observers: Observer[] = [];
  class Observer {
    disconnected = false;
    constructor(readonly callback: IntersectionObserverCallback) {observers.push(this);}
    observe(_element: Element) {}
    unobserve(_element: Element) {}
    disconnect() {this.disconnected = true;}
    emit(changes: {target: Element; time: number; isIntersecting: boolean; intersectionRatio: number}[]) {
      this.callback(changes as IntersectionObserverEntry[], this as unknown as IntersectionObserver);
    }
  }
  const document = {defaultView: {IntersectionObserver: Observer, performance: {now: () => now}}} as unknown as Document;
  return {element: () => ({ownerDocument: document}) as Element, observers, time: (at: number) => {now = at;}};
}
const change = (target: Element, visible: boolean, time = 0) => ({target, time, isIntersecting: visible, intersectionRatio: visible ? 1 : 0});

test('a visibility batch registers every incoming predecessor before retirements and ready offers', () => {
  const dom = host(), a = dom.element(), b = dom.element(), seen: string[] = [];
  const stopA = observeVisibleArtwork(a, {change: value => seen.push(`a:${value}`), flush: () => seen.push('a:offer')});
  const stopB = observeVisibleArtwork(b, {change: value => seen.push(`b:${value}`), flush: () => seen.push('b:offer')});
  assert.equal(dom.observers.length, 1);
  // A stale intermediate B exit must not release ready siblings before B enters.
  dom.observers[0].emit([change(a, false), change(b, false), change(b, true)]);
  assert.deepEqual(seen, ['b:true', 'a:false', 'b:offer', 'a:offer']);
  stopA(); stopB(); assert.equal(dom.observers[0].disconnected, true);
});

test('queued old observations cannot lend visibility to a replacement original registration', () => {
  const dom = host(), a = dom.element(), b = dom.element(), seen: string[] = [];
  const old = observeVisibleArtwork(a, {change: value => seen.push(`old:${value}`), flush: () => seen.push('old:offer')});
  const keep = observeVisibleArtwork(b, {change() {}, flush() {}});
  dom.time(10); old();
  assert.deepEqual(seen, ['old:false'], 'withdrawal is synchronous');
  const fresh = observeVisibleArtwork(a, {change: value => seen.push(`fresh:${value}`), flush: () => seen.push('fresh:offer')});
  dom.observers[0].emit([change(a, true, 5)]);
  assert.deepEqual(seen, ['old:false']);
  dom.observers[0].emit([change(a, true, 11)]);
  assert.deepEqual(seen, ['old:false', 'fresh:true', 'fresh:offer']);
  fresh(); keep(); dom.observers[0].emit([change(a, true, 12)]);
  assert.equal(seen.at(-1), 'fresh:false'); assert.equal(dom.observers[0].disconnected, true);
});

test('a browser without an observer reports absence of a visibility witness explicitly', () => {
  const element = {ownerDocument: {defaultView: {}}} as Element, seen: unknown[] = [];
  const stop = observeVisibleArtwork(element, {change: value => seen.push(value), flush: () => seen.push('offer')});
  assert.deepEqual(seen, [undefined, 'offer']); stop(); assert.equal(seen.at(-1), false);
});
