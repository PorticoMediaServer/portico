import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule, hooks} from './helpers/component-harness.mjs';
import {installOfflineDownloads, clearOfflineDownloads, useOfflineDownloads, type OfflineDownloadsPort} from '../src/shell/offline-downloads.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {systemAnswer} from '../../packages/client-core/test/next-wire.ts';

test('porticoServerAccess disposes its session listener and ends on cross-tab account switch (signIns increment)', async () => {
  const listeners = new Set<() => void>();
  let signedIn = true;
  let signIns = 1;
  const emit = () => { for (const fn of [...listeners]) fn(); };
  const mod = await componentModule(new URL('../src/platform/portico-server-access.ts', import.meta.url), {
    '../services/account-session': {
      account: {serverIdentity: async () => ({identity: {}})},
      accountSession: {
        subscribe: (fn: () => void) => { listeners.add(fn); return () => { listeners.delete(fn); }; },
        signedIn: () => signedIn,
        signIns: () => signIns,
      },
    },
  });

  // Dispose releases the listener immediately and marks access ended.
  const first = mod.porticoServerAccess('acct_1', 'https://web.getportico.tv');
  assert.equal(first.isCurrent(), true);
  assert.equal(listeners.size, 1);
  first.dispose!();
  assert.equal(first.isCurrent(), false);
  assert.equal(listeners.size, 0, 'dispose releases accountSession listener');

  // Cross-tab account switch (signIns increments while signedIn remains true) ends access and unsubscribes.
  const second = mod.porticoServerAccess('acct_1', 'https://web.getportico.tv');
  assert.equal(second.isCurrent(), true);
  assert.equal(listeners.size, 1);
  signIns = 2;
  emit();
  assert.equal(second.isCurrent(), false, 'cross-tab sign-in ends previous account access');
  assert.equal(listeners.size, 0, 'listener cleaned up after account switch');

  // Signing out and back in never resurrects a retired access handle.
  const third = mod.porticoServerAccess('acct_2', 'https://web.getportico.tv');
  signedIn = false;
  emit();
  signedIn = true;
  emit();
  assert.equal(third.isCurrent(), false);
  assert.equal(listeners.size, 0);
});

test('offline-downloads notifies multiple concurrent subscribers on install, removal, and clear without leaking', async () => {
  clearOfflineDownloads();
  const subscribeFns: ((l: () => void) => () => void)[] = [];
  const modA = await componentModule(new URL('../src/shell/offline-downloads.ts', import.meta.url), {
    react: {
      useSyncExternalStore: (sub: (l: () => void) => () => void, snap: () => unknown) => {
        subscribeFns.push(sub);
        return snap();
      },
    },
  });
  let retired = 0;
  const port = (id: string): OfflineDownloadsPort => ({
    subscribe: () => () => {},
    snapshot: () => ({phase: 'ready', items: [{id, itemId: id, revision: 1, quality: 'high', bytes: 1, title: id, artist: '', kind: 'movie', onServer: true}]}),
    openFolder: async () => {},
    retire: () => { retired++; },
  });

  // Subscribe two independent consumers via useSyncExternalStore.
  const seenA: unknown[] = [], seenB: unknown[] = [];
  assert.equal(modA.useOfflineDownloads(), undefined);
  assert.equal(modA.useOfflineDownloads(), undefined);
  assert.equal(subscribeFns[0], subscribeFns[1], 'subscribe reference is stable across renders');
  const unsubA = subscribeFns[0]!(() => seenA.push('notified'));
  const unsubB = subscribeFns[1]!(() => seenB.push('notified'));

  const p1 = port('one'), p2 = port('two');
  const remove1 = modA.installOfflineDownloads(p1);
  assert.equal(seenA.length, 1);
  assert.equal(seenB.length, 1);
  assert.equal(modA.useOfflineDownloads(), p1);

  // Removing subscriber A does not affect subscriber B.
  unsubA();
  const remove2 = modA.installOfflineDownloads(p2);
  assert.equal(seenA.length, 1);
  assert.equal(seenB.length, 2);
  // Stale remover for p1 does not clobber p2.
  remove1();
  assert.equal(modA.useOfflineDownloads(), p2);
  assert.equal(seenB.length, 2);

  // clearOfflineDownloads notifies remaining subscribers and retires the port.
  modA.clearOfflineDownloads();
  assert.equal(modA.useOfflineDownloads(), undefined);
  assert.equal(seenB.length, 3);
  assert.equal(retired, 1);
  remove2();
  unsubB();
  // Smoke check direct import exports remain functional.
  const removeDirect = installOfflineDownloads(p1);
  removeDirect();
  assert.equal(typeof useOfflineDownloads, 'function');
});

test('ServicesApp renders IconSprite standalone and omits duplicate IconSprite when hosted with app history', async () => {
  const host = hooks();
  const mod = await componentModule(new URL('../src/services/ServicesApp.tsx', import.meta.url), {
    react: {...host.react, useLayoutEffect: () => {}, useMemo: (fn: () => unknown) => fn()},
    '@tanstack/react-router': {
      createRootRoute: (opts: unknown) => ({addChildren: () => opts}),
      createRoute: (opts: unknown) => opts,
      createRouter: (opts: unknown) => ({...opts, history: {push() {}, replace() {}}}),
      Outlet: 'Outlet',
      RouterProvider: 'RouterProvider',
      useParams: () => ({}),
    },
    '../platform/hosted-navigation': {registerInPageNavigation: () => () => {}, goTo: () => {}},
    '../kit/Icon': {IconSprite: 'IconSprite'},
    '../kit/accounts/ServicesAuth': {ServicesAuthStage: 'ServicesAuthStage'},
    '../shell/DocumentBootReady': {DocumentBootReady: 'DocumentBootReady'},
    './account-session': {accountSession: {subscribe: () => () => {}, signedIn: () => false}},
    './AccountSignIn': {AccountSignIn: 'AccountSignIn', returnPath: () => '/account'},
    './AccountSignUp': {AccountConfirm: 'AccountConfirm', AccountSignUp: 'AccountSignUp'},
    './AccountReset': {AccountReset: 'AccountReset'},
    './ApproveServer': {ApproveServer: 'ApproveServer'},
    './AccountProviders': {AccountProviderReturn: 'AccountProviderReturn'},
    './AccountWorkspace': {AccountWorkspace: 'AccountWorkspace'},
    './LinkTV': {LinkTV: 'LinkTV'},
    './JoinArrival': {JoinArrival: 'JoinArrival'},
    './AccountServerReturn': {AccountServerReturn: 'AccountServerReturn'},
    './paths': {isServicesPath: () => true},
  });
  const flatten = (node: any): any[] => Array.isArray(node) ? node.flatMap(flatten) : node?.props ? [node, ...flatten(node.props.children)] : [];

  const standalone = flatten(host.render(() => mod.ServicesApp({})));
  assert.equal(standalone.some(n => n.type === 'IconSprite'), true, 'standalone ServicesApp renders IconSprite');

  const hosted = flatten(host.render(() => mod.ServicesApp({history: {} as never})));
  assert.equal(hosted.some(n => n.type === 'IconSprite'), false, 'hosted ServicesApp omits duplicate IconSprite');
});

test('serversSignInReturnPath preserves query params and chooser hashes without leaking handoff fragments', async () => {
  const {serversSignInReturnPath} = await componentModule(new URL('../src/screens/accounts/HostedServers.tsx', import.meta.url), {
    react: {useEffect() {}, useLayoutEffect() {}, useRef: (v: unknown) => ({current: v}), useState: (v: unknown) => [v, () => {}], useSyncExternalStore: () => false},
    '@tanstack/react-router': {useRouterState: () => ''},
    '@i18n': {defaultI18n: {t: (k: string) => k, date: (v: string) => v}},
    '@core/route-identity.ts': {RouteError: Error},
    '@core/index.ts': {ApiError: Error},
    '@core/presentation/errors.ts': {presentError: () => ({silent: false, body: ''})},
    '../../platform/portico-account-holder': {porticoAccountHolder: {}},
    '../../router': {router: {}},
    '../../platform/hosted-navigation': {goTo() {}},
    '../../platform/account-entry': {createBrowserAccountEntry() {}},
    '../../platform/server-connections': {browserAppSessions() {}},
    '../../platform/app-sessions': {browserAppSessionActions() {}, signOutBrowserPorticoAccount() {}},
    '../../kit/accounts/ServerDirectory': {ServicesServerDirectory: 'ServicesServerDirectory'},
    '../../kit/accounts/DirectoryStatus': {ServicesDirectoryStatus: 'ServicesDirectoryStatus'},
    '../../kit/accounts/ServicesAuth': {HostedError: 'HostedError', ServicesAuthStage: 'ServicesAuthStage', ServicesNotice: 'ServicesNotice'},
    '../../kit/accounts/ServicesWorkspace': {ServicesHeader: 'ServicesHeader'},
    '../../kit/overlays/Dialog': {Dialog: 'Dialog'},
    '../../kit/controls': {Button: 'Button'},
    '../../shell/DocumentBootReady': {DocumentBootReady: 'DocumentBootReady'},
    '../../services/directory-flow': {ServicesDirectoryFlow: class {}},
    './hosted-fallback': {fallbackFor: () => undefined},
    './hosted-notice': {directoryNotice: () => undefined},
    '../../services/directory-errors': {DirectoryAccountUnavailable: Error},
    '../../services/time': {servicesRelativeTime: () => ''},
    '../../services/distribution': {distributionPage: ''},
    './ServerlessAccountMenu': {ServerlessAccountMenu: 'ServerlessAccountMenu'},
    './AccountSignInView': {AccountSignInView: 'AccountSignInView'},
    './sign-in-form': {AccountSignInForm: class {}},
    './chooser-resume': {resumeAfterProfiles: async () => 'entry'},
    './ChooserMove': {ChooserMove: 'ChooserMove'},
    '../../platform/invitation-carry': {carryInvitation() {}, takeCarriedInvitation: () => undefined},
    './ChooserStart': {
      ChooserJoin: 'ChooserJoin', ChooserLocalAddress: 'ChooserLocalAddress', ChooserStart: 'ChooserStart',
      keepHandoffInvitation() {},
      chooserIntent: (h: string) => h === '#join' ? 'join' : h === '#setup' ? 'setup' : h === '#local' ? 'local' : undefined,
    },
  });
  assert.equal(serversSignInReturnPath('', ''), '/servers');
  assert.equal(serversSignInReturnPath('?open=srv_1', ''), '/servers?open=srv_1');
  assert.equal(serversSignInReturnPath('', '#join'), '/servers#join');
  assert.equal(serversSignInReturnPath('', '#setup'), '/servers#setup');
  assert.equal(serversSignInReturnPath('', '#local'), '/servers#local');
  assert.equal(serversSignInReturnPath('?link=srv_1', '#join'), '/servers?link=srv_1#join');
  assert.equal(serversSignInReturnPath('', '#invitation=SECRET&address=https://x'), '/servers', 'never leaks handoff secret into ?next=');
  assert.equal(serversSignInReturnPath('', '#ticket=SECRET'), '/servers');
});

test('completeLocalOwnerMove returns true when the admitted grant lands in password-change', async () => {
  const origin = 'https://local-move.example', serverId = 'srv_' + 'm'.repeat(43);
  const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
  let raw: AppSessionRecord | undefined = {version: 1, revision: 1, selectedServerId: undefined, servers: []};
  const store = new AppSessionStore({read: async () => raw, change: async (update, beforeCommit) => { const next = update(raw); beforeCommit?.(); raw = next; }});
  const session: LocalSession = {
    accessToken: 'access-move-token-1', refreshToken: 'refresh-move-token-1', sessionFamilyId: 'family-move', tokenGeneration: '1',
    installationId: 'i'.repeat(43), deviceId: 'dev', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
    viewer: {serverId, authority: 'local', accountId: 'owner-1', profileId: 'main', role: 'owner'},
  };
  const snapshot = {
    authority: 'local' as const, serverId, canManage: true,
    capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
    account: {id: 'owner-1', username: 'owner', primaryProfileId: 'main', role: 'owner' as const, revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Main', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false}],
  };
  const connection = {pin, logicalOrigin: origin, getSnapshot: () => ({phase: 'ready'}), getMemory: () => ({paired: [origin]}), setPersistence() {}, dispose() {}};
  const api = new HttpLocalApi(origin, '');
  api.getRouteConnection = () => connection as never;
  api.request = (async (path: string) => {
    if (path === '/v1/system') return systemAnswer({id: serverId, name: 'Local Move', version: '1', signIn: 'local', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []});
    throw new Error('Unexpected ' + path);
  }) as HttpLocalApi['request'];
  const entry = new BrowserAccountEntry({
    store, actions: {} as never,
    environment: () => ({storage: store.connectionStorage(serverId)}),
    restore: async () => undefined,
    inspect: async () => pin,
    connect: (async () => ({api, connection})) as never,
    remember: async (_api, value, env) => {
      await env.storage.change(() => ({version: '1', selectionId: 's'.repeat(32), logicalOrigin: origin, pin, routes: {paired: [origin]}, session: value}));
    },
    retire: async () => {},
  }, {
    inspect: async () => ({id: serverId, name: 'Local Move', version: '1', signIn: 'local', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []}),
    getSnapshot: () => ({origin, phase: 'ready'}),
    cancel() {},
    dispose() {},
  } as never);
  const custody = {
    claim: () => ({
      serverId, selectionRevision: 1, issuer: {origin, pin},
      grant: {kind: 'signed-in' as const, snapshot, session, accountScoped: false, passwordChangeRequired: true},
    }),
    transfer: () => true,
    retire: async () => {},
  };
  const admitted = await entry.completeLocalOwnerMove(custody as never);
  assert.equal(admitted, true, 'password-change phase is recognized as an admitted continuation');
  assert.equal(entry.getSnapshot().phase, 'password-change');
  entry.dispose();
});

test('signOutBrowserPorticoAccount skips portico entries without connection.hosted (e.g. resetFileOwner)', async () => {
  const originalLocation = Object.getOwnPropertyDescriptor(globalThis, 'location');
  Object.defineProperty(globalThis, 'location', {configurable: true, value: {origin: 'https://web.getportico.tv'}});
  try {
    const signedOut: unknown[] = [];
    const mod = await componentModule(new URL('../src/platform/app-sessions.ts', import.meta.url), {
      './server-connections': {
        browserAppSessions: () => ({
          read: async () => ({
            version: 1, revision: 1, selectedServerId: 'srv_hosted',
            servers: [
              // First portico server has no hosted scope (owner reset file session).
              {signIn: 'portico', resetFileOwner: true, connection: {pin: {serverId: 'srv_reset'}}},
              // Second portico server holds the actual hosted Portico Account scope.
              {signIn: 'portico', connection: {pin: {serverId: 'srv_hosted'}, hosted: {origin: 'https://web.getportico.tv', accountId: 'acct_real'}}},
            ],
          }),
          signOutPorticoAccount: async (account: unknown) => { signedOut.push(account); return []; },
        }),
        browserConnectionEnvironment: () => ({}),
      },
      './portico-account-holder': {
        porticoAccountHolder: {
          signedIn: () => false,
          who: async () => { throw new Error('offline'); },
          signOut: async () => {},
        },
      },
    });
    await mod.signOutBrowserPorticoAccount();
    assert.deepEqual(signedOut, [{origin: 'https://web.getportico.tv', accountId: 'acct_real'}]);
  } finally {
    if (originalLocation) Object.defineProperty(globalThis, 'location', originalLocation);
    else Reflect.deleteProperty(globalThis, 'location');
  }
});
