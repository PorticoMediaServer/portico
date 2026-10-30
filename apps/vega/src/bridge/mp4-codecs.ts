/** Codec strings from ISO BMFF initialization metadata, never guessed from a
 * filename. Bounds/depth limits reject malformed or truncated boxes before MSE. */
export function mp4Codecs(data: ArrayBuffer): readonly string[] {
  const bytes = new Uint8Array(data), view = new DataView(data), codecs = new Set<string>();
  let remaining = 4096;
  const invalid = (): never => {throw new Error('invalid_mp4_initialization');};
  const text = (at: number) => String.fromCharCode(...bytes.subarray(at, at + 4));
  const hex = (value: number, width = 2) => value.toString(16).toUpperCase().padStart(width, '0');
  type Box = Readonly<{type: string; from: number; to: number}>;
  const boxes = (from: number, to: number): Box[] => {
    const result: Box[] = [];
    while (from < to) {
      if (--remaining < 0 || to - from < 8) invalid();
      let size = view.getUint32(from), header = 8;
      const type = text(from + 4);
      if (size === 1) {
        if (to - from < 16) invalid();
        size = view.getUint32(from + 8) * 0x100000000 + view.getUint32(from + 12); header = 16;
      } else if (size === 0) size = to - from;
      if (!Number.isSafeInteger(size) || size < header || size > to - from) invalid();
      result.push({type, from: from + header, to: from + size}); from += size;
    }
    return result;
  };
  const descriptor = (from: number, to: number) => {
    if (from >= to) return invalid();
    const tag = bytes[from++]; let size = 0, length = 0, unit: number;
    do {
      if (from >= to || length++ >= 4) return invalid();
      unit = bytes[from++]; size = size * 128 + (unit & 127);
    } while (unit & 128);
    if (size > to - from) return invalid();
    return {tag, from, to: from + size};
  };
  const aac = (box: Box): string => {
    if (box.to - box.from < 5) return invalid();
    const es = descriptor(box.from + 4, box.to);
    if (es.tag !== 3 || es.to - es.from < 3) return invalid();
    const flags = bytes[es.from + 2]; let from = es.from + 3;
    if (flags & 128) from += 2;
    if (flags & 64) {if (from >= es.to) return invalid(); from += 1 + bytes[from];}
    if (flags & 32) from += 2;
    const decoder = descriptor(from, es.to);
    if (decoder.tag !== 4 || decoder.to - decoder.from < 13) return invalid();
    const objectType = bytes[decoder.from];
    if (objectType !== 0x40) {
      if (![0x66, 0x67, 0x68, 0x69, 0x6b].includes(objectType)) return invalid();
      return `mp4a.${hex(objectType)}`;
    }
    const config = descriptor(decoder.from + 13, decoder.to);
    if (config.tag !== 5 || config.to - config.from < 2) return invalid();
    const first = bytes[config.from];
    const audioType = first >> 3 === 31 ? 32 + ((first & 7) << 3) + (bytes[config.from + 1] >> 5) : first >> 3;
    if (!audioType) return invalid();
    return `mp4a.40.${audioType}`;
  };
  const sample = (box: Box) => {
    const visual = /^(avc[13]|hvc1|hev1|dvh[1e]|av01)$/.test(box.type);
    const audio = /^(mp4a|ac-3|ec-3|Opus|fLaC)$/.test(box.type);
    if (!visual && !audio) {if (['wvtt', 'stpp', 'tx3g'].includes(box.type)) return; invalid();}
    if (box.to - box.from < (visual ? 78 : 28)) invalid();
    const version = audio ? view.getUint16(box.from + 8) : 0;
    if (audio && version > 2) invalid();
    const offset = visual ? 78 : 28 + (version === 1 ? 16 : version === 2 ? 36 : 0);
    const children = boxes(box.from + offset, box.to);
    const config = (type: string) => children.find(child => child.type === type);
    const dv = config('dvcC') ?? config('dvvC');
    if (dv && /^dvh/.test(box.type)) {
      if (dv.to - dv.from < 4) invalid();
      const profile = bytes[dv.from + 2] >> 1, level = ((bytes[dv.from + 2] & 1) << 5) | (bytes[dv.from + 3] >> 3);
      codecs.add(`${box.type}.${String(profile).padStart(2, '0')}.${String(level).padStart(2, '0')}`); return;
    }
    const avc = config('avcC');
    if (avc && /^avc/.test(box.type)) {
      if (avc.to - avc.from < 7 || bytes[avc.from] !== 1) invalid();
      codecs.add(`${box.type}.${hex(bytes[avc.from + 1])}${hex(bytes[avc.from + 2])}${hex(bytes[avc.from + 3])}`); return;
    }
    const hevc = config('hvcC');
    if (hevc && /^(hvc1|hev1)$/.test(box.type)) {
      if (hevc.to - hevc.from < 23 || bytes[hevc.from] !== 1) invalid();
      const profile = bytes[hevc.from + 1], compatibility = view.getUint32(hevc.from + 2);
      let reversed = 0;
      for (let bit = 0; bit < 32; bit++) reversed = (reversed * 2) + ((compatibility >>> bit) & 1);
      const constraints = [...bytes.subarray(hevc.from + 6, hevc.from + 12)];
      while (constraints.length && constraints.at(-1) === 0) constraints.pop();
      codecs.add(`${box.type}.${['', 'A', 'B', 'C'][profile >> 6]}${profile & 31}.${hex(reversed, 1)}.${profile & 32 ? 'H' : 'L'}${bytes[hevc.from + 12]}${constraints.map(value => `.${hex(value)}`).join('')}`); return;
    }
    const av1 = config('av1C');
    if (av1 && box.type === 'av01') {
      if (av1.to - av1.from < 4 || bytes[av1.from] !== 0x81) invalid();
      const flags = bytes[av1.from + 2], depth = flags & 64 ? flags & 32 ? 12 : 10 : 8;
      codecs.add(`av01.${bytes[av1.from + 1] >> 5}.${String(bytes[av1.from + 1] & 31).padStart(2, '0')}${flags & 128 ? 'H' : 'M'}.${String(depth).padStart(2, '0')}`); return;
    }
    const esds = config('esds') ?? children.filter(child => child.type === 'wave').flatMap(child => boxes(child.from, child.to)).find(child => child.type === 'esds');
    if (box.type === 'mp4a' && esds) {codecs.add(aac(esds)); return;}
    if (box.type === 'ac-3' && config('dac3')) {codecs.add('ac-3'); return;}
    if (box.type === 'ec-3' && config('dec3')) {codecs.add('ec-3'); return;}
    if (box.type === 'Opus' && config('dOps')) {codecs.add('opus'); return;}
    if (box.type === 'fLaC' && config('dfLa')) {codecs.add('flac'); return;}
    invalid();
  };
  const visit = (from: number, to: number, depth = 0) => {
    if (depth > 12) invalid();
    for (const box of boxes(from, to)) {
      if (['moov', 'trak', 'mdia', 'minf', 'stbl'].includes(box.type)) visit(box.from, box.to, depth + 1);
      else if (box.type === 'stsd') {
        if (box.to - box.from < 8) invalid();
        const entries = boxes(box.from + 8, box.to);
        if (entries.length !== view.getUint32(box.from + 4)) invalid();
        entries.forEach(sample);
      }
    }
  };
  visit(0, bytes.length);
  if (!codecs.size) invalid();
  return [...codecs];
}
