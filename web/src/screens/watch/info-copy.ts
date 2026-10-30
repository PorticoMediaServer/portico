import type {I18n} from '@i18n';
import {playbackInfoProjection, type PlaybackInfoWords} from '@core/playback-info-projection.ts';
import {videoTrackWords} from '@core/video-track-labels.ts';
import type {PlaybackInfo} from '@core/playback-v1/playback-info.ts';
import type {ObservedPlayback} from '@core/playback-v1/observed-playback.ts';
import type {PlaybackInfoCopy, PlaybackInfoProjection} from '../../kit/media';

const CONTAINERS: Readonly<Record<string, string>> = {matroska: 'MKV', mkv: 'MKV', mp4: 'MP4', mov: 'MOV', webm: 'WebM', mpegts: 'MPEG-TS', avi: 'AVI'};
const RANGES: Readonly<Record<string, string>> = {hdr10: 'HDR10', hdr10plus: 'HDR10+', hlg: 'HLG', dolby_vision: 'Dolby Vision'};
const container = (value: string | undefined) => value ? CONTAINERS[value] ?? value.toUpperCase() : '';

/** Frame copy for this device's panel; every value comes from the server's own
 * committed comparison facts or the renderer's own observation. */
export function playbackInfoWords(i18n: I18n): PlaybackInfoWords {
  const t = i18n.t, range = (value: string | undefined) => value ? RANGES[value] ?? t('next.info.standardRange') : t('next.info.standardRange');
  return {
    locale: i18n.locale, tracks: videoTrackWords(t, i18n.locale), standardRange: t('next.info.standardRange'),
    hls: value => t('next.info.hls', {container: value}),
    bitDepth: bits => t('next.info.bitDepth', {bits}),
    frameRate: value => t('next.info.frameRate', {value}),
    sampleRate: khz => t('next.info.sampleRate', {value: khz}),
    dolbyVision: (profile, enhancement) => !profile ? t('next.info.dolbyVision')
      : enhancement ? t('next.info.dolbyVisionProfileEnhancement', {profile, enhancement}) : t('next.info.dolbyVisionProfile', {profile}),
    convertedFromDolbyVision: (delivered, profile, enhancement) => t('next.info.convertedFrom', {delivered,
      source: !enhancement ? t('next.info.dolbyVisionProfile', {profile}) : t('next.info.dolbyVisionProfileEnhancement', {profile, enhancement})}),
    reason: reason => {
      if (reason.kind === 'compatible') return t('next.info.reason.compatible');
      if (reason.kind === 'container') return t('next.info.reason.container', {container: container(reason.container)});
      if (reason.kind === 'quality') return t('next.info.reason.quality');
      if (reason.kind === 'subtitles') return t('next.info.reason.subtitles');
      const video = !!reason.videoCodec, audio = !!reason.audioCodec;
      return t(video && audio ? 'next.info.reason.both' : video ? 'next.info.reason.video' : audio ? 'next.info.reason.audio' : 'next.info.reason.both');
    },
    loss: loss => loss.feature === 'dolbyVision' ? t('next.info.loss.dolbyVision') : loss.feature === 'hdr10plus' ? t('next.info.loss.hdr10Plus')
      : loss.feature === 'hdr' ? t('next.info.loss.hdr') : loss.feature === 'atmos' ? t('next.info.loss.atmos') : t('next.info.loss.dtsx'),
    network: (network, measuredKbps) => {
      const name = network === 'home' ? t('next.info.network.home') : network === 'away' ? t('next.info.network.away') : t('next.info.network.unknown');
      return measuredKbps ? t('next.info.networkRate', {network: name, rate: (measuredKbps / 1000).toFixed(measuredKbps >= 10000 ? 0 : 1)}) : name;
    },
    screen: observed => {
      const value = range(observed.dynamicRange);
      return observed.refreshRateHz ? t('next.info.screenRate', {range: value, rate: String(Math.round(observed.refreshRateHz * 100) / 100)}) : value;
    },
    output: audio => audio.handling === 'passthrough' ? t('next.info.output.passthrough') : t('next.info.output.decoded'),
    shown: rendering => rendering === 'burnedIn' ? t('next.info.shown.burned') : rendering === 'off' ? t('next.info.shown.off') : t('next.info.shown.drawn'),
  };
}

export function playbackInfoCopy(i18n: I18n): PlaybackInfoCopy {
  const t = i18n.t;
  return {
    title: t('next.info.title'), close: t('next.info.close'), back: t('next.info.back'), route: t('next.info.route'),
    inFile: t('next.info.inFile'), playingHere: t('next.info.playingHere'),
    routes: {original: t('next.info.routes.original'), repackaged: t('next.info.routes.repackaged'), converted: t('next.info.routes.converted')},
    sections: {picture: t('next.info.section.picture'), sound: t('next.info.section.sound'), subtitles: t('next.info.section.subtitles')},
    fields: {container: t('next.info.field.container'), codec: t('next.info.field.codec'), hdr: t('next.info.field.hdr'),
      bitDepth: t('next.info.field.bitDepth'), resolution: t('next.info.field.resolution'), frameRate: t('next.info.field.frameRate'),
      screen: t('next.info.field.screen'), format: t('next.info.field.format'), channels: t('next.info.field.channels'),
      sampleRate: t('next.info.field.sampleRate'), output: t('next.info.field.output'), track: t('next.info.field.track'), shown: t('next.info.field.shown')},
    routeName: route => t(route === 'converted' ? 'next.info.routes.converted' : route === 'repackaged' ? 'next.info.routes.repackaged' : 'next.info.routes.original'),
    originalValue: value => t('next.info.originalValue', {value}),
  };
}

/** The panel's whole projection: the server's VS-10 facts plus this renderer's own
 * observations. Nothing is synthesized when the server sent no comparison. */
export function watchPlaybackInfo(info: PlaybackInfo, observed: ObservedPlayback | undefined, i18n: I18n, title: string, deviceName?: string): PlaybackInfoProjection | undefined {
  if (!title) return undefined;
  const compared = playbackInfoProjection(info, observed, playbackInfoWords(i18n));
  return {...compared, itemTitle: title, ...(deviceName ? {deviceName} : {})};
}
