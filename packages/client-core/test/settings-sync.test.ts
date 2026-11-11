import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {parsePreferenceSnapshot} from '../src/preferences.ts';
import {PreferencesReader} from '../src/preferences-reader.ts';
import {settingsBinding, settingsBindingPatch} from '../src/presentation/settings-bindings.ts';
import {outboxBody, outboxKind} from '../src/outbox-routes.ts';
import {serverEventsFrom} from '../src/server-events.ts';

// `GET /v1/preferences?deviceClass=web` as the rebuilt server answers it for a new profile.
const answer = JSON.parse(readFileSync(new URL('./fixtures/next-preferences.json', import.meta.url), 'utf8'));
const kept = () => Object.assign(new Error('Saved on this device.'), {code: 'sync_pending'});

test('the server\'s settings read as they are, and what this app does not know is passed over', () => {
  const settings = parsePreferenceSnapshot(answer);
  assert.equal(settings.effective['quality.home'], 'original');
  assert.equal(settings.effectiveSource['quality.home'], 'default');
  assert.deepEqual(settings.registry.fields.find(field => field.key === 'quality.home')?.scopes, ['profile-device']);
  assert.deepEqual(settings.documents.map(document => document.scope), ['profile-server', 'profile-device-class', 'profile-device']);
  // A newer server's field, a setting of a type this app cannot draw and a value it cannot read leave the rest whole.
  const newer = parsePreferenceSnapshot({...answer, futureField: 1,
    registry: {...answer.registry, fields: [...answer.registry.fields, {key: 'appearance.accent', type: 'colour', default: '#fff', scopes: ['profile-server'], group: 'appearance', labelKey: 'x'}]},
    effective: {...answer.effective, 'appearance.accent': '#fff', 'playback.autoplayNext': 'yes'}});
  assert.equal(newer.registry.fields.some(field => field.key === 'appearance.accent'), false);
  assert.equal(newer.effective['playback.autoplayNext'], true);
  assert.equal(newer.registry.fields.length, settings.registry.fields.length);
});

test('quality is saved for this device only, and a setting for the whole profile everywhere', async () => {
  const sent: unknown[] = [];
  const api = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    if (method === 'PATCH') sent.push({path, body});
    return answer as T;
  }};
  const reader = new PreferencesReader(api);
  const settings = await reader.preferences('web');
  for (const [row, value] of [['quality.home', '720p-high'], ['playback.autoplayNext', false]] as const) {
    const patch = settingsBindingPatch(settingsBinding(row)!, value, {preferences: settings});
    const scope = row === 'quality.home' ? 'profile-device' : 'profile-server';
    await reader.applyPreferences(scope, 'web', patch.values as Record<string, string | boolean>);
  }
  assert.deepEqual(sent, [
    {path: '/v1/preferences', body: {scope: 'profile-device', deviceClass: 'web', values: {'quality.home': '720p-high'}}},
    {path: '/v1/preferences', body: {scope: 'profile-server', deviceClass: 'web', values: {'playback.autoplayNext': false}}},
  ]);
  // Quality has no place at the kind-of-device scope: two phones on one profile keep their own.
  await assert.rejects(reader.applyPreferences('profile-device-class', 'web', {'quality.home': '720p-high'}));
});

test('a change made out of reach of the server shows at once, the later change wins, and a cleared one falls back', async () => {
  let reachable = true;
  const api = {request: async <T>(_path: string, method = 'GET'): Promise<T> => {
    if (method === 'PATCH' && !reachable) throw kept();
    return answer as T;
  }};
  const reader = new PreferencesReader(api);
  await reader.preferences('web');
  reachable = false;
  await reader.applyPreferences('profile-server', 'web', {'playback.skipBackMs': 15000});
  const later = await reader.applyPreferences('profile-server', 'web', {'playback.skipBackMs': 30000});
  assert.equal(later.effective['playback.skipBackMs'], 30000);
  assert.equal(later.effectiveSource['playback.skipBackMs'], 'profile-server');
  assert.equal(reader.getSnapshot('web'), later);
  const cleared = await reader.applyPreferences('profile-server', 'web', {'playback.skipBackMs': null});
  assert.equal(cleared.effective['playback.skipBackMs'], 10000);
  assert.equal(cleared.effectiveSource['playback.skipBackMs'], 'default');
  // A save goes through the outbox, which keeps it on the device until the server answers.
  assert.equal(outboxKind('/v1/preferences', 'PATCH'), 'settings');
  assert.equal(outboxKind('/v1/preferences?deviceClass=web', 'GET'), undefined);
  // Sent later, it says how long ago it was made, so a later change made on another device is not overwritten.
  assert.deepEqual(outboxBody({path: '/v1/preferences', method: 'PATCH', body: {scope: 'profile-server', deviceClass: 'web', values: {'playback.skipBackMs': null}}, createdAt: 1_000}, 91_000),
    {scope: 'profile-server', deviceClass: 'web', values: {'playback.skipBackMs': null}, madeAgoMs: 90_000});
});

test('a change to the profile\'s settings on another device reads them again', () => {
  assert.deepEqual(serverEventsFrom({id: '12', event: 'change', data: JSON.stringify({topic: 'preferences', op: 'updated'})}),
    [{id: '12', type: 'settings.changed', resource: {kind: 'settings', id: 'preferences'}}]);
});
