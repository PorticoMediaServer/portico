import type {AudioStream, SubtitleStream, VideoStream} from './playback-v1/types.ts';
import type {SubtitleResource} from './subtitles.ts';
import type {MessageId, MessageValues} from '../../i18n/src/index.ts';
import {trackName} from './presentation/language.ts';

/** Technical naming accepts facts without requiring an invented track identity. */
export type VideoAudioFormatFacts = Pick<AudioStream, 'codec' | 'profile' | 'variant' | 'objectAudio' | 'channels' | 'layout'> & Partial<Pick<AudioStream, 'atmos' | 'dtsx'>>;
export type VideoAudioNameFacts = VideoAudioFormatFacts & Pick<AudioStream, 'title' | 'language'>;
export type VideoSubtitleNameFacts = Pick<SubtitleStream, 'format' | 'language' | 'title'> & Partial<Pick<SubtitleStream, 'source' | 'forced' | 'sdh'>> & Partial<Pick<SubtitleResource, 'hearingImpaired'>>;
export type VideoPictureNameFacts = Pick<VideoStream, 'width' | 'height' | 'interlaced' | 'hdr' | 'hdr10Plus'>;

type Translate = (id: MessageId, values?: MessageValues) => string;
export type VideoTrackWords = Readonly<{
  fourK: string; stereo: string; mono: string;
  channels(count: number): string;
  withAtmos(format: string): string;
  withDtsx(value: string): string;
  besideFile(format: string): string;
  language(tag: string): string;
  /** Supplied by videoTrackWords; optional for existing format-only ports. */
  subtitleFlags?: Readonly<{sdh: string; forced: string}>;
}>;
export function videoTrackWords(t: Translate, locale?: string): VideoTrackWords {
  return {fourK: t('quality.4k'), stereo: t('mediaInfo.stereo'), mono: t('mediaInfo.mono'),
    channels: count => t('mediaInfo.channels', {count}),
    withAtmos: format => t('player.track.formatWithAtmos', {format}),
    withDtsx: value => t('player.track.formatWithDtsx', {value}),
    besideFile: format => t('player.track.besideFile', {format}),
    subtitleFlags: {sdh: t('next.kit.fileInfo.sdh'), forced: t('mediaInfo.forced')},
    language: tag => trackName({language: tag}, locale)};
}

const AUDIO: Readonly<Record<string, string>> = {aac: 'AAC', ac3: 'Dolby Digital', eac3: 'Dolby Digital Plus', truehd: 'Dolby TrueHD', dts: 'DTS', dca: 'DTS', flac: 'FLAC', alac: 'ALAC', opus: 'Opus', mp3: 'MP3', vorbis: 'Vorbis', pcm: 'LPCM'};
const VARIANTS: Readonly<Record<string, string>> = {dts_hd_ma: 'DTS-HD Master Audio', dts_hd_hra: 'DTS-HD High Resolution Audio', dts_express: 'DTS Express', dts_core: 'DTS', truehd_atmos: 'Dolby TrueHD', eac3_joc: 'Dolby Digital Plus', lpcm: 'LPCM'};
const DTS_PROFILES: Readonly<Record<string, string>> = {'dts-hd ma': 'DTS-HD Master Audio', 'dts hd ma': 'DTS-HD Master Audio', 'dts-hd master audio': 'DTS-HD Master Audio', 'dts-hd hra': 'DTS-HD High Resolution Audio', 'dts hd hra': 'DTS-HD High Resolution Audio', 'dts express': 'DTS Express'};
const SUBTITLES: Readonly<Record<string, string>> = {ass: 'ASS', ssa: 'SSA', srt: 'SRT', subrip: 'SRT', pgs: 'PGS', hdmv_pgs_subtitle: 'PGS', vobsub: 'VobSub', dvd_subtitle: 'VobSub', dvb: 'DVB', dvb_subtitle: 'DVB', webvtt: 'WebVTT', vtt: 'WebVTT', mov_text: 'tx3g'};
const technical = (value: string): string => /^[a-z0-9][a-z0-9_-]{0,63}$/i.test(value) ? value.toUpperCase() : '';
function layoutName(track: Pick<AudioStream, 'layout' | 'channels'>, words: VideoTrackWords): string | undefined {
  const layout = track.layout?.toLowerCase();
  if (layout === 'stereo') return words.stereo;
  if (layout === 'mono') return words.mono;
  const named = /^(\d{1,2}\.\d)(?:\([a-z-]+\))?$/.exec(layout ?? '');
  if (named) return named[1];
  // A count alone does not establish 5.1, 7.1 or stereo speaker geometry.
  return Number.isSafeInteger(track.channels) && track.channels! > 0 && track.channels! <= 64 ? words.channels(track.channels!) : undefined;
}
function audioParts(track: VideoAudioFormatFacts, words: VideoTrackWords) {
  const codec = track.codec.toLowerCase();
  const format = VARIANTS[track.variant ?? ''] ?? ((codec === 'dts' || codec === 'dca') ? DTS_PROFILES[track.profile?.toLowerCase() ?? ''] : undefined) ?? AUDIO[codec] ?? (codec.startsWith('pcm_') ? 'LPCM' : technical(track.codec));
  const objects = track.objectAudio === 'atmos' || track.atmos || track.variant === 'truehd_atmos' || track.variant === 'eac3_joc' ? 'atmos' : track.objectAudio === 'dtsx' || track.dtsx || track.variant === 'dtsx' ? 'dtsx' : undefined;
  const layout = layoutName(track, words);
  return {format, objects, layout};
}
function audioFormatName(track: VideoAudioFormatFacts, words: VideoTrackWords, separator: string): string {
  const {format, objects, layout} = audioParts(track, words);
  const named = format && objects === 'atmos' ? words.withAtmos(format) : format;
  const rest = [named, layout].filter(Boolean).join(separator);
  return rest && objects === 'dtsx' ? words.withDtsx(rest) : rest;
}
/** Version/file format only, with the chooser's space-separated format and layout. */
export function videoAudioFormatName(track: VideoAudioFormatFacts, words: VideoTrackWords): string {
  return audioFormatName(track, words, ' ');
}
/** Picture facts only; unknown scan never becomes a p/i suffix. */
export function videoPictureName(video: VideoPictureNameFacts, words: Pick<VideoTrackWords, 'fourK'>): string {
  const width = video.width ?? 0, height = video.height ?? 0;
  const resolution = width >= 3840 || height >= 2160 ? words.fourK : height > 0 && Number.isSafeInteger(height) && typeof video.interlaced === 'boolean' ? `${height}${video.interlaced ? 'i' : 'p'}` : '';
  const ranges: Readonly<Record<string, string>> = {dolbyvision: 'Dolby Vision', dolby_vision: 'Dolby Vision', hdr10plus: 'HDR10+', hdr10_plus: 'HDR10+', hdr10: video.hdr10Plus === true ? 'HDR10+' : 'HDR10', hlg: 'HLG'};
  return [resolution, ranges[video.hdr ?? '']].filter(Boolean).join(' ');
}
export type VideoTrackNameParts = Readonly<{first: string; rest: string; label: string; title?: string; language?: string; format?: string; objectAudio?: string; layout?: string; subtitleOrigin?: SubtitleStream['source']}>;
/** Menus consume explicit parts. Rendition identity labels never enter this function. */
export function videoTrackName(track: VideoAudioNameFacts | VideoSubtitleNameFacts, words: VideoTrackWords): VideoTrackNameParts {
  const title = track.title?.trim() || undefined, language = track.language || undefined;
  const first = title ?? (language ? words.language(language) : '');
  if ('codec' in track) {
    const parts = audioParts(track, words), rest = audioFormatName(track, words, ', ');
    return {first, rest, label: [first, rest].filter(Boolean).join(', '), title, language, format: parts.format || undefined, objectAudio: parts.objects, layout: parts.layout};
  }
  const format = SUBTITLES[track.format.toLowerCase()] ?? technical(track.format);
  const rest = format && track.source === 'sidecar' ? words.besideFile(format) : format;
  const flags = [track.hearingImpaired === true || track.sdh === true ? words.subtitleFlags?.sdh : undefined, track.forced === true ? words.subtitleFlags?.forced : undefined].filter(Boolean);
  const named = flags.length ? [first, `(${flags.join(', ')})`].filter(Boolean).join(' ') : first;
  return {first: named, rest, label: [named, rest].filter(Boolean).join(', '), title, language, format: format || undefined, subtitleOrigin: track.source};
}
/** Same binary unit convention as File info, with one decimal retained at 58.2 GB. */
export function formatVideoBytes(bytes: number | undefined, locale?: string): string | undefined {
  if (bytes === undefined || !Number.isSafeInteger(bytes) || bytes < 0) return undefined;
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let value = bytes, unit = 0;
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
  return `${new Intl.NumberFormat(locale, {maximumFractionDigits: unit === 0 ? 0 : 1}).format(value)} ${units[unit]}`;
}

/** Explicit layout or an actual channel count; counts never imply geometry. */
export function videoAudioChannelName(track: Pick<AudioStream, 'layout' | 'channels'>, words: VideoTrackWords): string | undefined {
  return layoutName(track, words);
}
