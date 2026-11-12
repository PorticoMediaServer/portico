import test from 'node:test';
import assert from 'node:assert/strict';
import {createProgrammeBindings} from '../src/screens/live/programme-bindings.ts';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import type {GuideChannelRow, GuideProgram} from '@core/guide/index.ts';
import {withQueries} from '../../packages/client-core/test/fixtures/viewer-queries.ts';

// Review desk B, #2896, point 3: the programme panel's Cancel on a recording in progress stops it, which keeps what was
// captured (rule 50); only a booking that has not started is cancelled. A DELETE without action=stop deletes the file.
const now = Date.parse('2026-11-12T20:15:00Z');
const row = {id: 'chn_1', kind: 'live', number: '101', name: 'Harbour News', group: '', favorite: false, tuneAvailable: true, recordAvailable: true,
  guide: 'full', native: {id: 'chn_1', name: 'Harbour News', number: '101', kind: 'source', favorite: false, hidden: false}} as unknown as GuideChannelRow;
const programme = {id: 'pg_1', channelId: 'chn_1', title: 'Evening Report', start: Date.parse('2026-11-12T20:00:00Z'), end: Date.parse('2026-11-12T21:00:00Z')} as GuideProgram;

async function cancelWith(state: string) {
  const sent: {method: string; path: string}[] = [];
  const recording = {id: 'rec_1', channel: 'Harbour News', title: 'Evening Report', startsAt: '2026-11-12T20:00:00Z', endsAt: '2026-11-12T21:00:00Z',
    padBeforeMs: 0, padAfterMs: 0, priority: 0, state, inGuide: true, keep: false, fromRule: false};
  const context = withQueries({scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => true, effects: {reportError() {}},
    api: {async request(path: string, method = 'GET') {
      if (method !== 'GET') {sent.push({method, path});return undefined;}
      if (path.startsWith('/v1/live/programmes/')) return {id: 'pg_1', title: 'Evening Report', startsAt: '2026-11-12T20:00:00Z', endsAt: '2026-11-12T21:00:00Z',
        channel: row.native, airings: [], recording, reminder: false, canRecord: true};
      return {items: []};
    }}}) as unknown as ScreenContext;
  const bindings = createProgrammeBindings(context, row, programme, {timezone: 'UTC', locale: 'en-US', now: () => now});
  await bindings.load();
  assert.equal(await bindings.cancel(), true);
  return {sent, notice: bindings.getSnapshot().notice};
}

test('Cancel on a recording in progress stops it and keeps what was captured', async () => {
  const {sent, notice} = await cancelWith('recording');
  assert.deepEqual(sent, [{method: 'DELETE', path: '/v1/dvr/recordings/rec_1?action=stop'}]);
  assert.equal(notice, 'stopped');
});

test('Cancel on a booking that has not started cancels it', async () => {
  const {sent, notice} = await cancelWith('scheduled');
  assert.deepEqual(sent, [{method: 'DELETE', path: '/v1/dvr/recordings/rec_1'}]);
  assert.equal(notice, 'cancelled');
});
