import test from 'node:test';
import assert from 'node:assert/strict';
import {createProgrammeBindings, defaultSeriesChoice} from '../src/screens/live/programme-bindings.ts';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import type {GuideChannelRow, GuideProgram} from '@core/guide/index.ts';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

// The Record series editor asks the server what a rule with its choices would record (POST /v1/dvr/rules?preview=true,
// which makes nothing), and More airings names each airing's own episode.
const now = Date.parse('2026-11-12T20:15:00Z');
const row = {id: 'chn_1', kind: 'live', number: '101', name: 'Harbour News', group: '', favorite: false, tuneAvailable: true, recordAvailable: true,
  guide: 'full', native: {id: 'chn_1', name: 'Harbour News', number: '101', kind: 'source', favorite: false, hidden: false}} as unknown as GuideChannelRow;
const programme = {id: 'pg_1', channelId: 'chn_1', title: 'Evening Report', start: Date.parse('2026-11-12T21:00:00Z'), end: Date.parse('2026-11-12T22:00:00Z')} as GuideProgram;

test('the series editor counts what the rule would record and lists those airings with their episodes', async () => {
  const sent: {method: string; path: string; body: unknown}[] = [];
  const context = withQueries({scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true, effects: {reportError() {}},
    api: {async request(path: string, method = 'GET', body?: unknown) {
      if (method !== 'GET') {sent.push({method, path, body});
        return {id: 'rule', title: 'Evening Report', source: 'lsr', byTitle: true, newOnly: true, priority: 0, paused: false, preview: {matches: 7, airings: [
          {title: 'Evening Report', episodeTitle: 'Lighthouse', season: 3, episode: 2, channel: 'Harbour News', startsAt: '2026-11-13T21:00:00Z', endsAt: '2026-11-13T22:00:00Z', alreadyBooked: false}]}};}
      if (path.startsWith('/v1/live/programmes/')) return {id: 'pg_1', title: 'Evening Report', startsAt: '2026-11-12T21:00:00Z', endsAt: '2026-11-12T22:00:00Z',
        channel: row.native, airings: [{id: 'pg_2', channel: 'Harbour News', channelId: 'chn_1', startsAt: '2026-11-14T21:00:00Z', endsAt: '2026-11-14T22:00:00Z', episodeTitle: 'Fog', season: 3, episode: 3}],
        reminder: false, canRecord: true};
      return {items: []};
    }}}) as unknown as ScreenContext;
  const bindings = createProgrammeBindings(context, row, programme, {timezone: 'UTC', locale: 'en-US', now: () => now});
  await bindings.load();
  assert.deepEqual(bindings.getSnapshot().preview?.airings.map(one => [one.title, one.episode, one.channelId]), [['Fog', 'S3 E3', 'chn_1']]);
  await bindings.preview({...defaultSeriesChoice, episodes: 'all'});
  assert.deepEqual(sent.map(one => [one.method, one.path, (one.body as {newOnly?: boolean}).newOnly]), [['POST', '/v1/dvr/rules?preview=true', false]]);
  assert.equal(bindings.getSnapshot().preview?.matches, 7);
  assert.deepEqual(bindings.getSnapshot().preview?.airings.map(one => [one.title, one.episode]), [['Lighthouse', 'S3 E2']]);
});
