import test from 'node:test';
import assert from 'node:assert/strict';
import {routePath} from '../../packages/contracts/src/index.ts';
import {createSeriesRulesBindings, editedRuleConfig, ruleSettings} from '../src/screens/live/series-rules-bindings.ts';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import type {RecordingRule} from '@core/dvr.ts';

const id = (number: number, width = 64) => number.toString(16).padStart(width, '0');
const sourceId = id(1, 48), channelId = id(2), generation = id(3, 48), serverId = 'unit-server';
const start = '2026-10-24T14:00:00Z', end = '2026-10-24T15:00:00Z';
const programme = (number = 1) => ({id: id(100 + number), channelId: number === 2 ? id(9) : channelId,
  title: 'Stored series', subtitle: 'Stored episode ' + number, start, end, lineage: 'provider-id', seriesId: 'series', episodeId: 'episode-' + number,
  newEvidence: number === 2 ? 'unknown' : 'new', episode: {season: 4, number}, description: '',
  ...(number === 1 ? {recordingId: id(701), recordingState: 'scheduled'} : {})});
const wireRule = (number = 1) => ({id: id(number), revision: 7, config: {name: 'Stored rule ' + number, sourceId, seriesId: 'series', enabled: true,
  episodes: 'all', allowedChannels: [], blockedChannels: [id(8)], keywords: ['stored keyword'], blockedKeywords: ['stored exclusion'],
  options: {beforeMs: 120000, afterMs: 7000, priority: 3, retentionDays: 42, episodeLimit: 6}}, reconcileState: 'ready', diagnostic: '', updatedAt: start,
  summary: {upcoming: 8, recorded: 20, conflicts: 1, matchingTonight: 4, next: programme(), channel: {id: channelId, name: 'Stored channel', number: '7'}}});
const envelope = (result: unknown) => ({protocolVersion: '1.0', serverId, result});
function deferred<T>() {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};}
const status = (status: number) => Object.assign(new Error('unit request rejected'), {status});

function setup(options: {total?: number; defaultNonce?: boolean; intercept?: (path: string, method: string, body: unknown, signal?: AbortSignal) => Promise<unknown | undefined>;
  requestId?: () => Promise<string>} = {}) {
  const total = options.total ?? 1, rows = Array.from({length: total}, (_, index) => wireRule(index + 1));
  const calls: {path: string; method: string; body: unknown; signal?: AbortSignal}[] = [];
  let active = true, nonces = 0;
  const api = {async request<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
    calls.push({path, method, body, signal});const response = await options.intercept?.(path, method, body, signal);if (response !== undefined) return response as T;
    const url = new URL(path, 'http://unit.invalid');
    if (url.pathname === routePath('get_guide_sources')) return {protocolVersion: '1.0', serverId, viewerFence: 'guide-only', revision: 'directory-only', sources: [{id: sourceId,
      name: 'Stored provider', generation, publishedAt: start, availableStart: start, availableEnd: '2026-10-27T00:00:00Z', provenance: 'live-source', refreshState: 'ready',
      position: 0, recordAvailable: true, guideDays: 3, channelCount: 14, favoriteCount: 0, groups: [], groupCounts: [], unavailableReasons: {}}]} as T;
    if (url.pathname === routePath('get_dvr_summary')) return envelope({upcoming: 8, recorded: 20, rules: rows.length, conflicts: 1, groups: 3, unwatched: 5}) as T;
    if (url.pathname === routePath('get_dvr_storage')) return envelope({policy: {revision: 1, retentionDays: 42, episodeLimit: 0, floorBytes: 0, capBytes: 0},
      measurement: {freeBytes: 412e9, usedBytes: 64e9, reservedBytes: 0, writeHealthy: true, measuredAt: start}, pendingDeleteBytes: 0, forecastBytes: 0,
      forecastHours: 230, forecastDescription: '', captureAvailable: true, warning: ''}) as T;
    if (url.pathname === routePath('get_dvr')) {
      const offset = Number(url.searchParams.get('cursor') ?? 0), next = offset + 50;
      return envelope({recordings: [], rules: rows.slice(offset, next), nextCursor: next < rows.length ? String(next) : '', revision: 5,
        captureAvailable: true, captureUnavailableReason: '', deletionAvailable: true, usage: {bytes: 64e9, pendingDeleteBytes: 0, recordings: 20}}) as T;
    }
    if (url.pathname === routePath('post_dvr_rules_preview')) return envelope({matches: 40, unknownNewEvidence: 2,
      sample: [programme(), programme(2)], generation, capacity: {known: true, effective: 2}}) as T;
    const index = rows.findIndex(row => url.pathname === routePath(method === 'DELETE' ? 'delete_dvr_rules_id' : 'put_dvr_rules_id', {id: row.id}));
    assert(index >= 0, 'only an actual offered rule is mutated');
    const command = body as {expectedRevision: number; requestId: string; config: typeof rows[number]['config']; future?: string};
    assert.equal(command.expectedRevision, rows[index].revision);assert.match(command.requestId, /^[a-f0-9]{48}$/);
    if (method === 'DELETE') {rows.splice(index, 1);return envelope({deleted: true}) as T;}
    rows[index] = {...rows[index], revision: rows[index].revision + 1, config: command.config};return envelope(rows[index]) as T;
  }};
  const context = {api, scope: {serverId, viewerId: 'actual-captured-viewer'}, isCurrent: () => active,
    identity: {serverName: 'Stored server'}, effects: {reportError() {}}} as unknown as ScreenContext;
  const binding = createSeriesRulesBindings(context, {timezone: 'UTC', locale: 'en-US', now: () => Date.parse('2026-10-24T12:00:00Z'),
    ...(options.defaultNonce ? {} : {requestId: options.requestId ?? (async () => {nonces++;return id(nonces, 48);})})});
  return {binding, calls, rows, nonces: () => nonces, authority: (value: boolean) => {active = value;}};
}

test('real DVR facade is lazy, uses global aggregates and advances only an explicitly requested cursor page', async () => {
  const f = setup({total: 60});assert.equal(f.calls.length, 0);
  try {
    await f.binding.load();const state = f.binding.getSnapshot(), row = state.view!.rules[0];
    assert.equal(state.phase, 'ready');assert.equal(state.view?.fromServer, true);assert.equal(state.view?.rules.length, 50);assert.equal(state.view?.ruleCount, 60);
    assert.equal(state.view?.upcomingCount, 8);assert.equal(state.view?.recordedCount, 20);assert.equal(state.view?.channelCount, 14);
    assert.equal(state.view?.sourceName, 'Stored server');assert.equal(row.title, 'Stored rule 1');assert.equal(row.upcomingCount, 8);
    assert.equal(row.recorded, 20);assert.equal(row.tone, undefined, 'illustrative fixture colours are not fabricated producer facts');
    assert.equal(row.conflict, false, 'an aggregate conflict elsewhere does not mark this scheduled next programme');
    assert.equal(row.settings.keep, '6');assert.equal(row.settings.endLate, 7 / 60);
    assert.equal(f.calls.filter(call => new URL(call.path, 'http://unit.invalid').pathname === routePath('get_dvr')).length, 1);
    await f.binding.more();assert.equal(f.binding.getSnapshot().view?.rules.length, 60);assert.equal(f.binding.getSnapshot().nextCursor, undefined);
    assert.strictEqual(f.binding.getSnapshot().view!.rules[0], row, 'appending keeps the actual current editor object');
  } finally {f.binding.dispose();}
});

test('preview uses server membership/count instead of locally filtering its bounded sample or inferring new evidence', async () => {
  const f = setup();
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0];await f.binding.preview(row, {...row.settings, episodes: 'new'});
    const preview = f.binding.getSnapshot().preview!;
    assert.equal(preview.count, 40);assert.equal(preview.airings.length, 2);assert.equal(preview.airings[0].isNew, true);assert.equal(preview.airings[1].isNew, false);
    assert.equal(preview.airings[0].scheduled, true);assert.equal(preview.airings[1].scheduled, undefined, 'a preview match does not invent a recording state');
    assert.equal(preview.airings[1].channel, undefined);assert.equal(preview.airings[0].channel, 'Stored channel');
    assert.equal(f.nonces(), 0, 'readonly preview is not a mutation receipt');
  } finally {f.binding.dispose();}
});

test('next-conflict presentation requires the actual next programme recording state', async () => {
  const f = setup({intercept: async path => {
    if (new URL(path, 'http://unit.invalid').pathname !== routePath('get_dvr')) return;
    const rule = wireRule();rule.summary.next = {...programme(), recordingId: id(702), recordingState: 'conflicted'};
    return envelope({recordings: [], rules: [rule], nextCursor: '', revision: 5, captureAvailable: true, captureUnavailableReason: '',
      deletionAvailable: true, usage: {bytes: 0, pendingDeleteBytes: 0, recordings: 20}});
  }});
  try {await f.binding.load();assert.equal(f.binding.getSnapshot().view?.rules[0].conflict, true);}
  finally {f.binding.dispose();}
});

test('a changed DVR cursor revision cannot mix rows or masquerade as a complete list', async () => {
  const f = setup({total: 60, intercept: async path => {
    const url = new URL(path, 'http://unit.invalid');
    if (url.pathname === routePath('get_dvr') && url.searchParams.get('cursor')) return envelope({recordings: [], rules: [wireRule(51)], nextCursor: '', revision: 6,
      captureAvailable: true, captureUnavailableReason: '', deletionAvailable: true, usage: {bytes: 0, pendingDeleteBytes: 0, recordings: 20}});
  }});
  try {
    await f.binding.load();const previous = f.binding.getSnapshot().view!;await assert.rejects(f.binding.more());
    const current = f.binding.getSnapshot();assert.strictEqual(current.view, previous);assert.equal(current.view?.rules.length, 50);
    assert.equal(current.view?.ruleCount, 60);assert(current.error);assert.equal(current.nextCursor, '50');
  } finally {f.binding.dispose();}
});

test('a newer draft aborts the old readonly preview and rejects its late sample', async () => {
  const held = deferred<unknown>();let previews = 0;let signal: AbortSignal | undefined;
  const f = setup({intercept: async (path, _method, _body, active) => {
    if (path !== routePath('post_dvr_rules_preview')) return;
    if (++previews === 1) {signal = active;return held.promise;}
    return envelope({matches: 3, unknownNewEvidence: 0, sample: [], generation, capacity: {known: true, effective: 2}});
  }});
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0];const first = f.binding.preview(row, row.settings);
    await f.binding.preview(row, {...row.settings, episodes: 'new'});assert.equal(signal?.aborted, true);assert.equal(f.binding.getSnapshot().preview?.count, 3);
    held.resolve(envelope({matches: 99, unknownNewEvidence: 0, sample: [programme()], generation, capacity: {known: true, effective: 2}}));await first;
    assert.equal(f.binding.getSnapshot().preview?.count, 3);assert.equal(f.binding.getSnapshot().preview?.airings.length, 0);
  } finally {held.resolve(envelope({}));f.binding.dispose();}
});

test('the installed DVR receipt producer emits the actual 48-hex grammar without a UUID compatibility alias', async () => {
  const f = setup({defaultNonce: true});
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0];assert.equal(await f.binding.save(row, row.settings), true);
    const request = f.calls.find(call => call.method === 'PUT')!.body as {requestId: string};assert.match(request.requestId, /^[a-f0-9]{48}$/);
  } finally {f.binding.dispose();}
});

test('saving uses original revision, canonical milliseconds and preserves every undrawn predicate/option', async () => {
  const f = setup();
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0];
    assert.equal(await f.binding.save(row, {...row.settings, enabled: false, episodes: 'new', keep: '3', startEarly: 3}), true);
    const write = f.calls.find(call => call.method === 'PUT')!, body = write.body as {expectedRevision: number; config: {sourceId: string; blockedChannels: string[]; keywords: string[]; blockedKeywords: string[]; options: Record<string, unknown>}};
    assert.equal(body.expectedRevision, 7);assert.equal(body.config.sourceId, sourceId);assert.deepEqual(body.config.blockedChannels, [id(8)]);
    assert.deepEqual(body.config.keywords, ['stored keyword']);assert.deepEqual(body.config.blockedKeywords, ['stored exclusion']);
    assert.equal(body.config.options.beforeMs, 180000);assert.equal(body.config.options.afterMs, 7000);assert.equal(body.config.options.priority, 3);assert.equal(body.config.options.retentionDays, 42);
    assert.equal(f.binding.getSnapshot().view!.rules[0].settings.enabled, false);
    await assert.rejects(f.binding.save(row, row.settings), {name: 'AbortError'});
  } finally {f.binding.dispose();}
});

test('unknown/conflicted writes keep the genuine original row and receipt instead of optimistic edits or automatic rebasing', async () => {
  const f = setup({intercept: async (_path, method) => {if (method === 'PUT') throw status(409);}});
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0], settings = {...row.settings, episodes: 'new' as const};
    assert.equal(await f.binding.save(row, settings), false);assert.strictEqual(f.binding.getSnapshot().view!.rules[0], row);
    assert.equal(row.settings.episodes, 'all');assert.equal(await f.binding.save(row, settings), false);assert.equal(f.nonces(), 1);
    const writes = f.calls.filter(call => call.method === 'PUT');assert.deepEqual(writes[0].body, writes[1].body);
  } finally {f.binding.dispose();}
});

test('delete preserves the explicit upcoming disposition and refreshes only after the actual receipt', async () => {
  const f = setup();
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0];assert.equal(await f.binding.remove(row, 'keep'), true);
    const command = f.calls.find(call => call.method === 'DELETE')!.body as {future: string; expectedRevision: number};
    assert.equal(command.future, 'keep');assert.equal(command.expectedRevision, 7);assert.equal(f.binding.getSnapshot().view!.rules.length, 0);
    assert.equal(f.binding.getSnapshot().view!.ruleCount, 0);
  } finally {f.binding.dispose();}
});

test('retirement during ID generation refuses before POST/PUT and old authority cannot revive on Back', async () => {
  const nonce = deferred<string>(), f = setup({requestId: () => nonce.promise});
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0], saving = f.binding.save(row, row.settings);
    f.authority(false);nonce.resolve(id(1, 48));assert.equal(await saving, false);assert.equal(f.binding.getSnapshot().phase, 'retired');
    f.authority(true);assert.equal(f.binding.getSnapshot().view, undefined);assert.equal(f.calls.some(call => call.method !== 'GET'), false);
  } finally {nonce.resolve(id(1, 48));f.binding.dispose();}
});

test('permission failure discards every private rule/header/preview fact and clears pending source work', async () => {
  let deny = false;
  const f = setup({intercept: async (_path, method) => {if (deny && method === 'POST') throw status(403);}});
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0];deny = true;await f.binding.preview(row, row.settings);
    const state = f.binding.getSnapshot();assert.equal(state.phase, 'retired');assert.equal(state.view, undefined);assert.equal(state.preview, undefined);
    assert.equal((state.error as {status: number}).status, 403);assert(f.calls.filter(call => call.signal).every(call => call.signal!.aborted));
  } finally {f.binding.dispose();}
});

test('complex existing channel filters remain unselected and untouched; title rules cannot widen channel scope', () => {
  const config = {...wireRule().config, options: {beforeSeconds: 120, afterSeconds: 7, priority: 3, retentionDays: 42, episodeLimit: 6},
    allowedChannels: [id(7), id(8)], episodes: 'all' as const};
  const rule = {id: id(1), revision: 7, config, reconcileState: 'ready', diagnostic: '', updatedAt: start} as RecordingRule;
  const settings = ruleSettings(rule);assert.equal(settings.channel, undefined);
  assert.deepEqual(editedRuleConfig(rule, settings).allowedChannels, [id(7), id(8)]);assert.equal(editedRuleConfig(rule, settings).options.afterSeconds, 7);
  const title = {...rule, config: {...config, matchKind: 'title' as const, title: 'Stored title', seriesId: '', channelId}};
  assert.equal(ruleSettings(title).channelLocked, true);assert.throws(() => editedRuleConfig(title, {...ruleSettings(title), channel: 'any'}));
});
