import {serverNow} from '../../../../../packages/client-core/src/server-clock';
import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, StyleSheet, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {nowNext} from '../../../../../packages/client-core/src/guide/now-next';
import {playbackUserMessage} from '../../../../../packages/client-core/src/playback-messages';
import {FocusRegion} from '../focus';
import {useOverlayLifecycle} from '../overlays/OverlayHost';
import {MiniGuideAll, MiniGuideHead, MiniGuideRow} from '../live/MiniGuide';
import {useTheme} from '../theme';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import type {NativeWatchHostContext} from '../../screens/watch/WatchHost';
import type {NativeLiveGuideSnapshot, NativeTVLiveGuide} from '../../screens/watch/live-guide';

/** The same bounded, authorized rows as TV, in the phone options pane or the
 * tablet's docked pane, drawn by the kit's `MiniGuideHead`, `MiniGuideRow` and
 * `MiniGuideAll`. The host keeps the existing native picture playing. */
export function NativeHandheldMiniGuide({context, guide, snapshot}: {
  context: NativeWatchHostContext; guide: NativeTVLiveGuide; snapshot: NativeLiveGuideSnapshot;
}) {
  const theme = useTheme(), t = defaultI18n.t, first = useRef<View>(null);
  const [now, setNow] = useState(() => serverNow());
  // The guide's clock moves on every 20 s; each tick schedules the next, so nothing runs once the guide is gone.
  useEffect(() => {const timer = setTimeout(() => setNow(serverNow()), 20_000); return () => clearTimeout(timer);}, [now]);
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
      <MiniGuideHead docked={tablet} upright={upright} close={{label: t('action.close'), onPress: close, ref: first}}/>
      {!!snapshot.error && <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.textSecondary, paddingHorizontal: 18, paddingBottom: 10}]}>{playbackUserMessage(snapshot.error)}</Text>}
      {snapshot.phase === 'loading' && <Text style={[theme.type.caption, {color: theme.colors.textTertiary, paddingHorizontal: 18, paddingBottom: 10}]}>{t('next.browse.native.markAllPreparing')}</Text>}
      <ScrollView contentContainerStyle={{paddingTop: 4, paddingHorizontal: tablet ? 0 : 8, paddingBottom: tablet ? 24 : 8}}>
        {snapshot.rows.map(row => {
          const {channel} = row, programme = nowNext(row.programmes, now), next = programme.next;
          const start = programme.now?.start, end = programme.now?.end;
          const progress = typeof start === 'number' && Number.isFinite(start) && typeof end === 'number' && Number.isFinite(end) && end > start
            ? Math.max(0, Math.min(1, (now - start) / (end - start))) : undefined;
          const id = `${channel.kind}/${channel.sourceId ?? ''}/${channel.id}`;
          return <MiniGuideRow key={id} id={id} channel={channel} logo={channel.logoUrl ? context.runtime.artwork(channel.logoUrl) : undefined} docked={tablet}
            title={programme.now?.title} progress={progress} next={next && {title: next.title, at: time(next.start)}} watching={row.step === 0}
            disabled={!current() || snapshot.busy || !channel.tuneAvailable} busy={snapshot.busy}
            onPress={() => {if (current()) void guide.tune(row, snapshot, context.runtime.reportError);}}/>;
        })}
        <MiniGuideAll docked={tablet} disabled={!current() || snapshot.busy} onPress={() => {if (current() && !snapshot.busy) guide.fullGuide(snapshot);}}/>
      </ScrollView>
    </FocusRegion>
  </View>;
}
