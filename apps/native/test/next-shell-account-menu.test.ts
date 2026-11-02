import test from 'node:test';
import assert from 'node:assert/strict';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import {nativeAccountMenu, nativeAccountOpenTransition, nativeMenuRouteAvailable} from '../src-next/shell/account';
import {nativeShellRoutes} from '../src-next/shell/route-registry';

const profile = {id: 'profile', name: 'Demo', primary: true, position: 0, art: 'blue' as const, revision: 1, pinRevision: 1, pinRequired: false};
const account: DirectSnapshot = {authority: 'local', serverId: 'server', account: {id: 'account', username: 'demo', primaryProfileId: profile.id,
  role: 'owner', revision: 1, disabled: false, allowedLibraries: []}, profiles: [profile], canManage: true,
  capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true}};
const factory = () => ({render: () => null});

test('the account menu offers only destinations whose screen is registered', () => {
  const releases = [
    nativeShellRoutes.registerRoute('profiles', factory),
    nativeShellRoutes.registerRoute('servers', factory),
    nativeShellRoutes.registerRoute('together', factory),
    nativeShellRoutes.registerDestination('settings', factory),
  ];
  try {
    assert.equal(nativeMenuRouteAvailable({kind: 'notifications'}), false);
    assert.equal(nativeMenuRouteAvailable({kind: 'shell', destination: {kind: 'settings'}}), true);
    assert.deepEqual(nativeAccountMenu(account, () => {}, false, nativeMenuRouteAvailable).actions.map(action => action.id),
      ['profiles', 'servers', 'together', 'settings'], 'no action opens an unregistered Notifications page');
    assert.equal(nativeAccountMenu(account, () => {}, false, nativeMenuRouteAvailable).serverAction, undefined, 'the Dashboard card waits for its route');

    const offNotifications = nativeShellRoutes.registerRoute('notifications', factory);
    const offDashboard = nativeShellRoutes.registerRoute('server-dashboard', factory);
    try {
      assert.deepEqual(nativeAccountMenu(account, () => {}, false, nativeMenuRouteAvailable).actions.map(action => action.id),
        ['profiles', 'servers', 'notifications', 'together', 'settings']);
      assert.equal(nativeAccountMenu(account, () => {}, false, nativeMenuRouteAvailable).serverAction?.id, 'server');
      assert.deepEqual(nativeAccountMenu({...account, account: {...account.account, role: 'member'}}, () => {}, false, nativeMenuRouteAvailable).actions.map(action => action.id),
        ['profiles', 'servers', 'notifications', 'together', 'settings']);
    } finally {offNotifications(); offDashboard();}
  } finally {for (const release of releases) release();}
});

test('TV Server settings has its own registered route and preserves the phone Dashboard', () => {
  const opened: unknown[] = [];
  const offDashboard = nativeShellRoutes.registerRoute('server-dashboard', factory);
  try {
    assert.equal(nativeAccountMenu(account, route => opened.push(route), true, nativeMenuRouteAvailable).serverAction, undefined);
    const offSettings = nativeShellRoutes.registerRoute('server-settings', factory);
    try {
      nativeAccountMenu(account, route => opened.push(route), true, nativeMenuRouteAvailable).serverAction!.onPress();
      nativeAccountMenu(account, route => opened.push(route), false, nativeMenuRouteAvailable).serverAction!.onPress();
      assert.deepEqual(opened, [{kind: 'server-settings'}, {kind: 'server-dashboard'}]);
      assert.equal(nativeAccountMenu({...account, account: {...account.account, role: 'member'}}, () => {}, true, nativeMenuRouteAvailable).serverAction, undefined);
    } finally {offSettings();}
  } finally {offDashboard();}
});

// W-9 (the audiobook player over Switch profile): an account destination opened while the TV music or
// audiobook player is expanded folds it away first, so the page opens on top and holds focus.
test('W-9: an account destination folds an expanded TV music or audiobook player away first, never video', () => {
  const fence = {sessionId: 'session'} as never;
  const dock = {collapse: (value: unknown) => ({kind: 'collapse', fence: value}), close: () => assert.fail('never closes')} as never;
  const snapshot = (kind: string, expanded = true) => ({fence, dock: {local: {kind, expanded}}}) as never;
  for (const kind of ['music', 'audiobook']) assert.deepEqual(nativeAccountOpenTransition(dock, snapshot(kind), true), {kind: 'collapse', fence}, kind);
  for (const kind of ['video', 'live']) assert.equal(nativeAccountOpenTransition(dock, snapshot(kind), true), undefined, `${kind} keeps its own Back`);
  assert.equal(nativeAccountOpenTransition(dock, snapshot('audiobook', false), true), undefined, 'a docked player stays docked');
  assert.equal(nativeAccountOpenTransition(dock, snapshot('audiobook'), false), undefined, 'phones keep their own dock');
  assert.equal(nativeAccountOpenTransition(dock, {fence: undefined, dock: {local: {kind: 'music', expanded: true}}} as never, true), undefined);
});
