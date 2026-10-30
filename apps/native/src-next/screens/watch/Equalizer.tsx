import React, {useCallback, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {findNodeHandle, Platform, ScrollView, Text, View} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import Svg, {Path} from 'react-native-svg';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {nativeEqualizer, type DeviceEqualizer, type DeviceEqualizerStore} from '../../platform/playback/equalizer';
import {Button, Focusable, Switch} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {Icon} from '../../kit/Icon';
import {nativeType, useTheme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';
import {useTVKeyEvent} from '../../platform/tv-keys';
import {useDestinationEntryFocus} from '../../kit/focus/useDestinationEntryFocus';
import {nativeEqualizerControls, nativeEqualizerFlat, nativeEqualizerGain, type NativeEqualizerTarget} from './equalizer-controls';
import {equalizerDragFraction, equalizerDragGain, equalizerFillTransform, equalizerFractionFromGain, equalizerThumbTranslate, equalizerTooltipTranslate} from './equalizer-gesture-paint';

export type NativeEqualizerCopy = Readonly<{
  title: string; back: string; thisDevice: string; bands: string; preamp: string; reset: string; musicAndAudiobooks: string;
  notBitPerfect: string;
  gain: (value: number, withUnit: boolean) => string;
  frequency: (index: number, accessible: boolean) => string;
}>;
/** A genuine filter projection supplied by the installed producer. A saved
 * switch/gain record cannot manufacture a curve or an applied-effect claim. */
export type NativeEqualizerResponse = Readonly<{value: DeviceEqualizer; points: readonly Readonly<{fraction: number; gainDb: number}>[]}>;
export type NativeWatchEqualizerProps = Readonly<{
  onBack: () => void;
  copy: NativeEqualizerCopy;
  reportError: (error: unknown) => void;
  /** Settings and the player shortcut bind this same app/device record. */
  store?: DeviceEqualizerStore;
  response?: NativeEqualizerResponse;
  focusScope?: string;
  active?: boolean;
}>;

/** music-plus13b/13t content. The genuine Settings route owns its enclosing
 * phone sheet / TV1920 canvas, rail and retained music card. No route/store is
 * registered by importing or rendering this editor. */
export function NativeWatchEqualizer({onBack, copy, reportError, store = nativeEqualizer, response, focusScope, active = true}: NativeWatchEqualizerProps) {
  const theme = useTheme(), snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot);
  const instance = useId(), scope = focusScope ?? `equalizer.${instance}`;
  const actionsRegion = `${scope}.actions`, bandsRegion = `${scope}.bands`;
  const commands = useMemo(() => nativeEqualizerControls(store), [store]);
  const focused = useRef<NativeEqualizerTarget | undefined>(undefined);
  const [graphWidth, setGraphWidth] = useState(0);
  useEffect(() => {if (store.getSnapshot().supported && !store.getSnapshot().ready) void store.refresh().catch(reportError);}, [store, reportError]);
  useTVKeyEvent(event => {
    if (!theme.isTV || !active || focused.current === undefined || event.phase === 'long-end' || !['up', 'down'].includes(event.key)) return;
    if (store.getSnapshot().value?.on) void commands.nudge(focused.current, event.key === 'up' ? 1 : -1).catch(reportError);
  });
  // Unsupported/missing producers never acquire a fabricated flat record or
  // an active editor. The route can keep its existing approved loading state.
  if (!snapshot.supported || !snapshot.ready || !snapshot.value) return null;
  const value = snapshot.value, enabled = value.on, tv = theme.isTV, height = tv ? 470 : 300;
  const execute = (promise: Promise<unknown>) => {void promise.catch(reportError);};
  const responsePath = response?.value === value && graphWidth > 0 && response.points.length > 1
    && response.points.every(point => Number.isFinite(point.fraction) && point.fraction >= 0 && point.fraction <= 1 && Number.isFinite(point.gainDb))
    ? response.points.map((point, index) => `${index ? 'L' : 'M'}${point.fraction * graphWidth},${(12 - Math.max(-12, Math.min(12, point.gainDb))) / 24 * height}`).join(' ') : undefined;
  const toggleLabel = <View style={{flex: 1, gap: tv ? 3 : 4}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
      <Text style={[nativeType({size: tv ? 24 : 15.5, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{copy.title}</Text>
      {!tv && <View style={{height: 20, paddingHorizontal: 7, borderWidth: 1, borderColor: '#edf5fa38', borderRadius: 6, justifyContent: 'center'}}>
        <Text style={[nativeType({size: 11.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{copy.thisDevice}</Text>
      </View>}
    </View>
    {(tv || Platform.OS === 'android') && <Text style={[nativeType({size: tv ? 20 : 12.5, weight: 400, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{tv ? copy.thisDevice : copy.notBitPerfect}</Text>}
  </View>;
  const toggleStyle = {backgroundColor: theme.colors.raised, borderRadius: tv ? 18 : 14, paddingHorizontal: tv ? 24 : 14,
    paddingVertical: 12, minHeight: tv ? 80 : undefined, flexDirection: 'row' as const, alignItems: 'center' as const, gap: 20};
  const toggle = tv ? <Focusable focusId="equalizer-enabled" accessibilityRole="switch" accessibilityLabel={copy.title}
    accessibilityState={{checked: enabled}} disabled={snapshot.pending} focusKind="row" focusRadius={18}
    onPress={() => execute(commands.enable(!enabled))} style={toggleStyle}>
    {toggleLabel}<View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants"><Switch checked={enabled} focusable={false}/></View>
  </Focusable> : <View style={toggleStyle}>{toggleLabel}<Switch checked={enabled} accessibilityLabel={copy.title} disabled={snapshot.pending} onCheckedChange={on => execute(commands.enable(on))}/></View>;
  const reset = <Button focusId="equalizer-reset" variant="glass" disabled={!enabled || snapshot.pending || nativeEqualizerFlat(value)}
    style={tv ? {alignSelf: 'flex-start', marginTop: 26, height: 64, paddingHorizontal: 30} : {marginHorizontal: 16, marginTop: 14, alignSelf: 'flex-start'}} onPress={() => execute(commands.reset())}>{copy.reset}</Button>;
  const band = (target: NativeEqualizerTarget, gain: number, index?: number) => <EqualizerBand key={target} target={target} value={gain} height={height} disabled={!enabled}
    horizontal={!tv && target === 'preamp'} label={target === 'preamp' ? copy.preamp : copy.frequency(index!, false)}
    accessibilityLabel={target === 'preamp' ? copy.preamp : copy.frequency(index!, true)} copy={copy}
    onFocus={target => {focused.current = target;}} onBlur={target => {if (focused.current === target) focused.current = undefined;}}
    onChange={asked => execute(commands.gain(target, asked))} onNudge={direction => execute(commands.nudge(target, direction))}/>;
  const graph = <View onLayout={event => setGraphWidth(event.nativeEvent.layout.width)} style={{flexDirection: 'row', paddingHorizontal: tv ? 0 : 6, paddingTop: tv ? 0 : 12, paddingBottom: tv ? 0 : 4}}>
    {!tv && <View pointerEvents="none" style={{position: 'absolute', left: 6, right: 6, top: 32 + height / 2, borderTopWidth: 1, borderStyle: 'dashed', borderColor: '#edf5fa33'}}/>}
    {responsePath && <Svg pointerEvents="none" width={graphWidth} height={height} style={{position: 'absolute', left: 0, top: tv ? 56 : 32}}><Path d={responsePath} stroke={theme.colors.accentStrong} strokeOpacity={.6} strokeWidth={tv ? 3 : 1.5} fill="none"/></Svg>}
    {value.gains.map((gain, index) => band(index, gain, index))}
  </View>;
  if (tv) return <FocusProvider><FocusRegion id={`${scope}.root`} hold enabled={active} accessibilityViewIsModal testID="watch-equalizer" style={{width: 1920, height: 1080}}>
    <EqualizerEntry active={active} region={actionsRegion}/>
    <View style={{position: 'absolute', left: 200, top: 56, width: 480}}>
      <Text style={[nativeType({size: 22, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, marginLeft: 20, marginBottom: 4}]}>{copy.musicAndAudiobooks}</Text>
      <Text accessibilityRole="header" style={[nativeType({size: 44, weight: 800, tracking: -.03, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text, marginLeft: 20, marginBottom: 30}]}>{copy.title}</Text>
      <FocusRegion id={actionsRegion} enabled={active} entry="equalizer-enabled" orientation="vertical" edges={{right: bandsRegion}}>{toggle}{reset}</FocusRegion>
    </View>
    <FocusRegion id={bandsRegion} enabled={active} entry="equalizer-preamp" orientation="horizontal" edges={{left: actionsRegion}} style={{position: 'absolute', left: 760, right: 96, top: 176, paddingTop: 30, paddingBottom: 34, paddingLeft: 20, paddingRight: 36, borderRadius: 18, backgroundColor: theme.colors.raised, flexDirection: 'row'}}>
      <View pointerEvents="none" style={{position: 'absolute', left: 96, right: 36, top: 86 + height / 2, borderTopWidth: 1, borderStyle: 'dashed', borderColor: '#edf5fa33'}}/>
      <View pointerEvents="none" style={{width: 76, height, marginTop: 56}}>{[12, 6, 0, -6, -12].map(gain => <Text key={gain} style={[nativeType({size: 18, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {position: 'absolute', top: (12 - gain) / 24 * height - 12, right: 16, color: theme.colors.textTertiary}]}>{copy.gain(gain, gain === 0)}</Text>)}</View>
      <View style={{width: 96}}>{band('preamp', value.preamp)}</View><View pointerEvents="none" style={{width: 28, height, marginTop: 56}}><View style={{position: 'absolute', left: 13, top: 0, width: 2, height, backgroundColor: theme.colors.line}}/></View>
      <View style={{flex: 1}}>{graph}</View>
    </FocusRegion>
  </FocusRegion></FocusProvider>;
  return <View testID="watch-equalizer" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <View style={{height: 50, justifyContent: 'center', alignItems: 'center'}}><Button focusId="equalizer-back" variant="text" icon="i-left" onPress={onBack} style={{position: 'absolute', left: 6}}>{copy.back}</Button>
      <Text accessibilityRole="header" style={[nativeType({size: 18, weight: 800, tracking: -.015, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{copy.title}</Text></View>
    <ScrollView contentContainerStyle={{paddingTop: 6, paddingBottom: 32}}>
      <View style={{marginHorizontal: 16}}>{toggle}</View>
      <Text style={[nativeType({size: 13, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, marginTop: 18, marginBottom: 8, marginHorizontal: 20}]}>{copy.bands}</Text>
      <View style={{marginHorizontal: 16, borderRadius: 14, backgroundColor: theme.colors.raised}}>{band('preamp', value.preamp)}{graph}</View>{reset}
    </ScrollView>
  </View>;
}

function EqualizerEntry({active, region}: {active: boolean; region: string}) {
  useDestinationEntryFocus({visible: active, intent: 'enter', revision: 0}, {regionId: region, childId: 'equalizer-enabled'});
  return null;
}

function EqualizerBand({target, value, height, disabled, horizontal = false, label, accessibilityLabel, copy, onChange, onNudge, onFocus, onBlur}: {
  target: NativeEqualizerTarget; value: number; height: number; disabled: boolean; horizontal?: boolean; label: string; accessibilityLabel: string;
  copy: NativeEqualizerCopy; onChange: (value: number) => void; onNudge: (direction: -1 | 1) => void; onFocus: (target: NativeEqualizerTarget) => void; onBlur: (target: NativeEqualizerTarget) => void;
}) {
  const theme = useTheme(), motion = useMotion(), last = useRef(value), dragging = useRef(false);
  const [draft, setDraft] = useState<number>(), [draggingNow, setDraggingNow] = useState(false), [focused, setFocused] = useState(false), [nativeHandle, setNativeHandle] = useState<number>();
  const scale = useSharedValue(1);
  // The drawn position lives on the UI thread: paintFraction is the current
  // gain's fraction, dragFraction only exists while a finger is down, so a
  // release or a cancellation falls straight back to the real value.
  const travel = useSharedValue(0), paintFraction = useSharedValue(equalizerFractionFromGain(value)), dragFraction = useSharedValue<number | null>(null), dragOrigin = useSharedValue(0);
  const focusFace = useAnimatedStyle(() => ({transform: [{scale: scale.value}]}));
  const attach = useCallback((node: View | null) => {if (node) setNativeHandle(findNodeHandle(node) ?? undefined);}, []);
  useEffect(() => {scale.value = withTiming(theme.isTV && focused && motion.transforms ? 1.06 : 1, motion.timing('quick')); return () => cancelAnimation(scale);}, [theme.isTV, focused, motion, scale]);
  useEffect(() => {if (!dragging.current) setDraft(undefined);}, [value]);
  // An external gain only retargets the preview when no finger is down.
  useEffect(() => {if (dragFraction.value === null) paintFraction.value = equalizerFractionFromGain(draft ?? value);}, [draft, value, paintFraction, dragFraction]);
  useEffect(() => () => {dragFraction.value = null;}, [dragFraction]);
  const shown = draft ?? value, fraction = (12 - shown) / 24;
  const commit = (asked: number) => {const gain = nativeEqualizerGain(asked); if (gain !== undefined && gain !== last.current && !disabled) {last.current = gain; setDraft(gain); onChange(gain);}};
  const markDrag = (active: boolean) => {dragging.current = active; setDraggingNow(active); if (active) last.current = value; else setDraft(undefined);};
  // The paint runs on the UI thread from the pointer displacement; the original
  // commit callback still crosses once per update with its own validation and
  // ordering, exactly as the previous runOnJS(true) gesture did.
  // The origin is frozen once at begin; every update derives from it plus the
  // TOTAL measured displacement, so a repeated event cannot move the preview
  // twice. Unmeasured travel paints and commits nothing.
  const pan = Gesture.Pan().enabled(!disabled && !theme.isTV).minDistance(0)
    .onBegin(() => {dragOrigin.value = paintFraction.value; dragFraction.value = paintFraction.value; runOnJS(markDrag)(true);})
    .onUpdate(event => {
      const next = equalizerDragFraction(dragOrigin.value, horizontal ? event.translationX : event.translationY, travel.value, horizontal);
      if (next === undefined) return;
      const gain = equalizerDragGain(dragOrigin.value, horizontal ? event.translationX : event.translationY, travel.value, horizontal);
      dragFraction.value = next;
      if (gain !== undefined) runOnJS(commit)(gain);
    })
    .onFinalize(() => {dragFraction.value = null; runOnJS(markDrag)(false);});
  // Translation comes before scale so the offset stays in track pixels and the
  // fill keeps its drawn edge on both axes.
  const fillPaint = useAnimatedStyle(() => {
    const t = equalizerFillTransform(dragFraction.value ?? paintFraction.value, travel.value, horizontal);
    return {transform: horizontal ? [{translateX: t.offset}, {scaleX: t.length}] : [{translateY: t.offset}, {scaleY: t.length}]};
  });
  const thumbPaint = useAnimatedStyle(() => {
    const offset = equalizerThumbTranslate(dragFraction.value ?? paintFraction.value, travel.value, horizontal);
    return {transform: horizontal ? [{translateX: offset}] : [{translateY: offset}]};
  });
  const tooltipPaint = useAnimatedStyle(() => ({transform: [{translateY: equalizerTooltipTranslate(dragFraction.value ?? paintFraction.value, travel.value)}]}));
  const knob = horizontal ? 18 : theme.isTV ? focused ? 34 : 28 : draggingNow ? 28 : 16;
  return <GestureDetector gesture={pan}><Focusable ref={attach} focusId={`equalizer-${target}`} testID={`equalizer-${target}`} accessibilityRole="adjustable" accessibilityLabel={accessibilityLabel}
    accessibilityValue={{min: -12, max: 12, now: shown, text: copy.gain(shown, true)}} disabled={disabled} focusable={!disabled}
    nextFocusUp={theme.isTV ? nativeHandle : undefined} nextFocusDown={theme.isTV ? nativeHandle : undefined}
    onFocus={() => {setFocused(true); onFocus(target);}} onBlur={() => {setFocused(false); onBlur(target);}} focusTreatment={theme.isTV ? 'none' : 'default'}
    accessibilityActions={[{name: 'increment'}, {name: 'decrement'}]} onAccessibilityAction={event => {if (!disabled) {if (event.nativeEvent.actionName === 'increment') onNudge(1); else if (event.nativeEvent.actionName === 'decrement') onNudge(-1);}}}
    style={horizontal ? {alignSelf: 'stretch', opacity: disabled ? .35 : 1} : {flex: 1, minWidth: 0, opacity: disabled ? .35 : 1}}>
    <Animated.View style={[horizontal ? {flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 14, paddingVertical: 12} : {alignItems: 'center', width: '100%'}, focusFace]}>
      {theme.isTV && focused && <View pointerEvents="none" style={{position: 'absolute', left: -8, right: -8, top: -24, bottom: -22, borderRadius: 26, borderWidth: 4, borderColor: theme.colors.focus, shadowColor: theme.colors.accentStrong, shadowOffset: {width: 0, height: 26}, shadowOpacity: .5, shadowRadius: 32}}>
        <View style={{position: 'absolute', inset: 0, borderRadius: 22, borderWidth: 4, borderColor: theme.colors.ink, backgroundColor: '#1d384899'}}/>
      </View>}
      {horizontal && <Text style={[nativeType({size: 15.5, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{label}</Text>}
      {!horizontal && <Text style={[nativeType({size: theme.isTV ? focused ? 22 : 20 : 10.5, weight: focused ? 800 : 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {height: theme.isTV ? 34 : 16, marginBottom: theme.isTV ? 22 : 4, color: focused || draggingNow || target === 'preamp' ? theme.colors.text : theme.colors.textSecondary}]}>{copy.gain(shown, false)}</Text>}
      <View onLayout={event => {travel.value = horizontal ? event.nativeEvent.layout.width : height;}} style={horizontal ? {flex: 1, height: 4, borderRadius: 2, backgroundColor: '#edf5fa24'} : {width: theme.isTV ? 6 : 4, height, borderRadius: theme.isTV ? 3 : 2, backgroundColor: '#edf5fa24'}}>
        {theme.isTV
          ? <View style={horizontal ? {position: 'absolute', left: `${Math.min(.5, 1 - fraction) * 100}%`, width: `${Math.abs(.5 - fraction) * 100}%`, height: 4, borderRadius: 2, backgroundColor: theme.colors.accentStrong}
              : {position: 'absolute', left: 0, right: 0, top: Math.min(.5, fraction) * height, height: Math.abs(.5 - fraction) * height, borderRadius: 3, backgroundColor: theme.colors.accentStrong}}/>
          : <Animated.View style={[{position: 'absolute', left: 0, right: 0, top: 0, bottom: 0, borderRadius: horizontal ? 2 : 3, backgroundColor: theme.colors.accentStrong}, fillPaint]}/>}
        {theme.isTV
          ? <View style={{position: 'absolute', ...(horizontal ? {left: `${(1 - fraction) * 100}%`, top: 2, marginLeft: -knob / 2} : {top: fraction * height, left: '50%', marginLeft: -knob / 2}), marginTop: -knob / 2,
              width: knob, height: knob, borderRadius: knob / 2, backgroundColor: theme.colors.text,
              shadowColor: focused ? theme.colors.accentStrong : '#000', shadowOffset: {width: 0, height: focused ? 0 : 2}, shadowOpacity: .55, shadowRadius: focused ? 8 : 4,
              ...(focused ? {borderWidth: 0, elevation: 4} : {})}}/>
          : <Animated.View style={[{position: 'absolute', ...(horizontal ? {left: 0, top: 2} : {left: '50%', top: 0}), marginTop: -knob / 2, marginLeft: -knob / 2,
              width: knob, height: knob, borderRadius: knob / 2, backgroundColor: theme.colors.text,
              shadowColor: draggingNow ? theme.colors.accentStrong : '#000', shadowOffset: {width: 0, height: draggingNow ? 0 : 2}, shadowOpacity: .55, shadowRadius: draggingNow ? 8 : 4,
              ...(draggingNow ? {borderWidth: 0, elevation: 4} : {})}, thumbPaint]}/>}
        {!horizontal && draggingNow && <Animated.View pointerEvents="none" style={[{position: 'absolute', top: 0, left: '50%', width: 76, marginLeft: -38, height: 30, borderRadius: 8, backgroundColor: theme.colors.text, alignItems: 'center', justifyContent: 'center'}, tooltipPaint]}>
          <View style={{position: 'absolute', width: 10, height: 10, bottom: -4, borderRadius: 2, backgroundColor: theme.colors.text, transform: [{rotate: '45deg'}]}}/>
          <Text style={[nativeType({size: 14, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.ink}]}>{copy.gain(shown, true)}</Text>
        </Animated.View>}
        {theme.isTV && focused && (['up', 'down'] as const).map(direction => <View pointerEvents="none" key={direction} style={{position: 'absolute', left: '50%', marginLeft: -15, top: fraction * height + (direction === 'up' ? -62 : 32), width: 30, height: 30, borderRadius: 15, backgroundColor: theme.colors.raisedBright, alignItems: 'center', justifyContent: 'center'}}><Icon name={direction === 'up' ? 'i-up' : 'i-down'} size={26}/></View>)}
      </View>
      {horizontal ? <Text style={[nativeType({size: 14, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {width: 58, textAlign: 'right', color: theme.colors.textSecondary}]}>{copy.gain(shown, true)}</Text>
        : <Text style={[nativeType({size: theme.isTV ? 20 : 10.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {marginTop: theme.isTV ? 20 : 9, color: focused ? theme.colors.text : target === 'preamp' ? theme.colors.textSecondary : theme.colors.textTertiary}]}>{label}</Text>}
    </Animated.View>
  </Focusable></GestureDetector>;
}
