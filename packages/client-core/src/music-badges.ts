import {musicFormatBadges, type SignalText} from './music-signal.ts';

/** Server-composed notable album formats. No source identity or inferred facts. */
export type MusicFormatBadge =
 | Readonly<{variant: 'lossless'; codec: string; bitDepth: number; sampleRate: number}>
 | Readonly<{variant: 'dsd' | 'atmos'}>
 | Readonly<{variant: 'surround'; layout: '5.1' | '7.1'}>;

/** Optional display-only data: unreadable/unknown descriptors stay absent. */
export function parseMusicBadges(value: unknown): readonly MusicFormatBadge[] | undefined {
 if (!Array.isArray(value) || value.length > 2) return undefined;
 const out: MusicFormatBadge[] = [];
 for (const item of value) {
  if (!item || typeof item !== 'object' || Array.isArray(item)) continue;
  const raw = item as Record<string, unknown>;
  if (raw.variant === 'atmos' || raw.variant === 'dsd') out.push(Object.freeze({variant: raw.variant}));
  else if (raw.variant === 'surround' && (raw.layout === '5.1' || raw.layout === '7.1')) out.push(Object.freeze({variant: raw.variant, layout: raw.layout}));
  else if (raw.variant === 'lossless' && typeof raw.codec === 'string' && /^[A-Za-z0-9_.+-]{1,64}$/.test(raw.codec)
   && typeof raw.bitDepth === 'number' && Number.isSafeInteger(raw.bitDepth) && raw.bitDepth > 16
   && typeof raw.sampleRate === 'number' && Number.isSafeInteger(raw.sampleRate) && raw.sampleRate > 0)
   out.push(Object.freeze({variant: raw.variant, codec: raw.codec, bitDepth: raw.bitDepth, sampleRate: raw.sampleRate}));
 }
 return Object.freeze(out.filter((badge, index) => out.findIndex(other => JSON.stringify(badge) === JSON.stringify(other)) === index));
}

/** Uses the same catalogue and codec spelling as song badges, without scanning tracks. */
export function albumBadgeText(badges: readonly MusicFormatBadge[] | undefined): readonly (SignalText | string)[] {
 return Object.freeze((badges ?? []).slice(0, 2).flatMap(badge => {
  switch (badge.variant) {
   case 'lossless': return musicFormatBadges({...badge, lossless: true});
   case 'atmos': return musicFormatBadges({atmos: true});
   case 'dsd': return musicFormatBadges({codec: 'dsd'});
   case 'surround': return musicFormatBadges({layout: badge.layout});
  }
 }));
}
