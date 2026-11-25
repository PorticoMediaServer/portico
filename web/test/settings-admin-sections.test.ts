import test from 'node:test';
import assert from 'node:assert/strict';
import {PreferencesReader} from '@core/preferences-reader.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {settingsSpec} from '@core/presentation/settings-spec.ts';
import {SettingsController} from '../src/screens/settings/controller.ts';
import {createSettingsBindings} from '../src/screens/settings/bindings.ts';

const preferences: PreferenceSnapshot = {deviceClass: 'web', registry: {revision: 'test', fields: []}, documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'test'};
const server = (id: string) => settingsSpec.find(section => section.scope === 'server' && section.id === id)!;

test('an administrator sees the Dashboard and Play history (console rule 1), not the owner\'s pages including libraries', () => {
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
    assert.equal(controller.sectionVisible(server('libraries')), false);
    assert.equal(controller.sectionVisible(server('storage')), false);
  } finally {controller.dispose();reader.dispose();}
});

test('libraries is owner-only across controller visibility and bindings audience', () => {
  const api = {request: async <T,>() => ({}) as T};
  const reader = new PreferencesReader(api);
  const make = (role: 'owner' | 'admin' | 'member') => {
    const options = {api, serverId: 'server', viewerId: 'viewer', role, mainProfile: true,
      capabilities: {manageServer: role !== 'member', managePeople: false, manageSignIn: false}, preferences, reader, isCurrent: () => true, reportError: () => {},
      environment: {platform: 'web' as const, os: 'browser' as const}, eligibility: {hasEngine: () => false, library: () => undefined},
      reading: {read: () => {throw new Error('Unexpected Reading read');}, patch: () => {throw new Error('Unexpected Reading write');}},
    };
    const controller = new SettingsController(options);
    const bindings = createSettingsBindings(controller, options, {serverName: 'Server', profileName: 'Viewer', signIn: 'local'} as never, {openHome: () => {}, special: () => null});
    return {controller, bindings};
  };
  const owner = make('owner'), admin = make('admin'), member = make('member');
  try {
    assert.equal(owner.controller.sectionVisible(server('libraries')), true);
    assert.equal(admin.controller.sectionVisible(server('libraries')), false);
    assert.equal(member.controller.sectionVisible(server('libraries')), false);
    assert.equal(owner.bindings.audience(server('libraries')), 'owner');
    assert.equal(owner.bindings.audience(server('dashboard')), 'administrators');
  } finally {owner.controller.dispose();admin.controller.dispose();member.controller.dispose();reader.dispose();}
});
