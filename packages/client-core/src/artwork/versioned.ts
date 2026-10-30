import {artworkDescriptor} from './renditions.ts';

/**
 * A versioned artwork route: `<prefix><64-hex source>`, optionally followed by both slot
 * descriptions (`&sd=…&ld=…`, the descriptor URLs of renditions.ts). Nothing else may follow, so
 * an artwork URL can't carry another query.
 */
export function isVersionedArtwork(value: string, prefix: string): boolean {
  if (!value.startsWith(prefix)) return false;
  const [source = '', ...slots] = value.slice(prefix.length).split('&');
  if (!/^[a-f0-9]{64}$/.test(source)) return false;
  return slots.length === 0 || slots.length === 2 && slots[0]!.startsWith('sd=') && slots[1]!.startsWith('ld=') && artworkDescriptor(value) !== undefined;
}
