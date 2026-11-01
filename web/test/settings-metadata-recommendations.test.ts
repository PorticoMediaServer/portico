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

test('the Metadata page carries the Recommendations group with its runtime switch', () => {
  const group = metadata.groups?.find(candidate => candidate.rows.some(row => row.id === 'metadata.titleDataset'));
  assert.ok(group, 'the spec draws the group');
  const row = group.rows.find(candidate => candidate.id === 'metadata.titleDataset')!;
  assert.equal(row.control.type, 'toggle');
  assert.equal(row.control.default, true);
  assert.deepEqual(settingsBinding(row.id), {form: 'runtime', key: 'recommendations.titleDataset', codec: 'boolean'});
});

test('opening Metadata reads the owner settings the switch is bound to', async () => {
  const calls: string[] = [];
  const api: SettingsControllerOptions['api'] = {request: async <T>(path: string) => {
    calls.push(path);
    return {scope: {serverId, viewerFence: fence}, data: {name: path.split('/').at(-1), observedAt: 1000, freshUntil: 31000, facts: {}}} as T;
  }};
  const reader = new PreferencesReader(api, {serverId, viewerId: 'captured-viewer'});
  const controller = new SettingsController({api, serverId, viewerId: 'captured-viewer', role: 'owner', mainProfile: true,
    capabilities: {manageServer: true, managePeople: false, manageSignIn: false}, preferences, reader,
    isCurrent: () => true, reportError: () => {},
    // As Settings.route decides: a row bound to the runtime form has its engine.
    environment: {platform: 'web', os: 'browser'}, eligibility: {hasEngine: row => settingsBinding(row.id)?.form === 'runtime', library: () => undefined},
    reading: {read: () => {throw new Error('Unexpected Reading read');}, patch: () => {throw new Error('Unexpected Reading write');}},
  });
  try {
    await controller.load(metadata);
    assert.ok(calls.includes('/v1/admin/console/settings'), `requests: ${calls.join(', ')}`);
  } finally {controller.dispose();reader.dispose();}
});
