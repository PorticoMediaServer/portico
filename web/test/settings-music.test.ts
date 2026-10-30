import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {settingsSpec, settingsRowIndex} = await import('../../packages/client-core/src/presentation/settings-spec.ts');
const {settingsBinding} = await import('../../packages/client-core/src/presentation/settings-bindings.ts');
const {hasWebPreferenceEffect} = await import('../src/screens/settings/effects.ts');
const {MusicEqualizerRow, musicEqualizerAvailable} = await import('../src/screens/settings/account-music/MusicEqualizerRow.tsx');
const {flatEqualizer} = await import('../src/platform/playback/audio/equalizer.ts');

const row = (id: string) => {
  const location = settingsRowIndex.get(id);
  assert.ok(location, id);
  return location.row;
};

test('Account Music exposes only the rows with a real web or server consumer', () => {
  const exposed = ['music.audioNormalization', 'music.crossfadeMs', 'music.smoothTransitions', 'music.gapless',
    'music.neverBoost', 'music.dsdGainDb', 'music.qualityAway',
    'audiobooks.skipBackMs', 'audiobooks.skipForwardMs', 'audiobooks.resumeRewindMs'];
  for (const id of exposed) {
    const binding = settingsBinding(id);
    assert.equal(binding?.form, 'preferences', id);
    assert.equal(hasWebPreferenceEffect(row(id)), true, id);
  }
  // The audio engine reads the first five through music-preferences.ts; the server applies
  // the away quality and DSD gain when it builds the playback plan.
  assert.equal(settingsBinding('music.audioNormalization')?.key, 'music.audioNormalization');
  assert.equal(settingsBinding('music.qualityAway')?.scope, 'profile-device-class');
});

test('rows without a web consumer stay hidden', () => {
  for (const id of ['playback.showSyncedLyrics', 'music.qualityMobile', 'music.downloadQuality', 'music.sampleRateMatching',
    'music.idlePause']) {
    assert.equal(hasWebPreferenceEffect(row(id)), false, id);
  }
});

test('the Equalizer link is the drawn device-scope row, not a preference', () => {
  const eq = row('music.eq');
  assert.equal(eq.scope, 'device');
  assert.equal(eq.control.type, 'link');
  assert.equal(settingsBinding('music.eq'), undefined);
  assert.equal(hasWebPreferenceEffect(eq), false);
});

const flat = {...flatEqualizer, bandsDb: [...flatEqualizer.bandsDb]};
function port(enabled: boolean) {
  const value = {...flat, enabled};
  return {getSnapshot: () => value, subscribe: () => () => {}, start: () => () => {},
    change: async () => {}, reset: async () => {}};
}
const web = {environment: {platform: 'web', os: 'browser'} as const};

test('the Equalizer row is offered only when the app really binds its device store', () => {
  assert.equal(musicEqualizerAvailable(undefined), false);
  assert.equal(musicEqualizerAvailable({environment: {platform: 'desktop', os: 'windows'}}), false);
  assert.equal(musicEqualizerAvailable({...web}), true);
  assert.equal(musicEqualizerAvailable({...web, equalizerPort: port(false)}), true);
  assert.equal(musicEqualizerAvailable({environment: {platform: 'desktop', os: 'windows'}, Equalizer: () => null}), true);
});

test('the link shows the same store state as the EQ page and starts closed', () => {
  const off = renderToStaticMarkup(createElement(MusicEqualizerRow, {platform: {...web, equalizerPort: port(false)}, isCurrent: () => true}));
  assert.match(off, />Off</);
  assert.doesNotMatch(off, /role="dialog"/);
  const on = renderToStaticMarkup(createElement(MusicEqualizerRow, {platform: {...web, equalizerPort: port(true)}, isCurrent: () => true}));
  assert.match(on, />On</);
});

test('the canonical music section keeps its four drawn groups', () => {
  const music = settingsSpec.find(section => section.scope === 'account' && section.id === 'music');
  assert.ok(music);
  assert.deepEqual(music.groups?.map(group => group.rows.map(value => value.id)), [
    ['music.audioNormalization', 'music.crossfadeMs', 'music.gapless', 'music.eq'],
    ['playback.showSyncedLyrics'],
    ['playback.audiobookSpeed', 'audiobooks.skipBackMs', 'audiobooks.skipForwardMs', 'audiobooks.resumeRewindMs'],
    ['music.qualityAway', 'music.qualityMobile', 'music.downloadQuality'],
  ]);
});
