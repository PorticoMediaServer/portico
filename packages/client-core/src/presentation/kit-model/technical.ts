import {integer, part, positive, supplied, type FactPart, type KitTranslate} from './text.ts';

const codecKeys = {
  h264: 'h264', avc: 'h264', hevc: 'hevc', h265: 'hevc', av1: 'av1', vp9: 'vp9', mpeg2video: 'mpeg2', mpeg4: 'mpeg4', vc1: 'vc1',
  aac: 'aac', ac3: 'ac3', eac3: 'eac3', truehd: 'truehd', dts: 'dts', flac: 'flac', opus: 'opus', mp3: 'mp3', vorbis: 'vorbis', alac: 'alac', pcm: 'pcm', pcm_s16le: 'pcm', pcm_s24le: 'pcm',
  subrip: 'srt', srt: 'srt', ass: 'ass', ssa: 'ssa', webvtt: 'vtt', vtt: 'vtt', mov_text: 'text', hdmv_pgs_subtitle: 'pgs', pgs: 'pgs', dvd_subtitle: 'vobsub', dvb_subtitle: 'dvb',
} as const;
export function codecText(codec: string | undefined, t: KitTranslate): string | undefined {
  if (!supplied(codec)) return undefined;
  const name = codecKeys[codec.toLowerCase() as keyof typeof codecKeys];
  // A new provider's token is data; never strip punctuation or rewrite protocol identities.
  return name ? t(`next.kit.codec.${name}`) : codec;
}
export function hdrText(range: string | undefined, hdr10Plus: boolean | undefined, t: KitTranslate): string | undefined {
  const keys = {dolby_vision: 'next.kit.hdr.dolbyVision', 'dolby-vision': 'next.kit.hdr.dolbyVision', dovi: 'next.kit.hdr.dolbyVision', hdr10: hdr10Plus ? 'next.kit.hdr.hdr10Plus' : 'next.kit.hdr.hdr10', hdr10plus: 'next.kit.hdr.hdr10Plus', 'hdr10+': 'next.kit.hdr.hdr10Plus', hlg: 'next.kit.hdr.hlg'} as const;
  const key = range ? keys[range.toLowerCase() as keyof typeof keys] : undefined;
  return key ? t(key) : undefined;
}
export const heightText = (height: number | undefined, t: KitTranslate): string | undefined => integer(height) ? t('next.kit.height', {height}) : undefined;
/** File info's horizontal source class, including cropped cinema. Never a delivered height. */
export function fileDisplayResolutionPart(width: number | undefined, height: number | undefined, t: KitTranslate): FactPart | undefined {
  const text = integer(width) && width >= 7680 ? t('next.kit.fileInfo.8k')
    : integer(width) && width >= 3840 ? t('next.kit.4k')
    : integer(width) && width >= 2560 ? heightText(1440, t)
    : integer(width) && width >= 1920 ? heightText(1080, t)
    : integer(width) && width >= 1280 ? heightText(720, t)
    : heightText(height, t);
  return text ? part('resolution-class', text) : undefined;
}
export function bitRateText(bits: number | undefined, t: KitTranslate): string | undefined {
  if (!positive(bits)) return undefined;
  const rate = bits / 1_000_000;
  return rate >= 1 ? t('next.kit.mbps', {rate: rate >= 10 ? String(Math.round(rate)) : rate.toFixed(1)}) : t('next.kit.kbps', {rate: String(Math.round(bits / 1000))});
}
export function bytesText(bytes: number | undefined, t: KitTranslate): string | undefined {
  if (!positive(bytes)) return undefined;
  const keys = ['next.kit.kib', 'next.kit.mib', 'next.kit.gib', 'next.kit.tib'] as const;
  if (bytes < 1024) return t('next.kit.bytes', {count: Math.floor(bytes)});
  const unit = Math.min(keys.length, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / 1024 ** unit;
  return t(keys[unit - 1]!, {value: Number(value.toFixed(1)).toString()});
}
export function dimensionsPart(width: number | undefined, height: number | undefined, t: KitTranslate, id = 'dimensions'): FactPart | undefined {
  return integer(width) && integer(height) ? part(id, t('next.kit.dimensions', {width, height})) : undefined;
}
