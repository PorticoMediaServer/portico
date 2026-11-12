import test from 'node:test';
import assert from 'node:assert/strict';
import {createSeriesRulesBindings, editedRuleConfig, ruleSettings} from '../src/screens/live/series-rules-bindings.ts';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import type {RecordingRule} from '@core/dvr.ts';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

// Series rules on the new server: GET /v1/dvr/rules (cursor pages), PATCH and DELETE /v1/dvr/rules/{id}, GET /v1/dvr for
// the counts and the space, and the first page of GET /v1/live/channels for the channels the guide reaches.
const id = (number: number, width = 64) => number.toString(16).padStart(width, '0');
const channelId = id(2), serverId = 'unit-server', start = '2026-10-24T14:00:00Z';
const nextRule = (number: number, patch: Record<string, unknown> = {}) => ({id: 'lru_' + number, title: 'Stored rule ' + number, source: 'lsr_1', byTitle: true,
  newOnly: true, priority: 0, paused: false, ...patch});
const status = (status: number) => Object.assign(new Error('unit request rejected'), {status});
type Call = {path: string; method: string; body: unknown};

function setup(options: {total?: number; answer?: (call: Call) => unknown} = {}) {
  const rows = Array.from({length: options.total ?? 1}, (_, index) => nextRule(index + 1));
  const calls: Call[] = [];
  let active = true;
  const api = {async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
    const call = {path, method, body};calls.push(call);
    const answer = await options.answer?.(call);
    if (answer instanceof Error) throw answer;
    if (answer !== undefined) return answer as T;
    const url = new URL(path, 'http://unit.invalid');
    if (url.pathname === '/v1/dvr') return {upcoming: 8, recorded: 20, unwatched: 5, conflicts: 1, freeBytes: 412e9, hoursLeft: 230, keepRule: 'until-space-needed',
      folderOk: true, canRecord: true} as T;
    if (url.pathname === '/v1/live/channels') return {items: [], sources: [{id: 'lsr_1', name: 'Stored provider', kind: 'live', channels: 14, favorites: 0, groups: [],
      guideThrough: '2026-10-27T00:00:00Z'}]} as T;
    if (url.pathname === '/v1/dvr/recordings') return {items: []} as T;
    if (url.pathname === '/v1/dvr/rules') {
      const offset = Number(url.searchParams.get('cursor') ?? 0), next = offset + 50;
      return {items: rows.slice(offset, next), ...(next < rows.length ? {nextCursor: String(next)} : {})} as T;
    }
    const index = rows.findIndex(row => url.pathname === '/v1/dvr/rules/' + row.id);
    assert(index >= 0, 'only a rule that is shown is changed');
    if (method === 'DELETE') {rows.splice(index, 1);return undefined as T;}
    rows[index] = {...rows[index]!, ...(body as object)};return rows[index] as T;
  }};
  const context = withQueries({api, scope: {serverId, viewerId: 'viewer'}, isCurrent: () => active,
    identity: {serverName: 'Stored server'}, effects: {reportError() {}}}) as unknown as ScreenContext;
  const binding = createSeriesRulesBindings(context, {timezone: 'UTC', locale: 'en-US', now: () => Date.parse('2026-10-24T12:00:00Z')});
  const sent = () => calls.filter(call => call.method !== 'GET').map(call => [call.method, call.path, call.body]);
  return {binding, calls, rows, sent, authority: (value: boolean) => {active = value;}};
}

test('nothing is read until the screen loads; then the rules, the server\'s counts and space, and further pages only when asked', async () => {
  const f = setup({total: 60});assert.equal(f.calls.length, 0);
  try {
    await f.binding.load();const state = f.binding.getSnapshot();
    assert.equal(state.phase, 'ready');assert.equal(state.view?.rules.length, 50);
    assert.deepEqual([state.view?.ruleCount, state.view?.upcomingCount, state.view?.recordedCount, state.view?.conflictCount, state.view?.channelCount], [60, 8, 20, 1, 14]);
    assert.deepEqual(state.view?.storage, {free: 412, hoursLeft: 230});
    assert.equal(state.view?.rules[0]?.title, 'Stored rule 1');
    assert.equal(state.view?.rules[0]?.upcomingCount, undefined, 'the server gives no per-rule counts, so none are drawn');
    const row = state.view!.rules[0];
    await f.binding.more();assert.equal(f.binding.getSnapshot().view?.rules.length, 60);assert.equal(f.binding.getSnapshot().nextCursor, undefined);
    assert.strictEqual(f.binding.getSnapshot().view!.rules[0], row, 'a further page keeps the rows already drawn');
    assert.deepEqual(f.sent(), []);
  } finally {f.binding.dispose();}
});

test('saving sends the editor\'s choices as one PATCH of the rule, then reads the rules again', async () => {
  const f = setup();
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0]!;
    assert.equal(await f.binding.save(row, {...row.settings, enabled: false, episodes: 'all', keep: '5', startEarly: 2, endLate: 1}), true);
    assert.deepEqual(f.sent(), [['PATCH', '/v1/dvr/rules/lru_1', {newOnly: false, priority: 0, paused: true, anyChannel: true, keepNewest: 5, padBeforeMs: 120_000,
      padAfterMs: 60_000}]]);
    assert.equal(f.binding.getSnapshot().view?.rules[0]?.settings.enabled, false);
  } finally {f.binding.dispose();}
});

test('deleting a rule keeps or cancels what it booked, as chosen', async () => {
  const f = setup({total: 2});
  try {
    await f.binding.load();
    assert.equal(await f.binding.remove(f.binding.getSnapshot().view!.rules[0]!, 'keep'), true);
    assert.equal(await f.binding.remove(f.binding.getSnapshot().view!.rules[0]!, 'cancel'), true);
    assert.deepEqual(f.sent().map(([method, path]) => [method, path]), [['DELETE', '/v1/dvr/rules/lru_1?bookings=keep'], ['DELETE', '/v1/dvr/rules/lru_2?bookings=cancel']]);
    assert.equal(f.binding.getSnapshot().view?.rules.length, 0);
  } finally {f.binding.dispose();}
});

test('a refused save keeps the rule as shown; a second command while one runs sends nothing', async () => {
  let release: (() => void) | undefined, refuse = false;
  const f = setup({answer: call => call.method === 'GET' ? undefined : refuse ? status(409) : new Promise<void>(resolve => {release = resolve;}).then(() => nextRule(1))});
  try {
    await f.binding.load();const row = f.binding.getSnapshot().view!.rules[0]!;
    const first = f.binding.save(row, row.settings);
    assert.equal(await f.binding.remove(row, 'keep'), false);
    release?.();assert.equal(await first, true);
    refuse = true;
    const shown = f.binding.getSnapshot().view!.rules[0]!;
    assert.equal(await f.binding.save(shown, {...shown.settings, enabled: false}), false);
    assert.equal(f.binding.getSnapshot().view?.rules[0]?.settings.enabled, true);
    assert.equal((f.binding.getSnapshot().error as {status?: number})?.status, 409);
    assert.equal(f.sent().length, 2);
  } finally {f.binding.dispose();}
});

test('the server previews no edit of an existing rule: Preview says so and sends nothing', async () => {
  const f = setup();
  try {
    await f.binding.load();const before = f.calls.length, row = f.binding.getSnapshot().view!.rules[0]!;
    await f.binding.preview(row, row.settings);
    assert.equal((f.binding.getSnapshot().previewError as {code?: string})?.code, 'not_on_server');
    assert.equal(f.calls.length, before);
  } finally {f.binding.dispose();}
});

test('a refused read (403) retires the screen and keeps no rule, and nothing is read once the screen is gone', async () => {
  let refuse = false;
  const f = setup({answer: () => refuse ? status(403) : undefined});
  await f.binding.load();
  refuse = true;
  await f.binding.load().catch(() => undefined);
  assert.equal(f.binding.getSnapshot().phase, 'retired');assert.equal(f.binding.getSnapshot().view, undefined);
  const before = f.calls.length;
  await assert.rejects(async () => f.binding.load());
  assert.equal(f.calls.length, before);
});

test('complex existing channel filters remain unselected and untouched; title rules cannot widen channel scope', () => {
  const config = {name: 'Stored rule', sourceId: 'lsr_1', seriesId: 'series', enabled: true, blockedChannels: [id(8)], keywords: [], blockedKeywords: [], options: {beforeSeconds: 120, afterSeconds: 7, priority: 3, retentionDays: 42, episodeLimit: 6},
    allowedChannels: [id(7), id(8)], episodes: 'all' as const};
  const rule = {id: id(1), revision: 7, config, reconcileState: 'ready', diagnostic: '', updatedAt: start} as RecordingRule;
  const settings = ruleSettings(rule);assert.equal(settings.channel, undefined);
  assert.deepEqual(editedRuleConfig(rule, settings).allowedChannels, [id(7), id(8)]);assert.equal(editedRuleConfig(rule, settings).options.afterSeconds, 7);
  const title = {...rule, config: {...config, matchKind: 'title' as const, title: 'Stored title', seriesId: '', channelId}};
  assert.equal(ruleSettings(title).channelLocked, true);assert.throws(() => editedRuleConfig(title, {...ruleSettings(title), channel: 'any'}));
});
