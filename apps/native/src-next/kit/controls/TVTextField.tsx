import React, {forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState} from 'react';
import {Keyboard, Platform, StyleSheet, Text, TextInput, View} from 'react-native';
import {fieldSpec} from '@portico/design';
import {Focusable} from './Focusable';
import {colorAlpha} from './control-model';
import {FormError} from './FormError';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {useOverlayPaintMode} from '../overlays/OverlayPaintContext';
import type {TextFieldProps} from './TextField';

/** TV draws its own field. The native input only supplies the system keyboard after Select. */
export const TVTextField = forwardRef<TextInput, TextFieldProps>(function TVTextField({
  label, description, error, leadingIcon, iconSize, trailing, variant = 'field', containerStyle, fieldStyle,
  appearance: _appearance, editable = true, focusable, style, onFocus, onBlur, accessibilityLabel, accessibilityState,
  placeholderTextColor, cursorColor, selectionColor, nativeID, multiline, focusId, testID, autoFocus, ...props
}, ref) {
  const theme = useTheme(), paint = useOverlayPaintMode(), input = useRef<TextInput>(null), target = useRef<View>(null);
  const [editing, setEditing] = useState(false), submitted = useRef(false);
  // tvOS opens its keyboard only for a text field the focus engine has focused; a hidden 1x1 field made first responder
  // after Select (Android's way) never shows it. So on Apple TV the drawn field stays the focus stop (its R1 ring, start
  // focus and registry place are the kit's), and Select mounts a real-size field over it, moves the focus engine there
  // and makes it first responder; the keyboard's Done or Back returns focus to the drawn field.
  const appleTV = Platform.OS === 'ios';
  const eligible = paint.interactive && editable && focusable !== false;
  useEffect(() => {
    if (!appleTV || !editing || !eligible) return;
    (input.current as unknown as {requestTVFocus?: () => void} | null)?.requestTVFocus?.();
    // First responder once the focus engine has moved (it does so on its next update).
    const id = setTimeout(() => input.current?.focus(), 200);
    return () => clearTimeout(id);
  }, [appleTV, editing, eligible]);
  const search = variant === 'search', height = search ? fieldSpec.search.height.tv : fieldSpec.height.tv;
  const suppliedRadius = StyleSheet.flatten(fieldStyle)?.borderRadius;
  const radius = typeof suppliedRadius === 'number' ? suppliedRadius : search ? fieldSpec.search.radius.tv : fieldSpec.radius.tv;
  const open = useCallback(() => {if (eligible && !editing) {submitted.current = false; setEditing(true);}}, [editing, eligible]);
  const close = useCallback(() => {
    if (!editing) return;
    setEditing(false);
    if (eligible && !submitted.current) target.current?.requestTVFocus();
  }, [editing, eligible]);
  useImperativeHandle(ref, () => ({focus: open, blur: () => input.current?.blur(),
    clear: () => input.current?.clear(), isFocused: () => input.current?.isFocused() ?? false}) as TextInput, [open]);
  useEffect(() => {
    if (!eligible) setEditing(false);
    if (!editing || !eligible || Platform.OS !== 'android') return;
    // The Android IME can close on Back without blurring its input.
    const subscription = Keyboard.addListener('keyboardDidHide', close);
    return () => subscription.remove();
  }, [close, editing, eligible]);
  const value = props.value ?? props.defaultValue ?? '';
  const displayed = props.secureTextEntry ? '•'.repeat(value.length) : value;
  const textStyle = [style, theme.type[value ? fieldSpec.tv.value : fieldSpec.tv.placeholder],
    {color: value ? theme.colors.text : placeholderTextColor ?? theme.colors[fieldSpec.tv.muted]}];
  return <View style={[{gap: fieldSpec.tv.labelGap, alignSelf: 'stretch'}, containerStyle]}>
    {label !== undefined && <Text style={[theme.type[fieldSpec.tv.label], {color: theme.colors[fieldSpec.tv.muted]}]}>{label}</Text>}
    <View><Focusable ref={target} focusId={focusId} testID={testID} disabled={!eligible} preferredFocus={autoFocus}
      accessibilityRole="button" accessibilityLabel={accessibilityLabel ?? (typeof label === 'string' ? label : props.placeholder)}
      accessibilityValue={{text: displayed || props.placeholder || ''}} accessibilityState={{...accessibilityState, disabled: !editable}}
      focusKind="field" focusRadius={radius} onPress={open} onAccessibilityTap={open} onFocus={onFocus} onBlur={onBlur}
      style={({focused}) => [{minHeight: height, borderRadius: radius, paddingHorizontal: search ? fieldSpec.search.paddingX.tv : fieldSpec.paddingX.tv,
        flexDirection: 'row', alignItems: 'center', gap: 10, opacity: editable ? 1 : .45}, fieldStyle,
        {backgroundColor: focused ? theme.colors[fieldSpec.fill.focus] : colorAlpha(theme.colors[fieldSpec.tv.rest.background], fieldSpec.tv.rest.backgroundAlpha ?? 1),
          borderWidth: fieldSpec.borderWidth.tv, borderColor: theme.colors[error ? fieldSpec.border.invalid : focused ? fieldSpec.border.focus : fieldSpec.border.rest]}]}>
      {leadingIcon && <Icon name={leadingIcon} size={iconSize ?? 30} color={theme.colors.textTertiary}/>}
      {/* A typed value keeps its end in view (an address's last part, as typing it); the placeholder starts at its first
          word and takes a second line rather than being cut (tv-more, final3-tv-09). */}
      {displayed ? <Text numberOfLines={1} ellipsizeMode="head" style={[textStyle, {flex: 1, minWidth: 0}]}>{displayed}</Text>
        : <Text style={[textStyle, {flex: 1, minWidth: 0}]}>{props.placeholder ?? ''}</Text>}
      {trailing}
    </Focusable>
    {appleTV && editing && <TextInput {...props} ref={input} nativeID={nativeID} value={value} editable={eligible} focusable={eligible}
      accessibilityLabel={accessibilityLabel ?? (typeof label === 'string' ? label : props.placeholder)} cursorColor={cursorColor} selectionColor={selectionColor}
      style={styles.appleTVInput} onChangeText={eligible ? props.onChangeText : undefined}
      onSubmitEditing={event => {submitted.current = true; setEditing(false); if (props.onSubmitEditing) props.onSubmitEditing(event); else target.current?.requestTVFocus();}} onBlur={close}/>}</View>
    {/* Focus on native mount so the text input is in the window when it asks for the keyboard. */}
    {!appleTV && <TextInput key={editing ? 'editing' : 'idle'} {...props} ref={input} nativeID={nativeID} value={value} multiline={multiline}
      autoFocus={editing && eligible} editable={eligible} focusable={editing && eligible}
      accessibilityLabel={accessibilityLabel ?? (typeof label === 'string' ? label : props.placeholder)}
      accessible={false} importantForAccessibility="no-hide-descendants" cursorColor={cursorColor} selectionColor={selectionColor}
      style={styles.keyboardInput} onChangeText={eligible ? props.onChangeText : undefined}
      onSubmitEditing={event => {
        submitted.current = true; setEditing(false);
        if (props.onSubmitEditing) props.onSubmitEditing(event); else target.current?.requestTVFocus();
      }} onBlur={close}/>}
    {error !== undefined ? <FormError placement="inline">{error}</FormError>
      : description !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{description}</Text>}
  </View>;
});

// The Apple TV field covers the drawn one while typing; the focus engine skips a view under ~1% opacity, so never 0.
const styles = StyleSheet.create({keyboardInput: {position: 'absolute', width: 1, height: 1, opacity: 0},
  appleTVInput: {position: 'absolute', top: 0, left: 0, right: 0, bottom: 0, opacity: 0.02}});
