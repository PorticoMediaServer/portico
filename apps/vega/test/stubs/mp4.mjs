/** Small ISO BMFF initialization fixtures. Media samples remain the test
 * element's JSON timeline descriptions; codec metadata uses real box layouts. */
export function box(type, ...parts) {
  const payload = Buffer.concat(parts.map(part => Buffer.from(part)));
  const header = Buffer.alloc(8);
  header.writeUInt32BE(payload.length + 8); header.write(type, 4, 4, 'ascii');
  return Buffer.concat([header, payload]);
}

export function sampleEntry(type, configType, config) {
  const visual = /^(avc|hvc|hev|dvh|av01)/.test(type);
  const header = Buffer.alloc(visual ? 78 : 28);
  header.writeUInt16BE(1, 6);
  return box(type, header, box(configType, config));
}

export function codecEntry(type) {
  if (type === 'avc1' || type === 'avc3') return sampleEntry(type, 'avcC', [1, 100, 0, 31, 255, 224, 0]);
  if (type === 'hvc1' || type === 'hev1') {
    const config = Buffer.alloc(23); config[0] = 1; config[1] = 2; config.writeUInt32BE(0x20000000, 2); config[6] = 0xb0; config[12] = 120;
    return sampleEntry(type, 'hvcC', config);
  }
  if (type === 'dvh1' || type === 'dvhe') return sampleEntry(type, 'dvcC', [1, 0, 16, 48]);
  if (type === 'av01') return sampleEntry(type, 'av1C', [0x81, 8, 0x40, 0]);
  if (type === 'mp4a') {
    const decoder = Buffer.concat([Buffer.from([0x40, 0x15]), Buffer.alloc(11), Buffer.from([5, 2, 0x12, 0x10])]);
    const es = Buffer.concat([Buffer.from([0, 1, 0, 4, decoder.length]), decoder, Buffer.from([6, 1, 2])]);
    return sampleEntry(type, 'esds', Buffer.concat([Buffer.alloc(4), Buffer.from([3, es.length]), es]));
  }
  if (type === 'ac-3') return sampleEntry(type, 'dac3', [0x10, 0x11, 0x40]);
  if (type === 'ec-3') return sampleEntry(type, 'dec3', [0, 0, 0, 0, 0]);
  if (type === 'Opus') return sampleEntry(type, 'dOps', [0, 2, 0, 0, 0, 0, 0xbb, 0x80, 0, 0, 0]);
  if (type === 'fLaC') return sampleEntry(type, 'dfLa', Buffer.alloc(38));
  throw new Error('Unknown fixture codec');
}

export function initWithEntries(entries) {
  return box('moov', ...entries.map(entry => {
    const full = Buffer.alloc(8); full.writeUInt32BE(1, 4);
    return box('trak', box('mdia', box('minf', box('stbl', box('stsd', full, entry)))));
  }));
}

export const mp4Init = (types = ['avc1', 'mp4a']) => initWithEntries(types.map(codecEntry));
