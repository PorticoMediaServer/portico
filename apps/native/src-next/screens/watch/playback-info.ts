import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {playbackInfoProjection, reconciledPlaybackLosses, type PlaybackComparisonProjection, type PlaybackInfoWords} from '../../../../../packages/client-core/src/playback-info-projection';
import type {PlaybackInfo, PlaybackReplacement} from '../../../../../packages/client-core/src/playback-v1/playback-info';
type PlaybackLoss = PlaybackInfo['losses'][number];
import {playbackFence, sameFence, type PlaybackFence, type QueuePlaybackFence} from '../../../../../packages/player-core/src';
import type {NativePlaybackAdapter, NativePlaybackReadLifetime} from '../../platform/playback/types';

/** tv-player/phone-player 5 and 13: one quiet note about what the committed plan lost.
 * `message` is the plain one-line union; `lead`/`detail` are its two drawn halves. */
export type NativeWatchNotice = Readonly<{message: string; lead: string; detail: string; icon: 'picture' | 'sound'}>;

const featureKeys: Readonly<Record<PlaybackLoss['feature'], MessageId>> = {
  dolbyVision: 'next.kit.hdr.dolbyVision', hdr10plus: 'next.kit.hdr.hdr10Plus', hdr: 'next.kit.hdr',
  atmos: 'next.kit.object.atmos', dtsx: 'next.kit.object.dtsx',
};
const rangeKeys: Readonly<Record<string, MessageId>> = {
  hdr10: 'next.kit.hdr.hdr10', hdr10plus: 'next.kit.hdr.hdr10Plus', hlg: 'next.kit.hdr.hlg',
  dolby_vision: 'next.kit.hdr.dolbyVision', sdr: 'next.watch.info.sdr',
};
const pictureFeature = (feature: PlaybackLoss['feature']) => feature === 'dolbyVision' || feature === 'hdr10plus' || feature === 'hdr';
/** What plays instead, in the notice's own short form: the delivered range or the layout. */
function playing(replacement: PlaybackReplacement): string | undefined {
  const t = defaultI18n.t;
  if (replacement.dynamicRange) return rangeKeys[replacement.dynamicRange] ? t(rangeKeys[replacement.dynamicRange]!) : replacement.dynamicRange.toUpperCase();
  // Server layouts carry a side suffix ("5.1(side)"); the note names the bed.
  if (replacement.layout) return replacement.layout.replace(/\(.*\)$/, '');
  if (replacement.channels) return t('next.kit.channels', {count: replacement.channels});
  return replacement.audioCodec?.toUpperCase();
}
function joinWithAnd(values: readonly string[]): string {
  const t = defaultI18n.t;
  if (values.length <= 1) return values[0] ?? '';
  return values.length === 2 ? t('next.watch.notice.and', {first: values[0]!, second: values[1]!})
    : t('next.watch.notice.and', {first: values.slice(0, -1).join(', '), second: values[values.length - 1]!});
}
/** One loss in the frames' own words: what is lost, where, and what plays instead. */
export function lossSentence(loss: PlaybackLoss): string | undefined {
  const t = defaultI18n.t, value = playing(loss.replacement);
  if (!value) return undefined;
  const where = t(loss.feature === 'hdr' ? 'next.watch.notice.onScreen' : 'next.watch.notice.onDevice');
  return t('next.watch.notice.one', {feature: t(featureKeys[loss.feature]), where, playing: value});
}
/** A loss the plan itself made, never one a viewer's choice in the player caused.
 * HDR lost because the screen is not HDR says so; every other loss is the device. */
export function nativeWatchLossNotice(info: Pick<PlaybackInfo, 'losses'>): NativeWatchNotice | undefined {
  if (!info.losses.length) return undefined;
  const t = defaultI18n.t;
  const features = [...new Set(info.losses.map(loss => t(featureKeys[loss.feature])))];
  const playingValues = [...new Set(info.losses.map(loss => playing(loss.replacement)).filter((value): value is string => !!value))];
  if (!playingValues.length) return undefined;
  // tv-player/phone-player 5 and 13: the frames put the lost feature and where in
  // bold, then say what plays in the quieter weight. Both halves are the
  // catalogue's own sentences; message stays their plain one-line union.
  const screenOnly = info.losses.every(loss => loss.feature === 'hdr');
  const lead = info.losses.length === 1
    ? t(screenOnly ? 'next.info.loss.one.screen' : 'next.info.loss.one.device', {feature: features[0]!})
    : t(screenOnly ? 'next.info.loss.many.screen' : 'next.info.loss.many.device', {features: joinWithAnd(features)});
  const detail = t('next.info.loss.playing', {values: info.losses.length === 1 ? playingValues[0]! : joinWithAnd(playingValues)});
  return Object.freeze({message: `${lead} ${detail}`, lead, detail, icon: info.losses.some(loss => pictureFeature(loss.feature)) ? 'picture' as const : 'sound' as const});
}

export type NativePlaybackInfo = Readonly<{
  fence: QueuePlaybackFence;
  revision: string;
  comparison: PlaybackComparisonProjection;
  /** Present only when the committed plan lost something and says what plays instead. */
  notice?: NativeWatchNotice;
}>;

/** A data read for the existing player. It owns no panel state, cache, clock or
 * renderer; committed and observed facts remain separate canonical inputs. */
export async function readNativePlaybackInfo(
  playback: Pick<NativePlaybackAdapter, 'getSnapshot' | 'readSession'>,
  fence: PlaybackFence,
  words: PlaybackInfoWords,
  lifetime: NativePlaybackReadLifetime,
): Promise<NativePlaybackInfo | undefined> {
  const read = playback.readSession;
  if (!read || fence.kind !== 'queue') return;
  const captured = playbackFence(fence);
  if (captured.kind !== 'queue') return;
  const current = () => {
    const source = playback.getSnapshot();
    return !lifetime.signal.aborted && lifetime.isCurrent() && !source.retired
      && sameFence(source.fence, captured) && sameFence(source.dock.local?.fence, captured);
  };
  if (!current()) return;
  try {
    const session = await read(captured, lifetime);
    if (!current() || !session?.playbackInfo || session.kind !== 'vod' || session.role !== 'local' || session.end || session.state === 'ended'
      || session.id !== captured.sessionId || session.itemId !== captured.itemId || session.versionId !== captured.sourceId
      || session.presentation.generation !== captured.generation || session.queue?.entryId !== captured.entryId) return;
    const comparison = playbackInfoProjection(session.playbackInfo, session.observedPlayback, words);
    if (!current()) return;
    // FMT-22: the notice and the comparison share one reconciliation of this occurrence's losses.
    const notice = nativeWatchLossNotice({losses: reconciledPlaybackLosses(session.playbackInfo, session.observedPlayback)});
    return Object.freeze({fence: captured, revision: session.revision, comparison, ...(notice ? {notice} : {})});
  } catch (error) {
    if (!current()) return;
    throw error;
  }
}
