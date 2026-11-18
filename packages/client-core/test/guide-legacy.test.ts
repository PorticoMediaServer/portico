import test from 'node:test';
import assert from 'node:assert/strict';
import {parseChannelGuide} from '../src/channel-guide.ts';
import {HOUR_MS, legacyGuideState, legacyProgram} from '../src/guide/index.ts';
const SERVER = 'srv';
const NOW = Date.parse('2026-09-22T19:20:00Z');

test('guide adapter: failed refreshes are out of date and unknown states remain ready', async () => {
  assert.equal(legacyGuideState('degraded'), 'stale');
  assert.equal(legacyGuideState('stale'), 'stale');
  assert.equal(legacyGuideState('credentials-required'), 'stale');
  for (const state of ['healthy', '', 'refreshed', 'something-new']) assert.equal(legacyGuideState(state), 'ready', state);
});

test('guide adapter: programme facts map behind presence, malformed facts never fatal', async () => {
  const route = {kind: 'live-source' as const, start: new Date(NOW).toISOString(), end: new Date(NOW + HOUR_MS).toISOString(), timezone: 'UTC', search: '', sourceId: ''};
  const base = {id: 'p', channelId: 'c', title: 'Harbour Lights', start: route.start, end: route.end, lineage: 'provider-id'};
  const full = parseChannelGuide({protocolVersion: '1.0', serverId: SERVER, guide: {state: 'ready', viewerFence: 'f', start: route.start, end: route.end, timezone: 'UTC', nextCursor: '', observedAt: route.start, days: 8, sources: [{id: 's', name: 'Antenna', generation: 'g', publishedAt: route.start, availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: 'healthy'}], channels: [{id: 'c', sourceId: 's', provenance: 'live-source', name: 'One', number: '1', group: '', generation: 'g', tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 0, programmes: [{...base, subtitle: 'The Arrival', episode: {season: 2, number: 5}, categories: ['Drama', 'Mystery'], rating: {system: 'VCHIP', value: 'TV-14'}, year: 2019, starRating: '7/10', flags: {live: true, new: false, premiere: false, repeat: false}, image: '/v1/items/item123/art/poster'}]}]}}, SERVER, route);
  const mapped = legacyProgram(full.channels[0].programmes[0]!);
  assert.equal(mapped.subtitle, 'The Arrival');
  assert.deepEqual(mapped.episode, {season: 2, number: 5});
  assert.deepEqual(mapped.categories, ['Drama', 'Mystery']);
  assert.equal(mapped.rating, 'TV-14');
  assert.equal(mapped.year, 2019);
  assert.equal(mapped.starRating, '7/10');
  assert.equal(mapped.flags?.live, true);
  assert.equal(mapped.image, '/v1/items/item123/art/poster');
  assert.equal(full.days, 8);
  const minimal = parseChannelGuide({protocolVersion: '1.0', serverId: SERVER, guide: {state: 'ready', viewerFence: 'f', start: route.start, end: route.end, timezone: 'UTC', nextCursor: '', observedAt: route.start, sources: [{id: 's', name: 'Antenna', generation: 'g', publishedAt: route.start, availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: 'healthy'}], channels: [{id: 'c', sourceId: 's', provenance: 'live-source', name: 'One', number: '1', group: '', generation: 'g', tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 0, programmes: [{...base}]}]}}, SERVER, route);
  const bare = legacyProgram(minimal.channels[0].programmes[0]!);
  assert.equal(bare.subtitle, undefined);
  assert.equal(bare.rating, undefined);
  assert.equal(bare.starRating, undefined);
  assert.equal(bare.image, undefined);
  assert.equal(minimal.days, undefined);
  const malformed = parseChannelGuide({protocolVersion: '1.0', serverId: SERVER, guide: {state: 'ready', viewerFence: 'f', start: route.start, end: route.end, timezone: 'UTC', nextCursor: '', observedAt: route.start, days: 'many' as never, sources: [{id: 's', name: 'Antenna', generation: 'g', publishedAt: route.start, availableStart: '', availableEnd: '', provenance: 'live-source', refreshState: 'healthy'}], channels: [{id: 'c', sourceId: 's', provenance: 'live-source', name: 'One', number: '1', group: '', generation: 'g', tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 0, programmes: [{...base, subtitle: 42 as never, episode: {season: 0} as never, categories: 'Drama' as never, rating: {value: 42} as never, year: 'n/a' as never, image: 'https://provider.invalid/x.png'}]}]}}, SERVER, route);
  const dropped = legacyProgram(malformed.channels[0].programmes[0]!);
  assert.equal(dropped.subtitle, undefined);
  assert.equal(dropped.episode, undefined);
  assert.deepEqual(dropped.categories, []);
  assert.equal(dropped.rating, undefined);
  assert.equal(dropped.image, undefined);
  assert.equal(malformed.days, undefined);
});

