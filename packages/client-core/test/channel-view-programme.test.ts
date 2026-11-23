import test from 'node:test';
import assert from 'node:assert/strict';
import {channelView, parseChannelSession} from '../src/playback-v1/channel-control.ts';
import type {ChannelReference} from '../src/playback/linear-protocol.ts';

// A live source's session names no programme; the player draws the one the guide had airing when it tuned.
const start = Date.parse('2026-11-22T20:00:00Z'), end = Date.parse('2026-11-22T21:00:00Z');
const reference: ChannelReference = {kind: 'live-source', sourceId: 'source', channelId: 'channel', generation: 'g1', name: 'Portico One',
  programme: {id: 'prog_1', title: 'Evening News', startMs: start, endMs: end}};
const attachment = {generation: 1, key: 's:1', seekId: 'seek-1', targetMs: null};
const live = () => parseChannelSession({id: 's', kind: 'live', state: 'playing', presentation: {generation: 1, mode: 'live', url: '/v1/live/streams/grant', route: 'original'}});
const title = (startsAt: string, endsAt: string) => parseChannelSession({id: 's', kind: 'title', state: 'playing', itemId: 'item',
  presentation: {generation: 1, mode: 'direct', url: '/v1/media/grant', route: 'original'}, channel: {id: 'slot', startsAt, endsAt}});

test('a live source draws the tuned programme while it airs, and the channel name', () => {
  const view = channelView(live(), reference, attachment, {now: 0, serverNow: start + 60_000}).linear;
  assert.equal(view.name, 'Portico One');
  assert.deepEqual({...view.programme}, {id: 'prog_1', itemId: '', title: 'Evening News', startMs: start, endMs: end});
});

test('once the tuned programme has ended a live source names none, and none without a guide programme', () => {
  assert.equal(channelView(live(), reference, attachment, {now: 0, serverNow: end}).linear.programme, null);
  assert.equal(channelView(live(), {...reference, programme: undefined}, attachment, {now: 0, serverNow: start + 60_000}).linear.programme, null);
});

test('a Library Channel keeps its session programme and takes the title only when the times match', () => {
  const same = channelView(title('2026-11-22T20:00:00Z', '2026-11-22T21:00:00Z'), reference, attachment, {now: 0, serverNow: start + 60_000}).linear.programme;
  assert.equal(same?.id, `slot@${start}`);
  assert.equal(same?.title, 'Evening News');
  const other = channelView(title('2026-11-22T21:00:00Z', '2026-11-22T22:00:00Z'), reference, attachment, {now: 0, serverNow: end + 60_000}).linear.programme;
  assert.equal(other?.title, '');
});
