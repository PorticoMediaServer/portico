import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

/**
 * FX-224 regression source: the actual General and Remote presenters render the
 * real FormSet's `saveError` through the shared inline retry state, while the
 * owner's unsaved draft, the separate local problem, partial multi-form failure
 * and page/authority retirement keep their existing behaviour.
 *
 * The standard-home app supplies the actual bootstrapped runtime, real module
 * resolution and the mock native host boundary; the shared fabric-host helper
 * mounts each product presenter on the same installed Fabric reconciler. The
 * real Settings ports, ConsoleClient/DraftForm/FormSet code and server form
 * services run unchanged. Only the native transport answers are controlled.
 * The cases remain unrun until the normal authorized test run.
 */

const viewerFence = 'b'.repeat(64);

/** The canonical runtime settings document used by the ConsoleClient tests. */
const runtimeValues = {
  name: 'My server', transcodingEnabled: true, conversionMode: 'all', outputFormat: 'auto', downloadMaxQuality: 'original', musicAwayQuality: 'original',
  perAccountCap: null, serverCap: null, diagnosticDays: 30, notificationDays: 180, jobDays: 30,
  hardwareBackend: 'auto', hardwareDevice: '', hdrToneMapping: false, hdrToneMappingAlgorithm: 'hable', x264Preset: 'veryfast', directStreamRemux: true,
  analysisStorageGiB: 32, analysisStageStorageMiB: 256, subtitleTrackLimit: 128, subtitleFileLimitMiB: 128, planningPolicy: 'maximum_fidelity',
  throttleBufferMs: 60000, playedRetentionMs: 180000, temporaryDirectory: '', ffmpegPath: '', sessionStorageMiB: 4096,
  maxConcurrentSessions: 0, maxHardwareSessions: 0, maxSoftwareSessions: 0, maxBackgroundSessions: 0,
  playHistoryDays: 0, 'home.communityActivityEnabled': false, 'updates.releaseChannel': 'stable', 'updates.feedUrl': '',
  'library.missingTitles': 'after_days', 'library.missingTitlesDays': 30, 'library.segmentDetection': 'standard',
  remoteSignInPolicy: 'allow', remoteBitrateLimitKbps: 0, lanNetworks: [], accessUrls: [], lanDiscoveryEnabled: true, dlnaEnabled: false, secureConnections: 'preferred',
  trustedProxies: [], uploadCapacityKbps: 0, pausedSessionTimeoutMinutes: 0, advertisedInterface: '', ownAddressEnabled: false,
  customCertificatePath: '', customCertificateKeyPath: '', customCertificateDomain: '', deviceApprovalRequired: false,
  connectionLimit: 0, advertiseUrl: '', apiRequestsPerMinute: 0, blockMemberAccessTokens: false, logLevel: 'info', logRetention: [],
};

const runtimeDocument = (revision: number, requested: typeof runtimeValues) => ({
  revision, digest: 'b'.repeat(64), activeRevision: revision, registryRevision: 'p08.0', restartFields: [],
  requested, effective: requested,
});

const maintenanceDocument = () => ({
  revision: 1, pausedUntilUnixMillis: 0,
  settings: {windows: [], retention: {}, backupKeepCount: 0, backgroundSpeed: 'normal'},
  cadences: [], tasks: [], days: [], retentionMaxima: {}, storageCategories: [],
});

const updateReport = {
  current: {version: '1.0', buildId: 'build-1', sourceDigest: 'd'.repeat(64), builtAt: '2026-10-01T00:00:00Z'},
  channel: 'stable', feedConfigured: false, latest: null, updateAvailable: false,
  checkedAt: '2026-10-01T00:00:00Z', status: 'no-feed', message: 'No update feed is configured.', autoInstall: false,
};

const connectivityStatus = () => ({
  connection: {secure: false, remote: false, plainHttpOutsideHome: false, clientAddress: '127.0.0.1', accessUrls: []},
  observedAt: '2026-10-01T00:00:00Z',
  policy: {revision: 7, remoteSignInPolicy: 'owner-only', remoteBitrateLimitKbps: 8000, connectionLimit: 0, advertiseUrl: '',
    lanNetworks: [], accessUrls: ['https://media.example.com'], lanDiscoveryEnabled: false, dlnaEnabled: false,
    trustedProxies: [], uploadCapacityKbps: 0, pausedSessionTimeoutMinutes: 0, advertisedInterface: '',
    ownAddressEnabled: false, customCertificatePath: '', customCertificateKeyPath: '', customCertificateDomain: ''},
  tls: {configured: false, tlsReady: false, publiclyTrusted: false, listenerBound: false, listenPort: 8443},
  discovery: {enabled: false, supported: true, state: 'stopped', serviceType: '_portico._tcp.local.'},
  addresses: [], interfaces: [], warnings: [],
});

const remoteStatus = (config: Record<string, unknown> = {}) => ({
  authorityId: 'authority', generation: '1', state: 'lan_only',
  config: {revision: '3', enabled: false, mapping: false, pcp: false, natpmp: false, upnp: false, publicPort: 32500, gateway: '', ...config},
  topology: {lan: [], public: [], bind: ''}, candidates: [], mappings: [],
});

const textOf = (value: unknown): string => Array.isArray(value)
  ? value.map(textOf).join('')
  : typeof value === 'string' ? value
  : typeof value === 'function' ? textOf((value as (state: {pressed: boolean}) => unknown)({pressed: false}))
  : value && typeof value === 'object' && 'props' in value ? textOf((value as {props?: {children?: unknown}}).props?.children)
  : '';

async function presenterFixture(t: any) {
  t.mock.timers.enable({apis: ['Date'], now: Date.now()});
  const failure = {consoleWrite: false, consoleConflict: false, maintenanceWrite: false, remoteWrite: false};
  const app = await standardHomeApp({
    accountRole: 'owner',
    mountUI: false,
    nativeResponse: async (request: any, fixture: any) => {
      const envelope = (data: unknown) => ({scope: {serverId: fixture.pin.serverId, viewerFence}, data});
      if (request.path === '/v1/admin/console/settings' && request.method === 'GET') return {status: 200, body: envelope(runtimeDocument(1, {...runtimeValues}))};
      // The server contract saves the owner settings document as a revision-guarded
      // PATCH (change_console_settings); ConsoleClient sends that same method.
      if (request.path === '/v1/admin/console/settings' && request.method === 'PATCH') {
        if (failure.consoleConflict) return {status: 409, body: {error: {code: 'console_conflict', message: 'The document changed.', currentRevision: 2}}};
        if (failure.consoleWrite) throw new Error('synthetic transport failure');
        return {status: 200, body: envelope(runtimeDocument(2, request.body.values))};
      }
      if (request.path === '/v1/admin/maintenance/settings' && request.method === 'GET') return {status: 200, body: {protocolVersion: '1', serverId: fixture.pin.serverId, result: maintenanceDocument()}};
      if (request.path === '/v1/admin/maintenance/settings' && request.method === 'PUT') {
        if (failure.maintenanceWrite) throw new Error('synthetic maintenance failure');
        return {status: 200, body: {protocolVersion: '1', serverId: fixture.pin.serverId, result: {...maintenanceDocument(), revision: 2, settings: request.body.settings}}};
      }
      if (request.path === '/v1/admin/updates' && request.method === 'GET') return {status: 200, body: updateReport};
      if (request.path === '/v1/admin/connectivity/status' && request.method === 'GET') return {status: 200, body: connectivityStatus()};
      if (request.path === '/v1/networking/remote' && request.method === 'GET') return {status: 200, body: remoteStatus()};
      if (request.path === '/v1/networking/remote/config' && request.method === 'POST') {
        if (failure.remoteWrite) throw new Error('synthetic remote failure');
        return {status: 200, body: remoteStatus({...request.body.config, revision: '4'})};
      }
      if (request.path === '/v1/networking/certificate' && request.method === 'GET') return {status: 404, body: {error: {code: 'not_found', message: 'No custom certificate.'}}};
      return undefined;
    },
  });
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime');
  const {createNativeSettingsPorts} = await import('../src-next/screens/settings-live/settings-ports');
  const {ThemeProvider} = await import('../src-next/kit/theme');
  const {NativeServerGeneral} = await import('../src-next/screens/settings-live/server/GeneralPage');
  const {NativeRemoteAccessPage} = await import('../src-next/screens/settings-live/server/RemoteAccessPage');
  const renderer = await fabricHost();
  let captured: NativeShellBootstrapContext | undefined;
  const runtime: RestoredNativeShellRuntime | undefined = await restoreNativeShellRuntime(
    context => {captured = context; return createNativeShellBindings(context);},
    {onAuthorityEnded() {}},
  );
  const context = captured;
  assert.ok(runtime);
  assert.ok(context);
  const lifetime = new NativeScreenLifetime(context);
  const ports = createNativeSettingsPorts(context, lifetime, () => {});
  const hosts = () => app.hosts() as any[];
  const byTestId = (id: string) => hosts().find(host => host.props?.testID === id);
  const pressable = (label: string) => hosts().find(host => host.name === 'Pressable' && app.nativeLabel(host) === label);
  const text = () => hosts().map(host => textOf(host.children)).join('\n');
  const writes = (path: string, method: string) => app.requests.filter((request: any) => request.path === path && request.method === method);
  const settle = async () => {await renderer.settle(); await app.settle();};
  const mount = async (presenter: 'general' | 'remote') => {
    await renderer.render(React.createElement(ThemeProvider, {width: 390, children:
      React.createElement(presenter === 'general' ? NativeServerGeneral : NativeRemoteAccessPage, {ports, runtime})}));
    await settle();
  };
  const unmount = async () => {await renderer.unmount(); await app.settle();};
  // The actual restored bootstrap context, not the separate ports lifetime: its
  // retirement is what ends the mounted group and every captured transport.
  let retired = false;
  const contextCurrent = () => context.current();
  const retireRuntime = async () => {if (retired) return; retired = true; runtime?.dispose(); await settle();};
  t.after(async () => {
    try {
      await renderer.unmount();
    } finally {
      try {
        await app.unmount();
      } finally {
        try {
          app.disposeLoader();
        } finally {
          try {
            lifetime.dispose();
          } finally {
            if (!retired) runtime?.dispose();
          }
        }
      }
    }
  });
  // Ambiguous delivery stays pending and retries the immutable key. Advance
  // only the coordinator's wall clock, then explicitly reconcile its real CAS
  // state; app/Fabric timers still use the real event loop.
  const failPending = async () => {
    for (let attempt = 0; attempt < 4; attempt++) {
      t.mock.timers.tick(60_000);
      await context.api.getUserMutationCoordinator()!.reconcile();
      await settle();
    }
  };
  const retryPending = async () => {
    t.mock.timers.tick(60_000);
    await context.api.getUserMutationCoordinator()!.reconcile();
    await settle();
  };
  return {app, failure, failPending, retryPending, lifetime, hosts, byTestId, pressable, text, writes, settle, mount, unmount, contextCurrent, retireRuntime};
}

const press = (app: any, host: any) => host.props.onPress({nativeEvent: {}, stopPropagation() {}});

test('a rejected General save shows the real FormSet saveError, keeps the draft and retries the original operation', async t => {
  const f = await presenterFixture(t);
  await f.mount('general');
  const name = f.byTestId('server-general-name');
  assert.ok(name, 'the real General presenter rendered its name field');
  assert.equal(name.props.value, 'My server');
  name.props.onChangeText('Rejected name');
  await f.settle();
  const save = f.pressable('Save');
  assert.ok(save, 'the dirty form offers the page Save');
  f.failure.consoleWrite = true;
  press(f.app, save);
  await f.settle();
  await f.failPending();
  assert.match(f.text(), /Something went wrong\. Try again\./, 'the rejected write projects the shared inline error state');
  assert.equal(f.byTestId('server-general-name').props.value, 'Rejected name', 'the unsaved draft is retained');
  assert.equal(f.writes('/v1/admin/console/settings', 'PATCH').length, 5, 'bounded transport retries keep one original intent');
  const original = f.writes('/v1/admin/console/settings', 'PATCH')[0].body.idempotencyKey;
  assert.equal(typeof original, 'string');
  f.failure.consoleWrite = false;
  const retry = f.pressable('Try again');
  assert.ok(retry, 'the visible error offers the shared retry action');
  press(f.app, retry);
  await f.retryPending();
  const retried = f.writes('/v1/admin/console/settings', 'PATCH');
  assert.equal(retried.length, 6);
  assert.ok(retried.every(request => JSON.stringify(request.body) === JSON.stringify(retried[0].body)), 'every physical retry keeps the original body');
  assert.equal(retried[1].body.idempotencyKey, original, 'the retry re-sends the same original operation identity');
  assert.equal(f.byTestId('server-general-name').props.value, 'Rejected name');
  assert.equal(f.pressable('Try again'), undefined, 'the accepted save clears the inline error');
  assert.equal(f.pressable('Save'), undefined, 'the accepted save clears dirty');
});

test('a conflict retains the General draft and reports the changed document instead of a success', async t => {
  const f = await presenterFixture(t);
  await f.mount('general');
  f.byTestId('server-general-name').props.onChangeText('Conflicted name');
  await f.settle();
  f.failure.consoleConflict = true;
  press(f.app, f.pressable('Save'));
  await f.settle();
  assert.match(f.text(), /This changed while you were looking at it\. Refresh to see the latest\./);
  assert.equal(f.byTestId('server-general-name').props.value, 'Conflicted name', 'the owner draft survives the out-of-date answer');
  assert.ok(f.pressable('Refresh'), 'the changed document offers its own refresh action');
  assert.equal(f.pressable('Try again'), undefined);
  assert.ok(f.pressable('Save'), 'a failed save leaves the page dirty');
  assert.ok(f.app.receipts.filter((receipt: any) => receipt.path === '/v1/admin/console/settings' && receipt.method === 'PATCH').every((receipt: any) => receipt.status === 409));
});

test('a local General validation problem stays separate from saveError and blocks Save', async t => {
  const f = await presenterFixture(t);
  await f.mount('general');
  f.byTestId('server-general-name').props.onChangeText('');
  await f.settle();
  assert.match(f.text(), /The server needs a name\./, 'the form-authored problem is shown in place');
  const save = f.pressable('Save');
  assert.ok(save);
  assert.equal(save.props.disabled, true, 'the local problem disables the page Save');
  assert.equal(save.props.onPress, undefined);
  assert.equal(f.pressable('Try again'), undefined, 'no persistence error state is invented for a local problem');
  assert.equal(f.writes('/v1/admin/console/settings', 'PATCH').length, 0);
});

test('a partial multi-form failure keeps the unsaved General form and still reports it', async t => {
  const f = await presenterFixture(t);
  await f.mount('general');
  f.byTestId('server-general-name').props.onChangeText('Stored name');
  await f.settle();
  // MOB-082: background speed is one choice row (Select) rather than three buttons.
  press(f.app, f.byTestId('general.backgroundSpeed'));
  await f.settle();
  press(f.app, f.byTestId('general.backgroundSpeed-option-fast'));
  await f.settle();
  f.failure.maintenanceWrite = true;
  press(f.app, f.pressable('Save'));
  await f.settle();
  await f.failPending();
  assert.match(f.text(), /Something went wrong\. Try again\./, 'the failed maintenance form keeps the page error visible');
  assert.equal(f.byTestId('server-general-name').props.value, 'Stored name', 'the first form kept the accepted value');
  assert.ok(f.pressable('Save'), 'the unsaved maintenance form keeps the page dirty');
  const runtimeWrites = f.writes('/v1/admin/console/settings', 'PATCH');
  const maintenanceWrites = f.writes('/v1/admin/maintenance/settings', 'PUT');
  assert.equal(runtimeWrites.length, 1, 'the runtime form saved first');
  assert.equal(maintenanceWrites.length, 5, 'the maintenance form retains its intent across bounded transport retries');
  assert.ok(f.app.receipts.some((receipt: any) => receipt.path === '/v1/admin/console/settings' && receipt.method === 'PATCH' && receipt.status === 200));
});

test('a really unmounted General page refuses its captured retry', async t => {
  const f = await presenterFixture(t);
  await f.mount('general');
  f.byTestId('server-general-name').props.onChangeText('Unmounted name');
  await f.settle();
  f.failure.consoleWrite = true;
  press(f.app, f.pressable('Save'));
  await f.settle();
  await f.failPending();
  const unmountedRetry = f.pressable('Try again');
  assert.ok(unmountedRetry);
  await f.unmount();
  const beforeUnmount = f.app.requests.length;
  press(f.app, unmountedRetry);
  await f.settle();
  assert.equal(f.app.requests.length, beforeUnmount, 'the unmounted page never dispatches through its retired form group');
});

test('a retired original bootstrap authority refuses its captured retry', async t => {
  const f = await presenterFixture(t);
  await f.mount('general');
  f.byTestId('server-general-name').props.onChangeText('Replaced name');
  await f.settle();
  f.failure.consoleWrite = true;
  press(f.app, f.pressable('Save'));
  await f.settle();
  await f.failPending();
  const replacedRetry = f.pressable('Try again');
  assert.ok(replacedRetry);
  assert.equal(f.contextCurrent(), true, 'the mounted page borrows the live bootstrap context before retirement');
  await f.retireRuntime();
  assert.equal(f.contextCurrent(), false, 'the actual original bootstrap authority is retired, not only the ports lifetime');
  const beforeRetirement = f.app.requests.length;
  press(f.app, replacedRetry);
  await f.settle();
  assert.equal(f.app.requests.length, beforeRetirement, 'the retired original authority refuses the captured retry');
});

test('a rejected Remote access save projects the same inline state and retries the same configuration', async t => {
  const f = await presenterFixture(t);
  await f.mount('remote');
  const toggle = f.byTestId('remote-access-enabled-switch');
  assert.ok(toggle, 'the real Remote access presenter rendered its switch');
  press(f.app, toggle);
  await f.settle();
  f.failure.remoteWrite = true;
  press(f.app, f.pressable('Save'));
  await f.settle();
  await f.failPending();
  assert.match(f.text(), /Something went wrong\. Try again\./);
  assert.ok(f.pressable('Save'), 'the rejected save keeps the remote form dirty');
  const writes = f.writes('/v1/networking/remote/config', 'POST');
  assert.equal(writes.length, 5);
  assert.equal(writes[0].body.config.enabled, true, 'the retry keeps the owner edit');
  f.failure.remoteWrite = false;
  press(f.app, f.pressable('Try again'));
  await f.retryPending();
  const retried = f.writes('/v1/networking/remote/config', 'POST');
  assert.equal(retried.length, 6);
  assert.ok(retried.every(request => JSON.stringify(request.body) === JSON.stringify(retried[0].body)), 'every physical retry keeps the original body');
  assert.deepEqual(retried[0].body, retried[1].body, 'the retry re-sends the same original configuration');
  assert.equal(f.pressable('Try again'), undefined, 'the accepted save clears the inline error');
  assert.equal(f.pressable('Save'), undefined, 'the accepted save clears dirty');
});
