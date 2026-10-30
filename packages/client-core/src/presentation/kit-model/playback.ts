import {wallClockText, type TimeEnvironment} from './channels.ts';
import {bitRateText, codecText, dimensionsPart, heightText} from './technical.ts';
import {clockText, compact, nonnegative, part, positive, supplied, type FactPart, type TextEnvironment} from './text.ts';
import type {TechnicalRow} from './files.ts';

export type StreamDecision = Readonly<{action: string; reasons?: readonly string[]; to?: string; channels?: number}>;
export type StreamDecisions = Readonly<{video?: StreamDecision; audio?: StreamDecision; subtitles?: StreamDecision}>;
export type SubtitleOrigin = Readonly<{kind: 'file' | 'sidecar' | 'embedded'}> | Readonly<{kind: 'provider'; name: string}>;
export type PlaybackFacts = Readonly<{
  decision?: StreamDecisions; delivery?: 'direct' | 'converted' | 'repackaged'; chosenQuality?: string;
  source?: Readonly<{container?: string; videoCodec?: string; audioCodec?: string; width?: number; height?: number}>;
  shown?: Readonly<{width?: number; height?: number}>; bitrateKbps?: number;
  hdr?: Readonly<{source?: string; delivered?: string; toneMapped: boolean}>;
  droppedFrames?: number; totalFrames?: number; bufferSeconds?: number; audioOnly?: boolean;
  subtitleOrigin?: SubtitleOrigin;
}>;
export type PlaybackInfoRow = TechnicalRow & Readonly<{from?: readonly FactPart[]; to?: readonly FactPart[]}>;
const converting = (d: StreamDecision | undefined) => d?.action === 'transcode' || d?.action === 'burn';
function deliveryKey(decisions?: StreamDecisions, fallback?: PlaybackFacts['delivery']): 'next.kit.converting' | 'next.kit.repackaged' | 'next.kit.originalDelivery' | undefined {
  const actions = Object.values(decisions ?? {}).map(d => d.action);
  if (actions.some(a => a === 'transcode' || a === 'burn')) return 'next.kit.converting';
  if (actions.includes('copy')) return 'next.kit.repackaged';
  if (actions.includes('direct')) return 'next.kit.originalDelivery';
  return fallback === 'converted' ? 'next.kit.converting' : fallback === 'repackaged' ? 'next.kit.repackaged' : fallback === 'direct' ? 'next.kit.originalDelivery' : undefined;
}
const reasons = {
  container_unsupported: 'next.kit.reason.container', video_codec_unsupported: 'next.kit.reason.videoCodec', audio_codec_unsupported: 'next.kit.reason.audioCodec',
  height_above_request: 'next.kit.reason.height', bitrate_above_request: 'next.kit.reason.bitrate', hdr_tone_mapping_required: 'next.kit.reason.hdr',
  subtitle_image_needs_burn: 'next.kit.reason.subtitles', subtitle_burn_in: 'next.kit.reason.subtitles', stream_normalized: 'next.kit.reason.normalized', alternate_audio_fixed_output_policy: 'next.kit.reason.audioTrack',
} as const;

/** Unknown diagnostics disappear. Member output never explains an away-from-home cap. */
export function playbackInfoModel(facts: PlaybackFacts, audience: 'viewer' | 'owner', env: TextEnvironment): readonly PlaybackInfoRow[] {
  const {t} = env, rows: PlaybackInfoRow[] = [], d = facts.decision, s = facts.source;
  const add = (id: string, label: string, parts: readonly (FactPart | undefined)[], more: Pick<PlaybackInfoRow, 'from' | 'to'> = {}) => { const present = compact(parts); if (present.length || more.from?.length || more.to?.length) rows.push(Object.freeze({id, label, parts: present, ...more})); };
  const delivery = deliveryKey(d, facts.delivery);
  add('delivery', t('next.kit.delivery'), [delivery ? part('delivery', t(delivery), 'status') : undefined]);
  if (delivery && delivery !== 'next.kit.originalDelivery') {
    const keys = new Set<typeof reasons[keyof typeof reasons] | 'next.kit.reason.ownerLimit'>();
    for (const decision of Object.values(d ?? {})) for (const code of decision.reasons ?? []) {
      const key = code === 'admin_cap_remote_bitrate' ? audience === 'owner' ? 'next.kit.reason.ownerLimit' : undefined : reasons[code as keyof typeof reasons];
      if (key) keys.add(key);
    }
    add('why', t('next.kit.why'), [supplied(facts.chosenQuality) ? part('chosen-quality', t('next.kit.reason.chosen', {quality: facts.chosenQuality}), 'reason') : undefined, ...[...keys].map(key => part(key, t(key), 'reason'))]);
  }
  const videoCodec = codecText(s?.videoCodec, t), audioCodec = codecText(s?.audioCodec, t);
  if (!facts.audioOnly) {
    const from = compact([videoCodec ? part('codec', videoCodec) : undefined, dimensionsPart(s?.width, s?.height, t)]);
    const toCodec = converting(d?.video) ? codecText(d?.video?.to, t) : undefined;
    const to = converting(d?.video) ? compact([toCodec ? part('codec', toCodec) : undefined, dimensionsPart(facts.shown?.width, facts.shown?.height, t)]) : [];
    const shown = !converting(d?.video) && (facts.shown?.width !== s?.width || facts.shown?.height !== s?.height) ? dimensionsPart(facts.shown?.width, facts.shown?.height, t, 'shown-dimensions') : undefined;
    add('video', t('next.kit.video'), [shown], {from, to});
    if (supplied(facts.hdr?.source)) add('hdr', t('next.kit.hdr'), [part('hdr', facts.hdr.toneMapped ? t('next.kit.toneMapped', {format: facts.hdr.source}) : facts.hdr.delivered || facts.hdr.source)]);
  }
  const from = compact([audioCodec ? part('codec', audioCodec) : undefined]);
  const toCodec = converting(d?.audio) ? codecText(d?.audio?.to, t) : undefined;
  const to = converting(d?.audio) ? compact([toCodec ? part('codec', toCodec) : undefined, positive(d?.audio?.channels) ? part('channels', t('next.kit.channels', {count: d.audio.channels})) : undefined]) : [];
  add('audio', t('next.kit.audio'), [], {from, to});
  add('container', t('next.kit.container'), [supplied(s?.container) ? part('container', s.container.toUpperCase()) : undefined]);
  const rate = positive(facts.bitrateKbps) ? bitRateText(facts.bitrateKbps * 1000, t) : undefined;
  add('bitrate', t('next.kit.bitrate'), [rate ? part('bitrate', rate) : undefined]);
  if (!facts.audioOnly && nonnegative(facts.droppedFrames) && positive(facts.totalFrames) && facts.droppedFrames <= facts.totalFrames) add('dropped', t('next.kit.dropped'), [part('dropped', t('next.kit.droppedOf', {dropped: facts.droppedFrames, total: facts.totalFrames}))]);
  if (nonnegative(facts.bufferSeconds)) add('buffer', t('next.kit.buffer'), [part('buffer', t('next.kit.bufferSeconds', {count: Math.round(facts.bufferSeconds)}))]);
  const origin = facts.subtitleOrigin;
  if (origin) {
    const text = origin.kind === 'provider' ? supplied(origin.name) ? origin.name : undefined : t(`next.kit.origin.${origin.kind}`);
    add('subtitle-origin', t('next.kit.subtitleOrigin'), [text ? part('subtitle-origin', text, 'context') : undefined]);
  }
  return Object.freeze(rows);
}

export type SessionFacts = Readonly<{
  user?: string; profile?: string; device?: string; state?: string; kind: 'vod' | 'audio' | 'channel';
  positionMs?: number; durationMs?: number; decision?: StreamDecisions; location?: 'local' | 'remote' | 'unknown';
  bandwidthKbps?: number; bitrateKbps?: number; startedAt?: string;
}>;

/** Only already-authorized admin sessions enter here. This never reads/admin-fetches a session. */
export function sessionFactParts(facts: SessionFacts, env: TimeEnvironment): readonly FactPart[] {
  const {t} = env, delivery = deliveryKey(facts.decision), rate = bitRateText((positive(facts.bandwidthKbps) ? facts.bandwidthKbps : positive(facts.bitrateKbps) ? facts.bitrateKbps : 0) * 1000, t);
  const state = facts.state === 'playing' ? 'next.kit.session.playing' : facts.state === 'paused' ? 'next.kit.session.paused' : facts.state === 'preparing' || facts.state === 'buffering' ? 'next.kit.session.starting' : undefined;
  const position = facts.kind !== 'channel' && nonnegative(facts.positionMs) ? clockText(facts.positionMs / 1000) : undefined;
  const duration = positive(facts.durationMs) ? clockText(facts.durationMs / 1000) : undefined;
  const started = facts.startedAt ? Date.parse(facts.startedAt) : NaN;
  // Reuse the same device-zone/clock validation, without reading the wall clock.
  const time = wallClockText(started, env);
  return compact([
    supplied(facts.profile) || supplied(facts.user) ? part('viewer', facts.profile || facts.user!, 'identity') : undefined,
    supplied(facts.profile) && supplied(facts.user) && facts.profile !== facts.user ? part('account', facts.user, 'context') : undefined,
    supplied(facts.device) ? part('device', t('next.kit.session.onDevice', {device: facts.device}), 'context') : undefined,
    state ? part('state', t(state), 'status') : undefined,
    position ? part('position', duration ? t('next.kit.session.positionOf', {position, duration}) : t('next.kit.session.position', {position}), 'supplement') : undefined,
    delivery ? part('delivery', t(delivery), 'status') : undefined,
    facts.location === 'local' || facts.location === 'remote' ? part('location', t(facts.location === 'local' ? 'next.kit.session.local' : 'next.kit.session.remote'), 'context') : undefined,
    rate ? part('bitrate', rate, 'supplement') : undefined,
    time ? part('started', t('next.kit.session.started', {time}), 'supplement') : undefined,
  ]);
}

export type QualityFacts = Readonly<{
  choice: Readonly<{kind: 'automatic'}> | Readonly<{kind: 'original'}> | Readonly<{kind: 'fixed'; height?: number}>;
  /** Current decoded/delivered picture only. Never the policy ceiling or source-file resolution. */
  observedHeight?: number;
  policy?: Readonly<{network: 'home' | 'away' | 'unknown'; maxHeight?: number}>;
}>;
export type QualityControlModel = Readonly<{label: string; parts: readonly FactPart[]; optionName: string; detail: readonly FactPart[]; footer: readonly FactPart[]}>;

export function qualityControlModel(facts: QualityFacts, audience: 'viewer' | 'owner', env: TextEnvironment): QualityControlModel {
  const {t} = env, observed = heightText(facts.observedHeight, t), fixed = facts.choice.kind === 'fixed' ? heightText(facts.choice.height, t) : undefined;
  const automatic = facts.choice.kind === 'automatic';
  const label = automatic ? observed ? t('next.kit.quality.autoNow', {quality: observed}) : t('next.kit.quality.auto') : facts.choice.kind === 'original' ? t('next.kit.quality.original') : fixed;
  const ceiling = heightText(facts.policy?.maxHeight, t);
  return Object.freeze({label: label ?? '',
    parts: compact(automatic ? [part('mode', t('next.kit.quality.auto'), 'context'), observed ? part('observed-quality', observed) : undefined] : [label ? part(fixed ? 'fixed-quality' : 'mode', label) : undefined]),
    optionName: automatic ? t('next.kit.quality.automatic') : label ?? '',
    detail: compact([automatic && observed ? part('observed-quality', t('next.kit.quality.adjusts', {quality: observed}), 'supplement') : facts.choice.kind === 'original' ? part('original', t('next.kit.quality.originalDetail'), 'supplement') : undefined]),
    footer: compact([ceiling && facts.policy?.network !== 'unknown' && (facts.policy?.network === 'home' || audience === 'owner') ? part('network-limit', t(facts.policy?.network === 'away' ? 'next.kit.quality.away' : 'next.kit.quality.home', {quality: ceiling}), 'supplement') : undefined]),
  });
}
