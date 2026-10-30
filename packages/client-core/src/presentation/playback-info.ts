import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';

/**
 * Playback information (Justin, 2 Oct 2026): what the player says under More about the stream on
 * screen. One model for every client: whether the file plays as it is or is being converted and
 * why, the codecs and picture size, the bitrate, and what the player itself measures (dropped
 * frames, buffer). The server's reason codes become plain words here; a code this build does not
 * know is left out rather than shown raw.
 */
type Translate = (id: MessageId, values?: MessageValues) => string;

export type PlaybackDecision = Readonly<{action: string; reasons: readonly string[]; to?: string; channels?: number}>;
export type PlaybackInfoFacts = Readonly<{
  /** The session's own account of each stream (Playback v1 `presentation.decision`). */
  decision?: Readonly<{video?: PlaybackDecision; audio?: PlaybackDecision; subtitles?: PlaybackDecision}>;
  /** Without a decision: how the session is delivered. */
  delivery?: 'direct' | 'converted';
  /** The quality the viewer chose, when not Automatic ("720p"). */
  chosenQuality?: string;
  source?: Readonly<{container?: string; videoCodec?: string; audioCodec?: string; width?: number; height?: number}>;
  /** The picture as the player decodes it. */
  shown?: Readonly<{width?: number; height?: number}>;
  bitrateKbps?: number;
  hdr?: Readonly<{source?: string; delivered?: string; toneMapped: boolean}>;
  droppedFrames?: number;
  totalFrames?: number;
  bufferSeconds?: number;
  audioOnly?: boolean;
}>;
export type PlaybackInfoRow = Readonly<{id: string; label: string; value: string}>;

const REASONS: Readonly<Record<string, MessageId>> = {
  container_unsupported: 'player.info.reason.container',
  video_codec_unsupported: 'player.info.reason.videoCodec',
  audio_codec_unsupported: 'player.info.reason.audioCodec',
  height_above_request: 'player.info.reason.height',
  bitrate_above_request: 'player.info.reason.bitrate',
  admin_cap_remote_bitrate: 'player.info.reason.ownerLimit',
  hdr_tone_mapping_required: 'player.info.reason.hdr',
  subtitle_image_needs_burn: 'player.info.reason.subtitles',
  subtitle_burn_in: 'player.info.reason.subtitles',
  stream_normalized: 'player.info.reason.normalized',
  alternate_audio_fixed_output_policy: 'player.info.reason.audioTrack',
};

const CODECS: Readonly<Record<string, string>> = {h264: 'H.264', avc: 'H.264', hevc: 'HEVC', h265: 'HEVC', av1: 'AV1', vp9: 'VP9', mpeg2video: 'MPEG-2', vc1: 'VC-1', aac: 'AAC', ac3: 'Dolby Digital', eac3: 'Dolby Digital Plus', truehd: 'Dolby TrueHD', dts: 'DTS', flac: 'FLAC', opus: 'Opus', mp3: 'MP3', alac: 'ALAC', vorbis: 'Vorbis', pcm: 'PCM'};
/** A codec as people write it ("HEVC"), or the server's name in capitals. */
export const codecName = (codec?: string): string | undefined => (codec ? CODECS[codec.toLowerCase()] ?? codec.toUpperCase() : undefined);

const size = (w?: number, h?: number) => (w && h ? `${w}×${h}` : undefined);
const converting = (d?: PlaybackDecision) => d?.action === 'transcode' || d?.action === 'burn';

/** The rows of the Playback information panel, in order. A fact nobody knows has no row. */
export function playbackInfoRows(facts: PlaybackInfoFacts, t: Translate): readonly PlaybackInfoRow[] {
  const rows: PlaybackInfoRow[] = [];
  const add = (id: string, label: MessageId, value?: string) => { if (value) rows.push({id, label: t(label), value}); };
  const d = facts.decision;
  const streams = d ? [d.video, d.audio, d.subtitles] : [];
  const anyConverting = d ? streams.some(converting) : facts.delivery === 'converted';
  const repackaged = !!d && !anyConverting && streams.some(x => x?.action === 'copy');
  add('delivery', 'player.info.delivery', t(anyConverting ? 'player.info.converting' : repackaged ? 'player.info.repackaged' : 'player.info.original'));
  if (anyConverting || repackaged) {
    const codes = new Set<string>();
    for (const x of streams) for (const r of x?.reasons ?? []) codes.add(r);
    const words = [...codes].map(code => REASONS[code]).filter((id): id is MessageId => !!id).map(id => t(id));
    if (facts.chosenQuality) words.unshift(t('player.info.reason.chosen', {quality: facts.chosenQuality}));
    add('why', 'player.info.why', [...new Set(words)].join(' ') || (anyConverting ? t('player.info.reason.unknown') : undefined));
  }
  const s = facts.source;
  if (!facts.audioOnly) {
    const from = [codecName(s?.videoCodec), size(s?.width, s?.height)].filter(Boolean).join(' · ');
    const to = converting(d?.video) ? [codecName(d?.video?.to), size(facts.shown?.width, facts.shown?.height)].filter(Boolean).join(' · ') : '';
    add('video', 'player.info.video', to && to !== from ? t('player.info.fromTo', {from: from || t('player.info.unknown'), to}) : from || size(facts.shown?.width, facts.shown?.height));
    if (facts.hdr?.source) add('hdr', 'player.info.hdr', facts.hdr.toneMapped ? t('player.info.toneMapped', {format: facts.hdr.source}) : facts.hdr.delivered ?? facts.hdr.source);
  }
  const audioFrom = codecName(s?.audioCodec);
  const audioTo = converting(d?.audio) ? [codecName(d?.audio?.to), d?.audio?.channels ? t('player.info.channels', {count: d.audio.channels}) : undefined].filter(Boolean).join(' · ') : '';
  add('audio', 'player.info.audio', audioTo && audioTo !== audioFrom ? t('player.info.fromTo', {from: audioFrom ?? t('player.info.unknown'), to: audioTo}) : audioFrom);
  add('container', 'player.info.container', s?.container ? s.container.toUpperCase() : undefined);
  if (facts.bitrateKbps && facts.bitrateKbps > 0) add('bitrate', 'player.info.bitrate', t('player.info.mbps', {rate: (facts.bitrateKbps / 1000).toFixed(facts.bitrateKbps >= 10000 ? 0 : 1)}));
  if (!facts.audioOnly && facts.totalFrames !== undefined && facts.totalFrames > 0) add('dropped', 'player.info.dropped', t('player.info.droppedOf', {dropped: facts.droppedFrames ?? 0, total: facts.totalFrames}));
  if (facts.bufferSeconds !== undefined) add('buffer', 'player.info.buffer', t('player.info.seconds', {count: Math.max(0, Math.round(facts.bufferSeconds))}));
  return rows;
}
