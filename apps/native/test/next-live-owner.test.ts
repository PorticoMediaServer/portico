import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveOwnerController} from '../src-next/screens/settings-live/live-owner-controller';
import {remoteSourceDraft} from '../../../packages/client-core/src/linear-api';
import {liveDefaultsView, rebuiltLiveDefaults, sourceSettingsChange, parseChannelMapPage, parseLiveDefaults, parseLiveSources, parseSourceDocument, parseTunerDiscovery, millisOf, secondsOf, listOf} from '../src-next/screens/settings-live/live-owner-model';
import {appendRecordingPage, needsRuleOwner, rulePaddingChoice, rulePaddingFromChoice, parseAdminRulePage, parseAdminRules, parseDVRDocument, parseTunerView} from '../src-next/screens/settings-live/recording-owner-model';
import type {ShellAuthority} from '../src-next/shell/model';

function authority() {
  let value: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'main', epoch: 'session1', viewerFence: 'home-fence'};
  let valid = true;
  const listeners = new Set<() => void>();
  return {authority: () => value, current: () => valid, subscribeAuthority: (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};}, revoke: () => {valid = false; for (const fn of [...listeners]) fn();}};
}

const source = {diagnostics: {skippedChannels: 2, skippedProgrammes: 340, adjustedProgrammes: 5, examples: ['live.import.no-title']}, deliveryValidation: 'ok', capacityKnown: true, planningEstimate: 2, id: 's1', name: 'Synthetic Live TV', revision: 4, state: 'ready', tunerCount: 2, tunerCountMode: 'discovered', generation: 'g1', publishedAt: '2026-10-24T15:50:00Z', channels: 14, programmes: 193};
const settings = {
  enabledGroups: ['News'], enabledChannels: [], kind: 'playlist', streamBufferMs: null, retryWindowMs: 30000, userAgent: '',
  filters: {categories: {mode: 'include', values: ['News']}, countries: {mode: 'exclude', values: []}, keywords: {mode: 'include', values: ['HD']}},
  logoImport: {enabled: true, overwriteExisting: false}, channelNumbering: {mode: 'sequential', startAt: 100, step: 1}, guideSourceId: '', importLimits: null,
};
const defaults = {importLimits: {bytes: 1 << 28, channels: 100000, programmes: 10000000}, streamBufferMs: 8000, retryWindowMs: 60000, userAgent: 'Portico/1.0', guideDays: 7, logoImport: true, discoveryEnabled: true};

test('live source list reads the server envelope and the fields the screen shows', () => {
  const value = parseLiveSources({protocolVersion: '1.0', serverId: 'server', sources: [source], refresh: [{sourceId: 's1', state: 'refreshing', errorCode: '', nextRefresh: '', failures: 0}]}, 'server');
  assert.equal(value.sources[0]!.name, 'Synthetic Live TV');
  assert.equal(value.sources[0]!.programmes, 193);
  assert.equal(value.sources[0]!.diagnostics.skippedProgrammes, 340);
  assert.equal(value.refresh[0]!.state, 'refreshing');
  assert.throws(() => parseLiveSources({protocolVersion: '1.0', serverId: 'other', sources: [], refresh: []}, 'server'), /invalid_response/);
  assert.throws(() => parseLiveSources({protocolVersion: '1.0', serverId: 'server', sources: [{...source, revision: -1}], refresh: []}, 'server'), /invalid_response/);
});

test('one source document keeps null overrides distinct from zero and refuses unreadable settings', () => {
  const value = parseSourceDocument({protocolVersion: '1.0', serverId: 'server', result: {sourceId: 's1', sourceName: 'Synthetic Live TV', revision: 4, digest: 'abc', effectiveLogoImport: {enabled: true, overwriteExisting: false}, settings, effective: defaults, availableKinds: ['playlist', 'xmltv-guide'], tunerCount: 2}}, 'server');
  assert.equal(value.settings.streamBufferMs, null);
  assert.equal(value.settings.retryWindowMs, 30000);
  assert.equal(value.effective.streamBufferMs, 8000);
  assert.deepEqual(value.settings.filters.keywords.values, ['HD']);
  assert.throws(() => parseSourceDocument({protocolVersion: '1.0', serverId: 'server', result: {sourceId: 's1', sourceName: 'x', revision: 4, digest: 'abc', effectiveLogoImport: {enabled: true, overwriteExisting: false}, settings: {...settings, filters: {}}, effective: defaults, availableKinds: [], tunerCount: 0}}, 'server'), {code: 'invalid_administration_response'});
});

test('channel map pages and tuner discovery read only named fields', () => {
  const map = parseChannelMapPage({protocolVersion: '1.0', serverId: 'server', result: {sourceId: 's1', generation: 'g1', nextCursor: 'c2', items: [{mark: {text: 'NN 12', seed: 's1:c1'}, channelId: 'c1', name: 'Northwind News', sourceNumber: '101', number: '12', group: 'News', guideChannelId: '', hidden: true, position: 3, overridden: true}]}}, 'server');
  assert.equal(map.items[0]!.number, '12');
  assert.equal(map.items[0]!.hidden, true);
  assert.deepEqual(map.items[0]!.mark, {text: 'NN 12', seed: 's1:c1'});
  assert.ok(Object.isFrozen(map.items[0]!.mark));
  const discovery = parseTunerDiscovery({protocolVersion: '1.0', serverId: 'server', result: {devices: [{deviceId: 'd', model: 'Prime', friendlyName: '', address: '192.168.1.50', baseUrl: 'http://192.168.1.50', tunerCount: 3, configured: false}], searchedFor: '', discoveryEnabled: true}}, 'server');
  assert.equal(discovery.devices[0]!.tunerCount, 3);
});

test('recording documents read the server vocabulary and keep signed priorities', () => {
  const dvr = parseDVRDocument({protocolVersion: '1.0', serverId: 'server', result: {revision: 9, digest: 'dvr-digest', folderTokens: [], settings: {prePaddingMs: 60000, postPaddingMs: 180000, keepPolicy: {mode: 'keep-count', keepCount: 10, keepDays: 0, keepUntilWatched: false}, conversion: {mode: 'none', ladderId: '', deleteOriginal: false}, folderTemplate: '', movieFolderTemplate: '', recordingProfile: 'original', sidecars: {nfo: false, poster: false, fanart: false, thumbnail: false}, tunerPreference: 'first-available', liveTunerConflict: 'keep-watching'}, enumerations: {keepMode: ['keep-all', 'keep-count', 'keep-days']}}}, 'server');
  assert.equal(dvr.settings.keepPolicy.keepCount, 10);
  assert.deepEqual(dvr.enumerations.keepMode, ['keep-all', 'keep-count', 'keep-days']);
  const rules = parseAdminRules({protocolVersion: '1.0', serverId: 'server', result: {items: [{id: 'r1', revision: 2, owner: {authority: 'local', accountId: 'a', profileId: 'p'}, status: '', kind: 'series', name: 'The Clockmakers', match: 'clock', sourceId: 's1', enabled: true, options: {prePaddingMs: null, postPaddingMs: null, keepPolicy: null, newEpisodesOnly: true, priority: -5, folderTemplate: ''}}]}}, 'server');
  assert.equal(rules[0]!.options.priority, -5);
  assert.equal(rules[0]!.options.prePaddingMs, null);
  const tuners = parseTunerView({protocolVersion: '1.0', serverId: 'server', result: {sources: [{sourceId: 's1', sourceName: 'Synthetic Live TV', tunerCount: 2, inUse: 1, conflicts: 1, active: [{kind: 'live', accountName: 'Demo', profileName: 'Sam', recordingId: '', title: 'Cycling: Mountain Stage', state: 'watching', startsAt: '', endsAt: ''}], upcoming: []}], observedAt: '2026-10-24T16:00:00Z', upcomingWindowHours: 48}}, 'server');
  assert.equal(tuners.sources[0]!.conflicts, 1);
  assert.equal(tuners.sources[0]!.active[0]!.profileName, 'Sam');
});

test('seconds, milliseconds and comma lists round-trip without inventing a default', () => {
  assert.equal(secondsOf(null), '');
  assert.equal(secondsOf(8000), '8');
  assert.equal(millisOf(''), null);
  assert.equal(millisOf('8'), 8000);
  assert.deepEqual(listOf(' News, Sports ,, HD '), ['News', 'Sports', 'HD']);
});

test('the owner controller reads the rebuilt sources and pauses one only after the server accepts it', async () => {
  const owner = authority(), calls: {path: string; method: string; ifMatch?: string; body?: unknown}[] = [];
  let refusal = true;
  const lifetime = new NativeScreenLifetime(owner);
  const rebuilt = {id: 's1', name: 'Antenna', kind: 'm3u', state: 'up_to_date', revision: '3', paused: false, tunerCount: 2, hasSignIn: false, config: {playlistUrl: 'http://tv.example/list.m3u'},
    report: {channels: 5, programmes: 10, skippedChannels: 0, skippedProgrammes: 0, channelsWithGuide: 5}};
  const api = {
    request: async <T,>(path: string, method = 'GET') => {
      calls.push({path, method});
      if (path === '/v1/admin/live/sources' && method === 'GET') return {items: [rebuilt]} as T;
      throw new Error(`unexpected ${method} ${path}`);
    },
    requestRaw: async (path: string, method: string, options: {headers?: Record<string, string>; body?: unknown} = {}) => {
      calls.push({path, method, ifMatch: options.headers?.['If-Match'], body: options.body});
      return refusal ? {status: 412, headers: {}, body: {error: {code: 'changed_elsewhere', message: 'Changed elsewhere.'}}} : {status: 200, headers: {}, body: {...rebuilt, paused: true, revision: '4'}};
    },
  };
  const controller = new NativeLiveOwnerController(lifetime, api as never, () => 'op1');
  await controller.load();
  assert.equal(controller.getSnapshot().list?.sources[0]!.channels, 5);
  await controller.setEnabled('s1', false);
  const patch = calls.find(call => call.method === 'PATCH');
  assert.deepEqual(patch, {path: '/v1/admin/live/sources/s1', method: 'PATCH', ifMatch: '"3"', body: {paused: true}});
  assert.equal((controller.getSnapshot().error as {code?: string}).code, 'changed_elsewhere', 'a refused change is shown');
  assert.equal(controller.getSnapshot().list?.sources[0]!.state, 'up_to_date', 'and the row stays as the server last said');
  await controller.setEnabled('s1', true);
  assert.equal(calls.filter(call => call.method === 'PATCH').length, 1, 'a source already running is not changed again');
  lifetime.dispose(); controller.dispose();
});

test('administrative rules retain legal DVR values, title and ownerless rows', () => {
  const row = {id: 'r', revision: 2, owner: {authority: 'local', accountId: 'a', profileId: 'p'}, status: '', kind: 'title', name: 'Title', match: 'Title', sourceId: 's', enabled: false, options: {prePaddingMs: 3600000, postPaddingMs: 21600000, keepPolicy: {mode: 'keep-count', keepCount: 5000, keepDays: 0, keepUntilWatched: false}, newEpisodesOnly: false, priority: 500, folderTemplate: ''}};
  const legacy = {...row, id: 'legacy', owner: {authority: '', accountId: '', profileId: ''}, status: 'needs-owner-and-guide-selection'};
  const envelope = (items: unknown[]) => ({serverId: 'server', result: {items, nextCursor: 'next'}});
  const page = parseAdminRulePage(envelope([row, legacy]), 'server');
  assert.equal(page.nextCursor, 'next'); assert.equal(page.items[0]!.options.prePaddingMs, 3600000);
  for (const value of [0, 60000, 1800000, 21600000, null]) {
    const parsed = parseAdminRulePage(envelope([{...row, kind: 'series', options: {...row.options, prePaddingMs: value, postPaddingMs: 7200000}}]), 'server');
    assert.equal(parsed.items[0]!.options.prePaddingMs, value); assert.equal(parsed.items[0]!.options.postPaddingMs, 7200000);
  }
  assert.equal(page.items[0]!.options.keepPolicy?.keepCount, 5000); assert.equal(needsRuleOwner(page.items[1]!), true);
  for (const value of [-1, 0.5, 21600001]) assert.throws(() => parseAdminRulePage(envelope([{...row, options: {...row.options, prePaddingMs: value}}]), 'server'));
});

test('recording continuation deduplicates full keys and refuses cursor cycles without consuming retries', () => {
  const seen = new Set<string>();
  const first = [{key: 'local/a/p'}, {key: 'hosted/a/p'}];
  const second = appendRecordingPage(first, [{key: 'local/a/p'}, {key: 'local/a/q'}], 'c1', 'c2', seen, row => row.key);
  assert.deepEqual(second.map(row => row.key), ['local/a/p', 'hosted/a/p', 'local/a/q']);
  seen.add('c1');
  assert.throws(() => appendRecordingPage(second, [{key: 'third'}], 'c2', 'c1', seen, row => row.key));
  assert.equal(seen.has('c2'), false);
  assert.deepEqual(appendRecordingPage(second, [{key: 'third'}], 'c2', '', seen, row => row.key).map(row => row.key), ['local/a/p', 'hosted/a/p', 'local/a/q', 'third']);
});

test('new-rule padding choice preserves minute wire units and null inheritance', () => {
  assert.equal(rulePaddingFromChoice('60'), 60000);
  assert.equal(rulePaddingChoice(60000), '60');
  assert.equal(rulePaddingFromChoice('default'), null);
  assert.equal(rulePaddingChoice(null), 'default');
});

test('native live documents accept the shared 100,000-choice limit and retain rewind', () => {
  const enabled = Array.from({length: 100000}, (_, index) => `channel-${index}`);
  const result = {sourceId: 's1', sourceName: 'Large source', revision: 4, digest: 'abc', effectiveLogoImport: {enabled: true, overwriteExisting: false}, settings: {...settings, enabledGroups: enabled, enabledChannels: enabled}, effective: {...defaults, timeshiftMinutes: 120}, availableKinds: ['playlist'], tunerCount: 2};
  const value = parseSourceDocument({serverId: 'server', result}, 'server');
  assert.equal(value.settings.enabledChannels.length, 100000);
  assert.equal(value.settings.enabledGroups.length, 100000);
  assert.equal(value.settings.enabledChannels[99999], 'channel-99999');
  assert.equal(value.effective.timeshiftMinutes, 120);
  assert.throws(() => parseSourceDocument({serverId: 'other', result}, 'server'), /invalid_response/);
  assert.throws(() => parseSourceDocument({serverId: 'server', result: {...result, settings: {...result.settings, enabledChannels: [...enabled, 'too-many']}}}, 'server'), {code: 'invalid_administration_response'});
  assert.throws(() => parseSourceDocument({serverId: 'server', result: {...result, settings: {...settings, kind: 'xmltv-guide', streamBufferMs: 8000}}}, 'server'), {code: 'invalid_administration_response'});
  const live = parseLiveDefaults({serverId: 'server', result: {scope: 'server', revision: 1, digest: 'defaults', settings: {...defaults, timeshiftMinutes: 60}}}, 'server');
  assert.equal(live.settings.timeshiftMinutes, 60);
});

test('native DVR defaults refuse deleting the only original and unknown conversion modes', () => {
  const result = {revision: 1, digest: 'dvr', folderTokens: [], enumerations: {}, settings: {prePaddingMs: 60000, postPaddingMs: 180000,
    keepPolicy: {mode: 'keep-count', keepCount: 10, keepDays: 0, keepUntilWatched: false},
    conversion: {mode: 'none', ladderId: '', deleteOriginal: false}, folderTemplate: '{Title}', movieFolderTemplate: '{Title} ({Year})',
    recordingProfile: 'original', sidecars: {nfo: false, poster: false, fanart: false, thumbnail: false}, tunerPreference: 'first-available', liveTunerConflict: 'keep-watching'}};
  const document = (conversion: typeof result.settings.conversion) => ({serverId: 'server', result: {...result, settings: {...result.settings, conversion}}});
  assert.equal(parseDVRDocument(document(result.settings.conversion), 'server').settings.folderTemplate, '{Title}');
  assert.throws(() => parseDVRDocument(document({...result.settings.conversion, deleteOriginal: true}), 'server'), {code: 'invalid_administration_response'});
  assert.throws(() => parseDVRDocument(document({...result.settings.conversion, mode: 'unexpected'}), 'server'), {code: 'invalid_administration_response'});
  assert.equal(parseDVRDocument(document({...result.settings.conversion, mode: 'remux', deleteOriginal: true}), 'server').settings.conversion.deleteOriginal, true);
  assert.throws(() => parseDVRDocument({...document(result.settings.conversion), serverId: 'other'}, 'server'), /invalid_response/);
});


test('adding a playlist source previews it, then creates it from the same body with one operation id', async () => {
  const owner = authority(), sent: {path: string; method: string; key?: string; body?: any}[] = [];
  const lifetime = new NativeScreenLifetime(owner);
  const created = {id: 's2', name: 'Cable', kind: 'playlist', state: 'up_to_date', revision: '1', paused: false, tunerCount: 2, hasSignIn: false, config: {playlistUrl: 'http://tv.example/c.m3u'},
    report: {channels: 7, programmes: 0, skippedChannels: 1, skippedProgrammes: 0, channelsWithGuide: 0}};
  const api = {
    request: async <T,>(path: string, method = 'GET', body?: unknown) => {
      sent.push({path, method, body});
      if (path === '/v1/admin/live/sources/preview') return {report: created.report, channels: [{Name: 'News'}, {Name: 'Sport'}], tuners: 0} as T;
      if (path === '/v1/admin/live/sources' && method === 'GET') return {items: [created]} as T;
      throw new Error(`unexpected ${method} ${path}`);
    },
    requestRaw: async (path: string, method: string, options: {headers?: Record<string, string>; body?: unknown} = {}) => {
      sent.push({path, method, key: options.headers?.['Idempotency-Key'], body: options.body});
      return {status: 201, headers: {}, body: created};
    },
  };
  const controller = new NativeLiveOwnerController(lifetime, api as never, () => 'op-preview-1');
  const draft = {...remoteSourceDraft('d1'), name: ' Cable ', locator: 'http://tv.example/c.m3u', ownerLimit: 2};
  const preview = await controller.previewRemote(draft);
  assert.equal(preview.preview.channels, 7); assert.deepEqual(preview.preview.names, ['News', 'Sport']); assert.equal(preview.preview.diagnostics.skippedChannels, 1);
  const saved = await controller.saveRemote(preview.previewId);
  assert.equal(saved?.id, 's2');
  const create = sent.find(call => call.method === 'POST' && call.path === '/v1/admin/live/sources')!;
  assert.deepEqual(create.body, sent.find(call => call.path === '/v1/admin/live/sources/preview')!.body, 'the saved body is the one previewed');
  assert.deepEqual(create.body, {kind: 'playlist', name: 'Cable', audience: 'owner', tunerCount: 2, config: {refreshHours: 6, playlistUrl: 'http://tv.example/c.m3u'}});
  assert.equal(create.key, preview.previewId);
  lifetime.dispose(); controller.dispose();
});

test('a source settings save sends only what the owner changed, and a new sign-in whole', async () => {
  const document = {etag: '"3"', imagesEnabled: true, source: {id: 's1', name: 'Cable', kind: 'xtream', state: 'up_to_date', revision: '3', paused: false, tunerCount: 2, audience: 'everyone', hasSignIn: true, config: {serverUrl: 'http://x', refreshHours: 6}}};
  const draft = {name: 'Cable', audience: 'everyone', paused: false, tunerCount: 2, config: {serverUrl: 'http://x', refreshHours: 12}, username: '', password: ''};
  assert.deepEqual(sourceSettingsChange(document, draft), {config: {serverUrl: 'http://x', refreshHours: 12}});
  assert.deepEqual(sourceSettingsChange(document, {...draft, config: document.source.config, username: 'u', password: 'p'}), {signIn: {username: 'u', password: 'p'}});
});

test('live defaults read the rebuilt document in its own units and save the owner\'s change back in them', () => {
  const document = {status: {etag: '"5"'}, value: {bufferSeconds: 60, retrySeconds: 20, guideDays: 7, rewindMinutes: 30, importImages: true, discoveryEnabled: false, userAgent: 'Portico'}};
  const view = liveDefaultsView(document);
  assert.equal(view.revision, 5); assert.equal(view.settings.streamBufferMs, 60000); assert.equal(view.settings.timeshiftMinutes, 30); assert.equal(view.settings.logoImport, true);
  assert.deepEqual(rebuiltLiveDefaults(document.value, {...view.settings, retryWindowMs: 45000, guideDays: 14}), {...document.value, retrySeconds: 45, guideDays: 14});
});
