import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {announceRoute, mutationMayAffectHeading, observeRouteHeading, routeDocumentTitle, routeHeading, routeLabelKey, routeTitleUpdates} from '../src/shell/route-accessibility.ts';

test('representative destinations use named routes without copying resource ids or query text', () => {
  assert.equal(routeLabelKey('/'), 'next.home.shell.home');
  assert.equal(routeLabelKey('/search'), 'next.home.shell.search');
  assert.equal(routeLabelKey('/search/reading'), 'next.home.shell.search');
  assert.equal(routeLabelKey('/library/opaque-id'), 'next.home.shell.libraries');
  assert.equal(routeLabelKey('/item/opaque-id'), 'next.controls.details');
  assert.equal(routeLabelKey('/settings'), 'next.shell.settings');
  assert.equal(routeLabelKey('/unknown/opaque-id'), 'next.platform.appName');
  assert.equal(routeDocumentTitle('Arrival', 'Portico'), 'Arrival — Portico');
  assert.equal(routeDocumentTitle('Portico', 'Portico'), 'Portico');
});

test('route title follows the visible destination heading and never the Home spotlight or modal', () => {
  const heading = (text: string, excluded = false, visible = true) => ({textContent: text, closest: () => excluded ? {} : null,
    getClientRects: () => visible ? [{}] : [], getAttribute: () => null, querySelector: () => null});
  const main = {querySelectorAll: () => [heading('Hidden title', false, false), heading('Modal', true), heading('  A long\nbook title  ')]} as unknown as HTMLElement;
  assert.equal(routeHeading(main, '/item/id'), 'A long book title');
  assert.equal(routeHeading(main, '/'), undefined);
});

function focusFixture(options: {covered?: boolean; connected?: boolean; modal?: boolean; destinationOwnsFocus?: boolean} = {}) {
  const calls: unknown[] = [], announcements: string[] = [];
  const active = {closest: () => options.modal ? {} : null};
  const document = {body: {}, activeElement: active};
  const main = {isConnected: options.connected !== false, closest: () => options.covered ? {} : null, ownerDocument: document,
    contains: () => options.destinationOwnsFocus === true, focus: (value: unknown) => calls.push(value)} as unknown as HTMLElement;
  return {main, calls, announcements, announce: (label: string) => announcements.push(label)};
}

test('accepted navigation hands focus to main without scrolling the restored entry', () => {
  const f = focusFixture();
  assert.equal(announceRoute(f.main, 'Settings', f.announce), true);
  assert.deepEqual(f.calls, [{preventScroll: true}]);
  assert.deepEqual(f.announcements, ['Settings']);
});

test('a covered, retired or modal-owned route never steals focus or announces underneath', () => {
  for (const options of [{covered: true}, {connected: false}, {modal: true}]) {
    const f = focusFixture(options);
    assert.equal(announceRoute(f.main, 'Hidden route', f.announce), false);
    assert.deepEqual(f.calls, []);
    assert.deepEqual(f.announcements, []);
  }
});

test('a sign-in field already focused by the destination retains its editing context', () => {
  const f = focusFixture({destinationOwnsFocus: true});
  assert.equal(announceRoute(f.main, 'Sign in', f.announce), true);
  assert.deepEqual(f.calls, []);
  assert.deepEqual(f.announcements, ['Sign in']);
});

test('title mutations coalesce per frame, skip covered routes and cancel on cleanup', () => {
  let covered = false, computations = 0, nextId = 0;
  const frames = new Map<number, FrameRequestCallback>();
  const main = {closest: () => covered ? {} : null} as unknown as HTMLElement;
  const titles = routeTitleUpdates(main, () => computations++, callback => {frames.set(++nextId, callback); return nextId;}, id => {frames.delete(id);});
  const flush = () => {const pending = [...frames.values()]; frames.clear(); for (const callback of pending) callback(0);};
  for (let mutation = 0; mutation < 100; mutation++) titles.schedule();
  assert.equal(frames.size, 1);
  flush(); assert.equal(computations, 1);
  titles.schedule(); covered = true; flush(); assert.equal(computations, 1);
  titles.schedule(); assert.equal(frames.size, 0);
  covered = false; titles.schedule(); flush(); assert.equal(computations, 2);
  titles.schedule(); titles.dispose(); assert.equal(frames.size, 0);
  flush(); assert.equal(computations, 2);
});

test('observeRouteHeading skips Home, ignores non-heading mutations, and narrows after a fresh heading settles', () => {
  type ObsRecord = {target: unknown; options: MutationObserverInit; disconnected: boolean; cb: MutationCallback};
  const created: ObsRecord[] = [];
  class FakeObserver {
    record?: ObsRecord;
    constructor(private readonly cb: MutationCallback) {}
    observe(target: Node, options?: MutationObserverInit) {
      this.record = {target, options: options ?? {}, disconnected: false, cb: this.cb};
      created.push(this.record);
    }
    disconnect() { if (this.record) this.record.disconnected = true; }
    takeRecords() { return []; }
  }
  const makeHeading = (text: string) => ({
    nodeType: 1, textContent: text, closest: () => null, getClientRects: () => [{}],
    getAttribute: () => null, querySelector: () => null, matches: (sel: string) => sel.includes('h1'),
    contains: () => false,
  }) as unknown as HTMLElement;

  let currentHeading: HTMLElement | undefined;
  const main = {querySelectorAll: () => currentHeading ? [currentHeading] : []} as unknown as HTMLElement;

  // Home attaches zero observers.
  const homeObs = observeRouteHeading(main, '/', () => {}, {Observer: FakeObserver as unknown as typeof MutationObserver});
  assert.equal(created.length, 0);
  homeObs.dispose();

  // Sibling transition: /item/A ("The Matrix") -> /item/B still showing stale "The Matrix".
  let changes = 0;
  const stale = makeHeading('The Matrix');
  currentHeading = stale;
  const itemObs = observeRouteHeading(main, '/item/B', () => { changes++; }, {previousHeading: 'The Matrix', Observer: FakeObserver as unknown as typeof MutationObserver});
  // Watches both the stale heading element (characterData/childList) and main (childList only, never characterData).
  const mainObs = created.find(r => r.target === main);
  const headingObs = created.find(r => r.target === stale);
  assert.ok(mainObs && headingObs);
  assert.equal(mainObs.options.childList, true);
  assert.equal(mainObs.options.characterData, undefined);
  assert.equal(mainObs.disconnected, false);

  // Unrelated card mutation on <main> does not trigger a title check.
  const unrelatedCard = {nodeType: 1, matches: () => false, querySelector: () => null, contains: () => false};
  assert.equal(mutationMayAffectHeading([{type: 'childList', target: {nodeType: 1, closest: () => null}, addedNodes: [unrelatedCard], removedNodes: []} as unknown as MutationRecord], stale), false);
  mainObs.cb([{type: 'childList', target: {nodeType: 1, closest: () => null}, addedNodes: [unrelatedCard], removedNodes: []} as unknown as MutationRecord], {} as MutationObserver);
  assert.equal(changes, 0);

  // New heading arrives on <main>: triggers callback, then reconcile disconnects <main> observer and narrows to new <h1>.
  const fresh = makeHeading('Inception');
  currentHeading = fresh;
  mainObs.cb([{type: 'childList', target: {nodeType: 1, closest: () => null}, addedNodes: [fresh], removedNodes: []} as unknown as MutationRecord], {} as MutationObserver);
  assert.equal(changes, 1);
  itemObs.reconcile();
  assert.equal(mainObs.disconnected, true);
  assert.equal(headingObs.disconnected, true);
  const narrowed = created.find(r => r.target === fresh && !r.disconnected);
  assert.ok(narrowed);
  itemObs.dispose();
  assert.equal(narrowed.disconnected, true);
});

test('Root uses a keyed child inside its persistent live region and re-announces resolved headings', () => {
  const rootSrc = readFileSync(new URL('../src/shell/Root.tsx', import.meta.url), 'utf8');
  assert.match(rootSrc, /<span key=\{announcement\.seq\}>\{announcement\.text\}<\/span>/);
  assert.match(rootSrc, /observeRouteHeading\(main, location\.pathname, title, \{previousHeading\}\)/);
  assert.doesNotMatch(rootSrc, /new MutationObserver\(/);
});
