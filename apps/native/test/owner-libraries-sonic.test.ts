import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeOwnerLibrariesController, sonicPageLease, type SonicPageLease} from '../src-next/screens/settings-live/owner-libraries/controller';
import type {LibraryAdminDocument} from '../../../packages/client-core/src/administration';
import type {LibrarySonicDocument} from '../../../packages/client-core/src/server-admin/library-sonic';

const scope = {serverId: 'server', viewerId: 'owner'};
const document = (id = 'music', revision = 4): LibraryAdminDocument => ({
  libraryId: id, libraryName: id, libraryKind: 'music', revision, digest: 'a'.repeat(64),
  eligibleControls: ['libraries.sonicAnalysis', 'libraries.sonicStatus'], audience: {everyone: true, accounts: 0},
  actions: [], settings: {} as LibraryAdminDocument['settings'], defaults: {} as LibraryAdminDocument['settings'],
  analysisMatrix: {costClasses: [], operations: []}, availableProviders: [], enumerations: {}, sources: [],
});
const sonic = (enabled = true, revision = 7): LibrarySonicDocument => ({
  libraryId: 'music', revision, enabled, defaultEnabled: true, available: true, state: enabled ? 'analyzing' : 'disabled',
  done: 10, total: 20, policyAllows: true, policyRevision: 2, observedAtMs: 1,
});
const lease = (parentCurrent: () => boolean = () => true): {command: SonicPageLease; controller: AbortController} => {
  const controller = new AbortController();
  return {controller, command: sonicPageLease(controller, parentCurrent)};
};

test('a hidden Sonic save releases its own busy and never adopts the late answer', async () => {
  const controller = new NativeOwnerLibrariesController({request: async () => {throw new Error('no read yet');}} as never, scope, () => 'operation');
  const state = controller as unknown as {state: Record<string, unknown>};
  const doc = document(), current = sonic(true), page = lease();
  state.state = {...state.state, selectedId: 'music', document: doc, sonic: current, sonicBusy: false, sonicRetryUnavailable: false};
  let release!: (value: LibrarySonicDocument) => void;
  (controller as unknown as {api: {request: unknown}}).api = {request: () => new Promise<LibrarySonicDocument>(resolve => {release = resolve;})};
  const saving = controller.setSonicEnabled(false, doc, current, page.command);
  assert.equal(state.state.sonicBusy, true);
  page.controller.abort();
  release(sonic(false, 8));
  assert.equal(await saving, false);
  assert.equal(state.state.sonicBusy, false);
  assert.equal(state.state.sonicNeedsReload, true);
  assert.equal((state.state.sonic as LibrarySonicDocument).enabled, true);
});

test('a retained callback cannot act after its Scanning page hides and reopens with the same row', async () => {
  let reads = 0;
  const controller = new NativeOwnerLibrariesController({request: async () => {reads++; throw new Error('must not run');}} as never, scope, () => 'operation');
  const state = controller as unknown as {state: Record<string, unknown>};
  const doc = document(), current = sonic(true), first = lease(), reopened = lease();
  state.state = {...state.state, selectedId: 'music', document: doc, sonic: current, sonicBusy: false};
  first.controller.abort();
  assert.equal(await controller.retrySonic(doc, current, first.command), false);
  assert.equal(await controller.setSonicEnabled(false, doc, current, first.command), false);
  assert.equal(reads, 0);
  assert.equal(await controller.retrySonic(doc, current, reopened.command), false);
  assert.equal(reads, 1);
  controller.dispose();
  assert.equal(await controller.retrySonic(doc, current, reopened.command), false);
});

test('a failed save recovers through the ordinary GET and clears the reload gate; a disabled row keeps that recovery', async () => {
  const controller = new NativeOwnerLibrariesController({request: async () => {throw new Error('no read yet');}} as never, scope, () => 'operation');
  const state = controller as unknown as {state: Record<string, unknown>};
  const doc = document(), current = sonic(false), page = lease();
  state.state = {...state.state, selectedId: 'music', document: doc, sonic: current, sonicBusy: false};
  (controller as unknown as {api: {request: unknown}}).api = {request: async () => {throw new Error('save failed');}};
  assert.equal(await controller.setSonicEnabled(true, doc, current, page.command), false);
  assert.equal(state.state.sonicNeedsReload, true);
  assert.equal((state.state.sonic as LibrarySonicDocument).enabled, false);
  (controller as unknown as {api: {request: unknown}}).api = {request: async () => sonic(false, 8)};
  assert.equal(await controller.reloadSonic(doc, current, page.command), true);
  assert.equal(state.state.sonicNeedsReload, false);
  assert.equal((state.state.sonic as LibrarySonicDocument).revision, 8);
});

test('the layout admission helper stays retired after cleanup even when a later setup is current', async () => {
  let oldAuthority = true, newAuthority = true;
  const first = lease(() => oldAuthority);
  const oldHandler = () => first.command;
  oldAuthority = false; first.controller.abort();
  const second = lease(() => newAuthority);
  assert.equal(oldHandler().isCurrent(), false);
  assert.equal(first.command.signal.aborted, true);
  assert.equal(second.command.isCurrent(), true);
  assert.notEqual(oldHandler(), second.command);
});
