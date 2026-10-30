import test from 'node:test';
import assert from 'node:assert/strict';
import {ConvertedSource} from '../src/platform/playback/audio/converted.ts';
import {crc16} from '../src/platform/playback/audio/flac.ts';

/** A minimal FLAC encoder for tests: 16-bit stereo, VERBATIM subframes, fixed block size. */
export function encodeFlac(left: Float32Array, right: Float32Array, rate: number, block = 4096): Uint8Array {
  const out: number[] = [...'fLaC'].map(c => c.charCodeAt(0));
  const total = left.length;
  const si = new Uint8Array(34);
  si[0] = block >> 8; si[1] = block & 255; si[2] = block >> 8; si[3] = block & 255;
  si[10] = rate >> 12; si[11] = (rate >> 4) & 255; si[12] = ((rate & 15) << 4) | (1 << 1) | 0; si[13] = (15 << 4) | Math.floor(total / 2 ** 32);
  si[14] = (total >>> 24) & 255; si[15] = (total >>> 16) & 255; si[16] = (total >>> 8) & 255; si[17] = total & 255;
  out.push(0x80, 0, 0, 34, ...si);
  const crc8 = (b: number[]) => { let c = 0; for (const x of b) { c ^= x; for (let k = 0; k < 8; k++) c = c & 0x80 ? ((c << 1) ^ 7) & 255 : (c << 1) & 255; } return c; };
  const utf8 = (n: number) => n < 0x80 ? [n] : n < 0x800 ? [0xc0 | (n >> 6), 0x80 | (n & 63)] : [0xe0 | (n >> 12), 0x80 | ((n >> 6) & 63), 0x80 | (n & 63)];
  for (let f = 0, at = 0; at < total; f++, at += block) {
    const n = Math.min(block, total - at);
    const header = [0xff, 0xf8, (7 << 4) | 0, (1 << 4) | (4 << 1), ...utf8(f), (n - 1) >> 8, (n - 1) & 255];
    header.push(crc8(header));
    const frame = [...header];
    for (const ch of [left, right]) {
      frame.push(0b00000010);
      for (let i = 0; i < n; i++) { const v = Math.max(-32768, Math.min(32767, Math.round(ch[at + i]! * 32768))) & 0xffff; frame.push(v >> 8, v & 255); }
    }
    const bytes = Uint8Array.from(frame), c = crc16(bytes, 0, bytes.length);
    out.push(...frame, c >> 8, c & 255);
  }
  return Uint8Array.from(out);
}


const rate = 44100, total = rate * 3;
const sig = (n: number) => 0.5 * Math.sin(2 * Math.PI * 441 * n / rate);
/** The server's converted route (§18.1): FLAC from `fromFrame`, trim {0, 0}, cut at `budget` bytes for frame 0. */
function server(budget?: number) {
  const requests: string[] = [];
  const fetch = async (url: string) => {
    requests.push(url);
    const from = Number(new URL(url, 'http://x').searchParams.get('fromFrame') ?? 0);
    const l = new Float32Array(total - from), r = new Float32Array(total - from);
    for (let i = 0; i < l.length; i++) l[i] = r[i] = sig(from + i);
    let bytes = encodeFlac(l, r, rate);
    if (budget !== undefined && from === 0) bytes = bytes.slice(0, budget);
    return new Response(new ReadableStream({start(c) { for (let o = 0; o < bytes.length; o += 7000) c.enqueue(bytes.slice(o, o + 7000)); c.close(); }}), {headers: {'X-Audio-Trim-Start': '0', 'X-Audio-Trim-End': '0'}});
  };
  return {requests, fetch};
}
const plan = {id: 'm1', url: '/v1/media/g/audio-converted', container: 'flac', codec: 'flac', sampleRate: rate, channels: 2, durationFrames: total, trim: {startFrames: 0, endFrames: 0}};

test('an injected fetch callback never receives the ConvertedSource as its receiver', async () => {
  const s = server();let calls = 0;
  const fetcher = function(this: unknown, url: string, init: RequestInit): Promise<Response> {
    assert.equal(this, undefined);calls++;
    assert.equal(init.credentials, 'omit');assert.equal(init.cache, 'no-store');assert.equal(init.redirect, 'error');assert.ok(init.signal);
    return s.fetch(url);
  };
  const source = new ConvertedSource(plan, 50_000, {locked: false, fetch: fetcher});
  try {
    const block = await source.read(10);
    assert.ok(block);assert.ok(Math.abs(block.channels[0]![0]! - sig(50_000)) < 1e-4);
    assert.equal(calls, 1);assert.deepEqual(s.requests, ['/v1/media/g/audio-converted?fromFrame=50000']);
  } finally {source.close();}
});

async function drain(source: ConvertedSource) {
  let n = 0, worst = 0;
  for (;;) { const b = await source.read(4096); if (!b) break; for (let i = 0; i < b.frames; i++, n++) worst = Math.max(worst, Math.abs(b.channels[0]![i]! - sig(n))); }
  return {n, worst};
}

test('(g) a converted FLAC stream plays exactly durationFrames', async () => {
  const s = server();
  const {n, worst} = await drain(new ConvertedSource(plan, 0, {locked: false, fetch: s.fetch}));
  assert.equal(n, total);
  assert.ok(worst < 1e-4);
  assert.deepEqual(s.requests, ['/v1/media/g/audio-converted']);
});

test('(g) a prepared stream cut at its budget waits for the commit, then continues from exactly the next frame', async () => {
  const s = server(60_000);
  const source = new ConvertedSource(plan, 0, {locked: true, fetch: s.fetch});
  let n = 0;
  // Before commit: only what the budget allowed.
  for (;;) {
    const read = source.read(4096);
    const r = await Promise.race([read, new Promise<'waiting'>(res => setTimeout(() => res('waiting'), 50))]);
    if (r === 'waiting') { source.unlock(); const b = await read; if (b) n += b.frames; break; }
    if (!r) break;
    n += r.frames;
  }
  assert.equal(s.requests.length, 2, 'one reopen after the commit');
  assert.match(s.requests[1]!, /\?fromFrame=\d+$/);
  const resumedAt = Number(s.requests[1]!.split('=')[1]);
  assert.ok(resumedAt > 0 && resumedAt < total);
  const rest = await drain(source);
  assert.equal(n + rest.n, total, 'every frame once');
});

test('(g) a seek opens the converted stream at ?fromFrame', async () => {
  const s = server();
  const source = new ConvertedSource(plan, 50_000, {locked: false, fetch: s.fetch});
  const b = await source.read(10);
  assert.ok(Math.abs(b!.channels[0]![0]! - sig(50_000)) < 1e-4);
  assert.deepEqual(s.requests, ['/v1/media/g/audio-converted?fromFrame=50000']);
  source.close();
});
