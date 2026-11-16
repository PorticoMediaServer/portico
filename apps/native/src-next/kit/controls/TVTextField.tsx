import React, {forwardRef, useEffect, useImperativeHandle, useRef, useState} from 'react';
import {StyleSheet, Text, TextInput, View} from 'react-native';
import {fieldSpec} from '@portico/design';
import {Focusable} from './Focusable';
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
  const eligible = paint.interactive && editable && focusable !== false;
  const search = variant === 'search', height = search ? fieldSpec.search.height.tv : fieldSpec.height.tv;
  const suppliedRadius = StyleSheet.flatten(fieldStyle)?.borderRadius;
  const radius = typeof suppliedRadius === 'number' ? suppliedRadius : search ? fieldSpec.search.radius.tv : fieldSpec.radius.tv;
  const open = () => {if (eligible) {submitted.current = false; setEditing(true);}};
  useImperativeHandle(ref, () => ({...input.current!, focus: open}) as TextInput, [eligible]);
  useEffect(() => {if (editing && eligible) input.current?.focus(); else input.current?.blur();}, [editing, eligible]);
  const value = props.value ?? props.defaultValue ?? '';
  const displayed = props.secureTextEntry ? '•'.repeat(value.length) : value;
  const textStyle = [theme.type.body, {color: value ? theme.colors.text : placeholderTextColor ?? theme.colors.placeholder}, style];
  return <View style={[{gap: 8, alignSelf: 'stretch'}, containerStyle]}>
    {label !== undefined && <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{label}</Text>}
    <Focusable ref={target} focusId={focusId} testID={testID} disabled={!eligible} preferredFocus={autoFocus}
      accessibilityRole="button" accessibilityLabel={accessibilityLabel ?? (typeof label === 'string' ? label : props.placeholder)}
      accessibilityValue={{text: displayed || props.placeholder || ''}} accessibilityState={{...accessibilityState, disabled: !editable}}
      focusKind="row" focusRadius={radius} onPress={open} onFocus={onFocus} onBlur={onBlur}
      style={[{minHeight: height, borderRadius: radius, paddingHorizontal: search ? fieldSpec.search.paddingX.tv : fieldSpec.paddingX.tv,
        flexDirection: 'row', alignItems: 'center', gap: 10, backgroundColor: theme.colors.raised,
        borderWidth: fieldSpec.borderWidth.tv, borderColor: error ? theme.colors.danger : theme.colors.lineControl,
        opacity: editable ? 1 : .45}, fieldStyle]}>
      {leadingIcon && <Icon name={leadingIcon} size={iconSize ?? 30} color={theme.colors.textTertiary}/>}
      <Text numberOfLines={1} ellipsizeMode="head" style={[textStyle, {flex: 1, minWidth: 0}]}>{displayed || props.placeholder || ''}</Text>
      {trailing}
    </Focusable>
    <TextInput {...props} ref={input} nativeID={nativeID} value={value} multiline={multiline} editable={eligible} focusable={editing && eligible}
      accessible={false} importantForAccessibility="no-hide-descendants" cursorColor={cursorColor} selectionColor={selectionColor}
      style={styles.keyboardInput} onChangeText={eligible ? props.onChangeText : undefined}
      onSubmitEditing={event => {
        submitted.current = true; setEditing(false);
        if (props.onSubmitEditing) props.onSubmitEditing(event); else target.current?.requestTVFocus();
      }} onBlur={() => {setEditing(false); if (eligible && !submitted.current) target.current?.requestTVFocus();}}/>
    {error !== undefined ? <FormError placement="inline">{error}</FormError>
      : description !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{description}</Text>}
  </View>;
});

const styles = StyleSheet.create({keyboardInput: {position: 'absolute', width: 1, height: 1, opacity: 0}});
