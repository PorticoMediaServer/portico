import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import type {NativeShellRoute} from '../src-next/shell/routes';

/**
 * NM-ID-02 / BLK-TV-005 / FX-155 account-overlay source. The standard-home app
 * supplies the actual bootstrapped runtime, real module resolution and native
 * host boundary; fabric-host mounts the actual AccountRoute on the installed
 * Fabric reconciler with the product focus registry and a TV theme. A healthy
 * in-memory server answers the real ManagedProfilesClient, IdentityClient and
 * restrictions reads, so the picker renders through the real account screen. Element state is UNRUN here.
 */

const profile = () => ({id: 'profile', name: 'Fixture profile', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: true});
const accountSnapshot = () => ({
  authority: 'local', serverId: '', canManage: true,
  capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
  account: {id: 'account', username: 'fixture_owner', primaryProfileId: 'profile', role: 'owner', revision: 1, disabled: false, allowedLibraries: ['movies']},
  profiles: [profile()],
  session: {sessionFamilyId: 'fixture_family'},
});
const restrictions = (revision = 1) => ({profileId: 'profile', ratingSystem: 'mpaa', maximumAgeRating: 'PG', maximumAge: 10, blockedLabels: [],
  allowUnrated: true, allowDownloads: true, allowLiveTv: true, allowDvr: true, allowWatchTogether: true, revision});
const ratingSystems = () => ({items: [{id: 'mpaa', name: 'MPAA', region: 'US', screens: [], values: [{code: 'PG', label: 'PG', minimumAge: 10}]}]});

test('NM-ID-02: the real TV profile picker offers only the faces, with no Add or Manage profiles', async () => {
  const writes: {path: string; method: string; body?: unknown}[] = [];
  const app = await standardHomeApp({accountRole: 'owner', mountUI: false, nativeResponse: async (request: any, fixture: any) => {
    // The real direct-account read is the contract's `get_direct_account` = /v1/direct.
    if (request.path === '/v1/direct' && request.method === 'GET') return {status: 200, body: {...accountSnapshot(), serverId: fixture.pin.serverId}};
    if (request.path === '/v1/direct/profiles' && request.method === 'POST') {writes.push(request); return {status: 200, body: {}};}
    if (request.path === '/v1/direct/profiles/profile' && request.method === 'PATCH') {writes.push(request); return {status: 200, body: {}};}
    if (request.path === '/v1/direct/trust' && request.method === 'DELETE') {writes.push(request); return {status: 200, body: {}};}
    if (request.path === '/v1/direct/profiles/profile/restrictions' && request.method === 'GET') return {status: 200, body: restrictions()};
    if (request.path === '/v1/direct/profiles/profile/restrictions' && request.method === 'PUT') {
      writes.push(request);
      return {status: 200, body: restrictions(2)};
    }
    if (request.path === '/v1/rating-systems' && request.method === 'GET') return {status: 200, body: ratingSystems()};
    if (request.path === '/v1/direct/profiles/avatars' && request.method === 'GET') return {status: 200, body: {items: []}};
    return undefined;
  }});
  const host = await fabricHost();
  let runtime: RestoredNativeShellRuntime | undefined;
  let context: NativeShellBootstrapContext | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {createFocusRegistry} = await import('../src-next/kit/focus/registry.ts');
    const {FocusProvider} = await import('../src-next/kit/focus/FocusRegion.tsx');
    const {ThemeProvider} = await import('../src-next/kit/theme.ts');
    const {AccountRoute} = await import('../src-next/screens/accounts/AccountRoutes.tsx');
    const {defaultI18n} = await import('../../../packages/i18n/src/index.ts');
    const {Platform} = await import('react-native');

    runtime = await restoreNativeShellRuntime(next => {context = next; return createNativeShellBindings(next);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    assert.ok(context);
    const booted = context;
    const route: NativeShellRoute = {kind: 'profiles'};
    assert.equal(booted.navigation.open(route, booted.authority()), true, 'the real navigation opened the profiles route the lease captures');
    const priorTV = Platform.isTV;
    (Platform as {isTV: boolean}).isTV = true;
    const registry = createFocusRegistry();
    const nodes = () => {
      const visit = (items: readonly any[]): any[] => items.flatMap(node => [node, ...visit(node.children ?? [])]);
      // standardHomeApp materialises the product's View/Text/Pressable boundaries in its own
      // host registry; host.root only carries the rendered raw text.
      return [...visit(host.root), ...app.hosts()];
    };
    const nodeWithTestID = (testID: string) => nodes().find(node => node.props?.testID === testID);
    const label = (node: any) => app.nativeLabel(node);
    const settle = async () => {await host.settle(); await app.settle();};

    try {
      await host.render(React.createElement(ThemeProvider, {width: 1920, children: React.createElement(FocusProvider, {registry, children:
        React.createElement(AccountRoute, {route, context: booted})})}));
      await settle();
      assert.equal(registry.order('profile-add').length, 0, 'no overlay is mounted before it is opened');

      // F-PROFILE-7 and design 6212c813: a picker has no Add or Manage profiles. On TV the
      // Settings Profiles row opens this same picker, so TV offers no profile management at
      // all; owners add and manage profiles in Settings on web or phone.
      assert.equal(nodes().some(node => node.name === 'Pressable' && label(node) === defaultI18n.t('profile.add')), false, 'the TV picker offers no Add profile');
      for (const testID of ['profile-actions', 'profile-picture', 'profile-rename', 'profile-pin', 'profile-limits', 'profile-forget-devices'])
        assert.equal(nodeWithTestID(testID), undefined, `the TV picker draws no ${testID} management row`);
      assert.equal(registry.order('profile-add').length, 0, 'no profile dialog region is attached');
      const pressable = nodes().filter(node => node.name === 'Pressable' && !node.props?.disabled).map(label);
      assert.deepEqual(pressable, ['Fixture profile'], 'only the faces are focus stops');
      assert.equal(writes.length, 0);
    } finally {
      (Platform as {isTV: boolean}).isTV = priorTV;
      await host.unmount();
    }
  } finally {
    try {
      await host.unmount();
    } finally {
      try {
        await app.unmount();
      } finally {
        try {
          app.disposeLoader();
        } finally {
          runtime?.dispose();
        }
      }
    }
  }
});
