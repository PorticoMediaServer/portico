import test from 'node:test';
import assert from 'node:assert/strict';
import {mp4Codecs} from '../src/bridge/mp4-codecs.ts';
import {box, codecEntry, initWithEntries, mp4Init, sampleEntry} from './stubs/mp4.mjs';

const bytes = (value: Uint8Array): ArrayBuffer => Uint8Array.from(value).buffer;

test('initialization boxes describe AVC, HEVC, Dolby Vision, AV1 and each audio format', () => {
  const formats = [
    ['avc1', 'avc1.64001F'], ['avc3', 'avc3.64001F'], ['hvc1', 'hvc1.2.4.L120.B0'], ['hev1', 'hev1.2.4.L120.B0'],
    ['dvh1', 'dvh1.08.06'], ['dvhe', 'dvhe.08.06'], ['av01', 'av01.0.08M.10'],
    ['mp4a', 'mp4a.40.2'], ['ac-3', 'ac-3'], ['ec-3', 'ec-3'], ['Opus', 'opus'], ['fLaC', 'flac'],
  ];
  for (const [type, codec] of formats) assert.deepEqual(mp4Codecs(bytes(mp4Init([type]))), [codec]);
  assert.deepEqual(mp4Codecs(bytes(mp4Init(['hvc1', 'ec-3']))), ['hvc1.2.4.L120.B0', 'ec-3']);
});

test('unknown, absent and truncated codec metadata is rejected without falling back to AVC/AAC', () => {
  for (const data of [
    Buffer.alloc(0), Buffer.from('not an initialization segment'), mp4Init().subarray(0, -1),
    initWithEntries([box('avc1', Buffer.alloc(78))]),
    initWithEntries([sampleEntry('avc1', 'avcC', [1, 100, 0, 31])]),
    initWithEntries([box('xxxx', Buffer.alloc(28)), codecEntry('mp4a')]),
    initWithEntries([sampleEntry('mp4a', 'esds', [0, 0, 0, 0, 3, 127])]),
  ]) assert.throws(() => mp4Codecs(bytes(data)), /invalid_mp4_initialization/);
});

test('invalid box lengths, excessive depth and mismatched sample counts are rejected', () => {
  const large = Buffer.alloc(16); large.writeUInt32BE(1); large.write('moov', 4); large.writeUInt32BE(0xffffffff, 8);
  let deep = mp4Init(); for (let i = 0; i < 15; i++) deep = box('moov', deep);
  const count = Buffer.alloc(8); count.writeUInt32BE(2, 4);
  for (const value of [large, deep, box('stsd', count, codecEntry('mp4a'))]) assert.throws(() => mp4Codecs(bytes(value)), /invalid_mp4_initialization/);
});
