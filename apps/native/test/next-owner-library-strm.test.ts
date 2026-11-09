import test from 'node:test';
import assert from 'node:assert/strict';
import * as routes from '../../../packages/contracts/src/routes.ts';
import {screenModule} from './helpers/screen-harness.mjs';

async function fixture() {
  const policies = new Map<string, (value: unknown) => void>(), writes: unknown[] = [];
  let finishWrite: (value: unknown) => void = () => {};
  class Management {
    selected: any = {phase: 'ready', data: {library: {id: '', lastScan: null}}};
    subscribe = () => () => {}; dispose() {}
    getSnapshot = () => ({directory: {phase: 'ready', data: {items: []}}, selected: this.selected});
    selectLibrary = async (id: string) => {this.selected = {phase: 'ready', data: {library: {id, lastScan: null}}};};
  }
  class Inventory {subscribe = () => () => {}; dispose() {} refresh = async () => {}; getSnapshot = () => ({});}
  class RemotePolicy {
    dispose() {}
    policy = (id: string) => new Promise(resolve => {policies.set(id, resolve);});
    setPolicy = (policy: unknown, enabled: boolean) => {writes.push({policy, enabled}); return new Promise(resolve => {finishWrite = resolve;});};
  }
  const base = '../../../../../../packages/client-core/src/';
  const module = await screenModule(new URL('../src-next/screens/settings-live/owner-libraries/controller.ts', import.meta.url), {
    react: {default: {}}, '../../../../../../packages/contracts/src/routes': routes, [base + 'remote-sources']: {RemoteSourceService: RemotePolicy},
    [base + 'library-management']: {LibraryManagementService: Management, fetchEpisodeIssues: async () => ({issues: [], nextCursor: ''})},
    [base + 'library-inventory']: {LibraryInventoryService: Inventory},
    [base + 'server-admin/libraries']: {readLibrarySettings: async (_api: unknown, id: string) => ({libraryId: id, libraryKind: 'movie'})},
    [base + 'scan-issues']: {}, [base + 'title-identity']: {},
    [base + 'server-admin/metadata-agent']: {metadataAgentPath: (id: string) => id, parseMetadataAgent: () => undefined},
    [base + 'server-admin/library-sonic']: {}, [base + 'presentation/settings-bindings']: {},
  });
  const controller = new module.NativeOwnerLibrariesController({request: async () => ({})}, {serverId: 'server', viewerId: 'owner'}, () => 'request');
  return {controller, policies, writes, lease: module.sonicPageLease, finishWrite: (value: unknown) => finishWrite(value)};
}

test('STRM rejects an old page and recovers a late accepted response by reading, without replaying the write', async () => {
  const f = await fixture(); await f.controller.load('movies');
  const policy = {libraryId: 'movies', enabled: false, revision: 3}; f.policies.get('movies')!(policy); await Promise.resolve();
  const document = f.controller.getSnapshot().document, request = new AbortController(), lease = f.lease(request, () => true);
  const pending = f.controller.setStrm(true, document, policy, lease);
  assert.deepEqual(f.writes, [{policy, enabled: true}]);
  request.abort(); f.finishWrite({...policy, enabled: true, revision: 4}); await pending;
  assert.equal(f.controller.getSnapshot().strm, policy); assert.ok(f.controller.getSnapshot().strmError);
  await f.controller.setStrm(true, document, policy, lease); assert.equal(f.writes.length, 1);
  const reloading = f.controller.reloadStrm(document), next = {...policy, enabled: true, revision: 4}; f.policies.get('movies')!(next); await reloading;
  assert.equal(f.controller.getSnapshot().strm, next); assert.equal(f.controller.getSnapshot().strmError, undefined);
  assert.equal(f.writes.length, 1); f.controller.dispose();
});

test('a policy read cannot replace the selected library after its opening changes', async () => {
  const f = await fixture(); await f.controller.load('first'); const previous = f.policies.get('first')!;
  await f.controller.load('second'); const latest = {libraryId: 'second', enabled: true, revision: 7};
  f.policies.get('second')!(latest); await Promise.resolve(); previous({libraryId: 'first', enabled: false, revision: 1}); await Promise.resolve();
  assert.equal(f.controller.getSnapshot().selectedId, 'second'); assert.equal(f.controller.getSnapshot().strm, latest); f.controller.dispose();
});
