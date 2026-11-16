import assert from 'node:assert/strict';
import {test} from 'node:test';
import {componentModule} from '../helpers/component-harness.mjs';
import {createRenderer, one, tree, tick} from '../helpers/interaction-harness.mjs';
import {bindReadingProgress} from '../../src/screens/read/progress.ts';

// Real screen and writer; the route port commits only after its async blocker allows it.
async function fixture(t) {
  const h = createRenderer(), listeners = new Set(), saves = [], pending = [];
  let allowed = true, blocker, retired = false;
  let snapshot = {status: 'ready', sourceId: 'src', sourceRevision: 'rev', location: {format: 'pdf', page: 0, progression: 0}};
  const controller = {getSnapshot: () => snapshot, subscribe: fn => {listeners.add(fn);return () => listeners.delete(fn);}};
  const authority = {isCurrent: () => allowed && !retired, subscribe: () => () => {}};
  const saved = {progress: {revision: 1, stale: false, location: {format: 'pdf', pageIndex: 0, progression: 0}}};
  const session = {manifest: {sessionId: 'read', sourceId: 'src', revision: 'rev', metadata: {}}, signal: new AbortController().signal,
    progress: async () => saved, saveProgress: (request, signal) => {saves.push(request);return new Promise((resolve, reject) => {
      const abort = () => reject(signal.reason);signal.addEventListener('abort', abort, {once: true});
      pending.push(() => {signal.removeEventListener('abort', abort);resolve({saved: true, progress: {revision: saves.length + 1}});});
    });}};
  const location = {pathname: '/read/book', search: {}, href: '/read/book', state: {__TSR_key: 'reader', __TSR_index: 1}};
  const context = {isCurrent: () => allowed, session: () => ({viewer: {}, sessionFamilyId: 'family'})};
  const navigate = async path => {
    const blocked = await blocker?.shouldBlockFn({next: {pathname: path}});
    if (!blocked) {retired = path !== '/settings';router.state.location = {...location, pathname: path};}
  };
  const router = {state: {location}, subscribe: () => () => {}, history: {back: () => navigate('/item/book')}, navigate: () => navigate('/item/book')};
  const module = await componentModule(new URL('../../src/screens/read/ReadEntry.tsx', import.meta.url), {
    react: h.react, '@tanstack/react-router': {useLocation: () => router.state.location, useBlocker: options => {blocker = options;}},
    '../../router': {router}, '../../shell/fact-source': {forwardFacts: (original, extra) => ({...original, ...extra})},
    '../browse/library-route-guard': {useLibraryRouteGuard: () => undefined, useGuardedLibraryContext: value => value},
    '../search/reading-open-handoff': {indexedReadingOpeningVersion: () => 0, receiveIndexedReadingTarget: () => ({kind: 'ordinary'})},
    './route-search': {readSearch: () => ({})}, './contents-handoff': {takeContentsTarget: () => undefined, retireContentsTarget: () => {}},
    './bindings': {createReadBindings: () => ({authority, client: {versions: async () => ({chosenSourceId: 'src', versions: [{sourceId: 'src', inventoryAvailable: true}]})}, open: async () => session, dispose: async () => {}})},
    '@portico/reader-core': {createPdfAdapter: () => ({}), createReadingPublication: async () => ({format: 'pdf', resumeLocation: () => undefined, dispose: () => {}})},
    './preferences': {bindReadPreferences: () => ({controller, dispose: async () => {}})}, './progress': {bindReadingProgress},
    './ReaderBook': {ReaderBook: 'ReaderBook', readerNear: () => undefined},
  });
  const render = () => tree(h.render(() => module.ReadEntry({captured: {context, binding: {home: {effects: {reportError: () => {}}}}}, itemId: 'book'})));
  t.after(() => h.dispose());
  render(); render(); await tick(); const book = one(render(), 'ReaderBook');
  return {book, saves, navigate, finishSave: () => pending.shift()(), retireViewer: () => {allowed = false;},
    current: () => router.state.location.pathname, move: page => {snapshot = {...snapshot, location: {format: 'pdf', page, progression: page / 10}};for (const fn of listeners) fn();}};
}

test('reader Back saves the final page before committing route departure', async t => {
  const f = await fixture(t); f.move(3); f.book.props.onBack();
  assert.equal(f.saves.length, 1); assert.equal(f.saves[0].location.pageIndex, 3);
  assert.equal(f.current(), '/read/book'); f.finishSave(); await tick();
  assert.equal(f.current(), '/item/book');
});

test('other departures wait for the writer, and Settings leaves it accepting later pages', async t => {
  const f = await fixture(t); f.move(2); const settings = f.navigate('/settings');
  assert.equal(f.current(), '/read/book'); f.finishSave(); await settings;
  f.move(4); const departure = f.navigate('/home');
  assert.equal(f.saves.at(-1).location.pageIndex, 4); assert.equal(f.current(), '/settings');
  f.finishSave(); await departure; assert.equal(f.current(), '/home');
});

test('a retired viewer never sends the queued final page', async t => {
  const f = await fixture(t); f.move(5); f.retireViewer(); await f.navigate('/profiles');
  assert.deepEqual(f.saves, []); assert.equal(f.current(), '/profiles');
});


test('an unresponsive save cannot trap route navigation', async t => {
  const f = await fixture(t); t.mock.timers.enable({apis: ['setTimeout']});
  f.move(3); const departure = f.navigate('/home');
  assert.equal(f.current(), '/read/book');t.mock.timers.tick(2000);
  await departure;assert.equal(f.current(), '/home');
});
