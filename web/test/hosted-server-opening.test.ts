import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';

function makeEntry() {
  let snapshot: Record<string, unknown> = {phase: 'welcome', busy: false};
  const listeners = new Set<() => void>();
  let disposed = false;
  const entry = {
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    getSnapshot: () => snapshot,
    openHostedServer: async () => true,
    dispose: () => { disposed = true; listeners.clear(); },
  };
  const publish = (next: Record<string, unknown>) => {
    if (disposed) return;
    snapshot = next;
    for (const listener of [...listeners]) listener();
  };
  return {entry, publish, isDisposed: () => disposed};
}

test('HostedServerOpening hands off unsaved single-profile hosted servers on ready without waiting for route changes', async () => {
  const h = createRenderer();
  let currentEntry = makeEntry();
  let twoStepOwed = false;
  class FakeForm {
    private listeners = new Set<() => void>();
    subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
    getSnapshot = () => ({factor: undefined, busy: false, inputEpoch: 0, username: ''});
    dispose() { this.listeners.clear(); }
  }
  const mod = await componentModule(new URL('../src/shell/SwitchProfileServerHost.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../kit/accounts': {AuthError: 'AuthError'},
    '../kit/shell/SwitchProfileServerDialog': {SwitchProfileServerDialog: 'SwitchProfileServerDialog'},
    '../platform/server-connections': {browserAppSessions: () => ({}), observeBrowserSessionChanges: () => () => {}},
    '../platform/server-display': {browserServerDisplay: () => ({})},
    '../platform/portico-account-holder': {porticoAccountHolder: {signedIn: () => false}},
    '../platform/account-entry': {createBrowserAccountEntry: () => currentEntry.entry},
    '../platform/two-step-continuation': {hasTwoStepSetup: () => twoStepOwed},
    '../screens/accounts/AccountSignInView': {AccountSignInView: 'AccountSignInView'},
    '../screens/accounts/sign-in-form': {AccountSignInForm: FakeForm},
  });

  const completed: Array<{serverId: string; twoStep: boolean}> = [];
  const selection = {server: {id: 'srv-b', name: 'Server B', publicKey: 'pk', baseUrl: 'https://b.example'}, access: {accountId: 'acc', origin: 'https://acc.example', isCurrent: () => true, observe: () => () => {}, identity: async () => ''}, revision: 3};
  tree(h.render(() => mod.HostedServerOpening({selection, cancel() {}, onComplete: (serverId: string, twoStep: boolean) => completed.push({serverId, twoStep})})));

  // Challenge and multi-profile states must not trigger single-profile completion.
  currentEntry.publish({phase: 'challenge', busy: false});
  currentEntry.publish({phase: 'profiles', busy: false, server: {id: 'srv-b'}, snapshot: {account: {id: 'a1'}}});
  assert.deepEqual(completed, []);

  // Single-profile ready fires completion exactly once.
  currentEntry.publish({phase: 'ready', busy: false, server: {id: 'srv-b'}, snapshot: {account: {id: 'a1'}}});
  currentEntry.publish({phase: 'ready', busy: false, server: {id: 'srv-b'}, snapshot: {account: {id: 'a1'}}});
  assert.deepEqual(completed, [{serverId: 'srv-b', twoStep: false}]);

  // Two-step setup owed forwards the flag on a new selection.
  currentEntry = makeEntry();
  twoStepOwed = true;
  const selection2 = {...selection, server: {...selection.server, id: 'srv-c'}};
  tree(h.render(() => mod.HostedServerOpening({selection: selection2, cancel() {}, onComplete: (serverId: string, twoStep: boolean) => completed.push({serverId, twoStep})})));
  currentEntry.publish({phase: 'ready', busy: false, server: {id: 'srv-c'}, snapshot: {account: {id: 'a2'}}});
  assert.deepEqual(completed, [{serverId: 'srv-b', twoStep: false}, {serverId: 'srv-c', twoStep: true}]);

  // After unmount, late publications do not fire onComplete.
  h.dispose();
  assert.equal(currentEntry.isDisposed(), true);
  currentEntry.publish({phase: 'ready', busy: false, server: {id: 'srv-c'}, snapshot: {account: {id: 'a2'}}});
  assert.equal(completed.length, 2);
});
