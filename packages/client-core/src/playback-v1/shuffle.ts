/**
 * Seeded shuffle (ARCH-MEDIA-04): a keyed bijection over positions [0, n), a 4-round Feistel network
 * on the smallest even-bit domain ≥ n with cycle-walking back into range. O(1) per position, never
 * materialised, with a computable inverse; stable across devices for the same (seed, lap).
 * The server owns the real order; this copy serves the fake server, tests and local prediction.
 */

/** 32-bit mix (a small keyed hash; not cryptographic). */
function mix(x: number, key: number): number {
  let h = (x ^ key) >>> 0;
  h = Math.imul(h ^ (h >>> 16), 0x7feb352d) >>> 0;
  h = Math.imul(h ^ (h >>> 15), 0x846ca68b) >>> 0;
  return (h ^ (h >>> 16)) >>> 0;
}

function roundKeys(seed: number, lap: number): number[] {
  const base = mix(seed >>> 0, 0x9e3779b9 ^ lap);
  return [0, 1, 2, 3].map(i => mix(base, 0x85ebca6b * (i + 1)));
}

export class ShufflePermutation {
  readonly n: number;
  private halfBits: number;
  private mask: number;
  private keys: number[];

  constructor(n: number, seed: number, lap = 0) {
    if (!Number.isSafeInteger(n) || n < 1 || n > 2 ** 40) throw new RangeError('n must be in [1, 2^40]');
    this.n = n;
    let bits = 2;
    while (2 ** bits < n) bits += 2;
    this.halfBits = bits / 2;
    this.mask = 2 ** this.halfBits - 1;
    this.keys = roundKeys(seed, lap);
  }

  private split(x: number): [number, number] { const r = x % (this.mask + 1); return [(x - r) / (this.mask + 1), r]; }
  private join(l: number, r: number): number { return l * (this.mask + 1) + r; }
  private f(r: number, k: number): number { return mix(r, k) & this.mask; }

  private forward(x: number): number {
    let [l, r] = this.split(x);
    for (const k of this.keys) { const t = r; r = (l ^ this.f(r, k)) >>> 0 & this.mask; l = t; }
    return this.join(l, r);
  }

  private backward(x: number): number {
    let [l, r] = this.split(x);
    for (let i = this.keys.length - 1; i >= 0; i--) { const t = l; l = (r ^ this.f(l, this.keys[i]!)) >>> 0 & this.mask; r = t; }
    return this.join(l, r);
  }

  /** The source position shown at shuffled position `i`. */
  at(i: number): number {
    if (!Number.isSafeInteger(i) || i < 0 || i >= this.n) throw new RangeError('position out of range');
    let x = this.forward(i);
    while (x >= this.n) x = this.forward(x);
    return x;
  }

  /** The shuffled position of source position `s` (the inverse of `at`). */
  indexOf(s: number): number {
    if (!Number.isSafeInteger(s) || s < 0 || s >= this.n) throw new RangeError('position out of range');
    let x = this.backward(s);
    while (x >= this.n) x = this.backward(x);
    return x;
  }
}
