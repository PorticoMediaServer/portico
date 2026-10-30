import React, {useEffect, useRef, useState} from 'react';
import {ActivityIndicator, ScrollView, Text, View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import {videoTrackWords} from '../../../../../packages/client-core/src/video-track-labels';
import {codecName} from '../../../../../packages/client-core/src/presentation/playback-info';
import type {PlaybackInfoField, PlaybackInfoWords} from '../../../../../packages/client-core/src/playback-info-projection';
import type {NativePlaybackReadLifetime} from '../../platform/playback/types';
import {Button, Focusable} from '../../kit/controls';
import {nativeType, useTheme} from '../../kit/theme';
import type {NativeWatchHostContext} from './WatchHost';
import {lossSentence, type NativePlaybackInfo} from './playback-info';
import {Quiet} from '../../kit/states';

const t = defaultI18n.t;
const fieldLabels: Record<PlaybackInfoField, MessageId> = {
  container: 'next.info.field.container', codec: 'next.info.field.codec', hdr: 'next.info.field.hdr', bitDepth: 'next.info.field.bitDepth',
  resolution: 'next.info.field.resolution', frameRate: 'next.info.field.frameRate', screen: 'next.info.field.screen',
  format: 'next.info.field.format', channels: 'next.info.field.channels', sampleRate: 'next.info.field.sampleRate',
  output: 'next.info.field.output', track: 'next.info.field.track', shown: 'next.info.field.shown',
};
const sectionLabels = {picture: 'next.info.section.picture', sound: 'next.info.section.sound', subtitles: 'next.info.section.subtitles'} as const;
const routeLabels = {original: 'next.info.routes.original', repackaged: 'next.info.routes.repackaged', converted: 'next.info.routes.converted'} as const;
const routes = ['original', 'repackaged', 'converted'] as const;
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

/** playback-info frames 3 and 4: the TV panel keeps the route and the column
 * heads still, and each section is one focus stop. Phone keeps its inline list. */
type Comparison = NonNullable<NativePlaybackInfo['comparison']>;
function TvComparison({context, comparison, restoreOffset}: {context: NativeWatchHostContext; comparison: Comparison; restoreOffset: number}) {
  const theme = useTheme(), first = useRef<View>(null), scroll = useRef<ScrollView>(null), pending = useRef(restoreOffset);
  const entry = context.source.entry, title = entry ? entry.displayTitle ?? entry.title : undefined;
  const label = nativeType({size: 20, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale);
  const valueType = nativeType({size: 22, weight: 500, tracking: 0, lineHeight: 29}, theme.textScale);
  const routeColour = (route: (typeof routes)[number]) => route === 'original' ? theme.colors.healthy : route === 'repackaged' ? theme.colors.accentStrong : '#e8bc6a';
  useEffect(() => {if (theme.isTV) first.current?.requestTVFocus?.();}, [comparison.route, theme.isTV]);
  return <View testID="watch-playback-info-comparison" style={{flex: 1, minHeight: 0}}>
    {!!title && <Text style={[nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 31.9}, theme.textScale), {color: theme.colors.textSecondary, marginTop: 10, marginBottom: 14}]}>
      <Text style={{fontFamily: theme.type.bodyStrong.fontFamily}}>{title}</Text></Text>}
    <View style={{paddingVertical: 20, paddingHorizontal: 24, borderRadius: 18, backgroundColor: 'rgba(237,245,250,.05)', marginBottom: 22}}>
      <View style={{flexDirection: 'row', alignItems: 'center'}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 14}}>
          {routes.map((route, index) => <React.Fragment key={route}>
            {index > 0 && <View style={{width: 60, height: 2, backgroundColor: 'rgba(237,245,250,.16)'}}/>}
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
              <View style={{width: 12, height: 12, borderRadius: 6, backgroundColor: comparison.route === route ? routeColour(route) : 'transparent',
                borderWidth: comparison.route === route ? 0 : 1.5, borderColor: 'rgba(237,245,250,.32)'}}/>
              <Text style={[nativeType({size: 22, weight: comparison.route === route ? 800 : 600, tracking: 0, lineHeight: 'normal'}, theme.textScale),
                {color: comparison.route === route ? theme.colors.text : theme.colors.textTertiary}]}>{t(routeLabels[route])}</Text>
            </View>
          </React.Fragment>)}
        </View>
        {!!comparison.networkAndBitrate && <Text style={[label, {marginLeft: 'auto', color: theme.colors.textTertiary}]}>{comparison.networkAndBitrate}</Text>}
      </View>
      {!!comparison.explanation && <Text style={[nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 31.9}, theme.textScale), {color: theme.colors.textSecondary, marginTop: 12}]}>{comparison.explanation}</Text>}
    </View>
    <View style={{flexDirection: 'row', columnGap: 24, paddingHorizontal: 24, paddingBottom: 10, marginBottom: 4, borderBottomWidth: 1.5, borderColor: 'rgba(40,64,79,.9)'}}>
      <View style={{width: 160}}/>
      <Text style={[nativeType({size: 19, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {flex: 1, color: theme.colors.textTertiary}]}>{t('next.info.inFile')}</Text>
      <Text style={[nativeType({size: 19, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {flex: 1, color: theme.colors.textTertiary}]}>{t('next.info.playingHere')}</Text>
    </View>
    <ScrollView ref={scroll} style={{flex: 1, minHeight: 0}} scrollEventThrottle={32} contentContainerStyle={{paddingTop: 10, paddingBottom: 72}}
      onContentSizeChange={() => {
        // The host remembered this panel's offset for the same panel and fence lifetime.
        const offset = pending.current;
        if (!offset) return;
        pending.current = 0;
        if (context.state.panel === 'playback-info' && sameFence(context.state.fence, context.fence)) scroll.current?.scrollTo({y: offset, animated: false});
      }}
      onScroll={event => {if (theme.isTV) context.rememberPanel(event.nativeEvent.contentOffset.y);}}>
      {comparison.sections.map((section, index) => <Focusable key={section.kind} ref={index === 0 ? first : undefined} focusId={`watch-info-${section.kind}`} testID={`watch-info-${section.kind}`}
        focusKind="row" focusRadius={18} hasTVPreferredFocus={index === 0} accessibilityLabel={t(sectionLabels[section.kind])}
        style={({focused}) => ({borderRadius: 18, paddingVertical: 16, paddingHorizontal: 24, marginBottom: 10, backgroundColor: focused ? theme.colors.raisedHigh : 'transparent'})}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 18, marginBottom: 8}}>
          <Text accessibilityRole="header" style={[nativeType({size: 26, weight: 800, tracking: -.015, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t(sectionLabels[section.kind])}</Text>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 7}}>
            <View style={{width: 7, height: 7, borderRadius: 3.5, backgroundColor: routeColour(section.route)}}/>
            <Text style={[nativeType({size: 20, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{t(routeLabels[section.route])}</Text>
          </View>
        </View>
        <View>
          {section.rows.map(row => <View key={row.field} style={{flexDirection: 'row', columnGap: 24, paddingVertical: 2.5}}>
            <Text style={[nativeType({size: 20, weight: 600, tracking: 0, lineHeight: 26}, theme.textScale), {width: 160, paddingTop: 2, color: theme.colors.textTertiary}]}>{t(fieldLabels[row.field])}</Text>
            <Text style={[valueType, {flex: 1, color: theme.colors.textSecondary}]}>{row.source}</Text>
            <View style={{flex: 1, flexDirection: 'row'}}>
              {row.changed && <View style={{width: 8, height: 8, borderRadius: 4, marginTop: 10, marginRight: 10, backgroundColor: theme.colors.accentStrong}}/>}
              <Text style={[valueType, {flex: 1, color: row.changed ? theme.colors.text : theme.colors.textSecondary, fontWeight: row.changed ? '700' : '500'}]}>{row.delivered}</Text>
            </View>
          </View>)}
        </View>
        {section.losses.map((loss, lossIndex) => <Text key={lossIndex} style={[nativeType({size: 20, weight: 500, tracking: 0, lineHeight: 28}, theme.textScale),
          {marginTop: 10, marginLeft: 184, color: theme.colors.textSecondary}]}>{loss}</Text>)}
      </Focusable>)}
    </ScrollView>
  </View>;
}

/** Read only on committed mount or explicit Retry. The existing factory owns authority and transport. */
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
  if (theme.isTV && comparison) return <TvComparison context={context} comparison={comparison} restoreOffset={restoreOffset}/>;
  const label = [theme.type.caption, {color: theme.colors.textSecondary}], value = [theme.type.body, {color: theme.colors.text}];
  return <View testID="watch-playback-info-comparison" style={{gap: 18, padding: 16}}>
    <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t(routeLabels[comparison.route])}</Text>
    {!!comparison.explanation && <Text style={value}>{comparison.explanation}</Text>}
    {!!comparison.networkAndBitrate && <Text style={label}>{comparison.networkAndBitrate}</Text>}
    {comparison.sections.map(section => <View key={section.kind} style={{gap: 10}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t(sectionLabels[section.kind])}</Text>
      <Text style={label}>{t(routeLabels[section.route])}</Text>
      <View style={{flexDirection: 'row', gap: 12}}><View style={{flex: 1}}/><Text style={[label, {flex: 1}]}>{t('next.watch.info.source')}</Text><Text style={[label, {flex: 1}]}>{t('next.watch.info.delivered')}</Text></View>
      {section.rows.map(row => <View key={row.field} style={{flexDirection: 'row', gap: 12}}>
        <Text style={[label, {flex: 1}]}>{t(fieldLabels[row.field])}</Text><Text style={[value, {flex: 1}]}>{row.source}</Text>
        <Text style={[value, {flex: 1, color: row.changed ? theme.colors.accentStrong : theme.colors.text}]}>{row.delivered}</Text>
      </View>)}
      {section.losses.map((loss, index) => <Text key={index} style={label}>{loss}</Text>)}
    </View>)}
  </View>;
}
