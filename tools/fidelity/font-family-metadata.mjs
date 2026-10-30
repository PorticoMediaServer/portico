import {brotliDecompressSync, inflateSync} from 'node:zlib';

// Read names only, not glyphs. WOFF2 table-directory rules: https://www.w3.org/TR/WOFF2/
// OpenType typographic family is name16, falling back to name1 when absent.
const limit = 8 * 1024 * 1024;
const slice = (bytes, offset, length) => {
  if (!Number.isInteger(offset) || !Number.isInteger(length) || offset < 0 || length < 0 || offset + length > bytes.length) throw new Error('Invalid bounded font table');
  return bytes.subarray(offset, offset + length);
};
function nameTable(bytes) {
  const signature = slice(bytes, 0, 4).toString('latin1');
  if (signature === 'wOF2') {
    if (bytes.length < 48 || bytes.readUInt32BE(8) !== bytes.length || bytes.toString('latin1', 4, 8) === 'ttcf') throw new Error('Unsupported font container');
    const count = bytes.readUInt16BE(12), compressed = bytes.readUInt32BE(20);
    if (!count || count > 128 || compressed > limit) throw new Error('Font directory exceeds bound');
    let cursor = 48, offset = 0, name;
    const base128 = () => {
      let value = 0;
      for (let i = 0; i < 5; i++) {
        const byte = slice(bytes, cursor++, 1)[0];
        if (!i && byte === 128) throw new Error('Noncanonical font length');
        value = value * 128 + (byte & 127);
        if (value > 0xffffffff) throw new Error('Font length overflow');
        if (!(byte & 128)) return value;
      }
      throw new Error('Font length exceeds bound');
    };
    for (let i = 0; i < count; i++) {
      const flags = slice(bytes, cursor++, 1)[0], index = flags & 63, version = flags >> 6;
      const tag = index === 63 ? slice(bytes, cursor, 4).toString('latin1') : ({3: 'hmtx', 5: 'name', 10: 'glyf', 11: 'loca'}[index] ?? String(index));
      if (index === 63) cursor += 4;
      const original = base128(), glyph = tag === 'glyf' || tag === 'loca';
      if (glyph ? ![0, 3].includes(version) : tag === 'hmtx' ? ![0, 1].includes(version) : version !== 0) throw new Error('Unknown font transform');
      const transformed = glyph ? version !== 3 : version !== 0;
      const length = transformed ? base128() : original;
      if (tag === 'loca' && transformed && length !== 0 || length > limit || offset + length > limit) throw new Error('Invalid transformed font length');
      if (tag === 'name') {if (name) throw new Error('Duplicate font name table'); name = {offset, length};}
      offset += length;
    }
    if (!name) throw new Error('Font names unavailable');
    const data = brotliDecompressSync(slice(bytes, cursor, compressed), {maxOutputLength: limit});
    if (data.length !== offset) throw new Error('Font data length mismatch');
    return slice(data, name.offset, name.length);
  }
  if (signature === 'wOFF') {
    if (bytes.length < 44 || bytes.readUInt32BE(8) !== bytes.length) throw new Error('Invalid font container');
    const count = bytes.readUInt16BE(12);
    if (!count || count > 128) throw new Error('Font directory exceeds bound');
    let name;
    for (let i = 0; i < count; i++) {
      const row = slice(bytes, 44 + 20 * i, 20);
      if (row.toString('latin1', 0, 4) !== 'name') continue;
      if (name) throw new Error('Duplicate font name table');
      const original = row.readUInt32BE(12), compressed = row.readUInt32BE(8);
      if (original > limit || compressed > original) throw new Error('Invalid font table size');
      if (row.readUInt32BE(4) < 44 + 20 * count) throw new Error('Overlapping font table');
      const data = slice(bytes, row.readUInt32BE(4), compressed);
      name = compressed < original ? inflateSync(data, {maxOutputLength: limit}) : data;
      if (name.length !== original) throw new Error('Font name length mismatch');
    }
    if (!name) throw new Error('Font names unavailable');
    return name;
  }
  if (!['\u0000\u0001\u0000\u0000', 'OTTO', 'true'].includes(signature)) throw new Error('Unsupported font container');
  const count = bytes.readUInt16BE(4);
  if (!count || count > 128) throw new Error('Font directory exceeds bound');
  let name;
  for (let i = 0; i < count; i++) {
    const row = slice(bytes, 12 + 16 * i, 16);
    if (row.toString('latin1', 0, 4) !== 'name') continue;
    if (name) throw new Error('Duplicate font name table');
    if (row.readUInt32BE(8) < 12 + 16 * count) throw new Error('Overlapping font table');
    name = slice(bytes, row.readUInt32BE(8), row.readUInt32BE(12));
  }
  if (!name) throw new Error('Font names unavailable');
  return name;
}

export function fontFamilyMetadata(input) {
  if (!(input instanceof Uint8Array) || input.byteLength > limit) throw new Error('Font resource exceeds bound');
  const bytes = Buffer.from(input.buffer, input.byteOffset, input.byteLength);
  const table = nameTable(bytes), version = table.readUInt16BE(0), count = table.readUInt16BE(2), storage = table.readUInt16BE(4);
  if (![0, 1].includes(version) || !count || count > 4096 || storage < 6 + 12 * count) throw new Error('Invalid font name records');
  const values = [];
  for (let i = 0; i < count; i++) {
    const row = slice(table, 6 + 12 * i, 12), platform = row.readUInt16BE(0), language = row.readUInt16BE(4), id = row.readUInt16BE(6);
    if (![1, 16].includes(id) || ![0, 3].includes(platform)) continue;
    const raw = slice(table, storage + row.readUInt16BE(10), row.readUInt16BE(8));
    if (raw.length % 2 || raw.length > 1024) throw new Error('Invalid font family encoding');
    const value = Buffer.from(raw).swap16().toString('utf16le');
    if (!value.trim() || value.length > 200 || /[\u0000-\u001f/\\]|(?:https?|file):|url\s*\(/i.test(value)) throw new Error('Unsafe font family metadata');
    values.push({id, value, rank: platform === 3 && language === 0x409 ? 0 : platform === 0 ? 1 : 2});
  }
  const select = id => {
    const rows = values.filter(value => value.id === id).sort((a, b) => a.rank - b.rank);
    const names = [...new Set(rows.filter(row => row.rank === rows[0]?.rank).map(row => row.value))];
    if (names.length > 1) throw new Error('Ambiguous typographic family');
    return names[0];
  };
  const typographic = select(16), family = typographic ?? select(1);
  if (!family) throw new Error('Observed family metadata unavailable');
  return {family, nameId: typographic ? 16 : 1, platformFamilies: [...new Set(values.map(value => value.value))]};
}
