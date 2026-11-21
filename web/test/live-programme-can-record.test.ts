import test from 'node:test';
import assert from 'node:assert/strict';
import {createProgrammeBindings} from '../src/screens/live/programme-bindings.ts';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import type {GuideChannelRow, GuideProgram} from '@core/guide/index.ts';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

// The guide row says a channel can record; the programme's own read says whether this viewer can record it. On the demo a
// member's panel drew Record from the row while the server said no, and Record then did nothing. The server's answer now
// reaches the panel's snapshot, which offers Record only when it is not false.
const now = Date.parse('2026-11-12T20:15:00Z');
const row = {id: 'chn_1', kind: 'live', number: '101', name: 'Harbour News', group: '', favorite: false, tuneAvailable: true, recordAvailable: true,
  guide: 'full', native: {id: 'chn_1', name: 'Harbour News', number: '101', kind: 'source', favorite: false, hidden: false}} as unknown as GuideChannelRow;
const programme = {id: 'pg_1', channelId: 'chn_1', title: 'Evening Report', start: Date.parse('2026-11-12T21:00:00Z'), end: Date.parse('2026-11-12T22:00:00Z')} as GuideProgram;

async function loaded(canRecord: boolean) {
  const sent: string[] = [];
  const context = withQueries({scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true, effects: {reportError() {}},
    api: {async request(path: string, method = 'GET') {
      if (method !== 'GET') {sent.push(`${method} ${path}`);return undefined;}
      if (path.startsWith('/v1/live/programmes/')) return {id: 'pg_1', title: 'Evening Report', startsAt: '2026-11-12T21:00:00Z', endsAt: '2026-11-12T22:00:00Z',
        channel: row.native, airings: [], reminder: false, canRecord};
      return {items: []};
    }}}) as unknown as ScreenContext;
  const bindings = createProgrammeBindings(context, row, programme, {timezone: 'UTC', locale: 'en-US', now: () => now});
  await bindings.load();
  return {bindings, sent};
}

test('a programme the server says this viewer cannot record has canRecord false, and Record sends nothing', async () => {
  const {bindings, sent} = await loaded(false);
  assert.equal(bindings.getSnapshot().canRecord, false);
  assert.equal(await bindings.record(), false);
  assert.deepEqual(sent, []);
});

test('a programme the server lets this viewer record has canRecord true', async () => {
  const {bindings} = await loaded(true);
  assert.equal(bindings.getSnapshot().canRecord, true);
});
