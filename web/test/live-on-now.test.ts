import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {onNowProgramme} from '../src/screens/live/on-now-programme.ts';
import {createLiveBindings} from '../src/screens/live/bindings.ts';
import type {ScreenContext} from '../src/screens/detail/context.ts';

test('On now retains genuine programme flags/episode/progress and never promotes a future airing', () => {
  const now = Date.parse('2026-10-23T20:05:00Z');
  const programme = {id: 'p', channelId: 'channel', title: 'Stored episode', start: now - 300_000, end: now + 1_500_000,
    subtitle: 'Stored subtitle', episode: {season: 3, number: 1}, flags: {live: false, new: true}, recording: {id: 'recording', state: 'recording'}};
  const result = onNowProgramme([programme], now, defaultI18n, 'America/Halifax')!;
  assert.equal(result.title, programme.title);assert.equal(result.live, false);assert.equal(result.isNew, true);assert.equal(result.recording, true);
  assert.equal(result.progress, 1 / 6);assert.equal(result.remaining, '25 min left');
  assert.deepEqual(result.facts.map(part => part.text), ['S3 E1', 'Stored subtitle']);
  assert.deepEqual(onNowProgramme([{...programme, start: now + 1}], now, defaultI18n, 'UTC'), {title: defaultI18n.t('guide.noInformation'), facts: []});
});
test('On now requests bounded server favorites order and retires with the same authority', async () => {
  const calls: URL[] = [], denied = Object.assign(new Error('denied'), {status: 403});
  const context = {scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true,
    api: {async request(path: string) {calls.push(new URL(path, 'https://example.test'));throw denied;}}} as unknown as ScreenContext;
  const owner = createLiveBindings(context, {kind: 'live-source', sourceId: 'actual-source', timezone: 'UTC', group: 'Drama', sort: 'number'});
  const shelf = owner.onNow();assert.equal(calls.length, 0);
  shelf.store.setViewport({firstRow: 0, lastRow: 4, start: 1_000, end: 1_001});
  for (let i = 0; i < 12; i++) await new Promise(setImmediate);
  assert.equal(calls.length, 1);assert.equal(calls[0].searchParams.get('sort'), 'favorites');
  assert.equal(calls[0].searchParams.get('sourceId'), 'actual-source');assert.equal(calls[0].searchParams.get('group'), 'Drama');
  assert.equal(calls[0].searchParams.get('limit'), '50');assert.equal(owner.life.current(), false);
  assert.deepEqual(shelf.store.resident(), {channelPages: 0, tiles: 0, programs: 0, inFlight: 0, queued: 0});
  shelf.dispose();owner.dispose();
});
