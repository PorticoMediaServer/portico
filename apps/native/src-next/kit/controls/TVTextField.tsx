import React, {forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState} from 'react';
import {Keyboard, Platform, StyleSheet, Text, TextInput, View} from 'react-native';
import {fieldSpec} from '@portico/design';
import {Focusable} from './Focusable';
import {colorAlpha} from './control-model';
import {FormError} from './FormError';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {useOverlayPaintMode} from '../overlays/OverlayPaintContext';
import {useFocusTarget} from '../focus';
import type {TextFieldProps} from './TextField';

/** TV draws its own field. The native input only supplies the system keyboard after Select. */
export const TVTextField = forwardRef<TextInput, TextFieldProps>(function TVTextField({
  label, description, error, leadingIcon, iconSize, trailing, variant = 'field', containerStyle, fieldStyle,
  appearance: _appearance, editable = true, focusable, style, onFocus, onBlur, accessibilityLabel, accessibilityState,
  placeholderTextColor, cursorColor, selectionColor, nativeID, multiline, focusId, testID, autoFocus, ...props
}, ref) {
  const theme = useTheme(), paint = useOverlayPaintMode(), input = useRef<TextInput>(null), target = useRef<View>(null);
  const [editing, setEditing] = useState(false), submitted = useRef(false);
  // tvOS opens its keyboard only when the focused text field itself is selected; a hidden field made first responder
  // after Select (Android's way) never shows it. So on Apple TV the native field, laid over the drawn one, is the focus
  // stop, and the drawn field takes its focus look from it.
  const appleTV = Platform.OS === 'ios', [typing, setTyping] = useState(false);
  const eligible = paint.interactive && editable && focusable !== false;
  // Apple TV: the input is the focus target, so it takes the field's place in the focus registry and its start focus.
  const {waiting: _waiting, ...appleFocus} = useFocusTarget<TextInput>({focusId, testID, disabled: !appleTV || !eligible, ref: input, preferred: appleTV && autoFocus});
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
    <View><Focusable ref={target} focusId={appleTV ? undefined : focusId} testID={appleTV ? undefined : testID} disabled={!eligible} preferredFocus={autoFocus && !appleTV} focusable={appleTV ? false : undefined}
      accessibilityRole="button" accessibilityLabel={accessibilityLabel ?? (typeof label === 'string' ? label : props.placeholder)}
      accessibilityValue={{text: displayed || props.placeholder || ''}} accessibilityState={{...accessibilityState, disabled: !editable}}
      focusKind="field" focusRadius={radius} onPress={open} onAccessibilityTap={open} onFocus={onFocus} onBlur={onBlur}
      style={({focused: own}) => {const focused = own || typing; return [{minHeight: height, borderRadius: radius, paddingHorizontal: search ? fieldSpec.search.paddingX.tv : fieldSpec.paddingX.tv,
        flexDirection: 'row', alignItems: 'center', gap: 10, opacity: editable ? 1 : .45}, fieldStyle,
        {backgroundColor: focused ? theme.colors[fieldSpec.fill.focus] : colorAlpha(theme.colors[fieldSpec.tv.rest.background], fieldSpec.tv.rest.backgroundAlpha ?? 1),
          borderWidth: fieldSpec.borderWidth.tv, borderColor: theme.colors[error ? fieldSpec.border.invalid : focused ? fieldSpec.border.focus : fieldSpec.border.rest]}];}}>
      {leadingIcon && <Icon name={leadingIcon} size={iconSize ?? 30} color={theme.colors.textTertiary}/>}
      {/* A typed value keeps its end in view (an address's last part, as typing it); the placeholder starts at its first
          word and takes a second line rather than being cut (tv-more, final3-tv-09). */}
      {displayed ? <Text numberOfLines={1} ellipsizeMode="head" style={[textStyle, {flex: 1, minWidth: 0}]}>{displayed}</Text>
        : <Text style={[textStyle, {flex: 1, minWidth: 0}]}>{props.placeholder ?? ''}</Text>}
      {trailing}
    </Focusable>
    {appleTV && <TextInput {...appleFocus} {...props} testID={appleFocus.testID} ref={appleFocus.ref} nativeID={nativeID} value={value} editable={eligible} focusable={eligible}
      accessibilityLabel={accessibilityLabel ?? (typeof label === 'string' ? label : props.placeholder)} cursorColor={cursorColor} selectionColor={selectionColor}
      style={styles.appleTVInput} onChangeText={eligible ? props.onChangeText : undefined}
      onFocus={event => {appleFocus.onFocus(); setTyping(true); onFocus?.(event);}} onBlur={event => {appleFocus.onBlur(); setTyping(false); onBlur?.(event);}}/>}</View>
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

// The Apple TV field covers the drawn one; the focus engine skips a view under ~1% opacity, so it is barely visible, never 0.
const styles = StyleSheet.create({keyboardInput: {position: 'absolute', width: 1, height: 1, opacity: 0},
  appleTVInput: {position: 'absolute', top: 0, left: 0, right: 0, bottom: 0, opacity: 0.02}});
