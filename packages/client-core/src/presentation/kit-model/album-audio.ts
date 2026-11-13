import type {TechnicalRow} from './files.ts';
import {bitRateText, codecText} from './technical.ts';
import {positive, type FactPart, type TextEnvironment} from './text.ts';

export type AlbumAudioFacts = Readonly<{state: string; codec?: string; bitDepth?: number; sampleRate?: number; channels?: number; bitrate?: number}>;
/** An album's File info (music-plus frame 7): the format its songs share, as the server summarises them. Only facts the
 * server sends are drawn ("FLAC, 96 kHz" when the bit depth is not known, never a guessed 24-bit). Mixed and unknown
 * albums have no shared format to draw: undefined, and the album offers no File info. */
export function albumAudioRows(summary: AlbumAudioFacts | undefined, env: TextEnvironment, context: 'desktop' | 'phone' = 'desktop'): readonly TechnicalRow[] | undefined {
  if (summary?.state !== 'common') return undefined;
  const {t} = env, part = (id: string, text: string): FactPart => Object.freeze({id, text, role: 'value'});
  const codec = codecText(summary.codec, t);
  const channels = summary.channels === 1 ? t('next.kit.mono') : summary.channels === 2 ? t('next.kit.stereo') : summary.channels === 6 ? '5.1' : summary.channels === 8 ? '7.1'
    : positive(summary.channels) ? t('next.kit.channels', {count: summary.channels}) : undefined;
  const facts = [codec && part('codec', codec), positive(summary.bitDepth) && part('bit-depth', t('next.kit.bitDepth', {bits: summary.bitDepth})),
    positive(summary.sampleRate) && part('sample-rate', t('next.kit.khz', {rate: String(summary.sampleRate / 1000)})), channels && part('channels', channels)].filter((value): value is FactPart => !!value);
  // Phones draw the parts as one line of text, so they carry their own separators, as a version's rows do.
  const audio = context === 'phone' ? facts.flatMap((fact, index) => index ? [Object.freeze({id: `separator-${index}`, text: t('next.kit.fileInfo.comma'), role: 'context' as const}), fact] : [fact]) : facts;
  const rate = bitRateText(summary.bitrate, t);
  const rows: TechnicalRow[] = [];
  if (audio.length) rows.push(Object.freeze({id: 'audio', label: t('next.kit.audio'), parts: Object.freeze(audio)}));
  if (rate) rows.push(Object.freeze({id: 'bitrate', label: t('next.kit.bitrate'), parts: Object.freeze([part('bitrate', rate)])}));
  return rows.length ? Object.freeze(rows) : undefined;
}
