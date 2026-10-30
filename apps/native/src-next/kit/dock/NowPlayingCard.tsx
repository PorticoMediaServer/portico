import React, {useCallback, useEffect, useRef, useState} from 'react';
import {BackHandler, Text, View} from 'react-native';
import {useTVKeyEvent} from '../../platform/tv-keys';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {AmbientArt, Artwork} from '../artwork';
import {Button, Focusable} from '../controls';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';
import {focusTestID, type FocusSnapshot} from '../focus/registry';
import {FocusRegion, useOptionalFocusRegistry} from '../focus';
import {useMotion} from '../useMotion';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {registerOverlayKeys} from '../../platform/overlay-keys';
import {commandAllowed, dockCommand, playbackPosition, progressOf, seekBounds, seekTo} from './model';
import {alpha, DockFacts, dockStyles as s, dockType, LevelMeter} from './visual';
import type {DockAction, DockRenderProps} from './types';

export type NowPlayingCardProps = DockRenderProps & {
  expanded?: boolean;
  onExpandedChange: (expanded: boolean) => void;
  returnFocusRef?: React.RefObject<View | null>;
};

// Page-local glass surfaces, verbatim tv-player.html:75 and351; all brand colours use Theme.
const sourceSurfaces = {quiet: '#09131b', expanded: '#0f1d28'} as const;

export function NowPlayingCard({occupant, onAction, expanded = false, onExpandedChange, returnFocusRef, visible = true, style, testID}: NowPlayingCardProps) {
  const theme = useTheme(), motion = useMotion(), t = defaultI18n.t;
  const opener = useRef<View>(null), pause = useRef<View>(null);
  const registry = useOptionalFocusRegistry();
  const saved = useRef<FocusSnapshot | undefined>(undefined), release = useRef<(() => void) | undefined>(undefined);
  const open = () => {
    saved.current = registry?.capture();
    if (saved.current && !release.current) release.current = registry?.retain(saved.current);
    onExpandedChange(true);
  };
  const [seekFocused, setSeekFocused] = useState(false);
  const seekWidth = useRef(640);
  const fade = useSharedValue(1);
  const {presentation: p, playback} = occupant;
  const progress = progressOf(occupant);
  const wasExpanded = useRef(false), restoringOpener = useRef(false);
  const fold = useCallback(() => onExpandedChange(false), [onExpandedChange]);
  useEffect(() => {
    const leaving = wasExpanded.current && !expanded;
    wasExpanded.current = expanded;
    if (expanded) {
      saved.current ??= registry?.capture();
      if (saved.current && !release.current) release.current = registry?.retain(saved.current);
      return testID ? registry?.registerReturn(testID, saved.current) : undefined;
    }
    if (!leaving && !release.current && !saved.current) return;
    const frame = requestAnimationFrame(() => {
      release.current?.(); release.current = undefined;
      // Resolve after the quiet opener has reattached, never before its remount.
      if (returnFocusRef?.current) {
        restoringOpener.current = returnFocusRef.current === opener.current;
        returnFocusRef.current.requestTVFocus?.();
      } else {
        restoringOpener.current = saved.current?.childId === controlID(testID, 'open');
        if (!registry?.restore(saved.current) && opener.current) {restoringOpener.current = true; opener.current.requestTVFocus?.();}
      }
      saved.current = undefined;
    });
    return () => cancelAnimationFrame(frame);
  }, [expanded, registry, returnFocusRef, testID]);
  useEffect(() => () => {release.current?.(); release.current = undefined;}, []);
  useEffect(() => {
    if (!expanded) return;
    const back = BackHandler.addEventListener('hardwareBackPress', () => {fold(); return true;});
    const release = registerOverlayKeys(key => {if (key === 'escape' || key === 'menu') {fold(); return true;} return false;});
    return () => {back.remove(); release();};
  }, [expanded, fold]);
  useEffect(() => {
    fade.value = 0;
    fade.value = withTiming(1, motion.timing('standard'));
    if (!expanded) return;
    const timer = setTimeout(() => pause.current?.requestTVFocus?.(), motion.duration('standard'));
    return () => clearTimeout(timer);
  }, [expanded, motion, fade]);
  const animated = useAnimatedStyle(() => ({opacity: fade.value}));
  const act = useCallback((type: DockAction) => {if (commandAllowed(occupant, type)) onAction(dockCommand(occupant, type));}, [occupant, onAction]);
  const seek = useCallback((value: number) => {const command = seekTo(occupant, value); if (command) onAction(command);}, [occupant, onAction]);
  useTVKeyEvent(event => {
    if (!expanded || !visible || event.phase !== 'press') return;
    if (event.key === 'down') fold();
    else if (seekFocused && (event.key === 'left' || event.key === 'right')) seek(playbackPosition(occupant) + (event.key === 'left' ? -p.skipBackSeconds : p.skipForwardSeconds ?? p.skipBackSeconds));
  });
  // TV never has a minimized video surface or a phone's sender controls.
  if (occupant.mode !== 'music' && occupant.mode !== 'audiobook') return null;
  if (!expanded) return <Animated.View style={[animated, style]} testID={testID}>
    <Focusable testID={controlID(testID, 'open')} ref={opener} disabled={!visible} focusRadius={18} focusKind="card" focusTreatment="none" accessibilityRole="button" accessibilityLabel={t('player.nowPlayingTitle', {title: p.identity.title})}
      onFocus={() => {if (restoringOpener.current) restoringOpener.current = false; else open();}} onPress={open} style={[s.tvQuiet, {backgroundColor: alpha(sourceSurfaces.quiet, .8), borderWidth: 1, borderColor: alpha(theme.colors.text, .1)}]}>
      {glassRecipe().nativeBlur && <BlurView pointerEvents="none" style={s.fill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={sourceSurfaces.quiet}/>}
      <Artwork source={p.artwork} width={64} height={64} visible={visible} priority style={{borderRadius: 10, overflow: 'hidden'}}/>
      <View style={s.text}><Text numberOfLines={1} ellipsizeMode="tail" style={[dockType(theme, 24, 700, -.01), {color: theme.colors.text}]}>{p.identity.title}</Text>
        <View style={{marginTop: 3}}><DockFacts parts={p.identity.subtitle} gap={16} style={{...dockType(theme, 20, 600), color: theme.colors.textSecondary}}/></View>
      </View>
      <View style={[s.center, {width: 28}]}>{playback.playing ? <LevelMeter large/> : <Icon name="i-pause" size={22} color={theme.colors.textTertiary}/>}</View>
      {progress !== undefined && <View pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, bottom: 0, height: 4, backgroundColor: alpha(theme.colors.text, .14)}}><View style={{height: 4, width: `${progress * 100}%`, backgroundColor: playback.playing ? theme.colors.accentStrong : alpha(theme.colors.text, .5)}}/></View>}
    </Focusable>
  </Animated.View>;

  const playAction = p.finished ? 'replay' : playback.playing ? 'pause' : 'play';
  const playLabel = p.finished ? t('player.post.replay') : t(playback.playing ? 'action.pause' : 'action.play');
  const control = (type: DockAction, icon: IconName, label: string, primary = false) => <Focusable key={primary ? 'transport' : type} testID={controlID(testID, primary ? 'transport' : type)} ref={primary ? pause : undefined} hasTVPreferredFocus={primary}
    focusRadius={primary ? 44 : 36} accessibilityRole="button" accessibilityLabel={label} disabled={!visible || !commandAllowed(occupant, type)} onPress={() => act(type)} onLongPressAction={fold}
    style={[s.tvButton, primary && s.tvPrimary, {backgroundColor: primary ? theme.colors.text : alpha(theme.colors.text, .12), opacity: commandAllowed(occupant, type) ? 1 : .32}]}>
    <Icon name={icon} size={primary ? 40 : type === 'stop' ? 26 : 30} color={primary ? theme.colors.ink : theme.colors.text}/>
  </Focusable>;
  const bounds = seekBounds(occupant);
  return <Animated.View style={[s.tvFlyout, {backgroundColor: sourceSurfaces.expanded, borderWidth: 1, borderColor: alpha(theme.colors.text, .1), shadowColor: theme.colors.shadow, shadowOpacity: .9, shadowOffset: {width: 0, height: 40}, shadowRadius: 45}, animated, style]} testID={testID}>
    <AmbientArt source={p.ambient} palette={p.palette} treatment="cover" width={700} height={470} visible={visible} priority style={s.fill}/>
    {/* The TV dock is given a testID only while a playback fence exists; an empty region id throws on TV
        ("requires a stable caller identity") and took Home down. One card is mounted at a time, so a fixed id is unique. */}
    <FocusRegion id={testID || 'tv.now-playing-card'} entry={controlID(testID, 'transport')} entryPolicy="entry" hold enabled={visible} accessibilityViewIsModal>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 26}}>
        <Artwork source={p.artwork} width={168} height={168} visible={visible} priority style={{borderRadius: 16, overflow: 'hidden'}}/>
        <View style={{flex: 1, minWidth: 0}}>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginBottom: 8}}>{playback.playing ? <LevelMeter large/> : <Icon name="i-pause" size={20} color={theme.colors.accentStrong}/>}<Text style={[dockType(theme, 20, 700), {color: theme.colors.accentStrong}]}>{t(playback.playing ? 'player.miniPlaying' : 'player.miniPaused')}</Text></View>
          <Text numberOfLines={1} ellipsizeMode="tail" style={[dockType(theme, 52, 800, -.04, 52), {color: theme.colors.text}]}>{p.identity.title}</Text>
          <View style={{marginTop: 10}}><DockFacts parts={p.identity.subtitle} style={{...dockType(theme, 26, 700), color: theme.colors.textSecondary}}/></View>
          {p.album?.length ? <View style={{marginTop: 6}}><DockFacts parts={p.album} gap={16} style={{...dockType(theme, 22, 400), color: theme.colors.textTertiary}}/></View> : null}
        </View>
      </View>
      <Focusable testID={controlID(testID, 'seek')} accessibilityRole="adjustable" accessibilityLabel={t('player.position')} focusRadius={8} disabled={!visible || !p.capabilities.seek || !bounds}
        accessibilityValue={{min: bounds?.start, max: bounds?.end, now: playbackPosition(occupant), text: clockText(playbackPosition(occupant))}}
        accessibilityActions={[{name: 'increment'}, {name: 'decrement'}]} onAccessibilityAction={event => seek(playbackPosition(occupant) + (event.nativeEvent.actionName === 'decrement' ? -p.skipBackSeconds : p.skipForwardSeconds ?? p.skipBackSeconds))}
        onFocus={() => setSeekFocused(true)} onBlur={() => setSeekFocused(false)} onLongPressAction={fold} onLayout={event => {seekWidth.current = event.nativeEvent.layout.width;}}
        onPress={event => {if (bounds) seek(bounds.start + Math.max(0, Math.min(1, event.nativeEvent.locationX / seekWidth.current)) * (bounds.end - bounds.start));}}
        style={{height: 8, borderRadius: 8, marginTop: 28, backgroundColor: alpha(theme.colors.text, .2)}}>
        {progress !== undefined && <View pointerEvents="none" style={{height: 8, borderRadius: 8, width: `${progress * 100}%`, backgroundColor: theme.colors.text}}/>}
      </Focusable>
      <View style={{flexDirection: 'row', justifyContent: 'space-between', marginTop: 10}}>{[playbackPosition(occupant), p.durationSeconds].map((seconds, index) => <Text key={index} style={[dockType(theme, 20, 600), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{clockText(seconds)}</Text>)}</View>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 18, marginTop: 22}}>
        {control('previous', 't-prev', p.previousLabel ?? t('player.previous'))}
        {control(playAction, p.finished ? 'i-restart' : playback.playing ? 't-pause' : 'i-play', playLabel, true)}
        {control('next', 't-next', p.nextLabel ?? t('player.next'))}
        {control('close', 'i-stop', t('action.stop'))}
        <Button testID={controlID(testID, 'expand')} disabled={!visible || !commandAllowed(occupant, 'expand')} variant="glass" size="compact" icon="i-expand" iconSize={26} accessibilityLabel={t('player.queueNowPlaying')} onLongPressAction={fold} onPress={() => act('expand')}
          style={{height: 72, paddingHorizontal: 28, marginLeft: 'auto', gap: 14, borderRadius: 14}} textStyle={dockType(theme, 24, 700)}>{t('player.queueNowPlaying')}</Button>
      </View>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 22}}><Text style={[dockType(theme, 17, 800), {height: 36, minWidth: 44, paddingHorizontal: 10, paddingTop: 5, borderWidth: 1.5, borderColor: alpha(theme.colors.text, .3), borderRadius: 9, color: theme.colors.textSecondary}]}>{t('action.back')}</Text><Text style={[dockType(theme, 20, 600), {color: theme.colors.textTertiary}]}>{t('next.dock.native.foldAway')}</Text></View>
    </FocusRegion>
  </Animated.View>;
}

const controlID = (prefix: string | undefined, ...parts: readonly string[]) => prefix ? focusTestID(prefix, JSON.stringify(parts)) : undefined;
