import React, {forwardRef, useEffect, useState} from 'react';
import {Pressable, StyleSheet, type PressableProps, type PressableStateCallbackType, type StyleProp, type View, type ViewStyle} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {focusSpec} from '@portico/design';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {colorAlpha, focusScale, type FocusKind} from './control-model';
import {useFocusedMenu} from './useFocusedMenu';
import {useOverlayPaintMode} from '../overlays/OverlayPaintContext';
import {useFocusTarget} from '../focus';
import {useNativeObservation} from '../dev-observation';

export type ControlState = PressableStateCallbackType & {focused: boolean; hovered: boolean};
export type FocusableProps = Omit<PressableProps, 'children'> & {
  children?: React.ReactNode | ((state: ControlState) => React.ReactNode);
  focusId?: string;
  /** TV read-only/pending choices remain focus stops while commands are disabled. */
  focusableWhenDisabled?: boolean;
  focusKind?: FocusKind;
  focusRadius?: number;
  focusInset?: boolean;
  /** Custom face/art layers may draw the focus look from the real render-prop state. */
  focusTreatment?: 'default' | 'none';
  /** A card family may have its own authored TV focus scale. */
  focusScale?: number;
  /** A specifically drawn press may override the ordinary quick press. */
  pressDurationMs?: number;
  pressScale?: number;
  /** Same context action for touch/Apple long press and Android TV Menu-up. */
  onLongPressAction?: () => void;
  /** The name and localized label of that same action for assistive technology. */
  longPressAccessibilityAction?: {name: string; label: string};
};

type PressableStyleAdapterProps = Omit<PressableProps, 'style'> & {
  style?: StyleProp<ViewStyle>;
  pressableStyleInput?: PressableProps['style'];
  observeResolvedStyle?: (style: ViewStyle | undefined) => void;
};

// Reanimated 4.6 turns its style prop into an array, including functions.
// Resolve caller styles inside the real Pressable so its native render state
// still drives them, while Reanimated animates that same forwarded host ref.
// Static styles stay on Reanimated's style prop for animated-handle discovery.
const PressableStyleAdapter = forwardRef<View, PressableStyleAdapterProps>(function PressableStyleAdapter({
  style, pressableStyleInput, observeResolvedStyle, ...props
}, ref) {
  return <Pressable {...props} ref={ref}
    style={state => {
      const resolved = [typeof pressableStyleInput === 'function' ? pressableStyleInput(state) : pressableStyleInput, style];
      observeResolvedStyle?.(StyleSheet.flatten(resolved));
      return resolved;
    }} />;
});
const AnimatedPressable = Animated.createAnimatedComponent(PressableStyleAdapter);

/** The platform moves focus; this primitive only draws it and owns its Menu registration. */
export const Focusable = forwardRef<View, FocusableProps>(function Focusable({
  children, style, disabled, focusable, focusableWhenDisabled, onPress, onAccessibilityAction, onAccessibilityTap, onMagicTap, focusId, testID, focusKind = 'button', focusRadius = 10,
  focusInset = false, focusTreatment = 'default', focusScale: focusedScale, pressDurationMs, pressScale, onFocus, onBlur, onHoverIn, onHoverOut, onPressIn, onPressOut, onLongPress,
  onLongPressAction, longPressAccessibilityAction, accessibilityActions, accessibilityState, ...props
}, ref) {
  const {isTV, colors, layers, platform} = useTheme();
  const motion = useMotion(), paint = useOverlayPaintMode();
  const retainDisabledFocus = isTV && focusableWhenDisabled === true && paint.interactive;
  const nativeDisabled = !paint.interactive || disabled && !retainDisabledFocus;
  const {waiting, ...binding} = useFocusTarget({focusId, testID, disabled: nativeDisabled || focusable === false, ref});
  const accessibilityRole = props.accessibilityRole ?? (onPress ? 'button' : undefined);
  const observation = useNativeObservation<View>({kind: 'view', semanticId: binding.testID, role: accessibilityRole,
    flattenedStyle: typeof __DEV__ !== 'undefined' && __DEV__ && typeof style !== 'function' ? StyleSheet.flatten(style) : undefined,
    declaredStyleComplete: typeof style !== 'function'}, binding.ref);
  const [focused, setFocused] = useState(false);
  const [hovered, setHovered] = useState(false);
  const scale = useSharedValue(1);
  const halo = useSharedValue(0);
  const pressed = useSharedValue(0);

  // motion.html 4.1: native focus draws with quick/out tokens.
  useEffect(() => {
    scale.value = motion.transforms && focusTreatment === 'default' ? withTiming(focused && (!disabled || retainDisabledFocus) ? focusedScale ?? focusScale(focusKind, isTV) : 1, motion.timing('quick')) : 1;
    halo.value = withTiming(focused && (!disabled || retainDisabledFocus) ? 1 : 0, motion.timing('quick'));
  }, [focused, disabled, retainDisabledFocus, isTV, focusKind, focusTreatment, focusedScale, motion, scale, halo]);
  useFocusedMenu(isTV, focused, disabled || !paint.interactive, waiting || !paint.interactive ? undefined : onLongPressAction);

  const animated = useAnimatedStyle(() => ({transform: [{scale: motion.transforms && focusTreatment === 'default' ? scale.value * (isTV && pressScale === undefined ? 1 : 1 - pressed.value * (1 - (pressScale ?? .97))) : 1}]}));
  const ring = useAnimatedStyle(() => ({opacity: halo.value}));
  const offset = focusInset ? 0 : focusSpec.offsetOuter[platform] + focusSpec.ringWidth[platform];

  return <AnimatedPressable {...binding} {...props} testID={binding.testID} ref={observation.ref} disabled={nativeDisabled} focusable={nativeDisabled ? false : focusable}
    accessibilityRole={accessibilityRole}
    accessibilityActions={longPressAccessibilityAction && onLongPressAction
      ? [...(accessibilityActions ?? []).filter(action => action.name !== longPressAccessibilityAction.name), ...(waiting ? [] : [longPressAccessibilityAction])] : accessibilityActions}
    onPress={disabled || !paint.interactive ? undefined : onPress} onAccessibilityAction={disabled || !paint.interactive ? undefined : event => {
      if (longPressAccessibilityAction && onLongPressAction && event.nativeEvent.actionName === longPressAccessibilityAction.name) {if (!waiting) onLongPressAction();}
      else onAccessibilityAction?.(event);
    }}
    onAccessibilityTap={disabled || !paint.interactive ? undefined : onAccessibilityTap} onMagicTap={disabled || !paint.interactive ? undefined : onMagicTap}
    accessibilityState={{...accessibilityState, disabled: !!disabled || accessibilityState?.disabled}}
    tvParallaxProperties={{...props.tvParallaxProperties, enabled: false}}
    onFocus={event => {if (!paint.interactive) return; binding.onFocus(); setFocused(true); onFocus?.(event); observation.recordFocus(event, true);}}
    onBlur={event => {if (!paint.interactive) return; binding.onBlur(); setFocused(false); pressed.value = 0; onBlur?.(event); observation.recordFocus(event, false);}}
    onHoverIn={event => {if (!paint.interactive) return; setHovered(true); onHoverIn?.(event);}}
    onHoverOut={event => {if (!paint.interactive) return; setHovered(false); onHoverOut?.(event);}}
    onPressIn={disabled || !paint.interactive ? undefined : event => {pressed.value = withTiming(1, {...motion.timing('quick'), ...(pressDurationMs !== undefined && !motion.reduce ? {duration: pressDurationMs} : {})}); onPressIn?.(event);}}
    onPressOut={disabled || !paint.interactive ? undefined : event => {pressed.value = withTiming(0, {...motion.timing('quick'), ...(pressDurationMs !== undefined && !motion.reduce ? {duration: pressDurationMs} : {})}); onPressOut?.(event);}}
    onLongPress={disabled || waiting || !paint.interactive ? undefined : event => {onLongPressAction?.(); onLongPress?.(event);}}
    pressableStyleInput={typeof style === 'function' ? style : undefined}
    observeResolvedStyle={typeof __DEV__ !== 'undefined' && __DEV__ ? observation.recordDeclaredStyle : undefined}
    style={[typeof style === 'function' ? undefined : style, {zIndex: focused ? layers.tvFocus : undefined}, animated]}>
    {(state: PressableStateCallbackType) => <>
      {focusTreatment === 'default' && isTV && !focusInset ? <Animated.View pointerEvents="none" accessible={false} importantForAccessibility="no-hide-descendants"
        style={[styles.ring, {inset: -focusSpec.offsetOuter.tv, borderRadius: focusRadius + focusSpec.offsetOuter.tv,
          borderWidth: focusSpec.offsetOuter.tv, borderColor: colors.ink}, ring]} /> : null}
      {focusTreatment === 'default' ? <Animated.View pointerEvents="none" accessible={false} importantForAccessibility="no-hide-descendants"
        style={[styles.ring, {inset: -offset, borderRadius: focusRadius + offset,
          borderWidth: focusSpec.ringWidth[platform], borderColor: colors.focus,
          ...(isTV ? {boxShadow: tvFocusGlow(colors.accentStrong)} : {})}, ring]} /> : null}
      {typeof children === 'function' ? children({...state, pressed: disabled || !paint.interactive ? false : state.pressed, focused, hovered}) : children}
    </>}
  </AnimatedPressable>;
});

const styles = StyleSheet.create({ring: {position: 'absolute'}});

/** The TV focus glow from the token. `boxShadow` draws on tvOS, Android TV and Vega alike;
 * the iOS-only `shadow*` props left every Android-based TV without it. It sits on the ring
 * layer, so the ring's opacity fades ring and glow together. */
export function tvFocusGlow(accentStrong: string): ViewStyle['boxShadow'] {
  return [{offsetX: focusSpec.glow.x, offsetY: focusSpec.glow.y, blurRadius: focusSpec.glow.blur, spreadDistance: focusSpec.glow.spread, color: colorAlpha(accentStrong, focusSpec.glow.alpha)}];
}
