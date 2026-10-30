import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService, type PlaybackApi} from '../src/index.ts';
import {musicAudioEffects, audioEffectsAvailability} from '../src/music-preferences.ts';
import {noAudioEffects} from '../src/audio-effects.ts';
import type {PreferenceField, PreferenceSnapshot, PreferenceValues} from '../src/preferences.ts';

const field = (key: string, type: PreferenceField['type'], value: PreferenceField['default'], extra: Partial<PreferenceField> = {}): PreferenceField =>
  ({key, type, default: value, scopes: ['profile-server'], group: 'music', labelKey: key, ...extra});
const registry = [
  field('music.gapless', 'boolean', true),
  field('music.crossfadeMs', 'integer', 0, {min: 0, max: 12000}),
  field('music.audioNormalization', 'string', 'off', {allowedValues: ['off', 'track', 'album']}),
];
const snapshot = (effective: PreferenceValues, fields = registry): PreferenceSnapshot => ({
  deviceClass: 'web', registry: {revision: '1', fields}, documents: [], effective, effectiveSource: {}, clampedFields: [], registryRevision: '1',
});
const api: PlaybackApi = {createPlayback: async () => { throw new Error('no playback here'); }, stopPlayback: async () => {}, progressPlayback: async () => {}};

test('X-02: the server’s music preferences are the effects; gapless is on by default', () => {
  assert.deepEqual(musicAudioEffects(snapshot({})), {neverBoost:false,smoothTransitions:true,gapless: true, crossfadeSeconds: 0, normalization: 'off'});
  assert.deepEqual(musicAudioEffects(snapshot({'music.gapless': false, 'music.crossfadeMs': 6000, 'music.audioNormalization': 'album'})), {neverBoost:false,smoothTransitions:true,gapless: false, crossfadeSeconds: 6, normalization: 'album'});
  // An older server that publishes none of them: the defaults, gapless on.
  assert.deepEqual(musicAudioEffects(snapshot({}, [])), {neverBoost:false,smoothTransitions:true,gapless: true, crossfadeSeconds: 0, normalization: 'off'});
  // Outside the engine's domain: brought inside it.
  assert.equal(musicAudioEffects(snapshot({'music.crossfadeMs': 30000})).crossfadeSeconds, 12);
  assert.equal(musicAudioEffects(snapshot({'music.audioNormalization': 'loud'})).normalization, 'off');
  const service = new PlaybackService(api, () => 'r', {});
  assert.equal(service.getSnapshot().audioEffects.settings.gapless, true, 'before the server answers, gapless is on');
});

test('X-02: preferences flow into the service and win over the device’s cached copy', () => {
  const service = new PlaybackService(api, () => 'r', {});
  // Offline (no server answer yet): the journal's copy is used.
  service.restoreAudioEffects(noAudioEffects);
  assert.deepEqual(service.getSnapshot().audioEffects.settings, noAudioEffects);
  // The server answers: its values apply, and a late journal read can't undo them.
  service.adoptAudioEffectsPreference(musicAudioEffects(snapshot({'music.crossfadeMs': 4000, 'music.audioNormalization': 'track'})));
  assert.deepEqual(service.getSnapshot().audioEffects.settings, {neverBoost:false,smoothTransitions:true,gapless: true, crossfadeSeconds: 4, normalization: 'track'});
  service.restoreAudioEffects(noAudioEffects);
  assert.equal(service.getSnapshot().audioEffects.settings.crossfadeSeconds, 4);
  // A preference change applies; the same settings again change nothing.
  service.adoptAudioEffectsPreference(musicAudioEffects(snapshot({'music.gapless': false})));
  assert.equal(service.getSnapshot().audioEffects.settings.gapless, false);
  const revision = service.getSnapshot().revision;
  service.adoptAudioEffectsPreference(musicAudioEffects(snapshot({'music.gapless': false})));
  assert.equal(service.getSnapshot().revision, revision);
});

test('b4: unavailable plans expose availability without diagnostic reason copy', () => {
  assert.equal(audioEffectsAvailability(undefined), undefined);
  assert.deepEqual(audioEffectsAvailability({audio: {mode: 'direct'}}), {available: true});
  assert.deepEqual(audioEffectsAvailability({audio: {mode: 'unavailable', reason: 'Audio processing requires a local source and owner permission to convert audio.'}}), {available: false});
});
