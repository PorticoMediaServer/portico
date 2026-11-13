import test from 'node:test';
import assert from 'node:assert/strict';
import {PreferencesReader} from '@core/preferences-reader.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {OwnerStreamingSettings} from '@core/server-admin/streaming-settings.ts';
import {readOwnerSignInConfiguration} from '@core/sign-in-policy.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {defaultI18n} from '@i18n';
import {SettingsController} from '../src/screens/settings/controller.ts';

const preferences: PreferenceSnapshot = {deviceClass: 'web', registry: {revision: 'test', fields: []}, documents: [], effective: {}, effectiveSource: {}, clampedFields: [], registryRevision: 'test'};
for (const name of ['sign-in', 'segments', 'streaming'] as const) test(`${name}: a refused save is said once on its row; the newer value stays`, {timeout: 5000}, async () => {
  const row = settingsRowIndex.get(name === 'streaming' ? 'streaming.maxConversions' : name === 'sign-in' ? 'signin.stayLength' : 'segments.shortestEpisode')!.row;
  const section = settingsRowIndex.get(row.id)!.section;
  let revision = 1;
  let values: Record<string, unknown> = name === 'streaming' ? {convertWhenNeeded: 'picture-and-sound', awayVideoKbps: 0, awayMusicKbps: 0, uploadSpeedKbps: 0,
    graphicsDevice: 'automatic', hardwareDecoding: true, conversionsAtOnce: 0, backgroundConversionsAtOnce: 0, toneMapping: true, conversionFolder: '', ffmpegProgram: ''} : name === 'sign-in' ? {staySignedInDays: 90, pinRememberDays: 30, requireTwoStep: 'nobody'} : {shortestEpisodeMinutes: 10, shortestFilmMinutes: 30};
  const defaults = {...values}, reports: unknown[] = [], writes: any[] = [];
  const key = name === 'streaming' ? 'conversionsAtOnce' : name === 'sign-in' ? 'staySignedInDays' : 'shortestEpisodeMinutes';
  const document = () => ({name, etag: `"${revision}"`, values, defaults});
  const api = {
    request: async <T>(path: string): Promise<T> => {
      if (path === `/v1/admin/settings/${name}`) return document() as T;
      if (path.endsWith('/settings/downloads')) return {name: 'downloads', etag: '"1"', values: {highestQuality: 'original'}, defaults: {highestQuality: 'original'}} as T;
      if (path.endsWith('/stream-limits')) return {total: 0, endPausedVideoAfterMs: 0} as T;
      throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
    },
    requestRaw: async (_path: string, _method: string, options: any) => {
      writes.push(options);
      if (options.headers['If-Match'] !== document().etag) return {status: 412, headers: {}, body: {error: {code: 'changed_elsewhere'}}};
      values = {...values, ...options.body}; revision++;
      return {status: 200, headers: {}, body: document()};
    },
  };
  const reader = new PreferencesReader(api), streaming = name === 'streaming' ? new OwnerStreamingSettings(api) : undefined;
  const controller = new SettingsController({api, serverId: 'server', viewerId: 'owner', role: 'owner', mainProfile: true,
    capabilities: {manageServer: true, managePeople: true, manageSignIn: true}, preferences, reader,
    streaming, readConfiguration: streaming ? streaming.readConfiguration : name === 'sign-in' ? signal => readOwnerSignInConfiguration(api, signal) : async () => ({revision: 1, registryRevision: 'test', signInPolicyRevision: '', path: '', fields: []}),
    isCurrent: () => true, reportError: error => reports.push(error), environment: {platform: 'web', os: 'browser', signIn: 'local'},
    eligibility: {hasEngine: () => true, library: () => undefined}, reading: {read: () => undefined as never, patch: () => {throw new Error('unexpected');}},
  });
  const change = (target: typeof row, value: string | number, done: () => boolean) => new Promise<void>(resolve => {
    const off = controller.subscribe(() => {if (done()) {off(); resolve();}});
    controller.change(target, value);
  });
  try {
    await controller.load(section);
    assert.equal(controller.rowState(row).present, true);
    values = {...values, [key]: name === 'streaming' ? 4 : name === 'sign-in' ? 365 : 15}; revision++;
    await change(row, name === 'streaming' ? '2' : name === 'sign-in' ? '30' : 20, () => !!controller.rowState(row).error && !controller.rowState(row).saving);
    assert.equal(controller.rowState(row).error, defaultI18n.t('error.changed'));
    assert.equal(Number(controller.rowState(row).value), values[key]);
    assert.deepEqual(reports, [], 'no duplicate global error');
    assert.equal(writes.length, 1, 'the conflicting edit was not retried');
    if (name === 'segments') {
      const other = settingsRowIndex.get('segments.shortestFilm')!.row;
      await change(other, 45, () => controller.getSnapshot(section).saveState === 'saved');
      assert.deepEqual(writes[1].body, {shortestFilmMinutes: 45}, 'the refused edit is not resubmitted by another row');
      assert.equal(values.shortestEpisodeMinutes, 15);
    }
  } finally {controller.dispose(); reader.dispose();}
});
