import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {createNativeSettingsDownloads} from '../src-next/screens/settings-live/downloads-summary';

async function fixture() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
  let current = true, backs = 0;
  const listeners = new Set<() => void>();
  const context = {
    authority: () => authority, current: () => current,
    subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    navigation: {back: (actual: unknown) => {assert.deepEqual(actual, authority); backs++;}},
  };
  const {react} = hooks();
  const module = await screenModule(new URL('../src-next/screens/settings-live/SettingsRoute.tsx', import.meta.url), {
    react, 'react-native': {Platform: {isTV: true, OS: 'android'}},
    '../../../../../packages/client-core/src/playback-v1/admin-sessions': {},
    '../../../../../packages/client-core/src/playback-v1/local-http': {},
    '../../../../../packages/client-core/src/console': {},
    './lifetime': {NativeScreenLifetime}, './settings-controller': {}, './device-preference-consumer': {},
    './dashboard-controller': {}, './server/tv-server-reader': {}, './server/TVServerScreen': {}, './SettingsScreen': {}, './DashboardScreen': {}, './settings-ports': {},
    './server/TVServerPageMenu': {isTVServerPage: () => false},
    './EqualizerSettingsScreen': {NativeEqualizerSettingsScreen: () => null},
    './downloads-summary': {createNativeSettingsDownloads},
    '../../platform/event-resync': {readEventState: (_events: unknown, _signal: AbortSignal, read: () => Promise<unknown>) => read()},
  });
  const renderer = module.createNativeEqualizerSettingsRenderer(context);
  const rendered = renderer.render({kind: 'settings-equalizer'}, {});
  return {renderer, back: rendered.props.onBack as () => void, backs: () => backs, listeners,
    switchProfile: () => {authority.profileId = 'other'; for (const listener of listeners) listener();},
    leave: () => {current = false;},
  };
}

test('Equalizer Back navigates once while its viewer lifetime remains current', async () => {
  const f = await fixture();
  try {f.back(); assert.equal(f.backs(), 1);} finally {f.renderer.dispose();}
  assert.equal(f.listeners.size, 0);
});

for (const retirement of ['dispose', 'profile', 'context'] as const) test(`retained Equalizer Back rejects a stale ${retirement} lifetime`, async () => {
  const f = await fixture();
  try {
    if (retirement === 'dispose') f.renderer.dispose();
    else if (retirement === 'profile') f.switchProfile();
    else f.leave();
    assert.throws(f.back, {name: 'AbortError', message: 'native_screen_scope_ended'});
    assert.equal(f.backs(), 0);
  } finally {f.renderer.dispose();}
  assert.equal(f.listeners.size, 0);
});

test('Equalizer renderer rejects another destination before exposing its editor', async () => {
  const f = await fixture();
  try {assert.throws(() => f.renderer.render({kind: 'server-dashboard'}, {}), /native_equalizer_settings_route_mismatch/);}
  finally {f.renderer.dispose();}
  assert.equal(f.backs(), 0);
});
