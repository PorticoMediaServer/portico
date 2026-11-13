import {musicFormatBadges, notableMusicFormats, type SignalText} from './music-signal.ts';

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

/** The album's notable formats from the server's own summary of its songs (`albumAudio` on GET /v1/items/{album}), by
 * the songs' own rule: only when every song shares them (`common`), and only what the server knows. A bit depth comes
 * only with lossless audio, so a hi-res badge needs one; it is never guessed. Mixed and unknown albums get none. */
export function albumAudioBadges(summary: Readonly<{state: string; codec?: string; bitDepth?: number; sampleRate?: number; channels?: number}> | undefined): readonly MusicFormatBadge[] | undefined {
 if (summary?.state !== 'common' || !summary.codec) return undefined;
 const badges = notableMusicFormats({codec: summary.codec, bitDepth: summary.bitDepth, sampleRate: summary.sampleRate, lossless: summary.bitDepth !== undefined,
  channels: summary.channels, layout: summary.channels === 6 ? '5.1' : summary.channels === 8 ? '7.1' : undefined});
 return badges.length ? badges : undefined;
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
