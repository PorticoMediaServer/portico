import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from '../helpers/component-harness.mjs';
import {createRenderer, tree, one} from '../helpers/interaction-harness.mjs';
import * as routeSearch from '../../src/screens/read/route-search.ts';

// Actual ReadHost callbacks and hook ownership with mocked existing ports. This
// harness models commits and wiring, not React scheduling or session disposal.
// The protected-context mock deliberately allows command-time isCurrent() false;
// the real hook would hide the host on a subsequent render.
function installHistory(t) {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'window');
  Object.defineProperty(globalThis, 'window', {value: {history: {length: 2}}, configurable: true});
  t.after(() => {if (previous) Object.defineProperty(globalThis, 'window', previous); else Reflect.deleteProperty(globalThis, 'window');});
}
async function fixture({strict = false} = {}) {
  const h = createRenderer(), calls = [], errors = [], replayers = [];
  let item = 'book', source = 'source', match = true, allowed = true, onCurrent, onNavigate, onReport;
  let context = {isCurrent: () => {onCurrent?.(); return allowed;}};
  let binding = {home: {effects: {reportError: error => {errors.push(error); onReport?.(error);}}}};
  const makeLocation = (path = '/read/book', search = {sourceId: source}) => ({pathname: path, search});
  let location = makeLocation();
  const router = {state: {location}, history: {location: {state: {__TSR_index: 0}}, back: () => calls.push({back: true})}, navigate: target => {calls.push(target); return onNavigate ? onNavigate() : Promise.resolve();}};
  const react = {...h.react};
  if (strict) react.useLayoutEffect = (effect, deps) => h.react.useLayoutEffect(() => {let cleanup = effect(); cleanup?.(); cleanup = effect(); replayers.push(() => {cleanup?.(); cleanup = effect();}); return () => cleanup?.();}, deps);
  const component = await componentModule(new URL('../../src/screens/read/ReadHost.tsx', import.meta.url), {
    react, '@tanstack/react-router': {useLocation: () => location, useMatch: () => match ? {params: {itemId: item}, search: {sourceId: source}} : undefined},
    '../../route-registry-view': {useProtectedWebScreenContext: () => ({context, binding})}, '../../router': {router}, './ReadEntry': {ReadEntry: 'ReadEntry'}, './route-search': routeSearch,
  });
  const render = () => tree(h.render(() => component.ReadHost()));
  const settle = () => {let view; for (let i = 0; i < 4; i++) view = render(); return view;};
  const close = () => one(settle(), 'ReadEntry').props.onClose;
  const change = (nextItem = item, nextSource = source, path = `/read/${encodeURIComponent(nextItem)}`, visible = true) => {
    item = nextItem; source = nextSource; match = visible; location = makeLocation(path, {sourceId: source}); router.state.location = location; settle();
  };
  return {calls, errors, close, settle, change, replay: () => {for (const replay of replayers) replay(); settle();}, sameHref: () => change(), settings: () => change(item, source, '/settings', false),
    replaceContext: () => {context = {isCurrent: () => allowed}; binding = {home: {effects: {reportError: error => {errors.push(error); onReport?.(error);}}}}; settle();},
    setCurrent: fn => {onCurrent = fn;}, setReport: fn => {onReport = fn;}, setNavigate: fn => {onNavigate = fn;}, setAllowed: value => {allowed = value;}};
}

test('Reader closes to the captured book with replace, including Settings detours', async t => {
  installHistory(t); const f = await fixture(); const close = f.close(); close(); close();
  assert.deepEqual(f.calls, [{to: '/item/$itemId', params: {itemId: 'book'}, replace: true}]);
  const detail = await fixture(); detail.close()(); assert.equal(detail.calls[0].params.itemId, 'book');
  const detour = await fixture(); const before = detour.close(); detour.settings(); before(); assert.equal(detour.calls.length, 0);
  detour.change(); detour.close()(); assert.deepEqual(detour.calls, [{to: '/item/$itemId', params: {itemId: 'book'}, replace: true}]);
});

test('old callbacks refuse item, source, scope, hidden and same-href entry replacement', async t => {
  installHistory(t); for (const replace of [f => f.change('other'), f => f.change('book', 'other-source'), f => f.replaceContext(), f => f.settings(), f => f.sameHref()]) {
    const f = await fixture(); const old = f.close(); replace(f); old(); assert.equal(f.calls.length, 0);
    if (!f.settle().find(node => node.type === 'ReadEntry')?.props.active) f.change();
    f.close()(); assert.equal(f.calls.length, 1);
  }
});

test('command-time original context refusal does not navigate', async t => {
  installHistory(t); const f = await fixture(), close = f.close();
  f.setAllowed(false); close(); assert.deepEqual(f.calls, []);
  f.setAllowed(true); close(); assert.equal(f.calls.length, 1);
});

test('reentrant context and navigation cannot spend or clear a successor', async t => {
  installHistory(t); const f = await fixture(); const old = f.close();
  f.setCurrent(() => {f.setCurrent(undefined); f.change('new-book');}); old(); assert.equal(f.calls.length, 0);
  const current = f.close(); f.setNavigate(() => {f.setNavigate(undefined); f.change('successor');}); current();
  assert.equal(f.calls.length, 1); assert.equal(one(f.settle(), 'ReadEntry').props.itemId, 'successor');
  f.close()(); assert.equal(f.calls.length, 2); assert.equal(f.calls[1].params.itemId, 'successor');
});

test('layout replay retires the prior receipt and a fresh callback remains usable', async t => {
  installHistory(t); const f = await fixture({strict: true}); const old = f.close(); f.replay(); old(); assert.equal(f.calls.length, 0);
  const fresh = f.close(); fresh(); fresh(); assert.equal(f.calls.length, 1); assert.equal(f.calls[0].replace, true);
});


test('same matched item on the wrong pathname is refused; encoded Reader path is accepted', async t => {
  installHistory(t); const f = await fixture(); const old = f.close();
  f.change('book', 'source', '/not-the-reader', true); old(); f.close()(); assert.deepEqual(f.calls, []);
  f.change('book space/é', 'source'); f.close()();
  assert.deepEqual(f.calls, [{to: '/item/$itemId', params: {itemId: 'book space/é'}, replace: true}]);
});


function deferred() {
  let resolve, reject;
  const promise = new Promise((accept, fail) => {resolve = accept; reject = fail;});
  return {promise, resolve, reject};
}

test('rejection, no-commit resolution and synchronous throw keep the Reader and allow explicit retry', async t => {
  installHistory(t); const f = await fixture(), pending = deferred(); f.setNavigate(() => pending.promise);
  const close = f.close(); close(); close(); assert.equal(f.calls.length, 1);
  assert.equal(one(f.settle(), 'ReadEntry').props.itemId, 'book');
  const failure = new Error('refused'); pending.reject(failure); await Promise.resolve();
  assert.deepEqual(f.errors, [failure]); assert.equal(one(f.settle(), 'ReadEntry').props.itemId, 'book');
  const noCommit = deferred(); f.setNavigate(() => noCommit.promise); f.close()(); assert.equal(f.calls.length, 2);
  noCommit.resolve(); await Promise.resolve(); assert.equal(one(f.settle(), 'ReadEntry').props.itemId, 'book');
  f.setNavigate(() => {throw new Error('sync');}); f.close()(); assert.equal(f.calls.length, 3); assert.equal(f.errors.length, 2);
  f.setNavigate(undefined); f.close()(); assert.equal(f.calls.length, 4);
});

test('actual committed leave retires retained book; old rejection cannot alter a pending successor', async t => {
  installHistory(t); const f = await fixture(), oldPending = deferred(); f.setNavigate(() => oldPending.promise); f.close()();
  f.change('successor'); const next = deferred(); f.setNavigate(() => next.promise); const nextClose = f.close(); nextClose();
  oldPending.reject(new Error('old')); await Promise.resolve(); assert.deepEqual(f.errors, []);
  nextClose(); assert.equal(f.calls.length, 2, 'old settlement cannot unclaim successor');
  assert.equal(one(f.settle(), 'ReadEntry').props.itemId, 'successor');
  f.change('successor', 'source', '/item/successor', false); assert.equal(f.settle().some(node => node.type === 'ReadEntry'), false);
  next.resolve(); await Promise.resolve(); assert.equal(f.settle().some(node => node.type === 'ReadEntry'), false);
});


test('error sink reentry starts a new exact attempt that an older duplicate cannot release', async t => {
  installHistory(t); const f = await fixture(), retry = deferred(), failure = new Error('first');
  const hostile = {then(resolve, reject) {reject(failure); reject(new Error('duplicate')); resolve();}};
  f.setNavigate(() => hostile);
  f.setReport(() => {f.setReport(undefined); f.setNavigate(() => retry.promise); f.close()();});
  f.close()(); await Promise.resolve(); await Promise.resolve();
  assert.deepEqual(f.errors, [failure]); assert.equal(f.calls.length, 2);
  f.close()(); assert.equal(f.calls.length, 2, 'new retry stays claimed after old duplicate settlement');
  retry.resolve(); await Promise.resolve(); f.setNavigate(undefined); f.close()(); assert.equal(f.calls.length, 3);
});
