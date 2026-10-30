import test from 'node:test';
import assert from 'node:assert/strict';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import {nativeAccountMenu, nativeMenuRouteAvailable} from '../src-next/shell/account';
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
