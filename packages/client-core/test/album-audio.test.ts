// An album's format, from the server's own summary of its songs (albumAudio, #3311): badges on the album page (at most
// two, notable only, music-plus notes) and its File info (music-plus frame 7). Only what the server sends; never guessed.
import test from 'node:test';
import assert from 'node:assert/strict';
import {albumAudioBadges, albumBadgeText} from '../src/music-badges.ts';
import {albumAudioRows} from '../src/presentation/kit-model/album-audio.ts';
import {entryFromItem} from '../src/items.ts';
import {validateContentEntry} from '../src/library-content.ts';
import {FileInfoSession} from '../src/file-info-session.ts';
import {fileInfoPanelModel} from '../src/presentation/file-info-versions.ts';
import {formatSignalText} from '../src/music-signal.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

const env = {t: defaultI18n.t};
const badges = (summary: Parameters<typeof albumAudioBadges>[0]) => albumBadgeText(albumAudioBadges(summary)).map(badge => typeof badge === 'string' ? badge : formatSignalText(badge, defaultI18n.t));
const rows = (summary: Parameters<typeof albumAudioRows>[0]) => albumAudioRows(summary, env)?.map(row => [row.label, row.parts.map(part => part.text).join(' ')]);

test('only notable shared formats are badges: hi-res lossless with its bits, DSD, 5.1 or 7.1', () => {
  assert.deepEqual(badges({state: 'common', codec: 'flac', bitDepth: 24, sampleRate: 96000, channels: 2}), ['FLAC 24/96']);
  assert.deepEqual(badges({state: 'common', codec: 'flac', bitDepth: 24, sampleRate: 96000, channels: 6}), ['FLAC 24/96', '5.1']);
  assert.deepEqual(badges({state: 'common', codec: 'dsd_lsbf', sampleRate: 2822400, channels: 2}), ['DSD']);
  assert.deepEqual(badges({state: 'common', codec: 'flac', bitDepth: 16, sampleRate: 44100, channels: 2}), [], 'CD quality is not notable');
  assert.deepEqual(badges({state: 'common', codec: 'flac', sampleRate: 96000, channels: 2}), [], 'no bit depth, no hi-res badge: never a guessed 24-bit');
  assert.deepEqual(badges({state: 'common', codec: 'mp3', sampleRate: 44100, channels: 2}), [], 'lossy is not notable');
  assert.deepEqual([badges({state: 'mixed'}), badges({state: 'unknown'}), badges(undefined)], [[], [], []]);
});

test('File info draws the shared format the server sends and nothing more; a mixed or unknown album has none', () => {
  assert.deepEqual(rows({state: 'common', codec: 'flac', bitDepth: 24, sampleRate: 96000, channels: 2}), [['Audio', 'FLAC 24-bit 96 kHz Stereo']]);
  assert.deepEqual(rows({state: 'common', codec: 'flac', sampleRate: 96000, channels: 2}), [['Audio', 'FLAC 96 kHz Stereo']]);
  assert.deepEqual(rows({state: 'common', codec: 'mp3', sampleRate: 44100, channels: 2, bitrate: 320000}), [['Audio', 'MP3 44.1 kHz Stereo'], ['Bit rate', '320 kbps']]);
  assert.deepEqual([rows({state: 'mixed'}), rows({state: 'unknown'})], [undefined, undefined]);
  assert.equal(albumAudioRows({state: 'common', codec: 'flac', bitDepth: 24, sampleRate: 96000, channels: 6}, env, 'phone')![0]!.parts.map(part => part.text).join(''), 'FLAC, 24-bit, 96 kHz, 5.1', 'a phone line carries its own separators');
});

test('the album\'s own read carries its format to the page; list rows and other kinds carry none', () => {
  const summary = {state: 'common', codec: 'flac', bitDepth: 24, sampleRate: 96000, channels: 2};
  const album = entryFromItem({id: 'alb', kind: 'album', title: 'A', libraryId: 'm', albumAudio: summary} as never)!;
  const kept = validateContentEntry(JSON.parse(JSON.stringify(album)));
  assert.deepEqual([kept.albumAudio, kept.badges?.length], [summary, 1], 'the page\'s entity keeps both');
  assert.equal(entryFromItem({id: 'alb', kind: 'album', title: 'A', libraryId: 'm'} as never)!.albumAudio, undefined);
  assert.equal(entryFromItem({id: 'f', kind: 'movie', title: 'F', libraryId: 'm', albumAudio: summary} as never)!.albumAudio, undefined);
});

test('File info for an album, through the shared session, shows its format block', async () => {
  const session = new FileInfoSession({itemId: 'alb', role: 'member', isCurrent: () => true, api: {async request<T>() {
    return {id: 'alb', kind: 'album', title: 'A', versions: [], albumAudio: {state: 'common', codec: 'flac', bitDepth: 24, sampleRate: 96000, channels: 2}} as T;
  }}});
  await session.groups();
  const model = fileInfoPanelModel(session.getSnapshot(), {tracks: 'raw', context: 'desktop', manageSurface: 'desktop', split: true, merge: true}, env)!;
  assert.deepEqual(model.album?.map(row => row.parts.map(part => part.text).join(' ')), ['FLAC 24-bit 96 kHz Stereo']);
  session.retire();
});
