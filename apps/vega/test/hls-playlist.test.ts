/** The Vega player reads the server's HLS playlists itself: a title's, and a live channel's. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {liveStart, numberAt, parseMaster, parseMedia, segmentAtDate, segmentNumbered} from '../src/bridge/hls-playlist.ts';

const base = 'http://server.test:32500/v1/media/GRANT/index.m3u8';

test('a master playlist names the best video variant and the audio renditions', () => {
  const master = parseMaster([
    '#EXTM3U',
    '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="audio-0.m3u8"',
    '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Deutsch, Stereo",LANGUAGE="de",DEFAULT=NO,URI="audio-1.m3u8"',
    '#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subtitles",NAME="English",URI="text-0.m3u8"',
    '#EXT-X-STREAM-INF:BANDWIDTH=8000000,CODECS="hvc1.2.4.L120.B0,mp4a.40.2",AUDIO="audio"',
    'video.m3u8',
  ].join('\n'), base);
  assert.ok(master);
  assert.equal(master.video, 'http://server.test:32500/v1/media/GRANT/video.m3u8');
  assert.deepEqual(master.codecs, ['hvc1.2.4.L120.B0', 'mp4a.40.2']);
  assert.deepEqual(master.audio.map(r => [r.name, r.language, r.isDefault]), [['English', 'en', true], ['Deutsch, Stereo', 'de', false]]);
  assert.equal(parseMaster('#EXTM3U\n#EXTINF:4,\ns0.ts\n', base), null);
});

test('a title: segments are numbered from zero and placed by their running length', () => {
  const list = parseMedia('#EXTM3U\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:7\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-MAP:URI="init.mp4"\n#EXTINF:6.000,\ns0.m4s\n#EXTINF:6.000,\ns1.m4s\n#EXTINF:2.500,\ns2.m4s\n#EXT-X-ENDLIST\n', base);
  assert.equal(list.init, 'http://server.test:32500/v1/media/GRANT/init.mp4');
  assert.equal(list.ended, true);
  assert.deepEqual(list.segments.map(s => [s.number, s.start, s.duration]), [[0, 0, 6], [1, 6, 6], [2, 12, 2.5]]);
  assert.equal(numberAt(list, 0), 0);
  assert.equal(numberAt(list, 6), 1);
  assert.equal(numberAt(list, 13.9), 2);
  // At or past the end of a finished stream: the last segment, so the player can arrive and end.
  assert.equal(numberAt(list, 500), 2);
  assert.equal(segmentNumbered(list, 1)?.url, 'http://server.test:32500/v1/media/GRANT/s1.m4s');
  assert.equal(segmentNumbered(list, 3), undefined);
});

test('only the chosen variant audio group is exposed and mixed codec declarations are never assigned to a rendition', () => {
  const master = parseMaster([
    '#EXTM3U',
    '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="low",NAME="Other",URI="low.m3u8"',
    '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="high",NAME="AAC",URI="aac.m3u8"',
    '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="high",NAME="EAC3",URI="eac3.m3u8"',
    '#EXT-X-STREAM-INF:BANDWIDTH=1,CODECS="avc1.64001f,mp4a.40.2",AUDIO="low"', 'low-video.m3u8',
    '#EXT-X-STREAM-INF:BANDWIDTH=2,CODECS="hvc1.2.4.L120.B0,mp4a.40.2,ec-3",AUDIO="high"', 'high-video.m3u8',
  ].join('\n'), base)!;
  assert.deepEqual(master.audio.map(rendition => rendition.name), ['AAC', 'EAC3']);
  assert.ok(master.audio.every(rendition => rendition.codecs === undefined));
  const single = parseMaster('#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a",NAME="AAC",URI="aac.m3u8"\n#EXT-X-STREAM-INF:BANDWIDTH=2,CODECS="avc1.64001f,mp4a.40.2",AUDIO="a"\nv.m3u8\n', base)!;
  assert.deepEqual(single.audio[0].codecs, ['mp4a.40.2']);
});

test('a stream still being made: a position past what exists waits for the segment that comes next', () => {
  const list = parseMedia('#EXTM3U\n#EXT-X-TARGETDURATION:7\n#EXTINF:6,\ns0.ts\n#EXTINF:6,\ns1.ts\n', base);
  assert.equal(list.ended, false);
  assert.equal(numberAt(list, 30), 2);
  assert.equal(segmentNumbered(list, 2), undefined);
});

const channel = (first: number, count: number, more = '') => {
  const start = Date.parse('2026-10-13T18:00:00Z');
  let text = `#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:${first}\n#EXT-X-DISCONTINUITY-SEQUENCE:0\n${more}`;
  for (let n = first; n < first + count; n++) text += `#EXT-X-PROGRAM-DATE-TIME:${new Date(start + n * 4000).toISOString()}\n#EXTINF:4.000000,\ns${n}.ts\n`;
  return {text, start};
};

test('a live channel: segments keep their numbers as the playlist slides, and say when they were on air', () => {
  const {text, start} = channel(100, 12);
  const list = parseMedia(text, base);
  assert.equal(list.sequence, 100);
  assert.equal(list.targetDuration, 4);
  assert.equal(list.segments[0].number, 100);
  assert.equal(list.segments[11].number, 111);
  assert.equal(list.segments[3].dateMs, start + 103 * 4000);
  // The same segment is found by its number after the playlist has moved on.
  const later = parseMedia(channel(105, 12).text, base);
  assert.equal(segmentNumbered(later, 108)?.url.endsWith('/s108.ts'), true);
  // One that has left the playlist: the oldest that is still there.
  assert.equal(segmentNumbered(later, 101)?.number, 105);
  // One not made yet.
  assert.equal(segmentNumbered(later, 117), undefined);
});

test('a live channel: a moment on its timeline is the segment that was on air then', () => {
  const {text, start} = channel(100, 12);
  const list = parseMedia(text, base);
  assert.equal(segmentAtDate(list, start + 104 * 4000 + 1500)?.number, 104);
  // Before the window: its oldest segment. Not broadcast yet: nothing.
  assert.equal(segmentAtDate(list, start)?.number, 100);
  assert.equal(segmentAtDate(list, start + 500 * 4000), undefined);
});

test('a live channel begins where the server says, else three segments behind the newest', () => {
  assert.equal(liveStart(parseMedia(channel(100, 12).text, base))?.number, 109);
  // Ten seconds behind the end of the playlist (48 s long): the segment holding second 38.
  assert.equal(liveStart(parseMedia(channel(100, 12, '#EXT-X-START:TIME-OFFSET=-10.000000,PRECISE=YES\n').text, base))?.number, 109);
  assert.equal(liveStart(parseMedia(channel(100, 12, '#EXT-X-START:TIME-OFFSET=-30.000000,PRECISE=YES\n').text, base))?.number, 104);
  assert.equal(liveStart(parseMedia('#EXTM3U\n', base)), undefined);
});

test('a change of programme is marked on the segment that starts it, and dates carry on without a tag', () => {
  const list = parseMedia('#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:7\n#EXT-X-PROGRAM-DATE-TIME:2026-10-13T18:00:00Z\n#EXTINF:4,\ns7.ts\n#EXTINF:4,\ns8.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:4,\ns9.ts\n', base);
  assert.deepEqual(list.segments.map(s => s.discontinuity), [false, false, true]);
  assert.equal(list.segments[2].dateMs, Date.parse('2026-10-13T18:00:08Z'));
});
