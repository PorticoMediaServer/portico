import React, {forwardRef, useId, useState} from 'react';
import {Platform, StyleSheet, Text, TextInput, View, type TextInputProps, type StyleProp, type ViewStyle} from 'react-native';
import {fieldSpec} from '@portico/design';
import {Icon, type IconName} from '../Icon';
import {fontFamilies, useTheme} from '../theme';
import {colorAlpha} from './control-model';
import {useFocusTarget} from '../focus';
import {useOverlayPaintMode} from '../overlays/OverlayPaintContext';
import {BlurView, glassRecipe} from '../../platform/native-blur';

export type TextFieldProps = TextInputProps & {
  focusId?: string;
  label?: React.ReactNode;
  description?: React.ReactNode;
  error?: React.ReactNode;
  leadingIcon?: IconName;
  iconSize?: number;
  trailing?: React.ReactNode;
  variant?: 'field' | 'search';
  containerStyle?: StyleProp<ViewStyle>;
  fieldStyle?: StyleProp<ViewStyle>;
  /** Authored selection/auth fields. Default and TV controls retain their recipe. */
  appearance?: 'default' | 'selection' | 'auth';
};

export const TextField = forwardRef<TextInput, TextFieldProps>(function TextField({
  label, description, error, leadingIcon, iconSize, trailing, variant = 'field', containerStyle, fieldStyle, appearance = 'default',
  editable = true, focusable, style, onFocus, onBlur, accessibilityLabel, accessibilityState,
  placeholderTextColor, cursorColor, selectionColor, nativeID, multiline, focusId, testID, ...props
}, ref) {
  const theme = useTheme(), paint = useOverlayPaintMode();
  const grow = theme.fontScale > 1 || theme.textScale > 1;
  const eligible = paint.interactive && editable && focusable !== false;
  const {waiting, ...binding} = useFocusTarget<TextInput>({focusId, testID, disabled: !eligible, ref});
  const [focused, setFocused] = useState(false);
  const id = useId();
  const search = variant === 'search';
  const selection = appearance === 'selection' && !theme.isTV && !search;
  const auth = appearance === 'auth' && !theme.isTV && !search, glass = glassRecipe();
  const height = auth ? 48 : search ? fieldSpec.search.height[theme.platform] : fieldSpec.height[theme.platform];
  const suppliedField = StyleSheet.flatten(fieldStyle);
  const suppliedHeight = suppliedField?.height;
  const inputHeight = typeof suppliedHeight === 'number' ? suppliedHeight : height;
  const radius = typeof suppliedField?.borderRadius === 'number' ? suppliedField.borderRadius : auth ? 11 : search ? fieldSpec.search.radius[theme.platform] : fieldSpec.radius[theme.platform];
  const color = error ? theme.colors.danger : focused ? theme.colors.accent : theme.colors.lineControl;
  const selectionBorder = focused || error ? 2 : suppliedField?.borderWidth ?? 1;
  const labelID = `${id}-label`;
  const faceStyle: StyleProp<ViewStyle> = [styles.field, {minHeight: inputHeight, height: multiline || auth ? undefined : inputHeight, borderRadius: radius,
      paddingHorizontal: auth ? 14 : search ? fieldSpec.search.paddingX[theme.platform] : fieldSpec.paddingX[theme.platform],
      gap: search ? fieldSpec.search.gap[theme.platform] : 10,
      backgroundColor: auth ? glass.nativeBlur ? 'rgba(7,16,24,.62)' : colorAlpha(glass.backgroundColor, glass.backgroundAlpha)
        : search || focused ? theme.colors.raisedHigh : theme.colors.raised,
      borderWidth: search && !error ? 0 : fieldSpec.borderWidth[theme.platform], borderColor: color,
      opacity: auth || editable ? 1 : .45}, fieldStyle, grow ? {height: undefined, minHeight: Math.max(inputHeight, typeof suppliedField?.minHeight === 'number' ? suppliedField.minHeight : 0)} : undefined, selection ? {backgroundColor: suppliedField?.backgroundColor ?? theme.colors.ink, borderWidth: selectionBorder,
        borderColor: error ? theme.colors.danger : focused ? theme.colors.focus : suppliedField?.borderColor ?? theme.colors.line} : undefined];
  const content = <>
      {leadingIcon ? <Icon name={leadingIcon} size={iconSize ?? (theme.isTV ? 30 : 20)} color={theme.colors.textTertiary} /> : null}
      <TextInput {...binding} {...props} testID={binding.testID} ref={binding.ref} nativeID={nativeID ?? id} editable={editable && paint.interactive} focusable={eligible}
        onChangeText={paint.interactive ? props.onChangeText : undefined} onSubmitEditing={paint.interactive ? props.onSubmitEditing : undefined} onKeyPress={paint.interactive ? props.onKeyPress : undefined} onEndEditing={paint.interactive ? props.onEndEditing : undefined} multiline={multiline}
        accessibilityLabel={accessibilityLabel ?? (typeof label === 'string' ? label : undefined)}
        accessibilityLabelledBy={label === undefined ? props.accessibilityLabelledBy : labelID}
        accessibilityState={{...accessibilityState, disabled: !editable, busy: waiting || accessibilityState?.busy}}
        placeholderTextColor={placeholderTextColor ?? theme.colors.placeholder}
        cursorColor={cursorColor ?? (selection && Platform.OS === 'android' ? theme.colors.accentStrong : undefined)}
        selectionColor={selectionColor ?? (selection && Platform.OS === 'ios' ? theme.colors.accentStrong : undefined)}
        underlineColorAndroid="transparent"
        onFocus={event => {if (!paint.interactive) return; binding.onFocus(); setFocused(true); onFocus?.(event);}}
        onBlur={event => {if (!paint.interactive) return; binding.onBlur(); setFocused(false); onBlur?.(event);}}
        style={[theme.type.body, styles.input, {fontFamily: selection || auth ? fontFamilies[600] : search ? fontFamilies[500] : fontFamilies[400],
          fontWeight: auth ? '600' : theme.type.body.fontWeight,
          fontSize: auth ? 16 * theme.textScale : search && !theme.isTV ? 15.5 * theme.textScale : theme.type.body.fontSize,
          color: theme.colors.text, minHeight: inputHeight - 2 * (selection ? selectionBorder : fieldSpec.borderWidth[theme.platform])}, style]} />
      {trailing}
    </>;
  const ring = focused && !selection ? <View pointerEvents="none" style={[styles.ring, {borderColor: search ? colorAlpha(theme.colors.accent, fieldSpec.search.focusAlpha) : theme.colors.focus,
    borderWidth: theme.isTV ? 4 : 3, borderRadius: radius + 4}]} /> : null;
  return <View style={[styles.container, selection ? {gap: 7} : auth ? {gap: 6} : undefined, containerStyle]}>
    {label !== undefined ? <Text nativeID={labelID} style={[theme.type.captionStrong, {color: theme.colors.textSecondary},
      selection ? {fontSize: 12.5 * theme.textScale, color: theme.colors.textTertiary} : auth ? {fontSize: 13 * theme.textScale} : undefined]}>{label}</Text> : null}
    {auth ? <View style={{opacity: editable ? 1 : .45}}>
      <View style={{borderRadius: radius, overflow: 'hidden'}}>
        {glass.nativeBlur && Platform.OS === 'android' ? <BlurView blurType="dark" blurAmount={16} overlayColor="transparent"
          reducedTransparencyFallbackColor={theme.colors.raisedHigh}>
          {/* The complete face stays inside Android's excluded blur subtree. */}
          <View collapsable={false} style={faceStyle}>{content}</View>
        </BlurView> : <>
          {glass.nativeBlur && <BlurView pointerEvents="none" style={StyleSheet.absoluteFill} blurType="dark" blurAmount={16} reducedTransparencyFallbackColor={theme.colors.raisedHigh}/>}
          <View style={faceStyle}>{content}</View>
        </>}
      </View>
      {ring}
    </View> : <View style={faceStyle}>{content}{ring}</View>}
    {error !== undefined ? <Text accessibilityRole="alert" accessibilityLiveRegion="polite" style={[theme.type.caption, {color: theme.colors.danger}]}>{error}</Text>
      : description !== undefined ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{description}</Text> : null}
  </View>;
});

const styles = StyleSheet.create({container: {gap: 8, alignSelf: 'stretch'}, field: {flexDirection: 'row', alignItems: 'center'}, input: {flex: 1, minWidth: 0, padding: 0}, ring: {position: 'absolute', inset: -4}});
