import test from 'node:test';
import assert from 'node:assert/strict';
import {cueAt, finishCues, type BitmapRect} from '../src/platform/playback/subtitles/bitmap.ts';
import {decodePgsRuns, parsePgs} from '../src/platform/playback/subtitles/pgs.ts';
import {parseVobSub, parseVobSubIndex} from '../src/platform/playback/subtitles/vobsub.ts';
import {characterText, packRuns, parseDvb, parseDvbPackets, readDvbString, unpackRuns} from '../src/platform/playback/subtitles/dvb.ts';
import {readMatroskaSubtitles} from '../src/platform/playback/subtitles/matroska.ts';
import {BitWriter, bytes, dvbClut, dvbLine8, dvbObject, dvbPage, dvbRegion, matroska, pgsEnd, pgsObject, pgsPalette, pgsPresentation, pgsRuns, pgsWindows, picture, programStream, stamp, subpicture} from './helpers/subtitle-samples.ts';

const pixel = (rect: BitmapRect, x: number, y: number) => [...rect.rgba.subarray((y * rect.width + x) * 4, (y * rect.width + x) * 4 + 4)];
const white = [1, 235, 128, 128, 255], halfBlack = [2, 16, 128, 128, 128];
const hd: [number, number] = [1920, 1080];

// ── PGS ────────────────────────────────────────────────────────────────
test('PGS run lengths: every code form, and nothing written outside the picture', () => {
  const cases: {name: string; data: number[]; width: number; height: number; want: number[]}[] = [
    {name: 'single pixels and a short run', data: [5, 0, 0x83, 7, 0, 0], width: 4, height: 1, want: [5, 7, 7, 7]},
    {name: 'short run of colour zero', data: [0, 0x02, 9, 9, 0, 0], width: 4, height: 1, want: [0, 0, 9, 9]},
    {name: 'long run of a colour (14-bit length)', data: [0, 0xc1, 0x04, 3, 0, 0], width: 260, height: 1, want: Array(260).fill(3)},
    {name: 'long run of colour zero', data: [0, 0x41, 0x00, 4, 0, 0], width: 257, height: 1, want: [...Array(256).fill(0), 4]},
    {name: 'a run longer than the line is cut at the edge', data: [0, 0x88, 6, 0, 0, 2, 0, 0], width: 3, height: 2, want: [6, 6, 6, 2, 0, 0]},
    {name: 'data that stops early leaves the rest clear', data: [1, 1], width: 2, height: 2, want: [1, 1, 0, 0]},
    {name: 'more lines than the picture has are ignored', data: [1, 0, 0, 2, 0, 0, 3, 0, 0], width: 1, height: 2, want: [1, 2]},
  ];
  for (const c of cases) assert.deepEqual([...decodePgsRuns(Uint8Array.from(c.data), c.width, c.height)], c.want, c.name);
  const drawn = picture('0110', '2222', '0003');
  assert.deepEqual([...decodePgsRuns(Uint8Array.from(pgsRuns(drawn)), 4, 3)], drawn.pixels, 'the sample encoder and the decoder agree');
});

test('PGS: a subtitle is shown from its display set until the next one clears it', () => {
  const art = picture('0110', '1221', '0110');
  const track = parsePgs(bytes(
    pgsPresentation(1000, hd, 'epoch', 0, [{id: 0, x: 100, y: 900}]), pgsWindows(1000, [[0, 90, 890, 200, 100]]), pgsPalette(1000, 0, [white, halfBlack]), pgsObject(1000, 0, art), pgsEnd(1000),
    pgsPresentation(3000, hd, 'normal', 0, []), pgsEnd(3000),
  ));
  assert.deepEqual([track.kind, track.width, track.height, track.cues.length, track.skipped], ['pgs', 1920, 1080, 1, 0]);
  const cue = track.cues[0]!;
  assert.deepEqual([cue.startMs, cue.endMs, cue.forced], [1000, 3000, false]);
  const [rect] = cue.paint();
  assert.deepEqual([rect!.x, rect!.y, rect!.width, rect!.height], [100, 900, 4, 3]);
  assert.deepEqual(pixel(rect!, 0, 0), [0, 0, 0, 0], 'an index the palette does not define is clear');
  assert.deepEqual(pixel(rect!, 1, 0), [255, 255, 255, 255]);
  assert.deepEqual(pixel(rect!, 1, 1), [0, 0, 0, 128]);
});

test('PGS: objects in pieces, cropping, forced flags, windows, palette-only updates and a new epoch', () => {
  const wide = picture('1'.repeat(300), '2'.repeat(300), '1'.repeat(300), '2'.repeat(300));
  const track = parsePgs(bytes(
    // An object delivered in three segments, shown cropped and marked forced.
    pgsPresentation(1000, hd, 'epoch', 3, [{id: 7, x: 50, y: 60, forced: true, crop: [10, 1, 20, 2]}]), pgsPalette(1000, 3, [white, halfBlack]), pgsObject(1000, 7, wide, 3), pgsEnd(1000),
    // The same object, whole, in a window that only lets its left part through; only the palette changes.
    pgsPresentation(2000, hd, 'normal', 3, [{id: 7, window: 1, x: 50, y: 60}], true), pgsWindows(2000, [[1, 0, 0, 150, 1080]]), pgsPalette(2000, 3, [[1, 235, 128, 128, 64]]), pgsEnd(2000),
    pgsPresentation(2500, hd, 'normal', 3, []), pgsEnd(2500),
    // A new epoch forgets object 7: a set that still names it shows nothing.
    pgsPresentation(4000, hd, 'epoch', 3, [{id: 7, x: 0, y: 0}]), pgsPalette(4000, 3, [white]), pgsEnd(4000),
  ));
  assert.equal(track.cues.length, 2);
  const [first, second] = track.cues;
  assert.deepEqual([first!.startMs, first!.endMs, first!.forced], [1000, 2000, true]);
  const [cropped] = first!.paint();
  assert.deepEqual([cropped!.x, cropped!.y, cropped!.width, cropped!.height, cropped!.forced], [50, 60, 20, 2, true], 'the cropped part sits at the stated position');
  assert.deepEqual(pixel(cropped!, 0, 0), [0, 0, 0, 128], 'row 1 of the object');
  assert.deepEqual(pixel(cropped!, 0, 1), [255, 255, 255, 255], 'painted with the palette as it was when the set was shown');
  assert.deepEqual([second!.startMs, second!.endMs], [2000, 2500]);
  const [windowed] = second!.paint();
  assert.deepEqual([windowed!.x, windowed!.width, windowed!.height], [50, 100, 4], 'clipped to its window');
  assert.deepEqual(pixel(windowed!, 0, 0), [255, 255, 255, 64], 'the palette update changed one entry');
  assert.deepEqual(pixel(windowed!, 0, 1), [0, 0, 0, 128], 'and kept the other');
  assert.ok(track.skipped >= 1);
});

test('PGS: damaged and hostile files keep what is good', () => {
  const art = picture('11', '11');
  const good = [...pgsPresentation(1000, hd, 'epoch', 0, [{id: 0, x: 0, y: 0}]), ...pgsPalette(1000, 0, [white]), ...pgsObject(1000, 0, art), ...pgsEnd(1000)];
  const clear = [...pgsPresentation(2000, hd, 'normal', 0, []), ...pgsEnd(2000)];
  const cases: {name: string; data: number[]; cues: number}[] = [
    {name: 'rubbish between segments', data: [...good, 1, 2, 3, 0x50, 9, ...clear], cues: 1},
    {name: 'a last segment cut short', data: [...good, ...clear.slice(0, clear.length - 4)], cues: 1},
    {name: 'an object claiming an impossible size', data: [...pgsPresentation(1000, hd, 'epoch', 0, [{id: 0, x: 0, y: 0}]), ...pgsPalette(1000, 0, [white]), 0x50, 0x47, 0, 0, 0, 0, 0, 0, 0, 0, 0x15, 0, 11, 0, 0, 0, 0xc0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, ...pgsEnd(1000)], cues: 0},
    {name: 'a canvas beyond any screen', data: [...pgsPresentation(1000, [60000, 60000], 'epoch', 0, [{id: 0, x: 0, y: 0}]), ...pgsPalette(1000, 0, [white]), ...pgsObject(1000, 0, art), ...pgsEnd(1000)], cues: 0},
    {name: 'an empty file', data: [], cues: 0},
    {name: 'not a subtitle at all', data: Array(4096).fill(0x41), cues: 0},
  ];
  for (const c of cases) { const track = parsePgs(Uint8Array.from(c.data)); assert.equal(track.cues.length, c.cues, c.name); for (const cue of track.cues) cue.paint(); }
  const orphan = parsePgs(Uint8Array.from(good));
  assert.equal(orphan.cues[0]!.endMs, 11000, 'a subtitle nothing ever clears is taken off after ten seconds');
});

// ── VobSub ─────────────────────────────────────────────────────────────
const palette = 'palette: 000000, ff0000, 00ff00, 0000ff, ffffff, 808080, 101010, 202020, 303030, 404040, 505050, 606060, 707070, 909090, a0a0a0, b0b0b0';
const index = (lines: string[]) => ['# VobSub index file, v7 (do not modify this line!)', 'size: 720x480', 'org: 0, 0', palette, 'langidx: 0', 'id: en, index: 0', ...lines].join('\n');

test('VobSub index: size, palette, languages, offsets and unreadable lines', () => {
  const parsed = parseVobSubIndex(['# VobSub index file, v7', 'size: 720x576', 'org: 4, 6', 'alpha: 50%', palette, 'custom colors: ON, tridx: 1000, colors: 000000, ffffff, 112233, 445566', 'langidx: 1',
    'id: en, index: 0', 'timestamp: 00:00:01:000, filepos: 000000000', 'id: fr, index: 1', 'time offset: 500', 'timestamp: 00:01:02:345, filepos: 00000a000', 'timestamp: nonsense, filepos: zz', 'timestamp: 01:00:00:000, filepos: 00000b000'].join('\r\n'));
  assert.deepEqual([parsed.width, parsed.height, parsed.originX, parsed.originY, parsed.alpha, parsed.stream], [720, 576, 4, 6, 0.5, 1]);
  assert.deepEqual(parsed.palette[1], [255, 0, 0]);
  assert.deepEqual(parsed.custom, {transparent: [true, false, false, false], colours: [[0, 0, 0], [255, 255, 255], [0x11, 0x22, 0x33], [0x44, 0x55, 0x66]]});
  assert.deepEqual(parsed.entries, [{timeMs: 62845, position: 0xa000}, {timeMs: 3600500, position: 0xb000}]);
  assert.equal(parseVobSubIndex('').entries.length, 0);
});

test('VobSub: two-bit pictures in two fields, coloured and timed by their commands', () => {
  const art = picture('0000000000', '0111111110', '0122222210', '0123333210', '0111111110', '0000000000');
  const first = programStream(subpicture(art, {x: 100, y: 400, colours: [0, 4, 1, 3], alphas: [0, 15, 15, 8], stopAfterMs: 2000}), 5000);
  // A long unit that crosses packs, with every run length the format has, in another language's company.
  const long = picture('1'.repeat(700), '0'.repeat(20) + '2'.repeat(70) + '3'.repeat(5) + '1'.repeat(605), '2'.repeat(700), '3'.repeat(699) + '0');
  const other = programStream(subpicture(picture('33', '33'), {x: 0, y: 0, colours: [0, 1, 2, 3], alphas: [15, 15, 15, 15]}), 9000, 3);
  const second = programStream(subpicture(long, {x: 10, y: 20, colours: [0, 4, 1, 3], alphas: [0, 15, 15, 15], forced: true}), 9000, 0, 120);
  const sub = bytes(first, other, second);
  const track = parseVobSub(index([`timestamp: ${stamp(5000)}, filepos: ${(0).toString(16).padStart(9, '0')}`, `timestamp: ${stamp(9000)}, filepos: ${(first.length + other.length).toString(16).padStart(9, '0')}`, 'timestamp: 00:00:20:000, filepos: 0ffffffff']), sub);
  assert.deepEqual([track.kind, track.width, track.height, track.cues.length, track.skipped], ['vobsub', 720, 480, 2, 1]);
  const [a, b] = track.cues;
  assert.deepEqual([a!.startMs, Math.round(a!.endMs), a!.forced], [5000, 7002, false], 'on at its time, off when its stop command says (in 1024/90000 s steps)');
  const [rect] = a!.paint();
  assert.deepEqual([rect!.x, rect!.y, rect!.width, rect!.height], [100, 400, 10, 6]);
  assert.deepEqual(pixel(rect!, 0, 0), [0, 0, 0, 0], 'background is clear');
  assert.deepEqual(pixel(rect!, 1, 1), [255, 255, 255, 255], 'pixel value 1 is palette entry 4');
  assert.deepEqual(pixel(rect!, 2, 2), [255, 0, 0, 255], 'an odd line comes from the second field');
  assert.deepEqual(pixel(rect!, 3, 3), [0, 0, 255, 136], 'contrast 8 of 15');
  assert.deepEqual([b!.startMs, b!.endMs, b!.forced], [9000, 19000, true], 'no stop command and nothing after it');
  const [big] = b!.paint();
  assert.deepEqual([big!.width, big!.height], [700, 4]);
  const colour = {0: '0,0,0,0', 1: '255,255,255,255', 2: '255,0,0,255', 3: '0,0,255,255'} as Record<number, string>;
  long.pixels.forEach((value, i) => { if (pixel(big!, i % 700, Math.floor(i / 700)).join() !== colour[value]) assert.fail(`pixel ${i} of the long unit`); });
});

test('VobSub: files that lie about themselves', () => {
  const unit = subpicture(picture('11', '11'), {x: 0, y: 0, colours: [0, 1, 2, 3], alphas: [0, 15, 15, 15], stopAfterMs: 1000});
  const good = programStream(unit, 1000);
  const at = (n: number) => `timestamp: 00:00:01:000, filepos: ${n.toString(16).padStart(9, '0')}`;
  const cases: {name: string; sub: number[]; lines: string[]; cues: number}[] = [
    {name: 'a position in the middle of a packet', sub: good, lines: [at(7)], cues: 0},
    {name: 'a unit cut off before its end', sub: good.slice(0, good.length - 6), lines: [at(0)], cues: 0},
    {name: 'a unit claiming more than is there', sub: programStream([0xff, 0xff, ...unit.slice(2)], 1000), lines: [at(0)], cues: 0},
    {name: 'command offsets pointing outside the unit', sub: programStream([...unit.slice(0, 2), 0x7f, 0xff, ...unit.slice(4)], 1000), lines: [at(0)], cues: 0},
    {name: 'an empty stream', sub: [], lines: [at(0)], cues: 0},
    {name: 'the good one', sub: good, lines: [at(0)], cues: 1},
  ];
  for (const c of cases) { const track = parseVobSub(index(c.lines), Uint8Array.from(c.sub)); assert.equal(track.cues.length, c.cues, c.name); for (const cue of track.cues) cue.paint(); }
  assert.equal(parseVobSub('size: 99999x99999\n' + at(0), Uint8Array.from(good)).cues.length, 0, 'a canvas beyond any screen');
});

// ── DVB ────────────────────────────────────────────────────────────────
test('DVB pixel strings: every code of the 2, 4 and 8 bit forms', () => {
  const b = () => new BitWriter();
  const cases: {name: string; depth: 2 | 4 | 8; data: number[]; want: number[]; keepOne?: boolean; map?: number[]}[] = [
    {name: '2-bit: single pixels', depth: 2, data: b().put(1, 2).put(2, 2).put(3, 2).put(0, 6).bytes(), want: [1, 2, 3]},
    {name: '2-bit: run of 3 to 10', depth: 2, data: b().put(0, 2).put(1, 1).put(2, 3).put(3, 2).put(0, 6).bytes(), want: [3, 3, 3, 3, 3]},
    {name: '2-bit: one and two pixels of colour zero', depth: 2, data: b().put(2, 2).put(0, 2).put(1, 2).put(0, 4).put(1, 2).put(1, 2).put(0, 6).bytes(), want: [2, 0, 0, 0, 1]},
    {name: '2-bit: run of 12 to 27', depth: 2, data: b().put(0, 4).put(2, 2).put(1, 4).put(2, 2).put(0, 6).bytes(), want: Array(13).fill(2)},
    {name: '2-bit: run of 29 to 284', depth: 2, data: b().put(0, 4).put(3, 2).put(1, 8).put(1, 2).put(0, 6).bytes(), want: Array(30).fill(1)},
    {name: '2-bit: widened to the region depth through the map', depth: 2, data: b().put(1, 2).put(3, 2).put(0, 6).bytes(), want: [7, 15], map: [0, 7, 8, 15]},
    {name: '4-bit: single pixels and short runs of zero', depth: 4, data: b().put(9, 4).put(0, 4).put(0, 1).put(3, 3).put(5, 4).put(0, 8).bytes(), want: [9, 0, 0, 0, 0, 0, 5]},
    {name: '4-bit: run of 4 to 7', depth: 4, data: b().put(0, 4).put(2, 2).put(1, 2).put(12, 4).put(0, 8).bytes(), want: Array(5).fill(12)},
    {name: '4-bit: one and two pixels of zero', depth: 4, data: b().put(1, 4).put(0, 4).put(3, 2).put(0, 2).put(0, 4).put(3, 2).put(1, 2).put(2, 4).put(0, 8).bytes(), want: [1, 0, 0, 0, 2]},
    {name: '4-bit: run of 9 to 24', depth: 4, data: b().put(0, 4).put(3, 2).put(2, 2).put(1, 4).put(7, 4).put(0, 8).bytes(), want: Array(10).fill(7)},
    {name: '4-bit: run of 25 to 280', depth: 4, data: b().put(0, 4).put(3, 2).put(3, 2).put(2, 8).put(4, 4).put(0, 8).bytes(), want: Array(27).fill(4)},
    {name: '8-bit: pixels, a run of zero and a run of a colour', depth: 8, data: [200, 0, 3, 0, 0x84, 77, 0, 0], want: [200, 0, 0, 0, 77, 77, 77, 77]},
    {name: 'pixel value 1 leaves what is there', depth: 8, data: [1, 5, 0, 0x83, 1, 0, 0], want: [9, 5, 9, 9, 9], keepOne: true},
    {name: 'a string with no end stops with the data', depth: 8, data: [4, 4], want: [4, 4]},
  ];
  for (const c of cases) {
    const row = new Uint8Array(c.want.length).fill(c.keepOne ? 9 : 0);
    const result = readDvbString(Uint8Array.from(c.data), 0, c.data.length, c.depth, row, 0, c.keepOne ?? false, c.map ?? null);
    assert.deepEqual([...row], c.want, c.name);
    assert.equal(result.x, c.want.length, c.name + ' (position)');
  }
  const narrow = new Uint8Array(2);
  readDvbString(Uint8Array.from([0, 0xff, 3, 0, 0]), 0, 5, 8, narrow, 0, false, null);
  assert.deepEqual([...narrow], [3, 3], 'a run past the end of the line writes no further');
  for (const sample of [new Uint8Array(0), Uint8Array.from([0, 0, 0, 5, 5, 5]), Uint8Array.from(Array(1000).fill(7)), Uint8Array.from(Array.from({length: 999}, (_, i) => i % 251))]) assert.deepEqual([...unpackRuns(packRuns(sample), sample.length)], [...sample]);
  assert.ok(packRuns(Uint8Array.from(Array.from({length: 999}, (_, i) => i % 251))).length <= 1000, 'a region that does not shrink is kept as it is');
});

test('DVB: a page of regions from a Matroska file, shown until the next page', () => {
  const page = 1;
  const top = [...dvbLine8([0, 1, 1, 2, 2, 2, 2, 0]), ...dvbLine8([3, 3, 3, 3, 3, 3, 3, 3])], bottom = [...dvbLine8([0, 1, 0, 0, 0, 0, 1, 0])];
  const show = [...dvbPage(page, 30, 2, [[5, 100, 500]]), ...dvbRegion(page, 5, [8, 4], 8, 0, 0, [[9, 0, 0]]), ...dvbClut(page, 0, 8, [[1, 235, 128, 128, 0], [2, 16, 128, 128, 127], [3, 81, 240, 90, 0]]), ...dvbObject(page, 9, top, bottom), 0x0f, 0x80, 0, page, 0, 0];
  // The next display set keeps the region and only draws a second object over part of it, leaving value 1 pixels alone.
  const more = [...dvbPage(page, 30, 0, [[5, 100, 500]]), ...dvbRegion(page, 5, [8, 4], 8, 0, 0, [[10, 4, 0]], false), ...dvbObject(page, 10, dvbLine8([1, 2, 1, 2]), dvbLine8([2, 2, 2, 2]), true)];
  const foreign = [...dvbPage(77, 5, 2, [[1, 0, 0]]), ...dvbRegion(77, 1, [2, 2], 8, 0, 1, [])];
  const clear = dvbPage(page, 5, 0, []);
  const file = matroska('S_DVBSUB', [0, page, 0, page, 0x10], [[1000, [0x20, 0x00, ...show, 0xff], 3000], [2000, more, null], [2500, foreign, null], [4000, clear, null]]);
  const packets = readMatroskaSubtitles(file);
  assert.deepEqual([packets.codec, packets.packets.length, packets.packets[0]!.timeMs, packets.packets[0]!.durationMs, packets.packets[1]!.durationMs], ['S_DVBSUB', 4, 1000, 3000, null]);
  const track = parseDvb(file);
  assert.deepEqual([track.kind, track.width, track.height, track.cues.length], ['dvb', 720, 576, 2]);
  const [first, second] = track.cues;
  assert.deepEqual([first!.startMs, first!.endMs, second!.startMs, second!.endMs], [1000, 2000, 2000, 4000]);
  const [rect] = first!.paint();
  assert.deepEqual([rect!.x, rect!.y, rect!.width, rect!.height], [100, 500, 8, 4]);
  assert.deepEqual(pixel(rect!, 0, 0), [0, 0, 0, 0]);
  assert.deepEqual(pixel(rect!, 1, 0), [255, 255, 255, 255]);
  assert.deepEqual(pixel(rect!, 3, 0), [0, 0, 0, 128], 'transparency 127 of 255');
  assert.deepEqual(pixel(rect!, 1, 1), [255, 255, 255, 255], 'the second field is the line between');
  assert.deepEqual(pixel(rect!, 2, 1), [0, 0, 0, 0]);
  assert.deepEqual(pixel(rect!, 0, 2), [254, 0, 0, 255], 'a saturated red through BT.601');
  const [later] = second!.paint();
  assert.deepEqual(pixel(later!, 4, 0), pixel(rect!, 4, 0), 'value 1 of a non-modifying object leaves the pixel');
  assert.deepEqual(pixel(later!, 5, 0), [0, 0, 0, 128]);
  assert.deepEqual(pixel(later!, 1, 0), [255, 255, 255, 255], 'the rest of the region is as it was');
  assert.deepEqual(pixel(rect!, 4, 1), [0, 0, 0, 0], 'and the earlier cue still paints what it showed');
});

test('DVB: default colours, narrow objects in deeper regions, and broken files', () => {
  const page = 2, b = new BitWriter().put(1, 2).put(2, 2).put(3, 2).put(0, 6).bytes();
  const set = [...dvbPage(page, 4, 1, [[1, 0, 0]]), ...dvbRegion(page, 1, [4, 2], 4, 0, 0, [[1, 0, 0]]), ...dvbObject(page, 1, [0x10, ...b, 0xf0], [])];
  const track = parseDvb(matroska('S_DVBSUB', [], [[500, set, null]]));
  assert.equal(track.cues.length, 1);
  assert.deepEqual([track.cues[0]!.startMs, track.cues[0]!.endMs], [500, 4500], 'taken off after the page time-out');
  const [rect] = track.cues[0]!.paint();
  // 2-bit codes 1, 2, 3 widen to 4-bit entries 7, 8, 15 of the default table.
  assert.deepEqual([pixel(rect!, 0, 0), pixel(rect!, 1, 0), pixel(rect!, 2, 0), pixel(rect!, 3, 0)], [[255, 255, 255, 255], [0, 0, 0, 255], [127, 127, 127, 255], [0, 0, 0, 0]]);
  assert.deepEqual(pixel(rect!, 0, 1), [255, 255, 255, 255], 'an object with one field repeats it');
  const cases: {name: string; file: Uint8Array}[] = [
    {name: 'empty', file: new Uint8Array(0)},
    {name: 'not Matroska', file: Uint8Array.from(Array(512).fill(0xff))},
    {name: 'a region beyond any screen', file: matroska('S_DVBSUB', [], [[0, [...dvbPage(page, 4, 1, [[1, 0, 0]]), ...dvbRegion(page, 1, [65535, 65535], 8, 0, 0, [])], null]])},
    {name: 'a segment longer than its packet', file: matroska('S_DVBSUB', [], [[0, [0x0f, 0x10, 0, page, 0xff, 0xff, 1, 2], null]])},
    {name: 'an object whose fields run past its segment', file: matroska('S_DVBSUB', [], [[0, [...dvbPage(page, 4, 1, [[1, 0, 0]]), ...dvbRegion(page, 1, [4, 2], 8, 0, 0, [[1, 0, 0]]), 0x0f, 0x13, 0, page, 0, 7, 0, 1, 0, 0xff, 0xff, 0xff, 0xff], null]])},
    {name: 'a file cut in the middle of a block', file: matroska('S_DVBSUB', [], [[500, set, null]]).slice(0, -9)},
  ];
  for (const c of cases) { const result = parseDvb(c.file); for (const cue of result.cues) cue.paint(); assert.ok(result.cues.length <= 1, c.name); }
});

// ── Shared ─────────────────────────────────────────────────────────────
test('cues are put in order, never overlap, and the one on screen is found', () => {
  const cues = finishCues([{startMs: 5000, endMs: 0}, {startMs: 1000, endMs: 9000}, {startMs: 3000, endMs: 3500}, {startMs: -5, endMs: 10}, {startMs: 7000, endMs: 6000}]);
  assert.deepEqual(cues.map(c => [c.startMs, c.endMs]), [[1000, 3000], [3000, 3500], [5000, 7000], [7000, 17000]]);
  const cases: [number, number][] = [[0, -1], [999, -1], [1000, 0], [2999, 0], [3000, 1], [3500, -1], [4999, -1], [6999, 2], [16999, 3], [17000, -1]];
  for (const [time, want] of cases) assert.equal(cueAt(cues, time), want, `at ${time} ms`);
  assert.equal(cueAt([], 5), -1);
});

test('VobSub: colours that change part-way along lines (CHG_COLCON)', () => {
  const art = picture('1111111111', '1111111111', '1111111111', '1111111111');
  // Lines 401–402 (picture coordinates): from column 104 the value 1 is palette entry 1 (red), opaque.
  const range = [401 >> 8, 401 & 0xff, 0x10 | (402 >> 8), 402 & 0xff];
  const point = [0x00, 104, 0x00, 0x10, 0x00, 0xf0];
  const block = [...range, ...point, 0x0f, 0xff, 0xff, 0xff];
  const extra = [0x07, (block.length + 2) >> 8, (block.length + 2) & 0xff, ...block];
  const unit = programStream(subpicture(art, {x: 100, y: 400, colours: [0, 4, 1, 3], alphas: [0, 15, 15, 15], stopAfterMs: 1000, extra}), 1000);
  const track = parseVobSub(index([`timestamp: ${stamp(1000)}, filepos: ${(0).toString(16).padStart(9, '0')}`]), Uint8Array.from(unit));
  const [rect] = track.cues[0]!.paint();
  assert.deepEqual(pixel(rect!, 0, 0), [255, 255, 255, 255], 'a line outside the range keeps the plain colours');
  assert.deepEqual(pixel(rect!, 3, 1), [255, 255, 255, 255], 'left of the change point too');
  assert.deepEqual(pixel(rect!, 4, 1), [255, 0, 0, 255], 'from the change point on: the new colour');
  assert.deepEqual(pixel(rect!, 9, 2), [255, 0, 0, 255]);
  assert.deepEqual(pixel(rect!, 4, 3), [255, 255, 255, 255], 'and after the last line of the range, the plain colours again');
});

test('DVB: a character-coded object is written in the region in its own colours', () => {
  const page = 1;
  const codes = [...'Hi'].flatMap(c => [0, c.charCodeAt(0)]);
  const object = [0x0f, 0x13, 0, page, 0, 4 + codes.length, 0, 7, 1 << 2, codes.length / 2, ...codes];
  // The region places object 7 at (1, 1) as a character object (type 1), letters in code 1 on code 2.
  const region = [0x0f, 0x11, 0, page, 0, 18, 5, 8, 0, 8, 0, 4, 3 << 2, 0, 0, 0, 0, 7, 0x40, 1, 0, 1, 1, 2];
  const show = [...dvbPage(page, 30, 2, [[5, 0, 0]]), ...region, ...dvbClut(page, 0, 8, [[1, 235, 128, 128, 0], [2, 16, 128, 128, 0]]), ...object];
  const drawn: string[] = [];
  const text = (letters: string, width: number, height: number) => { drawn.push(`${letters} ${width}x${height}`); return {width: 2, height: 2, mask: Uint8Array.from([1, 0, 0, 1])}; };
  const track = parseDvbPackets([{timeMs: 0, durationMs: null, data: Uint8Array.from(show)}], [page], {}, text);
  assert.deepEqual(drawn, ['Hi 7x3'], 'drawn where the region places it, no larger than what is left of the region');
  const [rect] = track.cues[0]!.paint();
  assert.deepEqual(pixel(rect!, 1, 1), [255, 255, 255, 255], 'ink in the foreground code');
  assert.deepEqual(pixel(rect!, 2, 1), [0, 0, 0, 255], 'the rest of the letters\' box in the background code');
  assert.deepEqual(pixel(rect!, 0, 0), [0, 0, 0, 0], 'outside the box, the region as it was');
  assert.equal(characterText(Uint8Array.from([0, 0x41, 0, 0x0a, 0xac, 0x00, 0, 0x9b]), 0, 4, 8), 'A가', 'controls are left out, Korean is kept');
});

test('DVB: a progressively coded object is an 8-bit bitmap of PNG-filtered scanlines in a zlib stream', async () => {
  const {deflateSync} = await import('node:zlib');
  const {inflateZlib, unfilterScanlines} = await import('../src/platform/playback/subtitles/inflate.ts');
  const page = 1;
  // 3×2 pixels: [1,2,1] then [2,1,2]; the first row Sub-filtered, the second Up-filtered.
  const scanlines = Uint8Array.from([1, 1, 1, 255, 2, 1, 255, 1]);
  for (const level of [0, 9]) assert.deepEqual([...inflateZlib(deflateSync(scanlines, {level}), 64)!], [...scanlines], `zlib level ${level}`);
  assert.deepEqual([...unfilterScanlines(scanlines, 3, 2)!], [1, 2, 1, 2, 1, 2]);
  const z = [...deflateSync(scanlines)];
  const object = [0x0f, 0x13, 0, page, 0, 9 + z.length, 0, 7, 2 << 2, 0, 3, 0, 2, z.length >> 8, z.length & 0xff, ...z];
  // Region 5, 8×4 at 8 bits, places bitmap object 7 at (1, 1).
  const region = [0x0f, 0x11, 0, page, 0, 16, 5, 8, 0, 8, 0, 4, 3 << 2, 0, 0, 0, 0, 7, 0x00, 1, 0, 1];
  const show = [...dvbPage(page, 30, 2, [[5, 0, 0]]), ...region, ...dvbClut(page, 0, 8, [[1, 235, 128, 128, 0], [2, 16, 128, 128, 0]]), ...object];
  const track = parseDvbPackets([{timeMs: 0, durationMs: null, data: Uint8Array.from(show)}], [page], {});
  assert.equal(track.skipped, 0);
  const [rect] = track.cues[0]!.paint();
  assert.deepEqual(pixel(rect!, 1, 1), [255, 255, 255, 255], 'code 1');
  assert.deepEqual(pixel(rect!, 2, 1), [0, 0, 0, 255], 'code 2');
  assert.deepEqual(pixel(rect!, 1, 2), [0, 0, 0, 255], 'second row, unfiltered from the first');
  assert.deepEqual(pixel(rect!, 0, 0), [0, 0, 0, 0], 'outside the object, the region as it was');
  // A stream that says more than it holds, or is not zlib, is skipped and nothing else breaks.
  assert.equal(inflateZlib(Uint8Array.from([0x78, 0x9c, 1, 2, 3, 4]), 64), null);
  assert.equal(inflateZlib(deflateSync(new Uint8Array(100)), 50), null, 'more out than the bitmap can hold');
});
