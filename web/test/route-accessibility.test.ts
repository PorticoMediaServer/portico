import test from 'node:test';
import assert from 'node:assert/strict';
import {announceRoute, routeDocumentTitle, routeHeading, routeLabelKey, routeTitleUpdates} from '../src/shell/route-accessibility.ts';

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
