import test from 'node:test';
import assert from 'node:assert/strict';
import {brotliCompressSync, deflateSync, constants} from 'node:zlib';
import {fontFamilyMetadata} from './font-family-metadata.mjs';

function names(legacy = 'Example ExtraLight', family = 'Example') {
  const values = [[1, legacy], ...(family == null ? [] : [[16, family]])];
  const header = Buffer.alloc(6 + 12 * values.length); header.writeUInt16BE(values.length, 2); header.writeUInt16BE(header.length, 4);
  const strings = []; let offset = 0;
  for (const [index, [id, value]] of values.entries()) {
    const data = Buffer.from(value, 'utf16le').swap16(), row = 6 + 12 * index;
    [3, 1, 0x409, id, data.length, offset].forEach((value, field) => header.writeUInt16BE(value, row + field * 2));
    offset += data.length; strings.push(data);
  }
  return Buffer.concat([header, ...strings]);
}
function sfnt(table) {
  const header = Buffer.alloc(28); header.writeUInt32BE(0x10000, 0); header.writeUInt16BE(1, 4);
  header.write('name', 12); header.writeUInt32BE(28, 20); header.writeUInt32BE(table.length, 24);
  return Buffer.concat([header, table]);
}
const base128 = value => {const result = [value & 127]; while ((value = Math.floor(value / 128))) result.unshift((value & 127) | 128); return Buffer.from(result);};
function woff2(table) {
  const directory = Buffer.concat([Buffer.from([10]), base128(100), base128(3), Buffer.from([11]), base128(20), base128(0), Buffer.from([5]), base128(table.length)]);
  const compressed = brotliCompressSync(Buffer.concat([Buffer.from('abc'), table]));
  const header = Buffer.alloc(48); header.write('wOF2'); header.writeUInt32BE(0x10000, 4);
  header.writeUInt32BE(header.length + directory.length + compressed.length, 8); header.writeUInt16BE(3, 12); header.writeUInt32BE(compressed.length, 20);
  return Buffer.concat([header, directory, compressed]);
}
function woff(table) {
  const compressed = deflateSync(table), header = Buffer.alloc(64); header.write('wOFF'); header.writeUInt32BE(0x10000, 4);
  header.writeUInt32BE(header.length + compressed.length, 8); header.writeUInt16BE(1, 12); header.write('name', 44);
  header.writeUInt32BE(64, 48); header.writeUInt32BE(compressed.length, 52); header.writeUInt32BE(table.length, 56);
  return Buffer.concat([header, compressed]);
}
test('observed typographic family comes from name16 without guessing from legacy style words', () => {
  for (const encode of [sfnt, woff, woff2]) {
    const bytes = encode(names()), original = Buffer.from(bytes);
    assert.deepEqual(fontFamilyMetadata(bytes), {family: 'Example', nameId: 16, platformFamilies: ['Example ExtraLight', 'Example']});
    assert.deepEqual(bytes, original);
  }
  const table = names('Important Bold', null);
  assert.equal(fontFamilyMetadata(sfnt(table)).family, 'Important Bold');
  assert.equal(fontFamilyMetadata(sfnt(table)).nameId, 1);
});
test('malformed, unsafe and excessive metadata fails closed', () => {
  for (const bytes of [Buffer.alloc(0), Buffer.from('font'), sfnt(names()).subarray(0, 30), sfnt(names('file:/private/font')), Buffer.alloc(8 * 1024 * 1024 + 1)]) assert.throws(() => fontFamilyMetadata(bytes));
  const invalid = woff2(names()); invalid[49] = 128; assert.throws(() => fontFamilyMetadata(invalid));
  const transformedName = woff2(names()); transformedName[54] = 69; assert.throws(() => fontFamilyMetadata(transformedName));
  const overlap = sfnt(names()); overlap.writeUInt32BE(0, 20); assert.throws(() => fontFamilyMetadata(overlap));
});

test('Brotli expansion is bounded before font metadata can allocate excessive output', () => {
  const valid = woff2(names()), prefix = Buffer.from(valid.subarray(0, valid.length - valid.readUInt32BE(20)));
  const payload = brotliCompressSync(Buffer.alloc(8 * 1024 * 1024 + 1), {params: {[constants.BROTLI_PARAM_QUALITY]: 0}});
  prefix.writeUInt32BE(prefix.length + payload.length, 8); prefix.writeUInt32BE(payload.length, 20);
  assert.throws(() => fontFamilyMetadata(Buffer.concat([prefix, payload])), error => error.code === 'ERR_BUFFER_TOO_LARGE');
});
