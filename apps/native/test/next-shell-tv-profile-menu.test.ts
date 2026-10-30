import assert from 'node:assert/strict';
import test from 'node:test';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import {nativeAccountMenu} from '../src-next/shell/account';
import {tvFocusIds} from '../src-next/shell/tv-focus';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {defaultI18n} from '../../../packages/i18n/src';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const profile = {id: 'profile', name: 'Demo', primary: true, position: 0, art: 'blue' as const, revision: 1, pinRevision: 1, pinRequired: false};
const account: DirectSnapshot = {authority: 'local', serverId: 'server', account: {id: 'account', username: 'demo', primaryProfileId: profile.id,
  role: 'owner', revision: 1, disabled: false, allowedLibraries: []}, profiles: [profile], canManage: true,
  capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true}};
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};

test('actual TV profile menu has ordered safe actions, member gating and no extra Settings stop', async () => {
  const routes: unknown[] = [], open = (route: unknown) => {routes.push(route);};
  const one = nativeAccountMenu(account, open, true);
  assert.deepEqual(one.actions.map(action => action.id), ['servers', 'notifications', 'together']);
  const two = nativeAccountMenu({...account, profiles: [profile, {...profile, id: 'other', primary: false}]}, open, true);
  assert.deepEqual(two.actions.map(action => action.id), ['profiles', 'servers', 'notifications', 'together']);
  assert.equal(nativeAccountMenu({...account, account: {...account.account, role: 'member'}, canManage: false,
    capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false}}, open, true).serverAction, undefined);
  assert.deepEqual(nativeAccountMenu(account, open).actions.map(action => action.id), ['profiles', 'servers', 'notifications', 'together', 'settings'], 'phone action contract stays unchanged');
  const h = hooks();
  const {TVProfileMenu} = await screenModule(new URL('../src-next/shell/TVProfileMenu.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../packages/i18n/src': {defaultI18n},
    '../kit/theme': {nativeType, useTheme: () => createTheme({isTV: true, width: 1920})}, '../kit/artwork': {ProfileFace: 'Face'},
    '../kit/overlays': {Menu: 'Menu'}, './tv-focus': {tvFocusIds},
  });
  const ref = {current: null};
  const snapshot = {authority, account: two, profile: {id: 'profile', name: 'Demo'}, serverName: 'Actual server', unreadNotificationCount: 7};
  const tree = TVProfileMenu({snapshot, open: true, onOpenChange() {}, returnFocusRef: ref});
  assert.equal(tree.props.testID, tvFocusIds(authority).profileMenu);
  assert.equal(tree.props.returnFocusRef, ref, 'the genuine rail profile ref is handed to the shared hold');
  assert.equal(tree.props.anchorRef, ref);
  assert.equal(tree.props.presentation, 'anchored');
  assert.equal(tree.props.placement, 'right-end');
  assert.equal(tree.props.footer, undefined, 'the frame has no Back/Close footer');
  assert.deepEqual(tree.props.items.map((item: any) => item.id), ['profiles', 'servers', 'notifications', 'together', 'server-divider', 'server']);
  assert.equal(tree.props.items.find((item: any) => item.id === 'notifications').trailing.props.children.flat().join(''), '7 new');
  assert.equal(tree.props.items.at(-1).trailing.props.children.flat().join(''), defaultI18n.t('next.shell.viewOnly'));
  const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
  assert.ok(nodes(tree.props.context).some(node => node.type === 'Face' && node.props.id === 'profile'));
  assert.ok(nodes(tree.props.context).some(node => node.type === 'Text' && node.props.children.flat().join('') === 'Actual server'));
  for (const unreadNotificationCount of [undefined, 0]) {
    const quiet = TVProfileMenu({snapshot: {...snapshot, unreadNotificationCount, serverName: undefined}, open: true, onOpenChange() {}, returnFocusRef: ref});
    assert.equal(quiet.props.items.find((item: any) => item.id === 'notifications').trailing, undefined);
    assert.equal(nodes(quiet.props.context).filter(node => node.type === 'Text').length, 1, 'missing server facts never fall back to a username or demo label');
  }
  assert.deepEqual(routes, [], 'rendering or native traversal never navigates');
  tree.props.items[0].onSelect();
  assert.deepEqual(routes, [{kind: 'profiles'}]);
});

// The flyout renders nativeAccountMenu's own projection, so these cases build real ones. The
// Menu itself is the stub `'Menu'` in this harness: these assertions cover the props the caller
// hands over, which is what this file can prove. The shared first-enabled-entry policy is NOT
// exercised here - see the handback note at the end.
type MenuView = ReturnType<typeof nativeAccountMenu>;

test('the profile flyout names no initial focus, so the shared menu takes the real first action', async () => {
  const h = hooks();
  const {TVProfileMenu} = await screenModule(new URL('../src-next/shell/TVProfileMenu.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../packages/i18n/src': {defaultI18n},
    '../kit/theme': {nativeType, useTheme: () => createTheme({isTV: true, width: 1920})}, '../kit/artwork': {ProfileFace: 'Face'},
    '../kit/overlays': {Menu: 'Menu'}, './tv-focus': {tvFocusIds},
  });
  const ref = {current: null};
  const render = (view: MenuView, extra: Record<string, unknown> = {}) => TVProfileMenu({snapshot: {
    authority, account: view, profile: {id: 'profile', name: 'Demo'}, serverName: 'Actual server', unreadNotificationCount: 7, ...extra,
  }, open: true, onOpenChange() {}, returnFocusRef: ref});

  // Two profiles: Switch profile is genuinely the first drawn action; Together is drawn later.
  const switching = nativeAccountMenu({...account, profiles: [profile, {...profile, id: 'other', primary: false}]}, () => {}, true);
  const two = render(switching);
  assert.equal(two.props.initialFocusId, undefined, 'no action is focused by name');
  assert.deepEqual(two.props.items.map((item: any) => item.id), ['profiles', 'servers', 'notifications', 'together', 'server-divider', 'server']);
  assert.ok(two.props.items.some((item: any) => item.id === 'together'), 'Together stays drawn, just not first');
  assert.equal(two.props.returnFocusRef, ref);
  assert.equal(two.props.anchorRef, ref);

  // Together genuinely absent, through the projection's own availability filter: the rendered
  // items carry no Together and the caller still names no initial focus.
  const withoutTogether = nativeAccountMenu({...account, profiles: [profile, {...profile, id: 'other', primary: false}]},
    () => {}, true, route => route.kind !== 'together');
  assert.ok(!withoutTogether.actions.some(action => action.id === 'together'), 'the fixture really omits Together');
  const absent = render(withoutTogether);
  assert.ok(!absent.props.items.some((item: any) => item.id === 'together'));
  assert.equal(absent.props.initialFocusId, undefined);

  // Exact identity: the rendered onSelect is the projection's own onPress reference, not a copy
  // and not a wrapper.
  for (const id of ['profiles', 'servers', 'notifications', 'together']) {
    const source = switching.actions.find(action => action.id === id);
    const drawn = two.props.items.find((item: any) => item.id === id);
    assert.equal(drawn.onSelect, source!.onPress, `rendered ${id} keeps the action's own press`);
  }

  // Member role: the owner-only server action and its divider are not drawn at all.
  const member = nativeAccountMenu({...account, account: {...account.account, role: 'member'}, canManage: false,
    capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false}}, () => {}, true);
  const memberView = render(member);
  assert.equal(memberView.props.initialFocusId, undefined);
  assert.ok(!memberView.props.items.some((item: any) => 'separator' in item || item.id === 'server'));

  // A truly empty projection: the projection's own availability filter refuses every action,
  // which also withholds the owner server action, so nothing is drawn and nothing is named.
  const emptyProjection = nativeAccountMenu(account, () => {}, true, () => false);
  assert.deepEqual(emptyProjection.actions, []);
  assert.equal(emptyProjection.serverAction, undefined, 'the owner action is withheld too');
  const empty = render(emptyProjection);
  assert.deepEqual(empty.props.items, []);
  assert.equal(empty.props.initialFocusId, undefined);

  // Routes still arrive through the drawn entries.
  const routed: unknown[] = [];
  const recorded = render(nativeAccountMenu({...account, profiles: [profile, {...profile, id: 'other', primary: false}]}, route => {routed.push(route);}, true));
  recorded.props.items.find((item: any) => item.id === 'together').onSelect();
  recorded.props.items.find((item: any) => item.id === 'profiles').onSelect();
  assert.deepEqual(routed, [{kind: 'together'}, {kind: 'profiles'}]);
});

/**
 * SB11-03 handback, recorded in the test rather than faked: this harness stubs `Menu` as the
 * string 'Menu', so no case here can exercise the shared safe-first entry, disabled-entry policy
 * or empty-list behaviour inside Menu.tsx. The empty and role-filtered cases above prove only
 * what the caller hands over. Proving the policy needs a fixture that renders the real Menu, which
 * this file does not have; that precise limit is handed back for SB11-03.
 */
