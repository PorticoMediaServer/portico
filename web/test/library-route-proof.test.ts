import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {encodedRoutePath} from '../../packages/contracts/src/routes.ts';
import {parseLibraryList} from '@core/library-list.ts';
import {createLibraryRouteProof, createLibraryRouteGuard, completeLibraryDirectory} from '../src/shell/library-route-proof.ts';
import {forwardFacts} from '../src/shell/fact-source.ts';
import {readTitleIdentity} from '../src/platform/title-identity.ts';
import {screenLifetime} from '../src/screens/detail/context.ts';
import {createReadBindings} from '../src/screens/read/bindings.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer} from './helpers/interaction-harness.mjs';
import type {ShellAssemblyContext} from '../src/shell/bootstrap.ts';

const viewer = {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local' as const, role: 'owner'};
const rows = (ids: string[]) => ({items: ids.map(id => ({id, name: id, kind: 'book'}))});
const turn = async () => {for (let i = 0; i < 30; i++) await Promise.resolve();};
function deferred<T>() {let resolve!: (value: T) => void, reject!: (error: unknown) => void;const promise = new Promise<T>((r, e) => {resolve = r;reject = e;});return {promise, resolve, reject};}
function fixture() {
  let alive = true, directoryVersion = 0, homeVersion = 0, requests = 0, session = {sessionFamilyId: 'family', deviceId: 'device', viewer};
  const proof = createLibraryRouteProof({session: () => session, isCurrent: () => alive, directoryVersion: () => directoryVersion, homeVersion: () => homeVersion, request: () => {requests++;}});
  const accepted = (fence = 'fence') => {const ticket = proof.beginHome();proof.observeHome({serverId: 'server', viewerFence: fence});return proof.homeAnswered(ticket);};
  const directory = (raw: unknown) => {directoryVersion++;homeVersion++;const ticket = proof.beginDirectory();return proof.directory(ticket, raw);};
  const confirm = (ids: string[]) => {assert.ok(directory(rows(ids)));assert.equal(accepted(), true);assert.ok(proof.source.getSnapshot());};
  return {proof, accepted, directory, confirm, session: () => session, current: () => alive, requests: () => requests,
    dirty(value: boolean) {if (value) {directoryVersion++;homeVersion++;}}, retire() {alive = false;}, replaceScope() {session = {...session, sessionFamilyId: 'other'};}};
}

test('only a complete bounded raw directory can prove membership; tolerant row drop is display only', () => {
  const invalid = [{items: [null]}, {items: [{id: 'target', kind: 'book'}]}, {items: [{id: 'other', name: 'Other'}]},
    {items: [rows(['a']).items[0], rows(['a']).items[0]]}, {items: [], hasMore: true}, {items: [], nextCursor: 'more'}, rows(Array.from({length: 2049}, (_, i) => 'id' + i)), rows(Array.from({length: 700}, (_, i) => '界'.repeat(250) + i))];
  for (const raw of invalid) assert.equal(completeLibraryDirectory(raw), undefined);
  assert.deepEqual(completeLibraryDirectory(rows([])), []);assert.deepEqual(completeLibraryDirectory(rows(['target'])), ['target']);
  const raw = {items: [null, ...rows(['other']).items]};assert.deepEqual(parseLibraryList(raw).items.map(row => row.id), ['other']);assert.equal(completeLibraryDirectory(raw), undefined);
});

test('one accepted current Home manifest pairs with the directory; no additional Home read is requested', () => {
  const f = fixture();assert.ok(f.directory(rows(['other'])));
  f.proof.observeHome({serverId: 'server', viewerFence: 'fence'});assert.equal(f.proof.source.getSnapshot(), undefined);
  assert.equal(f.accepted(), true);assert.deepEqual(f.proof.source.getSnapshot()?.libraryIds, ['other']);f.proof.dispose();
});

test('Home may finish before directory: one accepted manifest still publishes exactly once when the matching raw body arrives', () => {
  const f = fixture(), navigations: unknown[] = [], guard = createLibraryRouteGuard({source: f.proof.source, opening: {}, isCurrent: f.current, canReplace: f.current,
    libraryId: 'target', replace: value => {navigations.push(value);}, reportError: error => {throw error;}});
  guard.start();const directory = f.proof.beginDirectory();
  assert.equal(f.accepted(), false);assert.equal(f.proof.source.getSnapshot(), undefined);
  assert.ok(f.proof.directory(directory, rows(['other'])));assert.deepEqual(navigations, [{to: '/libraries', replace: true}]);
  assert.equal(f.requests(), 1);guard.dispose();f.proof.dispose();
});

test('failed/malformed directory or Home, wrong requested server and superseded observations cannot redirect', () => {
  for (const scenario of ['directory-failed', 'row-drop', 'home-failed', 'home-no-observation', 'wrong-server'] as const) {
    const f = fixture(), navigations: unknown[] = [], guard = createLibraryRouteGuard({source: f.proof.source, opening: {}, isCurrent: f.current, canReplace: f.current,
      libraryId: 'target', replace: value => {navigations.push(value);}, reportError: error => {throw error;}});
    guard.start();const ticket = f.proof.beginDirectory();
    if (scenario === 'row-drop') {assert.equal(f.proof.directory(ticket, {items: [null]}), false);f.accepted();f.accepted();}
    else if (scenario !== 'directory-failed') {
      assert.ok(f.proof.directory(ticket, rows([])));const home = f.proof.beginHome();
      if (scenario === 'wrong-server') f.proof.observeHome({serverId: 'other-server', viewerFence: 'fence'});
      if (scenario !== 'home-failed') assert.equal(f.proof.homeAnswered(home), false);
    }
    assert.deepEqual(navigations, []);assert.ok(guard.isCurrent());assert.equal(guard.signal.aborted, false);guard.dispose();f.proof.dispose();
  }
});

test('a newer directory or newer owed hint defeats an in-flight absence candidate', () => {
  const f = fixture(), navigations: unknown[] = [], guard = createLibraryRouteGuard({source: f.proof.source, opening: {}, isCurrent: f.current, canReplace: f.current,
    libraryId: 'target', replace: value => {navigations.push(value);}, reportError: error => {throw error;}});
  guard.start();const old = f.proof.beginDirectory(), next = f.proof.beginDirectory();
  assert.equal(f.proof.directory(old, rows([])), false);assert.ok(f.proof.directory(next, rows(['target'])));f.accepted();f.accepted();assert.ok(guard.isCurrent());
  f.directory(rows([]));const oldHome = f.proof.beginHome();f.dirty(true);f.proof.observeHome({serverId: 'server', viewerFence: 'fence'});assert.equal(f.proof.homeAnswered(oldHome), false);
  assert.equal(f.proof.source.getSnapshot(), undefined);assert.deepEqual(navigations, []);assert.ok(guard.isCurrent());f.confirm(['target']);assert.deepEqual(navigations, []);guard.dispose();f.proof.dispose();
});

test('confirmed absence synchronously retires every original route action before one replace navigation', () => {
  const f = fixture(), opening = {}, order: string[] = [], guards: ReturnType<typeof createLibraryRouteGuard>[] = [], navigations: unknown[] = [];
  for (let i = 0; i < 2; i++) {
    const guard = createLibraryRouteGuard({source: f.proof.source, opening, isCurrent: f.current, canReplace: f.current, libraryId: 'target',
      replace: value => {assert.ok(guards.every(guard => !guard.isCurrent() && guard.signal.aborted));order.push('navigate');navigations.push(value);}, reportError: error => {throw error;}});
    guard.signal.addEventListener('abort', () => {order.push('retire' + i);});guards.push(guard);guard.start();
  }
  f.confirm(['other']);assert.deepEqual(order, ['retire0', 'retire1', 'navigate']);assert.deepEqual(navigations, [{to: '/libraries', replace: true}]);
  f.proof.source.reconcile();f.confirm([]);assert.equal(navigations.length, 1);for (const guard of guards) guard.dispose();f.proof.dispose();
});

test('empty authoritative directory uses the existing Home empty route, with no duplicate navigation', () => {
  const f = fixture(), navigations: unknown[] = [], guard = createLibraryRouteGuard({source: f.proof.source, opening: {}, isCurrent: f.current, canReplace: f.current,
    libraryId: 'target', replace: value => {assert.equal(guard.isCurrent(), false);navigations.push(value);}, reportError: error => {throw error;}});
  guard.start();f.confirm([]);assert.deepEqual(navigations, [{to: '/', replace: true}]);f.proof.source.reconcile();assert.equal(navigations.length, 1);guard.dispose();f.proof.dispose();
});

test('Home-fence retirement runs first; the new admitted context can later prove absence and redirect', () => {
  const old = fixture(), order: string[] = [], guard = createLibraryRouteGuard({source: old.proof.source, opening: {}, isCurrent: old.current, canReplace: old.current,
    libraryId: 'target', replace: () => {order.push('old-navigate');}, reportError: error => {throw error;}});
  guard.start();old.confirm(['target']);old.directory(rows([]));const pending = old.proof.beginHome();
  order.push('runtime-retire');old.retire();assert.equal(old.proof.homeAnswered(pending), false);assert.equal(guard.isCurrent(), false);assert.deepEqual(order, ['runtime-retire']);
  const fresh = fixture(), next = createLibraryRouteGuard({source: fresh.proof.source, opening: {}, isCurrent: fresh.current, canReplace: fresh.current, libraryId: 'target',
    replace: value => {assert.equal(guard.isCurrent(), false);order.push('new-navigate');assert.equal(value.replace, true);}, reportError: error => {throw error;}});
  next.start();fresh.confirm([]);assert.deepEqual(order, ['runtime-retire', 'new-navigate']);guard.dispose();next.dispose();old.proof.dispose();fresh.proof.dispose();
});

// H-11: a library added or removed changes only the viewer fence; the shell (and this proof) stays.
test('a viewer-fence change re-proves under the new fence: kept libraries stay open, a removed one redirects once', async () => {
  const f = fixture(), order: string[] = [];
  const guard = (libraryId: string) => createLibraryRouteGuard({source: f.proof.source, opening: {}, isCurrent: f.current, canReplace: f.current,
    libraryId, replace: () => {order.push(libraryId);}, reportError: error => {throw error;}});
  const kept = guard('target'), removed = guard('music');kept.start();removed.start();
  assert.ok(f.directory(rows(['target', 'music'])));assert.equal(f.accepted('before'), true);
  const requests = f.requests();
  f.proof.observeHome({serverId: 'server', viewerFence: 'after'});
  assert.equal(f.proof.source.getSnapshot(), undefined);await turn();
  assert.equal(f.requests(), requests + 1, 'the new fence asks for the directory again');
  assert.ok(f.directory(rows(['target'])));assert.equal(f.accepted('after'), true);
  assert.deepEqual(f.proof.source.getSnapshot()?.libraryIds, ['target']);assert.deepEqual(order, ['music']);
  assert.equal(f.accepted('after'), true, 'the adopted fence never invalidates again');assert.deepEqual(order, ['music']);
  kept.dispose();removed.dispose();f.proof.dispose();
});

test('scope change or retirement reentry preempts replacement and cannot revive old callbacks', () => {
  for (const scenario of ['family', 'retirement-reentry'] as const) {
    const f = fixture(), navigations: unknown[] = [], guard = createLibraryRouteGuard({source: f.proof.source, opening: {}, isCurrent: f.current, canReplace: f.current,
      libraryId: 'target', replace: value => {navigations.push(value);}, reportError: error => {throw error;}});
    guard.start();
    if (scenario === 'family') {f.directory(rows([]));const ticket = f.proof.beginHome();f.replaceScope();f.proof.observeHome({serverId: 'server', viewerFence: 'fence'});f.proof.homeAnswered(ticket);}
    else {guard.signal.addEventListener('abort', () => f.retire());f.directory(rows([]));f.accepted();f.accepted();}
    assert.deepEqual(navigations, []);f.retire();assert.equal(guard.isCurrent(), false);guard.dispose();f.proof.dispose();
  }
});

test('item/Reading removal requires a successful current parent document; failed and old reads remain inert', async () => {
  const f = fixture(), reads: {signal: AbortSignal; done: ReturnType<typeof deferred<string>>}[] = [], navigations: unknown[] = [];
  const guard = createLibraryRouteGuard({source: f.proof.source, opening: {}, isCurrent: f.current, canReplace: f.current,
    readParent: signal => {const done = deferred<string>();reads.push({signal, done});return done.promise;}, replace: value => {navigations.push(value);}, reportError: error => {throw error;}});
  guard.start();f.confirm(['other']);await turn();assert.equal(reads.length, 1);assert.ok(guard.isCurrent());
  const error = new Error('parent-unreadable');reads[0]!.done.reject(error);await turn();assert.strictEqual(guard.getFailure(), error);assert.deepEqual(navigations, []);
  f.confirm(['other']);await turn();const obsolete = reads[1]!;f.confirm(['target']);await turn();assert.equal(obsolete.signal.aborted, true);obsolete.done.resolve('target');await turn();assert.deepEqual(navigations, []);
  reads[2]!.done.resolve('target');await turn();assert.ok(guard.isCurrent());assert.equal(guard.getFailure(), undefined);
  f.confirm(['other']);await turn();reads[3]!.done.resolve('target');await turn();assert.equal(guard.isCurrent(), false);assert.deepEqual(navigations, [{to: '/libraries', replace: true}]);guard.dispose();f.proof.dispose();
});

async function mounted(target: {libraryId?: string; itemId?: string}) {
  const f = fixture(), h = createRenderer(), opening = {}, navigations: unknown[] = [], errors: unknown[] = [];
  let location: object = opening, parentRaw: unknown = {id: 'item', libraryId: 'target', title: 'Book', kind: 'book', available: true, durationMs: 0, progressMs: 0};
  let parentCalls = 0, parentHold: ReturnType<typeof deferred<unknown>> | undefined;
  const api = new HttpLocalApi('https://unit.invalid', '', async (input: RequestInfo | URL) => {parentCalls++;const reply = parentHold ? await parentHold.promise : parentRaw;
    // The parent is read from the title's own detail route (E-web-23): the same facts in its shape.
    const parent = reply as {id?: unknown; libraryId?: unknown} | null;
    const raw = String(input).includes('/detail') && parent && typeof parent === 'object' ? {item: parent, scope: {itemId: parent.id, libraryId: parent.libraryId}} : reply;
    return new Response(JSON.stringify(raw), {headers: {'Content-Type': 'application/json'}});});
  const context = {api, session: f.session, isCurrent: f.current, libraryRouteProof: f.proof.source,
    facts: {identity: {libraries: [{id: 'target', name: 'Before', kind: 'book'}]}, preferences: {}}, epoch: 1} as unknown as ShellAssemblyContext;
  let onNavigate: (() => void) | undefined;
  const module = await componentModule(new URL('../src/screens/browse/library-route-guard.ts', import.meta.url), {react: h.react,
    '../../shell/fact-source': {forwardFacts}, '../../shell/library-route-proof': {createLibraryRouteGuard}, '../../platform/title-identity': {readTitleIdentity},
    '../../../../packages/contracts/src/routes.ts': {encodedRoutePath},
    '../../router': {router: {navigate: (value: unknown) => {onNavigate?.();navigations.push(value);}}}});
  const draw = () => h.render(() => {const guard = module.useLibraryRouteGuard(context, opening, () => location === opening, target, (error: unknown) => errors.push(error));return {guard, context: module.useGuardedLibraryContext(context, guard)};});
  return {f, h, context, draw, navigations, errors, parentCalls: () => parentCalls, parent(value: unknown) {parentRaw = value;},
    holdParent() {return parentHold = deferred<unknown>();}, setLocation() {location = {};}, onNavigate(value: () => void) {onNavigate = value;}};
}

test('mounted route keeps its guard and original Screen/Read authorities on rename, then aborts them before navigation', async () => {
  const m = await mounted({libraryId: 'target'});
  try {
    const first = m.draw(), guarded = first.context;
    const screen = screenLifetime({api: m.context.api, isCurrent: guarded.isCurrent, libraryRouteSignal: guarded.libraryRouteSignal} as never);
    const reading = createReadBindings(guarded);let retired = 0;const off = reading.authority.subscribe(() => {retired++;});
    m.f.confirm(['target']);(m.context.facts.identity.libraries[0] as {name: string}).name = 'Renamed';
    const renamed = m.draw();assert.strictEqual(renamed.context, guarded);assert.strictEqual(renamed.guard, first.guard);
    assert.ok(reading.authority.isCurrent());assert.equal(guarded.libraryRouteSignal.aborted, false);assert.equal(screen.signal().aborted, false);assert.deepEqual(m.navigations, []);
    const oldCommand = () => {screen.assert();return 'acted';};assert.equal(oldCommand(), 'acted');
    m.onNavigate(() => {assert.equal(guarded.isCurrent(), false);assert.equal(reading.authority.isCurrent(), false);assert.equal(reading.client.signal.aborted, true);assert.equal(screen.signal().aborted, true);assert.throws(oldCommand, {name: 'AbortError'});});
    m.f.confirm(['other']);assert.deepEqual(m.navigations, [{to: '/libraries', replace: true}]);assert.equal(retired, 1);assert.equal(m.parentCalls(), 0);
    await assert.rejects(reading.client.versions('item'));assert.equal(m.parentCalls(), 0);off();screen.dispose();await reading.dispose();
  } finally {m.h.dispose();m.f.proof.dispose();}
});

test('mounted item route refuses malformed/obsolete parent replies; current successful parent absence redirects once', async () => {
  const m = await mounted({itemId: 'item'});
  try {
    const original = m.draw();m.parent({id: 'item'});m.f.confirm([]);await turn();assert.ok(original.guard.isCurrent());assert.equal(m.navigations.length, 0);assert.ok(original.guard.getFailure());
    const held = m.holdParent();m.f.confirm([]);await turn();m.f.directory(rows(['target']));held.resolve({id: 'item', libraryId: 'target', title: 'Book', kind: 'book', available: true, durationMs: 0, progressMs: 0});await turn();assert.equal(m.navigations.length, 0);
    m.f.confirm(['other']);await turn();assert.equal(original.guard.isCurrent(), false, String(original.guard.getFailure()));assert.deepEqual(m.navigations, [{to: '/libraries', replace: true}]);
  } finally {m.h.dispose();m.f.proof.dispose();}
});

test('Reading descendants retain the provided guarded context instead of reacquiring raw shell authority', async () => {
  const m = await mounted({libraryId: 'target'}), guarded = m.draw().context, provided = {context: guarded, binding: {}}, raw = {context: m.context, binding: {}};
  try {
    for (const name of ['Library', 'Series', 'Periodical', 'Item']) {
      const module = await componentModule(new URL(`../src/screens/read/Read${name}Entry.tsx`, import.meta.url), {react: m.h.react,
        '../../route-registry-view': {useProtectedWebScreenContext: () => raw}});
      const element = module['Read' + name + 'Entry']({captured: provided, handoff: m.context, libraryId: 'target', seriesId: 'series', kind: 'magazine', itemId: 'item', eligibility: {itemId: 'item'}});
      assert.strictEqual(element.props.captured, provided, name);
      if (name === 'Item') assert.strictEqual(element.props.handoff, m.context);
    }
    m.f.confirm([]);assert.equal(guarded.isCurrent(), false);assert.equal(m.navigations.length, 1);
  } finally {m.h.dispose();m.f.proof.dispose();}
});


test('a preexisting current proof denies a newly rendered library action before its layout starts; disposal aborts all original guards', () => {
  const f = fixture();f.confirm(['other']);const navigations: unknown[] = [];
  const guard = createLibraryRouteGuard({source: f.proof.source, opening: {}, isCurrent: f.current, canReplace: f.current, libraryId: 'target',
    replace: value => {navigations.push(value);}, reportError: error => {throw error;}});
  assert.equal(guard.isCurrent(), false);assert.equal(guard.signal.aborted, true);assert.equal(navigations.length, 0);guard.start();assert.equal(navigations.length, 1);
  const current = createLibraryRouteGuard({source: f.proof.source, opening: {}, isCurrent: f.current, canReplace: f.current, libraryId: 'other', replace() {throw new Error('unexpected');}, reportError(error) {throw error;}});
  current.start();assert.ok(current.isCurrent());f.proof.dispose();assert.equal(current.isCurrent(), false);assert.equal(current.signal.aborted, true);guard.dispose();current.dispose();
});
