import React, {forwardRef} from 'react';
import {ActivityIndicator, Platform, StyleSheet, Text, View, type TextStyle, type ViewStyle, type StyleProp} from 'react-native';
import {buttonSpec} from '@portico/design';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';
import {Focusable, type FocusableProps} from './Focusable';
import {buttonGeometry, colorAlpha, minimumHitSlop, progressFraction, type ButtonSize, type ButtonVariant} from './control-model';
import {useNativeObservation} from '../dev-observation';

export type ButtonProps = Omit<FocusableProps, 'children'> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
  children?: React.ReactNode;
  icon?: IconName;
  trailingIcon?: IconName;
  iconSize?: number;
  /** Optional drawn glyph color; labels continue to use textStyle. */
  iconColor?: string;
  iconOnly?: boolean;
  /** Providing a loading state reserves its icon slot, keeping both label and width stable. */
  loading?: boolean;
  textStyle?: StyleProp<TextStyle>;
};

export type ResumeButtonProps = ButtonProps & {progress: number; remaining?: React.ReactNode};
type ButtonContentProps = ButtonProps & {progress?: number; remaining?: React.ReactNode};
const paddingKeys = ['padding', 'paddingHorizontal', 'paddingVertical', 'paddingLeft', 'paddingRight', 'paddingTop', 'paddingBottom', 'paddingStart', 'paddingEnd', 'paddingBlock', 'paddingBlockStart', 'paddingBlockEnd', 'paddingInline', 'paddingInlineStart', 'paddingInlineEnd'] as const;

const ButtonContent = forwardRef<View, ButtonContentProps>(function ButtonContent({
  variant = 'primary', size = 'hero', children, icon, trailingIcon, iconSize, iconColor, iconOnly = variant === 'icon', loading,
  disabled, style, textStyle, accessibilityState, hitSlop, progress, remaining, ...props
}, ref) {
  const theme = useTheme();
  const grow = !iconOnly && (theme.fontScale > 1 || theme.textScale > 1);
  const geometry = buttonGeometry(size, theme.platform, variant);
  const nativeStyle = StyleSheet.flatten(typeof style === 'function' ? undefined : style);
  const height = typeof nativeStyle?.height === 'number' ? nativeStyle.height : geometry.height;
  const radius = typeof nativeStyle?.borderRadius === 'number' ? nativeStyle.borderRadius : iconOnly ? height / 2 : geometry.radius;
  const glyphSize = iconSize ?? geometry.icon;
  const recipe = glassRecipe();
  const white = variant === 'primary';
  const glass = variant === 'glass' || variant === 'icon' || variant === 'danger';
  const androidGlass = glass && recipe.nativeBlur && Platform.OS === 'android';
  const foreground = white ? theme.colors.ink : variant === 'danger' ? theme.colors.record : variant === 'text' ? theme.platform === 'web' ? theme.colors.textSecondary : theme.colors.accentStrong : theme.colors.text;
  const glyphColor = iconColor ?? foreground;
  const labelStyle = [theme.type.button, styles.label, {fontSize: geometry.fontSize * theme.textScale, color: foreground}, textStyle];
  const labelId = props.testID ?? props.focusId;
  const labelObservation = useNativeObservation<Text>({kind: 'text', semanticId: labelId ? `${labelId}/label` : undefined,
    flattenedStyle: typeof __DEV__ !== 'undefined' && __DEV__ ? StyleSheet.flatten(labelStyle) : undefined, declaredStyleComplete: true}, undefined, children);
  const fillObservation = useNativeObservation<View>({kind: 'view', semanticId: labelId ? `${labelId}/face` : undefined});
  const slop = minimumHitSlop(height, theme.isTV);
  const fraction = progress === undefined ? undefined : progressFraction(progress);
  const inline = theme.isTV && fraction !== undefined;
  const blocked = disabled || loading;

  return <Focusable {...props} ref={ref} disabled={blocked} focusRadius={props.focusRadius ?? radius}
    accessibilityRole={props.accessibilityRole ?? 'button'} accessibilityState={{...accessibilityState, busy: loading}}
    hitSlop={hitSlop ?? {top: slop, bottom: slop, left: slop, right: slop}}
    style={state => {
      const supplied = typeof style === 'function' ? style(state) : style, resolved = StyleSheet.flatten(supplied);
      const minimum = typeof resolved?.height === 'number' ? resolved.height : height;
      return [styles.button, {height, paddingHorizontal: iconOnly ? 0 : geometry.paddingX,
        width: iconOnly ? height : undefined, borderRadius: radius,
        opacity: disabled ? buttonSpec.disabledOpacity : 1}, supplied,
        grow ? {height: undefined, minHeight: Math.max(minimum, typeof resolved?.minHeight === 'number' ? resolved.minHeight : 0), paddingVertical: resolved?.paddingVertical ?? 4} : undefined,
        androidGlass ? styles.androidHost : undefined];
    }}>
    {state => {
      const suppliedStyle = StyleSheet.flatten(typeof style === 'function' ? style(state) : style);
      const fillStyle = [StyleSheet.absoluteFill, {backgroundColor: white ? state.hovered && !blocked ? theme.colors.white : theme.colors.text : glass ? colorAlpha(theme.colors.text, state.focused && theme.isTV ? buttonSpec.glass.focusedTvAlpha : state.hovered && !blocked ? buttonSpec.glass.hoverAlpha : buttonSpec.glass.alpha) : 'transparent'}];
      if (typeof __DEV__ !== 'undefined' && __DEV__) fillObservation.recordDeclaredStyle(StyleSheet.flatten(fillStyle));
      const fill = <View ref={fillObservation.ref} style={fillStyle} />;
      const content = <View pointerEvents="none" style={[styles.content, {gap: suppliedStyle?.columnGap ?? suppliedStyle?.gap ?? geometry.gap}]}>
        {icon || loading !== undefined ? <View style={{width: glyphSize, height: glyphSize, alignItems: 'center', justifyContent: 'center'}}>
          {loading ? <ActivityIndicator size={glyphSize} color={glyphColor} /> : icon ? <Icon name={icon} size={glyphSize} color={glyphColor} /> : null}
        </View> : null}
        {!iconOnly ? <Text {...labelObservation.textProps} numberOfLines={grow ? undefined : 1} ellipsizeMode="tail" style={labelStyle}>{children}</Text> : null}
        {inline && fraction !== undefined && !iconOnly ? <View style={[styles.inlineTrack, {backgroundColor: colorAlpha(white ? theme.colors.ink : theme.colors.text, .16)}]}><View style={[styles.fill, {width: `${fraction * 100}%`, backgroundColor: white ? theme.colors.accent : theme.colors.accentStrong}]} /></View> : null}
        {remaining !== undefined && !iconOnly ? <>
          <Text numberOfLines={grow ? undefined : 1} style={[theme.type.button, {fontSize: (theme.isTV ? 22 : geometry.fontSize) * theme.textScale, color: white ? colorAlpha(theme.colors.ink, .66) : theme.colors.textTertiary, flexShrink: grow ? 1 : undefined}, textStyle]}>{remaining}</Text>
        </> : null}
        {trailingIcon ? <Icon name={trailingIcon} size={glyphSize} color={glyphColor} /> : null}
      </View>;
      const edge = fraction !== undefined && !inline ? <View pointerEvents="none" style={[styles.edgeTrack, {borderBottomLeftRadius: suppliedStyle?.borderRadius ?? radius, borderBottomRightRadius: suppliedStyle?.borderRadius ?? radius, backgroundColor: colorAlpha(white ? theme.colors.ink : theme.colors.text, .14)}]}><View style={[styles.fill, {width: `${fraction * 100}%`, backgroundColor: white ? theme.colors.accent : theme.colors.accentStrong}]} /></View> : null;
      if (androidGlass) {
        const padding: ViewStyle = {paddingHorizontal: iconOnly ? 0 : geometry.paddingX, paddingVertical: grow ? 4 : undefined};
        for (const key of paddingKeys) if (suppliedStyle?.[key] !== undefined) padding[key] = suppliedStyle[key];
        // Android's backdrop capture skips a BlurView and its descendants. Keep the complete
        // face inside that subtree, so its own text, tint and progress cannot feed the blur.
        return <View pointerEvents="none" style={[styles.androidFace, {borderRadius: suppliedStyle?.borderRadius ?? radius, overflow: 'hidden'}]}>
          <BlurView style={styles.androidFace} overlayColor="transparent" blurType="dark" blurAmount={recipe.blurAmount} reducedTransparencyFallbackColor={theme.colors.raisedHigh}>
            {/* BlurView is an Android FrameLayout: keep one real RN child to lay out
                the face, and let the outer RN View clip its rounded corners. */}
            <View collapsable={false} style={[styles.androidFace, padding, {backgroundColor: suppliedStyle?.backgroundColor,
              alignItems: suppliedStyle?.alignItems ?? 'center', justifyContent: suppliedStyle?.justifyContent ?? 'center'}]}>
              {fill}{content}{edge}
            </View>
          </BlurView>
        </View>;
      }
      return <>
      <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: suppliedStyle?.borderRadius ?? radius, overflow: 'hidden'}]}>
        {glass && recipe.nativeBlur ? <BlurView style={StyleSheet.absoluteFill} blurType="dark" blurAmount={recipe.blurAmount} reducedTransparencyFallbackColor={theme.colors.raisedHigh} /> : null}
        {fill}
      </View>
      {content}{edge}
    </>;}}
  </Focusable>;
});

export const Button = forwardRef<View, ButtonProps>(function Button(props, ref) {return <ButtonContent {...props} ref={ref} />;});
export const ResumeButton = forwardRef<View, ResumeButtonProps>(function ResumeButton({icon = 'i-play', ...props}, ref) {return <ButtonContent {...props} icon={icon} ref={ref} />;});

const styles = StyleSheet.create({
  button: {alignItems: 'center', justifyContent: 'center', alignSelf: 'flex-start', flexShrink: 1},
  androidHost: {...Object.fromEntries(paddingKeys.map(key => [key, 0])), backgroundColor: 'transparent'},
  androidFace: {alignSelf: 'stretch', flexGrow: 1, flexShrink: 1},
  content: {flexDirection: 'row', alignItems: 'center', maxWidth: '100%'}, label: {flexShrink: 1},
  inlineTrack: {width: 96, height: 6, borderRadius: 6, overflow: 'hidden', marginLeft: 8},
  edgeTrack: {position: 'absolute', bottom: 0, left: 0, right: 0, height: 4, overflow: 'hidden'},
  fill: {height: '100%', borderRadius: 6},
});
