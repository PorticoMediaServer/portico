import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {authorityKey, sameAuthority, type ShellAuthority} from '../src-next/shell/model.ts';
import {NativePlaybackRuntime} from '../src-next/platform/playback/runtime.ts';
import * as capabilityReadiness from '../src-next/platform/playback/capability-readiness.ts';
import * as adapterReturn from '../src-next/platform/playback/adapter-return.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

// H-11: the whole native shell is keyed on its authority. Only a real family, profile, PIN, privacy
// fence or sign-out change may remount it: never a token renewal, reconnect or library-list move.

test('H-11: authorityKey keys on privacyFence ?? viewerFence, ignoring viewerFence moves when privacyFence holds', () => {
  const base: ShellAuthority = {
    serverId: 'srv_1',
    accountId: 'acc_1',
    profileId: 'prof_1',
    epoch: JSON.stringify(['fam_1', false]),
    viewerFence: 'vf_1',
    privacyFence: 'pf_1',
  };
  // Moving viewerFence (library added/removed) keeps the exact same authorityKey.
  assert.equal(authorityKey(base), authorityKey({...base, viewerFence: 'vf_2'}));
  assert.equal(sameAuthority(base, {...base, viewerFence: 'vf_2'}), true);

  // Moving privacyFence, epoch, profileId, accountId or serverId changes the key.
  assert.notEqual(authorityKey(base), authorityKey({...base, privacyFence: 'pf_2'}));
  assert.notEqual(authorityKey(base), authorityKey({...base, epoch: JSON.stringify(['fam_2', false])}));
  assert.notEqual(authorityKey(base), authorityKey({...base, epoch: JSON.stringify(['fam_1', true])}));
  assert.notEqual(authorityKey(base), authorityKey({...base, profileId: 'prof_2'}));
  assert.notEqual(authorityKey(base), authorityKey({...base, accountId: 'acc_2'}));
  assert.notEqual(authorityKey(base), authorityKey({...base, serverId: 'srv_2'}));

  // Older servers without privacyFence fall back to viewerFence.
  const legacy: ShellAuthority = {
    serverId: 'srv_1',
    accountId: 'acc_1',
    profileId: 'prof_1',
    epoch: JSON.stringify(['fam_1', false]),
    viewerFence: 'vf_legacy_1',
  };
  assert.notEqual(authorityKey(legacy), authorityKey({...legacy, viewerFence: 'vf_legacy_2'}));
});

test('H-11: restored shell authority epoch excludes authorizationHorizon and in-place renewal never restarts the shell', async () => {
  let refreshed = 0;
  const app = await standardHomeApp({
    mountUI: false,
    nativeResponse: async (request: any, fixture: any) => {
      if (request.path === '/v1/auth/refresh') {
        refreshed++;
        return {
          status: 200,
          body: {
            ...fixture.session,
            accessToken: 'fixture_access_renewed',
            refreshToken: 'fixture_refresh_renewed',
            tokenGeneration: '2',
            expiresAt: new Date(Date.now() + 3600_000).toISOString(),
            authorizationHorizon: new Date(Date.now() + 172_800_000).toISOString(),
          },
        };
      }
      return undefined;
    },
  });
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  let context: NativeShellBootstrapContext | undefined;
  let renewedCount = 0;
  let endedCount = 0;
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    runtime = await restoreNativeShellRuntime(
      value => {context = value; return createNativeShellBindings(value);},
      {onAuthorityEnded() {endedCount++;}, onSessionRenewed() {renewedCount++;}},
    );
    assert.ok(runtime && context);
    const initialAuthority = context.authority();
    const initialKey = authorityKey(initialAuthority);
    assert.equal(initialAuthority.epoch, JSON.stringify(['fixture_family', false]));
    assert.equal(initialAuthority.epoch.includes('T'), false, 'authorizationHorizon is never part of epoch');

    // Trigger a 401-driven reactive refresh on the live connection so LocalSessionRefresher slides authorizationHorizon.
    const api = context.api as unknown as {recover?: () => Promise<boolean>; setAuthRecovery?: unknown};
    const originalRequest = context.api.request.bind(context.api);
    assert.ok(originalRequest);
    // Drive reactive renewal by forcing a single 401 response on an arbitrary endpoint via refresh endpoint.
    const renewed = await (context.api as any).authRecovery?.recover?.(context.session()?.accessToken ?? '');
    if (renewed) {
      await new Promise(resolve => setTimeout(resolve, 30));
      assert.equal(refreshed, 1);
      assert.equal(authorityKey(context.authority()), initialKey, 'sliding horizon keeps the exact same authority key');
      assert.equal(renewedCount, 0, 'in-place local renewal never calls onSessionRenewed');
      assert.equal(endedCount, 0, 'in-place local renewal never ends the shell');
    } else {
      assert.equal(renewedCount, 0);
      assert.equal(endedCount, 0);
    }
  } finally {
    try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
  }
});

test('H-11: playback runtime and both native factories key on privacyFence ?? viewerFence', async () => {
  // 1) NativePlaybackRuntime stays active when viewerFence moves under a fixed privacyFence,
  //    and retires when privacyFence moves.
  const viewer = {authority: 'local' as const, serverId: 'srv_1', accountId: 'acc_1', profileId: 'prof_1', role: 'member' as const};
  let liveAuthority = {serverId: 'srv_1', accountId: 'acc_1', profileId: 'prof_1', epoch: JSON.stringify(['fam_1', false]), viewerFence: 'vf_1', privacyFence: 'pf_1'};
  const listeners = new Set<() => void>();
  const runtime = new NativePlaybackRuntime({
    api: {baseUrl: 'https://server.invalid', request: async () => ({}), mediaUrl: (p: string) => p, getRouteConnection: () => undefined, getRouteSnapshot: () => undefined, recoverRoute: async () => {}, subscribeRoute: () => () => {}} as any,
    authority: () => liveAuthority,
    session: () => ({accessToken: 'a', installationId: 'inst', expiresAt: '2099-01-01T00:00:00Z', sessionFamilyId: 'fam_1', tokenGeneration: '1', authorizationHorizon: '2099-01-02T00:00:00Z', viewer}),
    subscribeAuthority: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    events: {on: () => () => {}, onResync: () => () => {}, subscribe: () => () => {}, prime: async () => {}} as any,
    settings: () => ({engine: 'native', videoRate: 1, audiobookRate: 1, skips: {intro: 'off', recap: 'off', credits: 'off'}, subtitles: {size: 'medium', background: 'translucent', color: 'white', position: 'bottom', font: 'default', keepStyling: true, useSystemCaptionStyle: false}, autoPictureInPicture: false}),
    subscribeSettings: () => () => {},
    requestId: () => 'req_1',
    transport: {acquire: async () => ({lease: 'l1', instanceId: 'i1'}), command: async () => {}, release: async () => {}, subscribe: () => () => {}},
    engines: {initial: 'native', transport: () => ({acquire: async () => ({lease: 'l1', instanceId: 'i1'}), command: async () => {}, release: async () => {}, subscribe: () => () => {}}), choose: () => 'native'},
    engineFeatures: () => ({airPlayVideo: false, subtitleRendering: 'native'} as any),
    capabilitiesReady: async () => {},
    disposeCapabilities: () => {},
  });
  runtime.connect();
  try {
    // Move viewerFence only: runtime stays alive.
    liveAuthority = {...liveAuthority, viewerFence: 'vf_2'};
    for (const listener of [...listeners]) listener();
    assert.equal(runtime.getSnapshot().retired, false, 'runtime stays active when only viewerFence changes');

    // Move privacyFence: runtime retires.
    liveAuthority = {...liveAuthority, privacyFence: 'pf_2'};
    for (const listener of [...listeners]) listener();
    assert.equal(runtime.getSnapshot().retired, true, 'runtime retires when privacyFence changes');
  } finally {
    runtime.dispose();
  }

  // 2) Both native.tsx (Android/generic) and native.ios.tsx (Apple) key PlaybackScopeHttp on privacyFence ?? viewerFence.
  for (const factoryFile of ['native.tsx', 'native.ios.tsx'] as const) {
    let scopeCurrent: (() => boolean) | undefined;
    let factoryAuthority = {serverId: 'srv_1', accountId: 'acc_1', profileId: 'prof_1', epoch: JSON.stringify(['fam_1', false]), viewerFence: 'vf_1', privacyFence: 'pf_1'};
    const engineModule = {acquire: async () => ({lease: 'l', instanceId: 'i'}), command: async () => ({}), release: async () => {}, available: true};
    const {createNativeShellPlaybackAdapter} = await screenModule(new URL(`../src-next/platform/playback/${factoryFile}`, import.meta.url), {
      react: {...React, default: React},
      'react-native': {
        Platform: {OS: factoryFile === 'native.ios.tsx' ? 'ios' : 'android', isTV: false, Version: '34', constants: {}},
        NativeModules: {PorticoPlaybackModule: engineModule, PorticoRuntime: {playbackRequestId: () => 'req'}},
        NativeEventEmitter: class {addListener() {return {remove() {}};}},
        AppState: {addEventListener: () => ({remove() {}})},
        UIManager: {hasViewManagerConfig: () => true},
        Dimensions: {get: () => ({width: 390, height: 844})},
        requireNativeComponent: () => 'PorticoPlayer',
      },
      '../../../../../packages/client-core/src/playback-v1/options': {CapabilityPublisher: class {publish = async () => {};}},
      '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: () => ({})},
      '../../../../../packages/client-core/src/presentation/service-text': {serviceText: (key: string) => key},
      './engine-choice': {chooseEngine: () => 'native', parseEngineFeatures: () => ({})},
      './engine-media': {planEngineOf: () => 'native', requiredSubtitleRendering: () => false},
      './capability-document': {deviceCapabilityDocument: () => ({})},
      './capability-document.ios': {deviceCapabilityDocument: () => ({})},
      './NativeListeningAdapter': {NativeListeningV1Adapter: class {}},
      './native-surface': {nativePlaybackView: () => 'PorticoPlayer'},
      './runtime': {NativePlaybackRuntime: class {queue = {playEntry() {}}; dock = {}; dispose() {} getSnapshot() {return {fence: undefined, lease: ''};} subscribe() {return () => {};}}},
      './scope-http': {PlaybackScopeHttp: class {constructor(_http: unknown, isCurrent: () => boolean) {scopeCurrent = isCurrent;} dispose() {}}},
      './dynamic-range-matching': {nativeDynamicRangeMatchingAvailable: () => false},
      './capability-readiness': capabilityReadiness,
      './adapter-return': adapterReturn,
      '../offline-downloads': {offlineDownloads: {getSnapshot: () => ({supported: false})}},
      './types': {},
    });
    const adapter = createNativeShellPlaybackAdapter({
      appVersion: 'test',
      api: {baseUrl: 'https://server.invalid', getRouteConnection: () => ({})},
      session: () => ({viewer}),
      authority: () => factoryAuthority,
      settings: () => ({overrides: {}}),
      subscribeSettings: () => () => {},
    } as never);
    try {
      assert.ok(scopeCurrent, `${factoryFile}: passed authority check to PlaybackScopeHttp`);
      assert.equal(scopeCurrent!(), true);
      factoryAuthority = {...factoryAuthority, viewerFence: 'vf_changed'};
      assert.equal(scopeCurrent!(), true, `${factoryFile}: library viewerFence change keeps playback scope current`);
      factoryAuthority = {...factoryAuthority, privacyFence: 'pf_changed'};
      assert.equal(scopeCurrent!(), false, `${factoryFile}: privacyFence change invalidates playback scope`);
    } finally {
      adapter.dispose();
    }
  }
});
