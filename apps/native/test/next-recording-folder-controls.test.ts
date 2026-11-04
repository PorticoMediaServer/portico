// @fabric-react
// Sol 8's controls on #831, on the real Fabric hook host: the actual RecordingFolder row, store, controller and lifetime;
// only the server, the explorer sheet and the error view are stand-ins.
import test from 'node:test';
import assert from 'node:assert/strict';
import {React} from './helpers/fabric-host.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {NativeRecordingRoot} from '../src-next/screens/settings-live/recording-folder-store.ts';
import {NativeLiveOwnerController} from '../src-next/screens/settings-live/live-owner-controller.ts';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime.ts';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const reactNamespace = {...React, default: React};

async function fixture() {
  const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'f'};
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}} as any);
  const writes: any[] = []; let fail = false;
  let status = {revision: 1, requested: '/srv/old', effective: '/srv/old', pendingRestart: false, operatorControlled: false};
  const controller = new NativeLiveOwnerController(lifetime, {request: async (_path: string, method: string, body: any) => {
    if (method === 'PATCH') {
      writes.push(body);
      if (fail) {fail = false; throw {code: 'network'};}
      status = {...status, revision: 2, requested: body.path, pendingRestart: true};
    }
    return status;
  }} as any, () => 'unused');
  const {RecordingFolder} = await screenModule(new URL('../src-next/screens/settings-live/RecordingFolder.tsx', import.meta.url), {
    react: reactNamespace, 'react-native': {Text: 'Text', View: 'View'}, '../../kit/controls': {Button: 'Button'}, '../../kit/states': {StateView: 'StateView'},
    '../../kit/shell/visual': {shellType: () => ({})}, '../../kit/theme': {useTheme: () => ({colors: {}})},
    './owner-libraries/FolderExplorer': {FolderExplorer: 'FolderExplorer'}, './recording-folder-store': {NativeRecordingRoot},
  });
  const runtime = {text: {t: (id: string) => id}, error: (_error: unknown, retry: () => void) => ({title: 'error', primaryAction: {onPress: retry}})};
  let tree: any;
  const host = createFabricHookHost(() => {tree = RecordingFolder({controller, runtime});});
  const button = () => nodes(tree).find(n => n.props.testID === 'live-owner-recording-folder-choose');
  const explorer = () => nodes(tree).find(n => n.type === 'FolderExplorer');
  return {host, writes, controller, explorer, error: () => nodes(tree).find(n => n.type === 'StateView'), failNext: () => {fail = true;},
    async open() {button().props.onPress(); await host.flush(); return explorer().props;},
    end: async () => {await host.unmount(); controller.dispose(); lifetime.dispose();}};
}

for (const mode of ['cancelled', 'replaced'] as const) test(`a ${mode} explorer cannot later save its old selection`, async () => {
  const f = await fixture();
  try {
    await f.host.mount();
    const old = await f.open(); old.onOpenChange(false); await f.host.flush();
    const fresh = mode === 'replaced' ? await f.open() : undefined;
    old.onUse([{path: '/srv/stale', name: 'stale'}]); await f.host.flush();
    assert.equal(f.writes.length, 0, 'a retired explorer opening must not send a write');
    if (fresh) assert.equal(f.explorer().props.api, fresh.api, 'and the replacement stays open');
  } finally {await f.end();}
});

test('the current explorer saves one folder immediately with the original revision', async () => {
  const f = await fixture();
  try {
    await f.host.mount();
    const current = await f.open(); current.onUse([{path: '/srv/new', name: 'new'}]); await f.host.flush();
    assert.equal(f.writes.length, 1); assert.equal(f.writes[0].path, '/srv/new'); assert.equal(f.writes[0].expectedRevision, 1); assert.ok(f.writes[0].operationId);
    assert.equal(f.explorer(), undefined);
  } finally {await f.end();}
});

test('an explorer retained across disposal sends nothing', async () => {
  const f = await fixture();
  try {
    await f.host.mount();
    const old = await f.open(); f.controller.dispose();
    old.onUse([{path: '/srv/stale', name: 'stale'}]); await f.host.flush();
    assert.equal(f.writes.length, 0);
  } finally {await f.end();}
});

test('a Try again retained from write A never replays a later write B; the current Try again still settles B with its own intent', async () => {
  const f = await fixture();
  try {
    await f.host.mount();
    f.failNext(); (await f.open()).onUse([{path: '/srv/A', name: 'A'}]); await f.host.flush();
    const retryA = f.error().props.primaryAction.onPress;
    retryA(); await f.host.flush();
    assert.deepEqual(f.writes.map(v => v.path), ['/srv/A', '/srv/A']);
    f.failNext(); (await f.open()).onUse([{path: '/srv/B', name: 'B'}]); await f.host.flush();
    retryA(); await f.host.flush();
    assert.equal(f.writes.length, 3, 'Try again from A must never adopt B\'s pending intent');
    f.error().props.primaryAction.onPress(); await f.host.flush();
    assert.deepEqual(f.writes.map(v => v.path), ['/srv/A', '/srv/A', '/srv/B', '/srv/B']);
    assert.equal(f.writes[3].operationId, f.writes[2].operationId, 'the same write, never reminted');
  } finally {await f.end();}
});
