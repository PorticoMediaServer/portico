/** PR-01 canonical JSON subset. Input is schema-validated before this encoding seam. */
export type CanonicalValue = null | boolean | number | string | readonly CanonicalValue[] | { readonly [key: string]: CanonicalValue };
const validText = (text: string) => {
  for (let i = 0; i < text.length; i++) {
    const unit = text.charCodeAt(i);
    if (unit >= 0xd800 && unit <= 0xdbff) {
      const low = text.charCodeAt(++i);
      if (!(low >= 0xdc00 && low <= 0xdfff)) throw new Error('Unpaired Unicode surrogate');
    } else if (unit >= 0xdc00 && unit <= 0xdfff) throw new Error('Unpaired Unicode surrogate');
  }
};
export function canonicalJSON(value: unknown): string {
  const ancestors = new Set<object>();
  function encode(v: unknown, depth: number): string {
    if (depth > 64) throw new Error('Canonical JSON nesting limit');
    if (v === null || typeof v === 'boolean') return String(v);
    if (typeof v === 'string') { validText(v); return JSON.stringify(v); }
    if (typeof v === 'number') {
      if (!Number.isInteger(v) || Math.abs(v) > 2147483647) throw new Error('Noncanonical numeric value');
      return Object.is(v, -0) ? '0' : String(v);
    }
    if (!v || typeof v !== 'object') throw new Error('Value is not JSON');
    if (ancestors.has(v)) throw new Error('Cyclic JSON value');
    ancestors.add(v);
    try {
      if (Array.isArray(v)) {
        const items: string[] = [];
        for (let i = 0; i < v.length; i++) {
          if (!Object.hasOwn(v, i)) throw new Error('Sparse JSON array');
          items.push(encode(v[i], depth + 1));
        }
        return '[' + items.join(',') + ']';
      }
      if (Object.getPrototypeOf(v) !== Object.prototype && Object.getPrototypeOf(v) !== null)
        throw new Error('JSON object must be a plain record');
      const keys = Object.keys(v).sort();
      if (keys.some(key => !/^[\x20-\x7e]*$/.test(key))) throw new Error('Schema member names must be ASCII');
      return '{' + keys.map(key => {
        const property = Object.getOwnPropertyDescriptor(v, key)!;
        if (!('value' in property)) throw new Error('JSON accessors are not permitted');
        return JSON.stringify(key) + ':' + encode(property.value, depth + 1);
      }).join(',') + '}';
    } finally { ancestors.delete(v); }
  }
  return encode(value, 0);
}
/** Avoid reliance on TextEncoder availability in a native JS runtime. */
export function canonicalUTF8(value: unknown): Uint8Array {
  const text = canonicalJSON(value), bytes: number[] = [];
  for (const character of text) {
    const code = character.codePointAt(0)!;
    if (code < 0x80) bytes.push(code);
    else if (code < 0x800) bytes.push(0xc0 | code >> 6, 0x80 | code & 63);
    else if (code < 0x10000) bytes.push(0xe0 | code >> 12, 0x80 | code >> 6 & 63, 0x80 | code & 63);
    else bytes.push(0xf0 | code >> 18, 0x80 | code >> 12 & 63, 0x80 | code >> 6 & 63, 0x80 | code & 63);
  }
  if (bytes.length > 65536) throw new Error('Canonical command exceeds protocol body limit');
  return Uint8Array.from(bytes);
}
