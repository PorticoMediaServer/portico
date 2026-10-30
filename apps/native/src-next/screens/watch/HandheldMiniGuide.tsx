import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, StyleSheet, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {nowNext} from '../../../../../packages/client-core/src/guide/now-next';
import {channelTint} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {playbackUserMessage} from '../../../../../packages/client-core/src/playback-messages';
import {Button, Focusable} from '../../kit/controls';
import {FocusRegion} from '../../kit/focus';
import {useOverlayLifecycle} from '../../kit/overlays/OverlayHost';
import {Artwork} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {nativeType, useTheme} from '../../kit/theme';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import type {NativeWatchHostContext} from './WatchHost';
import type {NativeLiveGuideSnapshot, NativeTVLiveGuide} from './live-guide';

/** The same bounded, authorized rows as TV, in the phone options pane or the
 * tablet's docked pane. The host keeps the existing native picture playing. */
export function NativeHandheldMiniGuide({context, guide, snapshot}: {
  context: NativeWatchHostContext; guide: NativeTVLiveGuide; snapshot: NativeLiveGuideSnapshot;
}) {
  const theme = useTheme(), t = defaultI18n.t, first = useRef<View>(null);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {const timer = setInterval(() => setNow(Date.now()), 20_000);return () => clearInterval(timer);}, []);
  const close = () => {guide.close(snapshot);};
  useOverlayLifecycle(snapshot.open, true, close, first, undefined, 0, 'watch-mini-guide');
  const current = () => snapshot.open && guide.getSnapshot() === snapshot;
  const upright = context.layout.mode === 'watch', paged = context.layout.panelPaged === true;
  const docked = context.layout.mode === 'full-window' && !!context.layout.surface && context.layout.surface.width < context.renderer.width;
  const tablet = docked && !paged;
  const glass = glassRecipe(), radius = docked || paged ? 0 : 20;
  const color = tablet ? '#0a1520' : paged ? 'rgba(22,44,58,.98)' : 'rgba(16,33,45,.95)';
  const time = (at: number): string | undefined => {
    if (!Number.isFinite(at) || !snapshot.timezone) return undefined;
    try {return new Intl.DateTimeFormat(context.runtime.text.locale, {hour: 'numeric', minute: '2-digit', timeZone: snapshot.timezone}).format(new Date(at));}
    catch {return undefined;}
  };
  return <View testID="watch-mini-guide" accessibilityViewIsModal onAccessibilityEscape={close}
    style={{flex: 1, borderRadius: radius, overflow: 'hidden', backgroundColor: color,
      boxShadow: tablet ? '-20px 0 60px -20px rgba(0,0,0,.8)' : '0 30px 60px -16px #000, inset 0 0 0 1px rgba(237,245,250,.09)'}}>
    {!docked && !paged && glass.nativeBlur && <View pointerEvents="none" style={StyleSheet.absoluteFill}>
      <BlurView style={StyleSheet.absoluteFill} blurType="dark" blurAmount={30} reducedTransparencyFallbackColor={theme.colors.raised}/>
      <View style={[StyleSheet.absoluteFill, {backgroundColor: color}]}/>
    </View>}
    {upright && <View pointerEvents="none" style={{height: 5, width: 36, borderRadius: 3, backgroundColor: '#9bbdce66', alignSelf: 'center', marginTop: 8}}/>}
    <FocusRegion id="watch-mini-guide" hold orientation="vertical" style={{flex: 1}}>
      <View style={{flexDirection: 'row', alignItems: 'center', paddingTop: tablet ? 24 : 8, paddingLeft: tablet ? 24 : 18, paddingRight: tablet ? 16 : 6, paddingBottom: tablet ? 10 : 0}}>
        <Text accessibilityRole="header" style={[nativeType({size: tablet ? 24 : upright ? 19 : 18, weight: 800, tracking: tablet ? -.025 : -.02, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text, flex: 1}]}>{t('next.controls.guide')}</Text>
        <Button ref={first} testID="watch-mini-guide-close" focusId="watch-mini-guide-close" variant="text" iconOnly icon="i-close"
          accessibilityLabel={t('action.close')} onPress={close} style={{width: 44, height: 44, borderRadius: 22}}/>
      </View>
      {!!snapshot.error && <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.textSecondary, paddingHorizontal: 18, paddingBottom: 10}]}>{playbackUserMessage(snapshot.error)}</Text>}
      {snapshot.phase === 'loading' && <Text style={[theme.type.caption, {color: theme.colors.textTertiary, paddingHorizontal: 18, paddingBottom: 10}]}>{t('next.browse.native.markAllPreparing')}</Text>}
      <ScrollView contentContainerStyle={{paddingTop: 4, paddingHorizontal: tablet ? 0 : 8, paddingBottom: tablet ? 24 : 8}}>
        {snapshot.rows.map(row => {
          const {channel} = row, programme = nowNext(row.programmes, now), next = programme.next, at = next ? time(next.start) : undefined;
          const start = programme.now?.start, end = programme.now?.end;
          const progress = typeof start === 'number' && Number.isFinite(start) && typeof end === 'number' && Number.isFinite(end) && end > start
            ? Math.max(0, Math.min(1, (now - start) / (end - start))) : undefined;
          const id = `${channel.kind}/${channel.sourceId ?? ''}/${channel.id}`, size = tablet ? 40 : 38;
          const logo = channel.logoUrl ? context.runtime.artwork(channel.logoUrl) : undefined;
          const disabled = !current() || snapshot.busy || !channel.tuneAvailable;
          return <Focusable key={id} testID={`watch-mini-guide-${id}`} focusId={`watch-mini-guide-${id}`} focusKind="row" focusRadius={12}
            accessibilityRole="button" accessibilityLabel={[channel.name, channel.number, programme.now?.title ?? t('guide.noInformation')].filter(Boolean).join(', ')}
            accessibilityState={{selected: row.step === 0, disabled, busy: snapshot.busy}} disabled={disabled}
            onPress={() => {if (!disabled && current()) void guide.tune(row, snapshot, context.runtime.reportError);}}
            style={{minHeight: tablet ? 64 : 60, paddingVertical: tablet ? 10 : 6, paddingLeft: tablet ? 24 : 10, paddingRight: tablet ? 20 : 10,
              flexDirection: 'row', alignItems: 'center', gap: tablet ? 14 : 12, borderRadius: tablet ? 0 : 12, opacity: channel.tuneAvailable ? 1 : .55,
              backgroundColor: row.step === 0 ? 'rgba(237,245,250,.07)' : 'transparent'}}>
            <View style={{width: size, height: size, borderRadius: 9, overflow: 'hidden', alignItems: 'center', justifyContent: 'center',
              backgroundColor: channel.mark ? channelTint(channel.mark.seed) : '#edf5fa14'}}>
              <Text numberOfLines={1} style={[nativeType({size: tablet ? 13 : 12, weight: 800, tracking: -.01, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{channel.mark?.text ?? channel.number}</Text>
              {logo && <Artwork source={logo} sized={false} visible style={StyleSheet.absoluteFillObject}/>}
            </View>
            <View style={{flex: 1, minWidth: 0}}>
              <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
                <Text numberOfLines={1} style={[nativeType({size: tablet ? 12.5 : 12, weight: tablet ? 600 : 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, flexShrink: 1}]}>{channel.name}</Text>
                {!!channel.number && <Text style={[theme.type.caption, {fontSize: 12, color: theme.colors.textTertiary}]}>{channel.number}</Text>}
                {row.step === 0 && <Text style={[theme.type.captionStrong, {fontSize: 12, color: theme.colors.accentStrong, marginLeft: 'auto'}]}>{t('next.live.watching')}</Text>}
              </View>
              <Text numberOfLines={1} style={[nativeType({size: tablet ? 15 : 14.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: programme.now ? theme.colors.text : theme.colors.textTertiary, marginTop: tablet ? 2 : 1}]}>{programme.now?.title ?? t('guide.noInformation')}</Text>
              <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginTop: tablet ? 6 : 5}}>
                {progress !== undefined && <View testID={`watch-mini-guide-progress-${id}`} style={{width: 70, height: 3, borderRadius: 2, backgroundColor: '#edf5fa26', overflow: 'hidden'}}>
                  <View style={{width: `${progress * 100}%`, height: '100%', backgroundColor: theme.colors.accentStrong}}/>
                </View>}
                {next && <Text numberOfLines={1} style={[nativeType({size: 12, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, flexShrink: 1}]}>
                  {at ? t('next.live.nextAt', {time: at}) : next.title}{at && !tablet && <Text style={{color: theme.colors.textSecondary}}>{`  ${next.title}`}</Text>}
                </Text>}
              </View>
            </View>
          </Focusable>;
        })}
        <Focusable testID="watch-mini-guide-all" focusId="watch-mini-guide-all" accessibilityRole="button" disabled={!current() || snapshot.busy}
          onPress={() => {if (current() && !snapshot.busy) guide.fullGuide(snapshot);}}
          style={{minHeight: 44, flexDirection: 'row', alignItems: 'center', gap: 4, paddingHorizontal: tablet ? 24 : 10, paddingTop: tablet ? 14 : 2, paddingBottom: 12}}>
          <Text style={[nativeType({size: tablet ? 14.5 : 14, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('next.live.openFullGuide')}</Text>
          <Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
        </Focusable>
      </ScrollView>
    </FocusRegion>
  </View>;
}
