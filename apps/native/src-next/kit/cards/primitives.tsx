import React, {useEffect, useRef, useState, type ComponentType, type ReactNode, type RefObject} from 'react';
import {Pressable, StyleSheet, Text, View, type PressableProps, type StyleProp, type TextStyle, type ViewStyle} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {useTouchHold} from '../browse/useTouchHold';
import {touchMenuHaptic} from '../../platform/haptics';
import {registerFocusedMenu} from '../../platform/menu-key';
import {Icon, type IconName} from '../Icon';
import {SelectionHoldFeedback} from '../selection/index';
import {nativeType, useTheme, type Theme} from '../theme';
import {useMotion} from '../useMotion';
import {useFocusTarget, useFocusWaiting} from '../focus';
import {activateCard, cardAccessibilityLabel, cardProgress, holdCard, type CardActions} from './model';

export const alpha = (color: string, opacity: number) => `${color}${Math.round(opacity * 255).toString(16).padStart(2, '0')}`;

export type CardNativeProps = Omit<PressableProps, 'children' | 'style' | 'onPress' | 'onLongPress' | 'onFocus' | 'onBlur' | 'onPressIn' | 'onPressOut' | 'onHoverIn' | 'onHoverOut'> & {
  focusId?: string;
  title: string;
  /** The caller's authored title allowance, shared by square, circle and mosaic cards. */
  titleLines?: 1 | 2;
  caption?: readonly FactPart[];
  detail?: readonly FactPart[];
  /** Other already-localized facts drawn by a specialized card (episode or session). */
  accessibilityDetails?: readonly (string | undefined)[];
  watched?: boolean;
  isNew?: boolean;
  progress?: number;
  width?: number;
  style?: StyleProp<ViewStyle>;
  onFocus?: () => void;
  onBlur?: () => void;
  density?: 'shelf' | 'grid';
  titleTextStyle?: StyleProp<TextStyle>;
  titleMargin?: number;
  captionTextStyle?: StyleProp<TextStyle>;
  menuVisibility?: 'hover' | 'always';
  /** Visibility is independent from selection/focus and cancels offscreen artwork slots. */
  moreExpanded?: boolean;
  menuAnchorRef?: RefObject<View | null>;
  pressableStyle?: StyleProp<ViewStyle>;
  visible?: boolean;
  priority?: boolean;
  /** Draws the still "Playing" badge on a squared card (tv-player 9). */
  playing?: boolean;
  /** TV sparse-slot composition may retain the one outer native host while replacing paint. */
  frame?: ComponentType<CardFrameProps>;
};
export type CardProps = CardNativeProps & CardActions;
export type Interaction = {focused: boolean; hovered: boolean; pressed: boolean; holding: boolean};

type ReadOnlyCardActions = Readonly<{primaryAction?: undefined; onNavigate?: undefined; onPlay?: undefined; onMore?: undefined; selection?: undefined}>;
export type CardFrameSurfaceProps = CardNativeProps & (CardActions | ReadOnlyCardActions);
export type CardFrameProps = CardFrameSurfaceProps & {children: (state: Interaction) => ReactNode; overlay?: (state: Interaction) => ReactNode};

/** One root TV focus target. Images and secondary affordances never register Menu. */
export function CardFrame({children, overlay, frame: _frame, title, titleMargin: _titleMargin, caption, detail, accessibilityDetails = [], watched, isNew, playing, progress, primaryAction, onNavigate, onPlay, onMore, selection, width, style, onFocus, onBlur, disabled, accessibilityLabel, testID, focusId, focusable, moreExpanded, pressableStyle, menuAnchorRef, ...native}: CardFrameProps) {
  const theme = useTheme(), motion = useMotion();
  const eligible = !disabled && focusable !== false;
  const {waiting, ...binding} = useFocusTarget({focusId, testID, disabled: !eligible, ref: menuAnchorRef});
  const [focused, setFocused] = useState(false), [hovered, setHovered] = useState(false), [pressed, setPressed] = useState(false);
  const owner = useRef({}), menuRegistration = useRef<(() => void) | undefined>(undefined), tvHeld = useRef(false), scale = useSharedValue(1);
  const actions = {primaryAction, onNavigate, onPlay, onMore, selection} as CardActions;
  const commandDisabled = !!disabled || waiting || !(primaryAction === 'play' ? onPlay : onNavigate);
  const touch = useTouchHold(() => {
    if (!selection?.active && onMore) void touchMenuHaptic();
    holdCard(actions, false);
  }, !disabled && !waiting && !theme.isTV && (!!onMore || !!selection?.active));
  const holding = touch.phase === 'pending';
  useEffect(() => {
    menuRegistration.current?.();
    menuRegistration.current = focused && !disabled && !waiting && onMore && theme.isTV ? registerFocusedMenu(owner.current, onMore) : undefined;
    return () => {menuRegistration.current?.(); menuRegistration.current = undefined;};
  }, [focused, disabled, waiting, onMore, theme.isTV]);
  useEffect(() => {
    const next = !motion.transforms || disabled ? 1 : theme.isTV && focused ? theme.spec.focus.scale.card.tv : pressed && theme.isTV ? 0.97 : 1;
    scale.value = withTiming(next, motion.timing('quick'));
  }, [focused, pressed, holding, disabled, theme.isTV, theme.spec.focus.scale.card.tv, motion, scale]);
  const animated = useAnimatedStyle(() => ({transform: [{scale: scale.value}]}));
  const label = waiting ? defaultI18n.t('next.selection.native.loading') : accessibilityLabel ?? cardAccessibilityLabel(title, caption, detail,
    [...accessibilityDetails, watched ? defaultI18n.t('card.watched') : undefined, isNew ? defaultI18n.t('next.kit.new') : undefined, playing ? defaultI18n.t('next.kit.session.playing') : undefined]);
  const fraction = cardProgress(progress);
  const frame = <Animated.View style={[{width, flex: pressableStyle ? 1 : undefined}, theme.isTV && style, animated]}>
    <Pressable {...binding} {...nativePressableProps(native)} ref={binding.ref} style={pressableStyle} testID={binding.testID} disabled={commandDisabled} focusable={eligible} accessibilityRole="button" accessibilityLabel={label}
      accessibilityValue={waiting ? undefined : native.accessibilityValue ?? (fraction === undefined ? undefined : {min: 0, max: 100, now: Math.round(fraction * 100)})}
      onAccessibilityTap={commandDisabled ? undefined : native.onAccessibilityTap} onMagicTap={commandDisabled ? undefined : native.onMagicTap}
      accessibilityState={{...native.accessibilityState, busy: waiting || native.accessibilityState?.busy, disabled: disabled ?? native.accessibilityState?.disabled, ...(selection?.active ? {selected: selection.selected} : {})}}
      accessibilityActions={waiting ? [] : [
        ...(native.accessibilityActions ?? []),
        ...(onPlay && primaryAction !== 'play' ? [{name: 'play', label: defaultI18n.t('card.play', {title})}] : []),
        ...(onMore ? [{name: 'more', label: defaultI18n.t('card.more', {title})}] : []),
        ...(selection ? [{name: 'select', label: defaultI18n.t('card.select', {title})}] : []),
      ]}
      onAccessibilityAction={event => {
        if (commandDisabled) return;
        const name = event.nativeEvent.actionName;
        if (name === 'play') onPlay?.();
        else if (name === 'more') onMore?.();
        else if (name === 'select') selection?.active ? selection.onToggle() : selection?.onEnter();
        else native.onAccessibilityAction?.(event);
      }}
      {...touch.handlers}
      onTouchStart={event => {touch.handlers.onTouchStart(event); native.onTouchStart?.(event);}}
      onTouchMove={event => {touch.handlers.onTouchMove(event); native.onTouchMove?.(event);}}
      onTouchEnd={event => {touch.handlers.onTouchEnd(); native.onTouchEnd?.(event);}}
      onTouchCancel={event => {touch.handlers.onTouchCancel(); native.onTouchCancel?.(event);}}
      onPress={() => {if (!commandDisabled && !tvHeld.current && !touch.consumePress()) activateCard(actions);}}
      delayLongPress={native.delayLongPress}
      onLongPress={theme.isTV ? () => {if (!commandDisabled) {tvHeld.current = true; holdCard(actions, true);}} : undefined}
      onPressIn={() => {tvHeld.current = false; setPressed(true);}}
      onPressOut={() => {setPressed(false); touch.end();}}
      onFocus={() => {
        binding.onFocus();
        menuRegistration.current?.();
        menuRegistration.current = !disabled && !waiting && onMore && theme.isTV ? registerFocusedMenu(owner.current, onMore) : undefined;
        setFocused(true); onFocus?.();
      }}
      onBlur={() => {binding.onBlur(); menuRegistration.current?.(); menuRegistration.current = undefined; setFocused(false); touch.cancel(); onBlur?.();}}
      onHoverIn={() => setHovered(true)} onHoverOut={() => setHovered(false)}>
      {children({focused: focused && !disabled, hovered: hovered && !disabled, pressed: pressed && !disabled, holding})}
    </Pressable>
    {!waiting && overlay?.({focused: focused && !disabled, hovered: hovered && !disabled, pressed: pressed && !disabled, holding})}
  </Animated.View>;
  return theme.isTV ? frame : <SelectionHoldFeedback active={holding || !selection?.active && (touch.phase === 'fired' || !!moreExpanded)} progress={touch.phase === 'fired' || moreExpanded ? 1 : 0} style={[{width, zIndex: holding || moreExpanded ? 4 : undefined}, style]}>{frame}</SelectionHoldFeedback>;
}

/** Geometry belongs to the art, so focusing a poster never outlines its caption. */
export function ArtFrame({children, width, height, radius, focused, selected, hovered = false, holding = false, hoverScale, circle = false, style}: {
  children: ReactNode; width?: number; height: number; radius?: number; focused: boolean; selected?: boolean; hovered?: boolean; holding?: boolean; hoverScale?: number; circle?: boolean; style?: StyleProp<ViewStyle>;
}) {
  const waiting = useFocusWaiting();
  const t = useTheme(), motion = useMotion(), scale = useSharedValue(1), focusOpacity = useSharedValue(0), selectionOpacity = useSharedValue(0);
  useEffect(() => {
    scale.value = withTiming(motion.transforms && !holding && hovered && !selected ? hoverScale ?? t.spec.card.hover.artScale : 1, motion.timing('hover'));
    focusOpacity.value = withTiming(focused ? 1 : 0, motion.timing('quick'));
    selectionOpacity.value = withTiming(selected ? 1 : 0, motion.timing('quick'));
  }, [focused, hovered, holding, hoverScale, selected, t.spec.card.hover.artScale, motion, scale, focusOpacity, selectionOpacity]);
  const artStyle = useAnimatedStyle(() => ({transform: [{scale: scale.value}]}));
  const focusStyle = useAnimatedStyle(() => ({opacity: focusOpacity.value}));
  const selectionStyle = useAnimatedStyle(() => ({opacity: selectionOpacity.value}));
  const r = circle ? height / 2 : radius ?? t.spec.card.radius[t.platform];
  const offset = t.spec.focus.offsetOuter[t.platform], ring = t.spec.focus.ringWidth[t.platform];
  return <Animated.View style={[{width, height, borderRadius: r, backgroundColor: t.colors.raised}, style, artStyle]}>
    <View style={[StyleSheet.absoluteFill, {overflow: 'hidden', borderRadius: r}]}>{waiting ? <View style={[StyleSheet.absoluteFill, {backgroundColor: t.colors.raisedHigh}]} /> : children}</View>
    <Animated.View pointerEvents="none" style={[{position: 'absolute', top: -3, left: -3, right: -3, bottom: -3, borderRadius: r + 3, borderWidth: 3, borderColor: holding ? t.colors.text : t.colors.accentStrong,
      ...(holding ? {shadowColor: t.colors.ink, shadowOpacity: 0.9, shadowOffset: {width: 0, height: 24}, shadowRadius: 20, elevation: 8} : {})}, selectionStyle]} />
    <Animated.View pointerEvents="none" style={[{position: 'absolute', top: -offset - ring, left: -offset - ring, right: -offset - ring, bottom: -offset - ring, borderRadius: r + offset + ring, borderWidth: ring, borderColor: t.colors.focus,
      ...(t.isTV ? {boxShadow: [{offsetX: t.spec.focus.glow.x, offsetY: t.spec.focus.glow.y, blurRadius: t.spec.focus.glow.blur, spreadDistance: t.spec.focus.glow.spread, color: alpha(t.colors.accentStrong, t.spec.focus.glow.alpha)}]} : {})}, focusStyle]} />
  </Animated.View>;
}

export function Parts({parts = [], style, gap = 8, strong = false}: {parts?: readonly FactPart[]; style?: StyleProp<TextStyle>; gap?: number; strong?: boolean}) {
  const t = useTheme();
  if (!parts.length) return null;
  return <View style={{flexDirection: 'row', alignItems: 'baseline', gap, minWidth: 0, overflow: 'hidden'}}>{parts.map(part => <Text key={part.id} numberOfLines={1} ellipsizeMode="tail" style={[
    t.type.cardSubtitle, {color: t.colors.textTertiary, flexShrink: part.role === 'context' ? 0 : 1},
    (strong || part.role === 'identity' || part.role === 'status') && {fontFamily: t.type.captionStrong.fontFamily, color: part.role === 'status' ? t.colors.accentStrong : t.colors.textSecondary}, style,
  ]}>{part.text}</Text>)}</View>;
}

export function Caption({title, titleLines, caption, detail, density, titleTextStyle, titleMargin, captionTextStyle, selected, centered, focused, tvRestingSecondary, lines = titleLines ?? 1}: Pick<CardProps, 'title' | 'titleLines' | 'caption' | 'detail' | 'density' | 'titleTextStyle' | 'titleMargin' | 'captionTextStyle'> & {selected?: boolean; centered?: boolean; focused?: boolean; tvRestingSecondary?: boolean; lines?: number}) {
  const t = useTheme(), waiting = useFocusWaiting();
  const compact = density === 'grid' && !t.isTV;
  return <View accessibilityElementsHidden={waiting} importantForAccessibility={waiting ? 'no-hide-descendants' : 'auto'} style={{opacity: waiting ? 0 : 1, marginTop: titleMargin ?? (compact ? 7 : t.spec.card.titleMargin[t.platform]), gap: t.spec.card.subtitleMargin[t.platform], alignItems: centered ? 'center' : undefined}}>
    <Text numberOfLines={lines} ellipsizeMode="tail" style={[t.type.cardTitle, compact && titleStyle(t, 12.5, 600), {color: selected ? t.colors.accentStrong : t.isTV && tvRestingSecondary && !focused ? t.colors.textSecondary : t.colors.text, textAlign: centered ? 'center' : 'left'}, titleTextStyle]}>{title}</Text>
    <Parts parts={caption} style={[compact && titleStyle(t, 11.5, 600), captionTextStyle]} /><Parts parts={detail} style={[compact && titleStyle(t, 11.5, 600), captionTextStyle]} />
  </View>;
}

export function Progress({value, height, radius = 4, paused = false, style}: {value?: number; height?: number; radius?: number; paused?: boolean; style?: StyleProp<ViewStyle>}) {
  const t = useTheme(), p = cardProgress(value);
  if (p === undefined) return null;
  return <View pointerEvents="none" style={[{height: height ?? t.spec.card.progress.height[t.platform], overflow: 'hidden', borderRadius: radius, backgroundColor: alpha(t.colors.text, 0.22)}, style]}>
    <View style={{height: '100%', width: `${p * 100}%`, borderRadius: radius, backgroundColor: paused ? t.colors.textTertiary : t.colors.accentStrong}} />
  </View>;
}

export function Scrim({horizontal = false, start = 0.45, opacity = 0.85}: {horizontal?: boolean; start?: number; opacity?: number}) {
  const t = useTheme();
  return <Svg pointerEvents="none" style={StyleSheet.absoluteFill} width="100%" height="100%"><Defs><LinearGradient id="card-scrim" x1="0" y1="0" x2={horizontal ? '1' : '0'} y2={horizontal ? '0' : '1'}>
    <Stop offset="0" stopColor={t.colors.ink} stopOpacity={0} /><Stop offset={start} stopColor={t.colors.ink} stopOpacity={0} /><Stop offset="1" stopColor={t.colors.ink} stopOpacity={opacity} />
  </LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#card-scrim)" /></Svg>;
}

export function Badge({children, kind = 'plain'}: {children?: ReactNode; kind?: 'new' | 'plain' | 'watched'}) {
  const t = useTheme();
  return <View style={{paddingHorizontal: t.spec.card.badge.paddingX[t.platform], paddingVertical: t.spec.card.badge.paddingY[t.platform], borderRadius: t.spec.card.badge.radius[t.platform], backgroundColor: kind === 'new' ? t.colors.accent : alpha(t.colors.ink, 0.72)}}>
    {kind === 'watched' ? <Icon name="i-check" size={t.isTV ? 22 : 16} color={t.colors.text} /> : <Text numberOfLines={1} style={[t.type.badge, {color: kind === 'new' ? t.colors.actionInk : t.colors.text}]}>{children}</Text>}
  </View>;
}

/** A separate phone/pointer action; TV keeps its one root focus and real hold-OK menu. */
export function CardAction({name, label, onPress, size = 40, style, color, testID, focusId}: {testID?: string; focusId?: string; name: IconName; label: string; onPress: () => void; size?: number; style?: StyleProp<ViewStyle>; color?: string}) {
  const t = useTheme();
  const {waiting, ...binding} = useFocusTarget({focusId, testID});
  return <Pressable {...binding} accessibilityRole="button" accessibilityLabel={label} hitSlop={Math.max(0, (44 - size) / 2)} onPress={event => {event.stopPropagation(); if (!waiting) onPress();}} style={({pressed}) => [{width: size, height: size, borderRadius: size / 2, alignItems: 'center', justifyContent: 'center', backgroundColor: t.colors.text, opacity: pressed ? 0.8 : 1}, style]}>
    <Icon name={name} size={20} color={color ?? t.colors.ink} />
  </Pressable>;
}

/** Composite-only inputs never reach a native view (in particular authenticated image sources). */
function nativePressableProps(props: Record<string, unknown>): PressableProps {
  const names = new Set(['accessible', 'accessibilityHint', 'accessibilityValue', 'accessibilityLiveRegion', 'accessibilityElementsHidden', 'importantForAccessibility', 'accessibilityLanguage', 'accessibilityLabelledBy', 'accessibilityIgnoresInvertColors', 'accessibilityViewIsModal', 'nativeID', 'collapsable', 'hitSlop', 'pressRetentionOffset', 'delayLongPress', 'unstable_pressDelay', 'android_disableSound', 'android_ripple', 'focusable', 'hasTVPreferredFocus', 'nextFocusDown', 'nextFocusForward', 'nextFocusLeft', 'nextFocusRight', 'nextFocusUp', 'tvParallaxProperties', 'onLayout', 'onAccessibilityEscape', 'onMagicTap', 'onAccessibilityTap', 'onTouchStart', 'onTouchEnd', 'onTouchCancel']);
  return Object.fromEntries(Object.entries(props).filter(([name]) => names.has(name))) as PressableProps;
}

export function titleStyle(theme: Pick<Theme, 'textScale'>, size: number, weight: 600 | 700 | 800 = 700, tracking = 0): TextStyle {
  return nativeType({size, weight, tracking, lineHeight: 'normal'}, theme.textScale);
}
