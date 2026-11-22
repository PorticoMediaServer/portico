import React, {useEffect, useRef, useState} from 'react';
import {ActivityIndicator, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import {videoTrackWords} from '../../../../../packages/client-core/src/video-track-labels';
import {codecName} from '../../../../../packages/client-core/src/presentation/playback-info';
import type {PlaybackInfoWords} from '../../../../../packages/client-core/src/playback-info-projection';
import type {NativePlaybackReadLifetime} from '../../platform/playback/types';
import {Button} from '../controls';
import {PlaybackInfoComparison} from './PlaybackInfo';
import {useTheme} from '../theme';
import type {NativeWatchHostContext} from '../../screens/watch/WatchHost';
import {lossSentence, type NativePlaybackInfo} from '../../screens/watch/playback-info';
import {Quiet} from '../states';

const t = defaultI18n.t;
const range = (value: string | undefined) => value === 'sdr' ? t('next.watch.info.sdr') : value === 'hdr10' ? t('next.kit.hdr.hdr10')
  : value === 'hdr10plus' ? t('next.kit.hdr.hdr10Plus') : value === 'hlg' ? t('next.kit.hdr.hlg')
    : value === 'dolby_vision' ? t('next.kit.hdr.dolbyVision') : '';
/** The one word port for the panel and the loss notice; no policy in it. */
export function playbackInfoWords(): PlaybackInfoWords {
  return {tracks: videoTrackWords(t), standardRange: t('next.watch.info.sdr'),
    hls: container => t('next.watch.info.hls', {container}), bitDepth: bits => t('next.kit.bitDepth', {bits}),
    frameRate: rate => t('next.kit.fps', {rate}), sampleRate: rate => t('next.watch.info.khz', {rate}),
    dolbyVision: (profile, enhancement) => t('next.watch.info.dolbyVision', {details: [profile, enhancement].filter(Boolean).join(' ')}).trim(),
    convertedFromDolbyVision: (delivered, profile, enhancement) => t('next.watch.info.convertedVision', {delivered, profile, enhancement: enhancement ?? ''}).trim(),
    // The panel's own sentences; a format reason names the codecs the server moved.
    reason: reason => reason.kind === 'compatible' ? t('next.info.reason.compatible')
      : reason.kind === 'container' ? reason.container ? t('next.info.reason.container', {container: reason.container}) : t('next.kit.reason.container')
        : reason.kind === 'subtitles' ? t('next.info.reason.subtitles')
          : reason.kind === 'quality' ? t('next.info.reason.quality')
            : reason.videoCodec && reason.audioCodec ? t('next.info.reason.both')
              : reason.audioCodec ? t('next.info.reason.audio') : reason.videoCodec ? t('next.info.reason.video') : t('next.kit.reason.videoCodec'),
    loss: loss => lossSentence(loss) ?? t('next.watch.info.lost', {feature: t(loss.feature === 'dolbyVision' ? 'next.kit.hdr.dolbyVision'
      : loss.feature === 'hdr10plus' ? 'next.kit.hdr.hdr10Plus' : loss.feature === 'hdr' ? 'next.kit.hdr' : loss.feature === 'atmos' ? 'next.kit.object.atmos' : 'next.kit.object.dtsx')}),
    network: (network, measured) => [network === 'home' ? t('next.kit.session.local') : network === 'away' ? t('next.kit.session.remote') : undefined,
      measured ? t('next.kit.kbps', {rate: measured}) : undefined].filter(Boolean).join(' ') || undefined,
    screen: value => [range(value.dynamicRange), value.refreshRateHz ? t('next.watch.info.hz', {rate: value.refreshRateHz}) : undefined].filter(Boolean).join(' ') || undefined,
    output: value => [value.handling ? t(`next.watch.info.${value.handling}`) : undefined, codecName(value.codec),
      value.channels ? t('mediaInfo.channels', {count: value.channels}) : undefined,
      value.sampleRate ? t('next.watch.info.khz', {rate: value.sampleRate / 1000}) : undefined,
      value.bitDepth ? t('next.kit.bitDepth', {bits: value.bitDepth}) : undefined].filter(Boolean).join(' ') || undefined,
    shown: value => t(`next.watch.info.${value}`),
  };
}
export type NativePlaybackInfoReader = (fence: PlaybackFence, words: PlaybackInfoWords, lifetime: NativePlaybackReadLifetime) => Promise<NativePlaybackInfo | undefined>;

/** Read only on committed mount or explicit Retry. The existing factory owns authority and transport; the kit's
 * `PlaybackInfoComparison` draws what it read. */
export function NativePlaybackInfoPanel({context, read, renderError}: {
  context: NativeWatchHostContext; read: NativePlaybackInfoReader;
  renderError: (error: unknown, retry: () => void) => React.ReactNode;
}) {
  const theme = useTheme(), latest = useRef(context), retryTarget = useRef<View>(null); latest.current = context;
  const [retry, setRetry] = useState(0), [result, setResult] = useState<{identity: string; runtime: NativeWatchHostContext['runtime']; value?: NativePlaybackInfo; error?: unknown}>();
  // The host remembers a panel's offset only for that panel and its fence; take it
  // once, when this panel opens, and let its own content apply it after layout.
  const remembered = useRef<number | undefined>(undefined);
  if (remembered.current === undefined) remembered.current = context.state.panel === 'playback-info' && sameFence(context.state.fence, context.fence)
    ? context.state.panelOffset : 0;
  const restoreOffset = remembered.current;
  const identity = JSON.stringify([context.fence, context.source.lease]);
  const runtime = context.runtime;
  useEffect(() => {
    const captured = latest.current, abort = new AbortController();
    const current = () => !abort.signal.aborted && latest.current.runtime === runtime
      && latest.current.source.lease === captured.source.lease && !latest.current.source.retired
      && latest.current.state.panel === 'playback-info' && sameFence(latest.current.fence, captured.fence)
      && sameFence(latest.current.source.fence, captured.fence) && sameFence(latest.current.state.fence, captured.fence);
    setResult(undefined);
    const deadline = setTimeout(() => {
      if (current()) setResult({identity, runtime});
      abort.abort();
    }, 15000);
    if (current()) void read(captured.fence, playbackInfoWords(), {signal: abort.signal, isCurrent: current})
      .then(value => {if (current()) setResult({identity, runtime, value});})
      .catch(error => {if (current()) setResult({identity, runtime, error});})
      .finally(() => clearTimeout(deadline));
    return () => {clearTimeout(deadline); abort.abort();};
  }, [identity, runtime, read, retry]);
  useEffect(() => {
    if (theme.isTV && result?.identity === identity && result.runtime === runtime && result.error === undefined && !result.value?.comparison
      && latest.current.state.panel === 'playback-info' && sameFence(latest.current.state.fence, latest.current.fence) && latest.current.panelCurrent()) retryTarget.current?.requestTVFocus?.();
  }, [theme.isTV, result, identity, runtime]);
  if (!result || result.identity !== identity || result.runtime !== runtime) return <Quiet><ActivityIndicator accessibilityLabel={t('player.opt.info')}/></Quiet>;
  if (result.error !== undefined) return <>{renderError(result.error, () => setRetry(value => value + 1))}</>;
  const comparison = result.value?.comparison;
  if (!comparison) return <View style={{gap: 12}}><Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.watch.info.unavailable')}</Text>
    <Button ref={retryTarget} focusId="watch-info-retry" testID="watch-info-retry" variant="glass" onPress={() => setRetry(value => value + 1)}>{t('action.tryAgain')}</Button></View>;
  const entry = context.source.entry;
  return <PlaybackInfoComparison comparison={comparison} title={theme.isTV && entry ? entry.displayTitle ?? entry.title : undefined} restoreOffset={restoreOffset}
    canRestore={() => latest.current.state.panel === 'playback-info' && sameFence(latest.current.state.fence, latest.current.fence)} onScroll={offset => latest.current.rememberPanel(offset)}/>;
}
