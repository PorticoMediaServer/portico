import test from 'node:test';
import assert from 'node:assert/strict';
import {PreferencesReader} from '@core/preferences-reader.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {settingsSpec} from '@core/presentation/settings-spec.ts';
import {settingsBinding} from '@core/presentation/settings-bindings.ts';
import {SettingsController, type SettingsControllerOptions} from '../src/screens/settings/controller.ts';

// Settings › Metadata › Recommendations: the one title dataset switch (B2 R3/R5).
const metadata = settingsSpec.find(section => section.scope === 'server' && section.id === 'metadata')!;
const serverId = 'captured-server', fence = 'a'.repeat(64);
const preferences: PreferenceSnapshot = {deviceClass: 'web', registry: {revision: 'test', fields: []}, documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'test'};

test('the Metadata page carries the Recommendations group with its title data switch', () => {
  const group = metadata.groups?.find(candidate => candidate.rows.some(row => row.id === 'metadata.titleDataset'));
  assert.ok(group, 'the spec draws the group');
  const row = group.rows.find(candidate => candidate.id === 'metadata.titleDataset')!;
  assert.equal(row.control.type, 'toggle');
  assert.equal(row.control.default, true);
  assert.deepEqual(settingsBinding(row.id), {form: 'titleData', key: 'download', codec: 'boolean'});
});

test('opening Metadata reads the title_data document and draws Download title data from it', async () => {
  const calls: string[] = [];
  const api: SettingsControllerOptions['api'] = {request: async <T>(path: string) => {
    calls.push(path);
    if (path === '/v1/admin/settings/title_data') return {name: 'title_data', etag: '"3"', values: {download: false}, defaults: {download: true}} as T;
    return {scope: {serverId, viewerFence: fence}, data: {name: path.split('/').at(-1), observedAt: 1000, freshUntil: 31000, facts: {}}} as T;
  }};
  const reader = new PreferencesReader(api);
  const controller = new SettingsController({api, serverId, viewerId: 'captured-viewer', role: 'owner', mainProfile: true,
    capabilities: {manageServer: true, managePeople: false, manageSignIn: false}, preferences, reader,
    isCurrent: () => true, reportError: () => {},
    // As Settings.route decides: a row bound to a settings document has its engine.
    environment: {platform: 'web', os: 'browser'}, eligibility: {hasEngine: row => settingsBinding(row.id)?.form === 'titleData', library: () => undefined},
    reading: {read: () => {throw new Error('Unexpected Reading read');}, patch: () => {throw new Error('Unexpected Reading write');}},
  });
  try {
    await controller.load(metadata);
    assert.ok(calls.includes('/v1/admin/settings/title_data'), `requests: ${calls.join(', ')}`);
    assert.ok(!calls.includes('/v1/admin/console/settings'), 'the old server\'s settings address is never asked');
    const row = metadata.groups!.flatMap(group => group.rows).find(row => row.id === 'metadata.titleDataset')!;
    const state = controller.rowState(row);
    assert.equal(state.present, true, 'the switch is drawn');
    assert.equal(state.value, false, 'with the stored value');
  } finally {controller.dispose();reader.dispose();}
});
