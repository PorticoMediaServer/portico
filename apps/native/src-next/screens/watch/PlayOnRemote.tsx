import React, {useEffect, useRef, useState} from 'react';
import {Modal, ScrollView, Text, View, useWindowDimensions} from 'react-native';
import Animated, {useAnimatedStyle} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {AmbientArt, Artwork, type AmbientArtSource, type ArtworkSource} from '../../kit/artwork';
import {Button, Focusable} from '../../kit/controls';
import {Icon, type IconName} from '../../kit/Icon';
import {BottomSheet} from '../../kit/overlays';
import {useOverlayLifecycle} from '../../kit/overlays/OverlayHost';
import {useSurfaceMotion} from '../../kit/overlays/surface';
import {nativeType, useTheme} from '../../kit/theme';
import {BlurView} from '../../platform/native-blur';
import {isTablet} from '../../platform/device-platform';
import type {PlayOnRemote} from '../../platform/play-on/controller';

export type NativePlayOnRemoteProps = Readonly<{
  open: boolean;
  remote: PlayOnRemote;
  artwork?: ArtworkSource;
  ambient?: AmbientArtSource;
  skipBackSeconds: number;
  skipForwardSeconds: number;
  close: () => void;
  pause: () => void;
  resume: () => void;
  seek: (position: number) => void;
  skip: (seconds: number) => void;
  startOver: () => void;
  stop: () => void;
  audio?: () => void;
  subtitles?: () => void;
  panel?: React.ReactNode;
}>;

export function NativePlayOnRemote(props: NativePlayOnRemoteProps) {
  return isTablet ? <NativePlayOnTabletRemote {...props}/> : <NativePlayOnPhoneRemote {...props}/>;
}

/** The tablet keeps its existing compact remote; SUP-011 changes the phone. */
export function NativePlayOnTabletRemote(props: NativePlayOnRemoteProps) {
  const {remote} = props, theme = useTheme(), t = defaultI18n.t;
  const control = (id: string, icon: IconName, label: string, onPress: () => void, primary = false, disabled = false) =>
    <Focusable key={id} focusId={`remote-${id}`} testID={`remote-${id}`} accessibilityRole="button" accessibilityLabel={label} disabled={disabled} onPress={onPress}
      style={{height: primary ? 64 : 52, width: primary ? 64 : 52, borderRadius: primary ? 32 : 26, alignItems: 'center', justifyContent: 'center',
        backgroundColor: primary ? theme.colors.text : 'rgba(237,245,250,.14)'}}>
      <Icon name={icon} size={primary ? 30 : 24} color={primary ? theme.colors.ink : theme.colors.text}/>
    </Focusable>;
  return <BottomSheet testID="play-on-remote" open={props.open} onOpenChange={open => {if (!open) props.close();}} title={t('player.playOn')}
    showClose={false} detents={[420]} contentStyle={{paddingHorizontal: 22, paddingBottom: 28}}>
    <View style={{flexDirection: 'row', gap: 16, alignItems: 'center'}}>
      <View style={{width: 92, height: 92, borderRadius: 12, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
        <Artwork source={props.artwork} width={92} height={92} visible={props.open}/>
      </View>
      <View style={{flex: 1, minWidth: 0}}>
        <Text numberOfLines={1} style={[nativeType({size: 20, weight: 800, tracking: -.02, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{remote.title ?? t('player.queueNowPlaying')}</Text>
        <Text numberOfLines={1} style={[theme.type.body, {marginTop: 2, color: theme.colors.textSecondary}]}>{remote.device}</Text>
        {remote.positionKnown && <Text style={[theme.type.bodyStrong, {marginTop: 6, color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{[clockText(remote.positionSeconds),
          remote.durationSeconds > 0 ? clockText(remote.durationSeconds) : undefined].filter(Boolean).join(' / ')}</Text>}
      </View>
      <Button focusId="remote-close" testID="remote-close" variant="text" iconOnly icon="i-close" accessibilityLabel={t('action.close')} onPress={props.close}/>
    </View>
    <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 18, marginTop: 26}}>
      {control('back', 'i-rew', t('player.skipBackSeconds', {seconds: props.skipBackSeconds}), () => props.skip(-props.skipBackSeconds), false, !remote.allows.seek || remote.stopped)}
      {control('restart', 'i-restart', t('next.controls.startOver'), props.startOver, false, !remote.allows.seek || remote.stopped)}
      {remote.playing ? control('pause', 'i-pause', t('action.pause'), props.pause, true, !remote.allows.pause || remote.stopped)
        : control('play', 'i-play', t('action.play'), props.resume, true, !remote.allows.resume || remote.stopped)}
      {control('forward', 'i-ffw', t('player.skipForwardSeconds', {seconds: props.skipForwardSeconds}), () => props.skip(props.skipForwardSeconds), false, !remote.allows.seek || remote.stopped)}
      {control('stop', 'i-stop', t('next.dock.native.stopRemote', {device: remote.device}), props.stop, false, !remote.allows.stop || remote.stopped)}
    </View>
    <View style={{marginTop: 22, alignItems: 'center'}}><Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('playOn.volumeOnTV')}</Text></View>
    {props.panel}
  </BottomSheet>;
}

/** phone-player 7b. The receiver owns the clock and the allowed controls. There
 * is no local player, queue operation, volume slider or inferred track choice. */
export function NativePlayOnPhoneRemote(props: NativePlayOnRemoteProps) {
  const {remote, open} = props, theme = useTheme(), t = defaultI18n.t;
  const viewport = useWindowDimensions(), insets = useSafeAreaInsets();
  const closeButton = useRef<View>(null);
  const animation = useSurfaceMotion(open, 'dialog');
  const fade = useAnimatedStyle(() => ({opacity: animation.motion.reduce ? animation.progress.value : 1}));
  useOverlayLifecycle(animation.present, true, props.close, closeButton, undefined, animation.motion.reduce ? 160 : 320, 'play-on-remote');
  const width = Math.max(0, Math.min(480, viewport.width - 48));
  const entry = remote.entry, episode = entry?.kind === 'episode' ? entry : undefined;
  const title = episode?.show?.name ?? remote.title ?? t('player.queueNowPlaying');
  const code = episode?.seasonNumber !== undefined && episode.episodeNumber !== undefined
    ? t('next.kit.episodeCode', {season: episode.seasonNumber, episode: episode.episodeNumber}) : undefined;
  const subtitle = [code, episode?.show?.name ? remote.title : undefined].filter(Boolean).join('  ');
  const seekable = remote.positionKnown && remote.allows.seek && !remote.stopped;
  const skip = (direction: 'back' | 'forward') => {
    const seconds = direction === 'back' ? props.skipBackSeconds : props.skipForwardSeconds;
    return seekable ? <Focusable testID={`remote-${direction}`} accessibilityRole="button"
      accessibilityLabel={t(direction === 'back' ? 'player.skipBackSeconds' : 'player.skipForwardSeconds', {seconds})}
      onPress={() => props.skip(direction === 'back' ? -seconds : seconds)} style={{width: 48, height: 48, alignItems: 'center', justifyContent: 'center'}}>
      <Icon name={direction === 'back' ? 'i-rew' : 'i-ffw'} size={38} color={theme.colors.text}/>
      <Text accessible={false} style={[nativeType({size: 10.5, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale),
        {position: 'absolute', color: theme.colors.text, marginTop: 2}]}>{seconds}</Text>
    </Focusable> : null;
  };
  const action = (id: string, icon: IconName, label: string, onPress: () => void) => <Focusable key={id} testID={`remote-${id}`}
    accessibilityRole="button" accessibilityLabel={label} onPress={onPress} style={{flex: 1, minHeight: 54, paddingHorizontal: 6, paddingVertical: 8,
      borderRadius: 12, overflow: 'hidden', gap: 2, alignItems: 'center', justifyContent: 'center', backgroundColor: 'rgba(237,245,250,.12)'}}>
    <BlurView pointerEvents="none" style={{position: 'absolute', inset: 0}} blurType="dark" blurAmount={16} reducedTransparencyFallbackColor={theme.colors.raisedBright}/>
    <Icon name={icon} size={18} color={theme.colors.text}/>
    <Text style={[nativeType({size: 11.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary, textAlign: 'center'}]}>{label}</Text>
  </Focusable>;
  return <Modal visible={animation.motion.reduce ? animation.present : open} transparent={animation.motion.reduce}
    animationType={animation.motion.reduce ? 'none' : 'slide'} presentationStyle={animation.motion.reduce ? 'overFullScreen' : 'fullScreen'}
    statusBarTranslucent navigationBarTranslucent supportedOrientations={['portrait', 'landscape']} onRequestClose={props.close}>
    <Animated.View testID="play-on-remote" accessibilityViewIsModal onAccessibilityEscape={props.close}
      pointerEvents={open ? 'auto' : 'none'} style={[{flex: 1, backgroundColor: theme.colors.ink}, fade]}>
      <AmbientArt source={props.ambient} treatment="cover" width={viewport.width} height={viewport.height} visible={open}/>
      <ScrollView contentContainerStyle={{flexGrow: 1, alignItems: 'center', paddingTop: insets.top, paddingBottom: Math.max(40, insets.bottom), paddingHorizontal: 24}}>
        <View style={{width: '100%', maxWidth: 500, minHeight: 48, flexDirection: 'row', alignItems: 'center'}}>
          <Button ref={closeButton} testID="remote-close" variant="text" iconOnly icon="i-down" accessibilityLabel={t('action.close')} onPress={props.close}/>
          <Text style={[nativeType({size: 14.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale),
            {flex: 1, textAlign: 'center', color: theme.colors.text}]}>{t('playOn.playingOn', {device: remote.device})}</Text>
          <View style={{width: 44}}/>
        </View>
        <View style={{width, marginTop: 30, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
          <Artwork source={props.artwork} width={width} height={width * 9 / 16} visible={open}/>
        </View>
        <View style={{width, marginTop: 34}}>
          <Text style={[nativeType({size: 23, weight: 800, tracking: -.025, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{title}</Text>
          {!!subtitle && <Text style={[nativeType({size: 16, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {marginTop: 2, color: theme.colors.textSecondary}]}>{subtitle}</Text>}
        </View>
        <View style={{width, marginTop: 24}}>
          <NativeRemoteSeek key={`${remote.id}:${remote.itemId ?? ''}`} remote={remote} enabled={seekable && open}
            seek={props.seek} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
        </View>
        <View style={{width, flexDirection: 'row', justifyContent: 'center', alignItems: 'center', gap: 38, marginTop: 26}}>
          {skip('back')}
          {!remote.stopped && (remote.playing ? remote.allows.pause : remote.allows.resume) && <Focusable testID={remote.playing ? 'remote-pause' : 'remote-play'}
            accessibilityRole="button" accessibilityLabel={t(remote.playing ? 'action.pause' : 'action.play')} onPress={remote.playing ? props.pause : props.resume}
            style={{width: 76, height: 76, borderRadius: 38, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.text}}>
            <Icon name={remote.playing ? 'i-pause' : 'i-play'} size={34} color={theme.colors.ink}/>
          </Focusable>}
          {skip('forward')}
        </View>
        {!remote.stopped && remote.positionKnown && remote.next?.available && <View testID="remote-next-peek" style={{marginTop: 30, flexDirection: 'row', flexWrap: 'wrap',
          justifyContent: 'center', gap: 8, paddingVertical: 8, paddingHorizontal: 14, borderRadius: 18, backgroundColor: 'rgba(7,16,24,.35)'}}>
          <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('player.post.upNext')}</Text>
          <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{remote.next.title}</Text>
        </View>}
        {remote.stopped && remote.positionKnown && <Text style={[theme.type.caption, {marginTop: 30, color: theme.colors.textSecondary}]}>{t('next.dock.native.stoppedAt', {position: clockText(remote.positionSeconds) ?? ''})}</Text>}
        <View style={{flex: 1, minHeight: 30}}/>
        {!remote.stopped && <View style={{width, flexDirection: 'row', gap: 8}}>
          {props.subtitles && action('subtitles', 'i-cc', t('player.opt.subtitles'), props.subtitles)}
          {props.audio && action('audio', 'i-audio', t('title.audio'), props.audio)}
          {seekable && action('restart', 'i-restart', t('next.controls.startOver'), props.startOver)}
          {remote.allows.stop && action('stop', 'i-stop', t('action.stop'), props.stop)}
        </View>}
        <Text style={[nativeType({size: 12.5, weight: 400, tracking: 0, lineHeight: 18.125}, theme.textScale),
          {width, marginTop: 14, color: theme.colors.textTertiary, textAlign: 'center'}]}>{t('playOn.volumeOnTV')}</Text>
      </ScrollView>
      {props.panel}
    </Animated.View>
  </Modal>;
}

/** Commit once on release, to the handler captured at touch-down. A receiver
 * replacement unmounts this item-keyed scrubber and invalidates that gesture. */
export function NativeRemoteSeek({remote, enabled, seek, skipBackSeconds, skipForwardSeconds}: Pick<NativePlayOnRemoteProps, 'remote' | 'seek' | 'skipBackSeconds' | 'skipForwardSeconds'> & {enabled: boolean}) {
  const theme = useTheme(), t = defaultI18n.t;
  const width = useRef(0), mounted = useRef(true), drag = useRef<{duration: number; width: number; commit: (position: number) => void} | undefined>(undefined);
  const [preview, setPreview] = useState<number>();
  useEffect(() => {mounted.current = true; return () => {mounted.current = false; drag.current = undefined;};}, []);
  useEffect(() => {if (!enabled) {drag.current = undefined; setPreview(undefined);}}, [enabled]);
  const canSeek = enabled && remote.durationSeconds > 0;
  const position = preview ?? remote.positionSeconds;
  const fraction = remote.positionKnown && remote.durationSeconds > 0 ? Math.max(0, Math.min(1, position / remote.durationSeconds)) : 0;
  const point = (x: number, span: number, duration: number) => Math.max(0, Math.min(1, x / span)) * duration;
  const cancel = () => {drag.current = undefined; setPreview(undefined);};
  return <View>
    <View testID="remote-seek" accessible accessibilityRole="adjustable" accessibilityLabel={t('player.position')}
      accessibilityState={{disabled: !canSeek}} accessibilityValue={remote.positionKnown ? {min: 0, max: remote.durationSeconds || undefined, now: position,
        text: clockText(position)} : undefined} accessibilityActions={canSeek ? [{name: 'increment'}, {name: 'decrement'}] : []}
      onAccessibilityAction={event => {if (!canSeek) return; const name = event.nativeEvent.actionName;
        if (name === 'increment' || name === 'decrement') seek(Math.max(0, Math.min(remote.durationSeconds, remote.positionSeconds + (name === 'increment' ? skipForwardSeconds : -skipBackSeconds))));}}
      onLayout={event => {width.current = event.nativeEvent.layout.width;}}
      onStartShouldSetResponder={() => canSeek && width.current > 0}
      onResponderGrant={event => {if (!canSeek || width.current <= 0) return; drag.current = {duration: remote.durationSeconds, width: width.current, commit: seek};
        setPreview(point(event.nativeEvent.locationX, width.current, remote.durationSeconds));}}
      onResponderMove={event => {const start = drag.current; if (start) setPreview(point(event.nativeEvent.locationX, start.width, start.duration));}}
      onResponderRelease={event => {const start = drag.current; cancel(); if (start && mounted.current && canSeek) start.commit(point(event.nativeEvent.locationX, start.width, start.duration));}}
      onResponderTerminate={cancel} onResponderTerminationRequest={() => true} style={{minHeight: 44, justifyContent: 'center'}}>
      <View pointerEvents="none" style={{height: 5, borderRadius: 3, backgroundColor: 'rgba(237,245,250,.22)'}}>
        <View style={{height: 5, width: `${fraction * 100}%`, borderRadius: 3, backgroundColor: theme.colors.text}}/>
        {canSeek && <View style={{position: 'absolute', left: `${fraction * 100}%`, marginLeft: -6.5, top: -4, width: 13, height: 13, borderRadius: 6.5, backgroundColor: theme.colors.text}}/>}
      </View>
    </View>
    {remote.positionKnown && <View style={{flexDirection: 'row', justifyContent: 'space-between'}}>
      <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(position)}</Text>
      {remote.durationSeconds > 0 && <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{`-${clockText(Math.max(0, remote.durationSeconds - position))}`}</Text>}
    </View>}
  </View>;
}
