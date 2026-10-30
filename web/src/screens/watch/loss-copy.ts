import type {I18n} from '@i18n';
import {videoAudioChannelName, videoTrackWords} from '@core/video-track-labels.ts';
import type {PlaybackInfo} from '@core/playback-v1/playback-info.ts';
import type {ObservedPlayback} from '@core/playback-v1/observed-playback.ts';
import {reconciledPlaybackLosses} from '@core/playback-info-projection.ts';
import type {PlaybackLossNotice} from '../../kit/media';

type Feature = PlaybackInfo['losses'][number]['feature'];
type Where = 'browser' | 'screen' | 'device';
const RANGES: Readonly<Record<string, string>> = {hdr10: 'HDR10', hdr10plus: 'HDR10+', hlg: 'HLG', dolby_vision: 'Dolby Vision'};
/** A picture loss on the web is the browser's capability; an SDR screen is the
 * screen itself; anything else is the device. Never a guessed hardware name. */
const where = (feature: Feature): Where => feature === 'hdr' || feature === 'hdr10plus' ? 'screen' : feature === 'dtsx' ? 'device' : 'browser';
const ONE = {browser: 'next.info.loss.one.browser', screen: 'next.info.loss.one.screen', device: 'next.info.loss.one.device'} as const;
const MANY = {browser: 'next.info.loss.many.browser', screen: 'next.info.loss.many.screen', device: 'next.info.loss.many.device'} as const;
const copy = (i18n: I18n, feature: Feature) => feature === 'dolbyVision' ? i18n.t('next.info.loss.feature.dolbyVision')
  : feature === 'hdr10plus' ? i18n.t('next.info.loss.feature.hdr10Plus') : feature === 'hdr' ? i18n.t('next.info.loss.feature.hdr')
  : feature === 'atmos' ? i18n.t('next.info.loss.feature.atmos') : i18n.t('next.info.loss.feature.dtsx');
const range = (i18n: I18n, value: string | undefined) => !value || value === 'sdr' ? i18n.t('next.info.standardRange') : RANGES[value];

function playing(i18n: I18n, info: PlaybackInfo, observed: ObservedPlayback | undefined, kind: 'picture' | 'sound'): string | undefined {
  if (kind === 'picture') return range(i18n, observed?.screen?.dynamicRange ?? info.delivered.video?.dynamicRange);
  const audio = info.delivered.audio;
  return audio ? videoAudioChannelName(audio, videoTrackWords(i18n.t, i18n.locale)) : undefined;
}

/** The drawn loss sentence for one kind of loss: the server's losses, less object audio this
 * occurrence's output is observed to have kept (the same answer as the comparison). */
export function lossSentences(i18n: I18n, info: PlaybackInfo, observed: ObservedPlayback | undefined, kind: 'picture' | 'sound' | 'all'): Readonly<{loss: string; replacement: string}> | undefined {
  const losses = reconciledPlaybackLosses(info, observed).filter(value => kind === 'all' || (kind === 'picture' ? value.feature === 'dolbyVision' || value.feature === 'hdr10plus' || value.feature === 'hdr' : value.feature === 'atmos' || value.feature === 'dtsx'));
  if (!losses.length) return undefined;
  const list = new Intl.ListFormat(i18n.locale ?? 'en-US', {type: 'conjunction', style: 'long'});
  const groups = new Map<Where, Feature[]>();
  for (const value of losses) groups.set(where(value.feature), [...groups.get(where(value.feature)) ?? [], value.feature]);
  const loss = [...groups].map(([at, features]) => features.length === 1
    ? i18n.t(ONE[at], {feature: copy(i18n, features[0]!)})
    : i18n.t(MANY[at], {features: list.format(features.map(feature => copy(i18n, feature)))})).join(' ');
  const picture = losses.some(value => value.feature === 'dolbyVision' || value.feature === 'hdr10plus' || value.feature === 'hdr') ? playing(i18n, info, observed, 'picture') : undefined;
  const sound = losses.some(value => value.feature === 'atmos' || value.feature === 'dtsx') ? playing(i18n, info, observed, 'sound') : undefined;
  const standard = i18n.t('next.info.standardRange');
  const replacement = picture && sound ? i18n.t('next.info.loss.playing', {values: list.format([picture, sound])})
    : picture ? picture === standard ? i18n.t('next.info.loss.playingRange', {range: picture}) : i18n.t('next.info.loss.playing', {values: picture})
    : sound ? i18n.t('next.info.loss.playing', {values: sound}) : '';
  return Object.freeze({loss, replacement});
}

/** One note per playback: both losses share one line, as frame 6 draws. The icon
 * follows the picture when a picture loss is among them. */
export function watchLossNotice(i18n: I18n, info: PlaybackInfo | undefined, observed: ObservedPlayback | undefined, playbackId: string): PlaybackLossNotice | undefined {
  if (!info || !playbackId) return undefined;
  const picture = reconciledPlaybackLosses(info, observed).some(value => value.feature === 'dolbyVision' || value.feature === 'hdr10plus' || value.feature === 'hdr');
  const sentences = lossSentences(i18n, info, observed, 'all');
  if (!sentences) return undefined;
  return Object.freeze({playbackId, cause: 'automatic', kind: picture ? 'picture' : 'sound', loss: sentences.loss, replacement: sentences.replacement});
}
