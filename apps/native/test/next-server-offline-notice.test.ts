import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as notifications from '../../../packages/client-core/src/account-notifications.ts';
import * as accountWork from '../src-next/platform/account-work.ts';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree?.props ? [tree, ...nodes(tree.props.children), ...nodes(tree.props.control)] : [];

async function fixture({hosted = true, matching = true, tv = false}: {hosted?: boolean; matching?: boolean; tv?: boolean} = {}) {
  const host = hooks(), effects: (() => void)[] = [];
  let deps: any[] | undefined, cleanup: (() => void) | undefined;
  const useLayoutEffect = (effect: () => (() => void) | undefined, next: any[]) => {
    if (!deps || next.some((value, index) => value !== deps![index])) {deps = next; effects.push(() => {cleanup?.(); cleanup = effect();});}
  };
  Object.assign(host.react, {useLayoutEffect}); Object.assign(host.react.default, {useLayoutEffect});
  const listeners = new Set<() => void>();
  let session: any = {account: {id: matching ? 'portico-account' : 'someone-else'}, familyId: 'portico-family', accessToken: 'fixture-token'};
  let lifetimeCurrent = true, credentialReads = 0;
  const context: any = {account: {account: {id: 'member', ...(hosted ? {hostedAccountId: 'portico-account'} : {})}}, current: () => true,
    api: {request: () => {throw new Error('Hosted notices must not use the server transport');}}};
  const lifetimeAbort = new AbortController();
  const lifetime: any = {current: () => lifetimeCurrent, signal: lifetimeAbort.signal};
  const calls: any[][] = [], signals: AbortSignal[] = [];
  let stored = false, failSave = false, failRead = false, saveGate: Promise<void> | undefined;
  const credentials = {getSnapshot: () => ({session}), subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);},
    synchronize: async () => {}, accessSession: async () => session};
  const {NativeServerOfflineNoticeRow} = await screenModule(new URL('../src-next/screens/settings-live/NativeServerOfflineNoticeRow.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View'},
    '../../../../../packages/client-core/src': {HttpLocalApi: class {
      constructor(readonly origin: string, readonly token: string) {}
      async request(path: string, method: string, body: unknown, signal?: AbortSignal) {
        calls.push([this.origin, this.token, path, method, body]); if (signal) signals.push(signal);
        if (method === 'GET') {if (failRead) throw new Error('read failed'); return {serverOffline: stored};}
        if (saveGate) await saveGate;
        if (failSave) throw new Error('save failed');
        stored = (body as {serverOffline: boolean}).serverOffline; return {serverOffline: stored};
      }}},
    '../../../../../packages/client-core/src/account-notifications': notifications,
    '../../kit/controls': {Switch: 'Switch'}, '../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../kit/states': {StateView: 'StateView'},
    '../../kit/theme': {useTheme: () => ({isTV: tv})},
    '../../platform/accounts': {nativeAccounts: {accountCredentials: () => {credentialReads++; return credentials;}}},
    '../../platform/account-authority': {hostedUrl: 'https://accounts.example.invalid'},
    '../../platform/account-work': accountWork, '../../platform/server-connections': {nativeNetworkFetch: () => {}},
  });
  const runtime: any = {text: {t: (key: string) => key}, error: (error: unknown, dismiss: () => void) => ({error, dismiss})};
  let tree: any;
  const render = () => {tree = host.render(() => NativeServerOfflineNoticeRow({context, runtime, lifetime})); for (const effect of effects.splice(0)) effect(); return tree;};
  const settle = async () => {for (let i = 0; i < 4; i++) await new Promise(resolve => setImmediate(resolve)); render();};
  const store = (value: boolean) => {stored = value;};
  return {render, settle, calls, signals, store, credentialReads: () => credentialReads,
    find: (type: string) => nodes(tree).find(node => node.type === type),
    failRead: () => {failRead = true;}, holdSave: () => {saveGate = new Promise(() => {});}, failSave: (value = true) => {failSave = value;},
    session: (value: any) => {session = value; for (const listener of [...listeners]) listener(); render();},
    retire: () => {lifetimeCurrent = false; lifetimeAbort.abort();}, dispose: () => cleanup?.()};
}

test('Portico Account: the switch shows the account\'s stored choice once read, and saves a flip over the hosted API', async () => {
  const f = await fixture();
  f.store(true); f.render();
  assert.equal(f.find('SettingsRow'), undefined, 'nothing is drawn until the account answers');
  await f.settle();
  const row = f.find('SettingsRow');
  assert.equal(row.props.testID, 'settings-notice-server-offline');
  assert.equal(row.props.label, 'next.settingsSpec.account.profile.groups.7.rows.1.label');
  assert.equal(f.find('Switch').props.checked, true);
  assert.deepEqual(f.calls[0], ['https://accounts.example.invalid', 'fixture-token', '/v1/account/notification-preferences', 'GET', undefined]);
  f.find('Switch').props.onCheckedChange(false);
  f.render();
  assert.equal(f.find('Switch').props.disabled, true, 'busy while the save is in flight');
  await f.settle();
  assert.deepEqual(f.calls[1].slice(2), ['/v1/account/notification-preferences', 'PUT', {serverOffline: false}]);
  assert.equal(f.find('Switch').props.checked, false);
  assert.equal(f.find('Switch').props.disabled, false);
  f.dispose();
});

test('A failed save keeps the stored value and says why; a failed read draws nothing', async () => {
  const f = await fixture();
  f.store(true); f.render(); await f.settle();
  f.failSave(); f.find('Switch').props.onCheckedChange(false); await f.settle();
  assert.equal(f.find('Switch').props.checked, true);
  assert.ok(f.find('StateView'), 'the save failure is shown inline');
  f.find('StateView').props.dismiss(); f.render();
  assert.equal(f.find('StateView'), undefined);
  f.dispose();
  const g = await fixture();
  g.failRead(); g.render(); await g.settle();
  assert.equal(g.find('SettingsRow'), undefined);
  g.dispose();
});

test('A local account, another Portico account, or a TV never consults the account or draws the row', async () => {
  const local = await fixture({hosted: false});
  local.render(); await local.settle();
  assert.equal(local.find('SettingsRow'), undefined);
  assert.equal(local.credentialReads(), 0);
  assert.equal(local.calls.length, 0);
  const other = await fixture({matching: false});
  other.render(); await other.settle();
  assert.equal(other.find('SettingsRow'), undefined);
  assert.equal(other.calls.length, 0);
  const tv = await fixture({tv: true});
  tv.render(); await tv.settle();
  assert.equal(tv.find('SettingsRow'), undefined);
  assert.equal(tv.calls.length, 0);
});

test('Signing out of the Portico Account mid-read removes the row and aborts the request', async () => {
  const f = await fixture();
  f.render();
  f.session(undefined);
  await f.settle();
  assert.equal(f.find('SettingsRow'), undefined);
  assert.ok(f.signals.every(signal => signal.aborted) || f.calls.length === 0);
  f.dispose();
});

test('Ending the Settings lifetime cancels a save in flight at once, not at unmount', async () => {
  const f = await fixture();
  f.store(true); f.render(); await f.settle();
  f.holdSave(); f.find('Switch').props.onCheckedChange(false);
  for (let i = 0; i < 4; i++) await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.calls.at(-1)![3], 'PUT');
  const put = f.signals.at(-1)!;
  assert.equal(put.aborted, false);
  f.retire();
  assert.equal(put.aborted, true, 'the PUT is aborted when the lifetime ends');
  f.dispose();
});
