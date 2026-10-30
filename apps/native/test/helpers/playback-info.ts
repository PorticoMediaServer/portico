import type {PlaybackInfo} from '../../../../packages/client-core/src/playback-v1/playback-info.ts';
import type {ObservedPlayback} from '../../../../packages/client-core/src/playback-v1/observed-playback.ts';
import type {PlaybackInfoWords} from '../../../../packages/client-core/src/playback-info-projection.ts';
import {videoTrackWords} from '../../../../packages/client-core/src/video-track-labels.ts';
import {defaultI18n} from '../../../../packages/i18n/src/index.ts';

export const committedInfo: PlaybackInfo = {route: 'converted', routes: {picture: 'converted', sound: 'converted'}, reason: {kind: 'format', audioCodec: 'aac'},
  source: {video: {codec: 'h264', width: 1920, height: 1080}, audio: {codec: 'flac', channels: 8, sampleRate: 96000}},
  delivered: {video: {codec: 'h264', width: 1280, height: 720}, audio: {codec: 'aac', channels: 2, sampleRate: 48000}}, losses: [], network: 'home', estimatedBitrateKbps: 9000, basis: 'committed_delivery_plan'};
export function infoWords() {
  const outputs: NonNullable<ObservedPlayback['audio']>[] = [], measured: (number | undefined)[] = [];
  const words: PlaybackInfoWords = {tracks: videoTrackWords(defaultI18n.t, 'en-US'), locale: 'en-US', standardRange: 'SDR', hls: value => value,
    bitDepth: String, frameRate: value => value, sampleRate: value => value, dolbyVision: value => value ?? 'DV', convertedFromDolbyVision: value => value,
    reason: () => undefined, loss: value => value.feature, network: (_network, kbps) => {measured.push(kbps); return kbps === undefined ? undefined : String(kbps);},
    screen: value => value.dynamicRange, output: value => {outputs.push(value); return `${value.handling}:${value.sampleRate}`;}, shown: value => value};
  return {words, outputs, measured};
}

