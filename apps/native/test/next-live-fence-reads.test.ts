import test from 'node:test';
import assert from 'node:assert/strict';
import {zapSourcesListed} from '../src-next/screens/watch/live-guide.ts';
import {liveActionOffer} from '../src-next/screens/watch/live-actions.ts';

// Step 7 (CLIENT-DATA-REVIEW 4.4), the playback lane's live reads: a moved viewer fence on a guide read is
// a newer answer; the channel's own identity (id, source, kind, generation) and the programme's exact
// time still bind it. A read the server refuses (401/403) never reaches these checks.
const fence = {sourceId: 'src', channelKind: 'live-source', channelGeneration: 'g1'};
const raw = {sourceId: 'src2', provenance: 'live-source', generation: 'g7'};
const listed = (rows: {id: string; provenance: string; generation: string}[], viewerFence = 'moved') => ({viewerFence, sources: rows});

test('zapping tunes while the directory still lists both sources, whatever its viewer fence; a missing or moved-on source still refuses', () => {
  const both = [{id: 'src', provenance: 'live-source', generation: 'g1'}, {id: 'src2', provenance: 'live-source', generation: 'g7'}];
  assert.equal(zapSourcesListed(listed(both, 'a-newer-fence'), fence, raw), true, 'a moved fence is a newer answer');
  assert.equal(zapSourcesListed(listed([both[0]!]), fence, raw), false, 'the target\'s source gone');
  assert.equal(zapSourcesListed(listed([{...both[0]!, generation: 'g2'}, both[1]!]), fence, raw), false, 'the opening\'s source moved to another generation');
  assert.equal(zapSourcesListed(listed(both), {...fence, sourceId: undefined}, raw), false, 'a library channel lists no live source');
});

const channel = (over: Record<string, unknown> = {}) => ({id: 'ch1', sourceId: 'src', provenance: 'live-source', generation: 'g1',
  programmes: [{id: 'p1', channelId: 'ch1', start: '2026-11-04T08:00:00Z', end: '2026-11-04T09:00:00Z'}], ...over});
const target = {channelId: 'ch1', sourceId: 'src', kind: 'live-source', generation: 'g1'};
const projected = {id: 'p1', startMs: Date.parse('2026-11-04T08:00:00Z'), endMs: Date.parse('2026-11-04T09:00:00Z')};

test('a live action reads its one channel and programme from a newer guide answer; another channel or programme still refuses', () => {
  const offer = liveActionOffer({channels: [channel()]} as never, target, projected);
  assert.equal(offer.channel.id, 'ch1'); assert.equal(offer.programme.id, 'p1');
  assert.throws(() => liveActionOffer({channels: [channel({generation: 'g2'})]} as never, target, projected), /channel_source_unavailable/);
  assert.throws(() => liveActionOffer({channels: [channel(), channel({id: 'ch2'})]} as never, target, projected), /channel_source_unavailable/);
  assert.throws(() => liveActionOffer({channels: [channel()]} as never, target, {...projected, id: 'p2'}), /channel_programme_unavailable/);
});
