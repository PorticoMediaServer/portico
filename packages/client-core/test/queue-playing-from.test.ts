import test from 'node:test';
import assert from 'node:assert/strict';
import {parseEntry, parseQueue} from '../src/playback-v1/types.ts';

const base = {entryId: 'qe_1', position: 0, itemId: 'itm_song', kind: 'track', title: 'Blinding Lights', available: true, images: {}};
const header = {id: 'q1', revision: 'r1', total: 2, medium: 'audio', current: {entryId: 'qe_1', position: 0}, repeat: 'off'};

test('a queue reads what it plays from, and an entry the segment it came with', () => {
  const q = parseQueue({...header, segments: [{id: 'source', kind: 'selector', label: 'After Hours', source: {kind: 'container', id: 'itm_album'}}]});
  assert.deepEqual(q.segments.map(s => [s.id, s.kind, s.label]), [['source', 'selector', 'After Hours']]);
  assert.deepEqual(parseQueue(header).segments, [], 'a queue started from nothing nameable');
  assert.equal(parseQueue({...header, segments: [{id: 'source', kind: 'radio'}]}).segments[0]!.kind, 'radio');
  // A malformed segment is dropped alone; the queue still parses.
  assert.deepEqual(parseQueue({...header, segments: [{id: 'bad id'}, {id: 'source', kind: 'weird', label: 7}, 'x']}).segments.map(s => [s.id, s.kind, s.label]), [['source', 'other', '']]);
  assert.equal(parseEntry({...base, segmentId: 'source'}).segmentId, 'source');
  assert.equal(parseEntry(base).segmentId, undefined, 'added by hand');
  assert.equal(parseEntry({...base, segmentId: 'bad id'}).segmentId, undefined);
});

test("the accepted window carries the label to the entry that came with it, for Now Playing's \"Playing from\"", async t => {
  const {PlaybackService} = await import('../src/index.ts');
  const {V1PlaybackApi} = await import('../src/playback-v1/legacy-bridge.ts');
  const {V1QueuePlayer} = await import('../src/playback-v1/v1-queue-player.ts');
  const {QueueClient} = await import('../src/playback-v1/queue.ts');
  const items = [{...base, entryId: 'qe_1', position: 0, itemId: 'itm_a', segmentId: 'source'}, {...base, entryId: 'qe_2', position: 1, itemId: 'itm_b'}];
  const http = {send: async () => ({status: 200, headers: {}, body: {items, start: 0, total: 2, revision: 'r1'}})};
  const api = new V1PlaybackApi(http);
  const playback = new PlaybackService(api, () => 'device');
  const player = new V1QueuePlayer({http, api, playback, viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'}});
  t.after(() => { player.dispose(); playback.leave(); });
  const window = await new QueueClient(http).window('q1');
  (player as unknown as {accept(h: unknown, w: unknown): void}).accept(parseQueue({...header, segments: [{id: 'source', kind: 'selector', label: 'After Hours'}]}), window);
  const view = (player.getSnapshot() as {view?: {window?: {segments: readonly {id: string; label?: string}[]; entries: readonly {entryId: string; segmentId?: string}[]}}}).view;
  assert.deepEqual(view?.window?.segments, [{id: 'source', label: 'After Hours'}]);
  assert.deepEqual(view?.window?.entries.map(e => [e.entryId, e.segmentId]), [['qe_1', 'source'], ['qe_2', undefined]]);
});
