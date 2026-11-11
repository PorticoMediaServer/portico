import test from 'node:test';
import assert from 'node:assert/strict';
import {PreferencesReader} from '@core/preferences-reader.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {settingsSpec} from '@core/presentation/settings-spec.ts';
import {SettingsController} from '../src/screens/settings/controller.ts';

const preferences: PreferenceSnapshot = {deviceClass: 'web', registry: {revision: 'test', fields: []}, documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'test'};
const server = (id: string) => settingsSpec.find(section => section.scope === 'server' && section.id === id)!;

test('an administrator sees the Dashboard and Play history (console rule 1), not the owner\'s pages', () => {
  const api = {request: async <T,>() => ({}) as T};
  const reader = new PreferencesReader(api);
  const controller = new SettingsController({api, serverId: 'server', viewerId: 'viewer', role: 'admin', mainProfile: true,
    capabilities: {manageServer: true, managePeople: false, manageSignIn: false}, preferences, reader, isCurrent: () => true, reportError: () => {},
    environment: {platform: 'web', os: 'browser'}, eligibility: {hasEngine: () => false, library: () => undefined},
    reading: {read: () => {throw new Error('Unexpected Reading read');}, patch: () => {throw new Error('Unexpected Reading write');}},
  });
  try {
    assert.equal(controller.sectionVisible(server('dashboard')), true);
    assert.equal(controller.sectionVisible(server('history')), true);
    assert.equal(controller.sectionVisible(server('storage')), false);
  } finally {controller.dispose();reader.dispose();}
});
