import test from 'node:test';
import assert from 'node:assert/strict';
import {mediaVersionNames, subtitleTrackNames, versionNames} from '../src/presentation/title-files.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

const t = defaultI18n.t;
const labels = (v: readonly {label: string}[]) => v.map(x => x.label);

test('versions that read differently are left as they are, with their edition', () => {
  assert.deepEqual(labels(versionNames([{id: 'a', label: '4K · HEVC · MKV'}, {id: 'b', label: '1080p · H.264 · MP4'}], {a: {edition: 'Director’s Cut', bitRate: 1}, b: {}}, t)), ['4K · HEVC · MKV · Director’s Cut', '1080p · H.264 · MP4']);
});

test('two copies with the same headline are told apart by bit rate, then size, then their number', () => {
  const same = [{id: 'a', label: 'SD · H.264 · MP4'}, {id: 'b', label: 'SD · H.264 · MP4'}];
  assert.deepEqual(labels(versionNames(same, {a: {bitRate: 1_400_000, size: 7_300_000}, b: {bitRate: 3_200_000, size: 17_000_000}}, t)), ['SD · H.264 · MP4 · 1.4 Mbps', 'SD · H.264 · MP4 · 3.2 Mbps']);
  // The same bit rate (or one unknown) does not tell them apart: the size does.
  const bySize = labels(versionNames(same, {a: {bitRate: 2_000_000, size: 7_300_000}, b: {size: 17_000_000}}, t));
  assert.notEqual(bySize[0], bySize[1]);
  assert.ok(bySize.every(label => /\d/.test(label.replace('SD · H.264 · MP4', ''))));
  // Nothing measured differs: their numbers in the Files list.
  assert.deepEqual(labels(versionNames(same, {}, t)), ['SD · H.264 · MP4 · Version 1', 'SD · H.264 · MP4 · Version 2']);
});

test('only the versions that collide gain a fact; an edition can be what tells them apart', () => {
  const versions = [{id: 'a', label: '1080p · H.264 · MKV'}, {id: 'b', label: '1080p · H.264 · MKV'}, {id: 'c', label: '4K · HEVC · MKV'}];
  assert.deepEqual(labels(versionNames(versions, {a: {edition: 'Theatrical'}, b: {edition: 'Extended'}, c: {}}, t)), ['1080p · H.264 · MKV · Theatrical', '1080p · H.264 · MKV · Extended', '4K · HEVC · MKV']);
  assert.deepEqual(labels(versionNames(versions, {a: {bitRate: 8_000_000}, b: {bitRate: 12_000_000}, c: {bitRate: 40_000_000}}, t)), ['1080p · H.264 · MKV · 8.0 Mbps', '1080p · H.264 · MKV · 12 Mbps', '4K · HEVC · MKV']);
});

const names = (tracks: Parameters<typeof subtitleTrackNames>[0]) => [...subtitleTrackNames(tracks, t, 'en-US').values()];

test('subtitle tracks that already read differently keep their plain names', () => {
  assert.deepEqual(names([{id: '1', language: 'en', title: '', format: 'srt'}, {id: '2', language: 'fr', title: 'Français', format: 'srt'}, {id: '3', language: 'en', title: 'SDH', format: 'srt'}]), ['English', 'French', 'English · SDH']);
});

test('two unnamed tracks in one language are told apart by format, as in the browser run', () => {
  assert.deepEqual(names([{id: '1', language: 'en', title: '', format: 'srt'}, {id: '2', language: 'en', title: '', format: 'ass'}, {id: '3', language: 'en', title: '', format: 'pgs'}]), ['English · SRT', 'English · ASS', 'English · PGS']);
});

test('the same language and format twice gains a number; marks the title did not say are added', () => {
  assert.deepEqual(names([{id: '1', language: 'en', format: 'srt'}, {id: '2', language: 'en', format: 'srt'}, {id: '3', language: 'en', format: 'ass'}]), ['English · SRT (1)', 'English · SRT (2)', 'English · ASS']);
  // Marked forced or for the hard of hearing: said once, whether the title said it or the mark did.
  assert.deepEqual(names([{id: '1', language: 'en', format: 'srt'}, {id: '2', language: 'en', format: 'srt', forced: true}, {id: '3', language: 'en', title: 'SDH', format: 'ass', hearingImpaired: true}]), ['English', 'English · Forced', 'English · SDH']);
  // Two that are the same in every word: the format, then a number.
  assert.deepEqual(names([{id: '3', language: 'en', title: 'SDH', format: 'srt', hearingImpaired: true}, {id: '4', language: 'en', format: 'srt', hearingImpaired: true}]), ['English · SDH · SRT (1)', 'English · SDH · SRT (2)']);
});

test('a track that names nothing is still something to choose', () => {
  assert.deepEqual(names([{id: '1', language: 'und', title: ''}, {id: '2', language: '', title: ''}]), ['Track 1', 'Track 2']);
});

test('versions from the playback options read as the Version menus list them, never twice the same', () => {
  const v = (id: string, more: Partial<Parameters<typeof mediaVersionNames>[0][number]> = {}) => ({id, label: 'File', container: 'mkv', video: [{height: 1080}], ...more});
  assert.deepEqual([...mediaVersionNames([v('a'), v('b', {video: [{height: 2160}]})], t).values()], ['File · 1080p · MKV', 'File · 2160p · MKV']);
  assert.deepEqual([...mediaVersionNames([v('a', {bitrateKbps: 8000}), v('b', {bitrateKbps: 12000})], t).values()], ['File · 1080p · MKV · 8.0 Mbps', 'File · 1080p · MKV · 12 Mbps']);
  assert.deepEqual([...mediaVersionNames([v('a', {edition: 'Extended'}), v('b')], t).values()], ['File · 1080p · MKV · Extended', 'File · 1080p · MKV']);
  assert.deepEqual([...mediaVersionNames([v('a'), v('b')], t).values()], ['File · 1080p · MKV · Version 1', 'File · 1080p · MKV · Version 2']);
  assert.deepEqual([...mediaVersionNames([{id: 'a', label: '', video: []}], t).values()], ['Version 1']);
  // The server's own label ("720p · MKV", "SD · MP4") already names the resolution and container.
  assert.deepEqual([...mediaVersionNames([{id: 'a', label: '720p · MKV', container: 'mkv', video: [{height: 720}]}, {id: 'b', label: 'SD · MP4', container: 'mp4', video: [{height: 360}]}], t).values()], ['720p · MKV', 'SD · 360p · MP4']);
});
