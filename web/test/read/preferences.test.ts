import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {parsePreferenceSnapshot, readingPreferencesPatch, type PreferenceSnapshot, type ReadingPreferences, type ReadingPreferencesChange} from '@portico/client-core';
import type {ReaderAdapter, ReaderMount} from '@portico/reader-core';
import {bindReadPreferences} from '../../src/screens/read/preferences.ts';
import {mountReaderSurface} from '../../src/screens/read/mount.ts';

const recorded = JSON.parse(readFileSync(new URL('../../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences;
const initial = () => parsePreferenceSnapshot(recorded);
const tick = () => new Promise<void>(resolve => setImmediate(resolve));

// Confirmed-response construction for this wiring test, not HTTP save proof.
function confirmed(snapshot: PreferenceSnapshot, changes: ReadingPreferencesChange): PreferenceSnapshot {
  const patch = readingPreferencesPatch(snapshot, changes);
  return parsePreferenceSnapshot({...snapshot, effective: {...snapshot.effective, ...patch},
    effectiveSource: {...snapshot.effectiveSource, ...Object.fromEntries(Object.keys(patch).map(key => [key, 'profile-server']))},
    documents: snapshot.documents.map(document => document.scope === 'profile-server'
      ? {...document, values: {...document.values, ...patch}, revision: document.revision + 1, activeRevision: document.activeRevision + 1} : document)});
}
function scope() {
  let snapshot = initial(), active = true;
  const changes = new Set<() => void>(), retirements = new Set<() => void>(), errors: string[] = [];
  const preferences = {getSnapshot: () => snapshot, subscribe(listener: () => void) { changes.add(listener); return () => { changes.delete(listener); }; }};
  const authority = {isCurrent: () => active, subscribe(listener: () => void) { retirements.add(listener); return () => { retirements.delete(listener); }; }};
  return {preferences, authority, errors, changes, retirements,
    confirm(next: PreferenceSnapshot) { snapshot = next; for (const listener of [...changes]) listener(); },
    retire() { active = false; for (const listener of [...retirements]) listener(); }};
}
function adapter() {
  let mount: ReaderMount<ReadingPreferences> | undefined, disposed = 0;
  const source = {id: 'host-preference-unit', revision: 'unit-revision-1', size: 1};
  const implementation: ReaderAdapter<ReadingPreferences> = {format: 'epub', async open() {
    return {id: source.id, revision: source.revision, format: 'epub', metadata: {}, toc: [], async *search() {}, dispose() {},
      async mount(options) { mount = options; return {async navigate() {}, async resize() {}, async applyPreferences() {}, getLocation: () => null, dispose() { disposed++; }}; }};
  }};
  return {implementation, source, get mount() { return mount; }, get disposed() { return disposed; }};
}

test('the web binding forwards confirmed snapshots to the actual core while preserving book overrides', async () => {
  const owner = scope(), format = adapter(), before = owner.preferences.getSnapshot(), original = JSON.stringify(before);
  const bound = bindReadPreferences({adapters: [format.implementation], preferences: owner.preferences, authority: owner.authority, reportError: code => owner.errors.push(code)});
  await bound.controller.open({source: format.source, format: 'epub', container: {} as HTMLElement, viewport: {width: 820, height: 1180}});
  await bound.controller.setOverride({font: 'publisher'});
  const next = confirmed(before, {font: 'manrope', theme: 'sepia'}); owner.confirm(next); await tick();
  assert.equal(bound.controller.getSnapshot().preferences.font, 'publisher');
  assert.equal(bound.controller.getSnapshot().preferences.theme, 'sepia');
  await bound.controller.close();
  await bound.controller.open({source: format.source, format: 'epub', container: {} as HTMLElement, viewport: {width: 820, height: 1180}});
  assert.equal(format.mount?.preferences.font, 'manrope');
  assert.deepEqual(bound.controller.getSnapshot().overriddenKeys, []);
  assert.equal(JSON.stringify(before), original); assert.deepEqual(owner.errors, []);
  await bound.dispose(); assert.equal(owner.changes.size, 0); assert.equal(owner.retirements.size, 0);
});

test('the public confirmed controller passes directly to the DOM mount and retires with its viewer', async t => {
  const prior = {ResizeObserver: globalThis.ResizeObserver, requestAnimationFrame: globalThis.requestAnimationFrame, cancelAnimationFrame: globalThis.cancelAnimationFrame};
  globalThis.ResizeObserver = class {observe() {} disconnect() {}} as unknown as typeof ResizeObserver;
  globalThis.requestAnimationFrame = () => 1; globalThis.cancelAnimationFrame = () => {};
  t.after(() => Object.assign(globalThis, prior));
  const owner = scope(), format = adapter();
  const bound = bindReadPreferences({adapters: [format.implementation], preferences: owner.preferences, authority: owner.authority, reportError: code => owner.errors.push(code)});
  const mounted = mountReaderSurface({controller: bound.controller, source: format.source, format: 'epub', authority: owner.authority,
    container: {clientWidth: 390, clientHeight: 844} as HTMLElement, reportError: code => owner.errors.push(code)});
  await mounted.ready; owner.retire(); await bound.dispose(); await mounted.close();
  owner.confirm(confirmed(owner.preferences.getSnapshot(), {theme: 'light'})); await tick();
  assert.equal(bound.controller.getSnapshot().status, 'closed'); assert.equal(format.disposed, 1);
  assert.equal(owner.changes.size, 0); assert.equal(owner.retirements.size, 0); assert.deepEqual(owner.errors, []);
});

test('failed subscription setup releases the already captured viewer listener', () => {
  const owner = scope(), format = adapter();
  assert.throws(() => bindReadPreferences({adapters: [format.implementation], preferences: {...owner.preferences, subscribe() { throw new Error('unit subscription failure'); }},
    authority: owner.authority, reportError: code => owner.errors.push(code)}), /unit subscription failure/);
  assert.equal(owner.changes.size, 0); assert.equal(owner.retirements.size, 0);
});
