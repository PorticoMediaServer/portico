import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError} from '@core/index.ts';
import {PreferencesReader} from '@core/preferences-reader.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {settingsRowIndex, settingsSpec} from '@core/presentation/settings-spec.ts';
import {routePath} from '../../packages/contracts/src/route-paths.ts';
import {SettingsController, type SettingsControllerOptions} from '../src/screens/settings/controller.ts';

// FB-10 regression: the owner's Quality page shows the server's away-from-home limit when it can be read.
// A refusal (a remembered owner past the management hour) or any failure leaves that one row out: the
// page still loads, and the read never asks for the PIN.
const serverId = 'captured-server';
const section = settingsSpec.find(section => section.scope === 'account' && section.id === 'quality')!;
const preferences: PreferenceSnapshot = {deviceClass: 'web', registry: {revision: 'test', fields: []}, documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'test'};

for (const refusal of [new ApiError(403, 'profile_pin_manage', 'Enter this profile\'s PIN to manage this account.', false), new Error('network down')]) test(`a refused away-limit read omits its row and the Quality page loads (${refusal.message})`, async () => {
  const calls: {path: string; options?: unknown}[] = [];
  const api: SettingsControllerOptions['api'] = {request: async <T>(path: string, _method?: string, _body?: unknown, _signal?: AbortSignal, options?: unknown) => {
    calls.push({path, options});
    if (path === routePath('get_connectivity_status')) throw refusal;
    return {scope: {serverId, viewerFence: 'a'.repeat(64)}, data: {}} as T;
  }};
  const reader = new PreferencesReader(api, {serverId, viewerId: 'viewer'});
  const controller = new SettingsController({api, serverId, viewerId: 'viewer', role: 'owner', mainProfile: true,
    capabilities: {manageServer: true, managePeople: false, manageSignIn: false}, preferences, reader, isCurrent: () => true, reportError: error => {throw error;},
    environment: {platform: 'web', os: 'browser'}, eligibility: {hasEngine: () => true, library: () => undefined},
    reading: {read: () => undefined as never, patch: () => {throw new Error('Unexpected Reading write');}}});
  try {
    await controller.load(section);
    assert.equal(controller.getSnapshot(section).error, undefined, 'the page loads');
    assert.equal(controller.getSnapshot(section).loading, false);
    const reads = calls.filter(call => call.path === routePath('get_connectivity_status'));
    assert.equal(reads.length, 1, 'one quiet read');
    assert.deepEqual(reads[0]!.options, {manageGate: false}, 'it never asks for the PIN');
    assert.equal(controller.rowState(settingsRowIndex.get('quality.awayLimit')!.row).present, false, 'the row is left out');
  } finally {controller.dispose(); reader.dispose();}
});
