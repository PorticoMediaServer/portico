import test from 'node:test';
import assert from 'node:assert/strict';
import {
  parseLibraryChannel, parseLibraryChannelConfig, librarySampleLabel,
  readLibraryChannels, readLibraryTemplates, previewLibraryChannel, saveLibraryChannel, readLibraryDefaults,
  type LibraryChannelConfig,
} from '../src/library-channels.ts';

function query(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {libraryIds: ['lib'], kinds: ['movie'], recentDays: 0, limit: 10, order: 'title', ...over};
}
function rule(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {id: 'r1', name: 'Rule', query: query(), mode: 'sequential', episodeMode: 'none', exhaustion: 'loop', deduplicationWindow: 0, maxConsecutive: 1, ...over};
}
function config(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    protocolVersion: '1.0', id: 'c1', name: 'Channel', description: '', enabled: true,
    position: 0, timezone: 'UTC', seed: 'seed', defaultRuleId: 'r1', viewerAccess: 'owner-only',
    logoItemId: '', templateId: '',
    quality: {mode: 'automatic', maxBitrate: 8000000, maxHeight: 1080, allowLossy: true, allowHdrToSdr: true},
    overlay: {enabled: false, corner: 'br', sizePercent: 10, insetPercent: 5, treatment: 'none'},
    rules: [rule()], blocks: [], ...over,
  };
}
function channel(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    config: config(), revision: 1, state: 'ok', healthCode: '', generation: 'g1',
    generatedThrough: '', candidateCount: 0, unresolvedDurationCount: 0, replacementBoundary: '', ...over,
  };
}
const api = (respond: (path: string, method: string, body: unknown) => unknown) => ({
  request: async (path: string, method = 'GET', body?: unknown) => respond(path, method, body),
});

test('a minimal config parses; protocol, order and enum violations do not', () => {
  const parsed = parseLibraryChannelConfig(config());
  assert.equal(parsed.id, 'c1');
  assert.equal(parsed.rules.length, 1);
  assert.throws(() => parseLibraryChannelConfig(config({protocolVersion: '2.0'})));
  assert.throws(() => parseLibraryChannelConfig(config({viewerAccess: 'everyone'})));
  assert.throws(() => parseLibraryChannelConfig({}));
  // Unknown rule order / mode / over-cap lists are refused, not defaulted.
  assert.throws(() => parseLibraryChannelConfig(config({rules: [rule({query: query({order: 'bogus'})})]})));
  assert.throws(() => parseLibraryChannelConfig(config({rules: [rule({mode: 'bogus'})]})));
  assert.throws(() => parseLibraryChannelConfig(config({rules: [rule({query: query({libraryIds: Array(65).fill('x')})})]})));
  assert.throws(() => parseLibraryChannelConfig(config({rules: [rule({maxConsecutive: 0})]})));
  assert.throws(() => parseLibraryChannelConfig(config({blocks: [{id: 'b1', weekdays: [0, 7], startMinute: 0, endMinute: 60, priority: 0, ruleId: 'r1', fallbackRuleId: '', anchor: 'channel-cursor', overrun: 'finish'}]})));
});

test('omitted lists read as empty; filter expressions are validated', () => {
  const parsed = parseLibraryChannelConfig(config({rules: [rule({query: {libraryIds: ['lib'], kinds: ['episode'], recentDays: 7, limit: 5, order: 'recent'}})]}));
  const q = parsed.rules[0]!.query;
  assert.deepEqual(q.showIds, []);
  assert.deepEqual(q.genres, []);
  assert.equal(q.yearFrom, 0);
  // A valid browse filter passes; a malformed one refuses the rule.
  const withFilter = parseLibraryChannelConfig(config({rules: [rule({query: query({filter: {variant:'predicate',field: 'title', operator: 'contains', value: 'x'}})})]}));
  assert.ok(withFilter.rules[0]!.query.filter);
  assert.throws(() => parseLibraryChannelConfig(config({rules: [rule({query: query({filter: {bogus: true}})})]})));
  // Oversize free text is refused.
  assert.throws(() => parseLibraryChannelConfig(config({rules: [rule({query: query({text: 'x'.repeat(201)})})]})));
});

test('a channel needs a valid revision and instants; bad server envelopes are refused', async () => {
  assert.equal(parseLibraryChannel(channel()).revision, 1);
  assert.throws(() => parseLibraryChannel(channel({revision: 0})));
  assert.throws(() => parseLibraryChannel(channel({generatedThrough: 'not-a-time'})));
  const good = channel();
  good.generatedThrough = '2026-09-01T00:00:00Z';
  assert.equal(parseLibraryChannel(good).generatedThrough, '2026-09-01T00:00:00Z');

  const list = await readLibraryChannels(api(async (path, method) => {
    assert.equal(path, '/v1/admin/library-channels');
    assert.equal(method, 'GET');
    return {serverId: 's', protocolVersion: '1.0', channels: [channel()]};
  }) as never, 's');
  assert.equal(list.length, 1);
  await assert.rejects(readLibraryChannels(api(async () => ({serverId: 'other', protocolVersion: '1.0', channels: []})) as never, 's'));
  await assert.rejects(readLibraryChannels(api(async () => ({serverId: 's', protocolVersion: '9.9', channels: []})) as never, 's'));
  await assert.rejects(readLibraryChannels(api(async () => ({serverId: 's', protocolVersion: '1.0', channels: Array(65).fill(channel())})) as never, 's'));
});

test('preview fills builder defaults older servers omit; templates and save/defaults parse', async () => {
  const preview = await previewLibraryChannel(api(async () => ({
    serverId: 's', protocolVersion: '1.0',
    preview: {
      catalogFence: 'f', boundaryPolicy: 'loop', blocks: [],
      rules: [{ruleId: 'r1', eligible: 3, unresolved: 0, semanticLimit: 10, sample: [{itemId: 'm1', title: 'Movie', durationMs: 100}]}],
    },
  })) as never, 's', config() as unknown as LibraryChannelConfig);
  assert.equal(preview.complete, true);
  assert.deepEqual(preview.firstDay, []);
  assert.equal(preview.durationMs, 0);
  assert.equal(preview.rules[0]!.complete, true);
  await assert.rejects(previewLibraryChannel(api(async () => ({serverId: 's', protocolVersion: '1.0', preview: {catalogFence: 'f'}})) as never, 's', config() as unknown as LibraryChannelConfig));

  const templates = await readLibraryTemplates(api(async () => ({
    serverId: 's', protocolVersion: '1.0',
    templates: [{id: 't1', name: 'T', description: '', minimumCandidates: 1, applicable: true, reason: '', config: config()}],
  })) as never, 's');
  assert.equal(templates.length, 1);
  const saved = await saveLibraryChannel(api(async (_p, m, body) => {
    assert.equal(m, 'POST');
    assert.equal((body as {expectedRevision: number}).expectedRevision, 2);
    return {serverId: 's', protocolVersion: '1.0', channel: channel()};
  }) as never, 's', config() as unknown as LibraryChannelConfig, 2, 'req-1');
  assert.equal(saved.revision, 1);
  const defaults = await readLibraryDefaults(api(async (path, method, body) => {
    assert.equal(path, '/v1/admin/library-channels/defaults');
    assert.equal(method, 'POST');
    assert.deepEqual(body, {timezone: 'UTC'});
    return {serverId: 's', config: config()};
  }) as never, 's', 'UTC');
  assert.equal(defaults.id, 'c1');
});

test('sample labels prefer the episode show name', () => {
  assert.equal(librarySampleLabel({title: 'Ep 3', showTitle: 'Show'}), 'Show');
  assert.equal(librarySampleLabel({title: 'Movie'}), 'Movie');
  assert.equal(librarySampleLabel({title: 'Ep', showTitle: ''}), 'Ep');
});

test('a channel with a setting this app does not know is left out of the list alone', async () => {
  const good = channel();
  const newer = channel({config: config({id: 'newer', rules: [rule({mode: 'ai-curated'})]})});
  const list = await readLibraryChannels(api(async () => ({serverId: 's', protocolVersion: '1.0', channels: [newer, good, 'nope']})) as never, 's');
  assert.equal(list.length, 1);
  assert.equal(list[0]!.config.id, (good.config as {id: string}).id);
});
