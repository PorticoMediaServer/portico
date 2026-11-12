// What a person loses without the fix: on a film with English and French audio, choosing French in
// the player does nothing (the web refuses the choice before asking the server), so they cannot
// change language at all. server-next names audio tracks by stream index ("1", "2"); the web's
// selectAudio (web/src/platform/playback/runtime.ts) matches them with audioStreamIndex, which only
// reads "a1"/"a2", against the offer's stream index.
import assert from 'node:assert/strict';
import test from 'node:test';
import {readPlayerOffers} from '../src/playback-v1/offers.ts';
import {audioStreamIndex} from '../src/playback-v1/choices.ts';

// The playback options server-next answered for a real two-audio MP4 in the review run.
const options = {itemId: 'itm_two', kind: 'movie', versions: [{id: 'med_two', label: '360p', container: 'mov', durationMs: 60000,
  video: [{id: '0', codec: 'h264', width: 640, height: 360}],
  audio: [{id: '1', codec: 'aac', language: 'eng', channels: 2, default: true}, {id: '2', codec: 'aac', language: 'fra', channels: 2, default: false}],
  subtitles: []}], chapters: [], markers: [], qualities: [{mode: 'original'}],
  preferred: {versionId: 'med_two', quality: {mode: 'original'}}, plan: {versionId: 'med_two', mode: 'direct', engine: 'native', route: 'original', decision: {}, quality: {mode: 'original'}}, alternatives: []};

test('each audio track the server lists can be chosen in the player', async () => {
  const api = {async request<T>(path: string): Promise<T> {
    if (path.includes('/playback-options')) return options as T;
    return {id: 'itm_two', libraryId: 'lib_films'} as T;
  }};
  const offers = await readPlayerOffers(api, {itemId: 'itm_two', libraryId: 'lib_films'} as never, {serverId: 'srv', viewerId: 'v'} as never);
  const source = offers.sources.find(s => s.id === 'med_two')!;
  for (const track of options.versions[0]!.audio) {
    // The check selectAudio makes before it sends the change.
    const offered = source.streams.some(s => s.type === 'audio' && s.enabled && s.index === audioStreamIndex(track.id));
    assert.ok(offered, `audio track ${track.id} (${track.language}) is refused`);
  }
});
