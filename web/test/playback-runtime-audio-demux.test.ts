import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, readdirSync} from 'node:fs';
import {openDemuxer} from '../src/platform/playback/audio/demux.ts';

const dir = new URL('../../fixtures/audio-gapless/', import.meta.url);
export const fileReader = (bytes: Uint8Array) => ({size: bytes.length, read: async (o: number, n: number) => bytes.slice(o, Math.min(bytes.length, o + n))});
const sidecars = readdirSync(dir).filter(f => f.endsWith('.json') && f !== 'album.json').map(f => JSON.parse(readFileSync(new URL(f, dir), 'utf8')));

test('every §18.8 fixture demuxes into packets whose frames add up to the raw decoded length', async () => {
  for (const s of sidecars) {
    const bytes = new Uint8Array(readFileSync(new URL(s.file, dir)));
    const d = await openDemuxer(s.container, fileReader(bytes));
    assert.equal(d.config.sampleRate, s.sampleRate, s.file);
    assert.equal(d.config.numberOfChannels, s.channels, s.file);
    let frames = 0, packets = 0, unknown = 0;
    for await (const p of d.packets()) { packets++; if (p.frames === undefined) unknown++; else frames += p.frames; }
    assert.ok(packets > 0, s.file);
    assert.equal(unknown, 0, s.file);
    assert.equal(frames, s.rawFrames, `${s.file}: ${packets} packets`);
  }
});

test('the JavaScript FLAC decoder reproduces every FLAC fixture exactly', async () => {
  const {JsFlacDecoder} = await import('../src/platform/playback/audio/flac-decoder.ts');
  for (const s of sidecars.filter(x => x.codec === 'flac')) {
    const d = await openDemuxer(s.container, fileReader(new Uint8Array(readFileSync(new URL(s.file, dir)))));
    const decoder = new JsFlacDecoder(d.config);
    let n = 0, worst = 0;
    const {amplitude: a, frequency: f, offset} = s.signal;
    for await (const p of d.packets()) {
      decoder.decode(p);
      for (const block of decoder.take()) {
        assert.equal(block.channels.length, s.channels);
        for (let i = 0; i < block.frames; i++, n++) {
          const want = a * Math.sin(2 * Math.PI * f * (n + offset) / s.sampleRate);
          worst = Math.max(worst, Math.abs(block.channels[0]![i]! - want), Math.abs(block.channels[1]![i]! - want));
        }
      }
    }
    assert.equal(n, s.durationFrames, s.file);
    assert.ok(worst <= s.tolerance, `${s.file}: ${worst}`);
  }
});

test('a FLAC is started part-way from the frames’ own positions, exactly, without reading what comes before', async () => {
  const {flacSeek, openFlac} = await import('../src/platform/playback/audio/flac.ts');
  const flacs = sidecars.filter(x => x.codec === 'flac');
  assert.ok(flacs.length > 0);
  let sought = 0;
  for (const s of flacs) {
    const bytes = new Uint8Array(readFileSync(new URL(s.file, dir)));
    // The whole file walked once: where every frame is and which sample it starts at.
    const walked: {offset: number; raw: number; frames: number}[] = [];
    let raw = 0;
    const whole = await openFlac(fileReader(bytes));
    for await (const p of whole.packets()) { walked.push({offset: p.offset, raw, frames: p.frames!}); raw += p.frames!; }
    const block = whole.info.maxBlock || 4096;
    for (const fraction of [0.5, 0.75, 0.97]) {
      const target = Math.floor(s.rawFrames * fraction);
      const touched: number[] = [];
      const counting = {size: bytes.length, read: async (o: number, n: number) => { touched.push(o); return bytes.slice(o, Math.min(bytes.length, o + n)); }};
      // The fixtures are a few seconds long: "close enough" is scaled down to them.
      // No seek table is given, so the place is found by proportion and the frames' own numbers.
      const found = await flacSeek(counting, whole.firstFrame, whole.info, [], target, 0, undefined, {near: block * 2});
      assert.ok(found, `${s.file} at ${fraction}: a start was confirmed`);
      const first = await found!.packets.next();
      assert.equal(first.done, false);
      const same = walked.find(w => w.offset === first.value!.offset);
      assert.ok(same, `${s.file}: the start is a real frame`);
      assert.equal(found!.rawStart, same!.raw, `${s.file}: its position is exact`);
      assert.ok(found!.rawStart <= target, 'the frame holding the position is not skipped');
      assert.ok(target - found!.rawStart <= block * 8, `${s.file}: and the start is close to it (${target - found!.rawStart} frames early)`);
      assert.ok(touched.length <= 12, `${s.file}: a bounded number of reads (${touched.length})`);
      await found!.packets.return(undefined);
      sought++;
      // With a seek table, its point is used once the frame found there confirms it; a wrong point is not trusted.
      const at = walked[Math.floor(walked.length * 0.6)]!;
      const good = await flacSeek(fileReader(bytes), whole.firstFrame, whole.info, [{sample: at.raw, offset: at.offset - whole.firstFrame, frames: at.frames}], at.raw + block, 0, undefined, {near: block * 4});
      assert.equal(good?.rawStart, at.raw);
      const bad = await flacSeek(fileReader(bytes), whole.firstFrame, whole.info, [{sample: at.raw + 12_345, offset: at.offset - whole.firstFrame + 7, frames: at.frames}], at.raw + 12_345 + block, 0, undefined, {near: block * 4});
      if (bad) assert.ok(walked.some(w => w.raw === bad.rawStart), 'a wrong point never yields a wrong position');
    }
  }
  assert.ok(sought >= 3);
});

test('MP3 synchronization tolerates repeated ID3 and substantial padding but remains bounded', async () => {
  const {openMp3} = await import('../src/platform/playback/audio/mp3.ts');
  const original=new Uint8Array(readFileSync(new URL('single-mp3-lame.mp3',dir)));
  const tag=new Uint8Array([73,68,51,4,0,0,0,0,0,0]);
  const prefix=new Uint8Array(140000);prefix.set(tag,0);prefix.set(tag,tag.length);
  const joined=new Uint8Array(prefix.length+original.length);joined.set(prefix);joined.set(original,prefix.length);
  const d=await openMp3(fileReader(joined));assert.equal(d.config.sampleRate,44100);
  assert.ok(!(await d.packets().next()).done);
  let fetched=0;
  const junk={size:10<<20,read:async (_o:number,n:number)=>{fetched+=n;return new Uint8Array(n);}};
  await assert.rejects(()=>openMp3(junk),/No MPEG audio/);
  assert.ok(fetched<2<<20,'hostile junk cannot cause a whole-file synchronization search');
});

test('FLAC seek points retain all high bytes of their 64-bit sample index',async()=>{
 const {openFlac}=await import('../src/platform/playback/audio/flac.ts');
 const bytes=new Uint8Array(64),view=new DataView(bytes.buffer);
 bytes.set([102,76,97,67,0,0,0,34],0);
 view.setUint16(8,4096);view.setUint16(10,4096);
 bytes[18]=10;bytes[19]=196;bytes[20]=0x40;bytes[21]=0xf2;view.setUint32(22,410065408);
 bytes.set([0x83,0,0,18],42);view.setUint32(46,1);view.setUint32(50,17);view.setUint32(58,1234);view.setUint16(62,4096);
 const reader={read:async(o:number,n:number)=>bytes.slice(o,o+n)};
 const d=await openFlac(reader);
 assert.equal(d.info.totalFrames,9000000000);
 assert.deepEqual(d.seekTable,[{sample:2**32+17,offset:1234,frames:4096}]);
});

test('a FLAC with nothing to confirm a deep start is handed on rather than read from the beginning; near the start it is walked', async () => {
  const {openFlac} = await import('../src/platform/playback/audio/flac.ts');
  const {DemuxError} = await import('../src/platform/playback/audio/types.ts');
  // STREAMINFO only: 44.1 kHz, a very long stream, no seek table, and a source of unknown size.
  const bytes = new Uint8Array(42), view = new DataView(bytes.buffer);
  bytes.set([102, 76, 97, 67, 0x80, 0, 0, 34], 0);
  view.setUint16(8, 4096); view.setUint16(10, 4096);
  bytes[18] = 10; bytes[19] = 196; bytes[20] = 0x40; bytes[21] = 0xf2; view.setUint32(22, 410065408);
  const d = await openFlac({read: async (o: number, n: number) => bytes.slice(o, o + n)});
  assert.equal(d.config.sampleRate, 44100);
  assert.equal(await d.seek!(44100 * 10, 0), undefined, 'ten seconds in: the caller walks from the start');
  await assert.rejects(async () => { await d.seek!(44100 * 3600, 0); }, DemuxError, 'an hour in: no start is confirmed, so the ordinary player takes it');
});
