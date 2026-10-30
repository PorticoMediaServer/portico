/** The two glow colours in desktop-read.html:664 and phone-read.html:1057. */
export type GeneratedCoverTint = Readonly<{first: string; second: string}>;

/** Ordered exactly as .tn-a through .tn-h; these are artwork tints, not brand tokens. */
const tints: readonly GeneratedCoverTint[] = Object.freeze([
  Object.freeze({first: '#2f86bb', second: '#1e5b52'}),
  Object.freeze({first: '#b4642a', second: '#5e2a22'}),
  Object.freeze({first: '#8f2e24', second: '#3f2150'}),
  Object.freeze({first: '#2e6a6f', second: '#1f4f7a'}),
  Object.freeze({first: '#55709a', second: '#2a3a66'}),
  Object.freeze({first: '#3f8f6e', second: '#153749'}),
  Object.freeze({first: '#6a58a8', second: '#26305f'}),
  Object.freeze({first: '#a8892a', second: '#5a3a1a'}),
]);

/**
 * Stable on web and native: FNV-1a over the title's exact UTF-16 code units,
 * wrapping to unsigned 32 bits after each multiply, then modulo eight.
 * No trimming, case folding, Unicode normalization, locale or platform APIs.
 * The hash is an engineering choice; the eight palette pairs are the design.
 */
export function generatedCoverTint(title: string): GeneratedCoverTint {
  let hash = 0x811c9dc5;
  for (let index = 0; index < title.length; index++) hash = Math.imul(hash ^ title.charCodeAt(index), 0x01000193) >>> 0;
  return tints[hash % tints.length]!;
}
