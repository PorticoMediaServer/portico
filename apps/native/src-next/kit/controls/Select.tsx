import React, {forwardRef, useCallback, useEffect, useRef} from 'react';
import {FlatList, StyleSheet, Text, View, type StyleProp, type TextStyle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Icon} from '../Icon';
import {Dialog} from '../overlays/surface';
import {fontFamilies, nativeType, useTheme} from '../theme';
import {Focusable, type FocusableProps} from './Focusable';
import {colorAlpha, enabledChoice} from './control-model';
import {useControlValue} from './useControlValue';

export type SelectOption = {
  value: string;
  label: React.ReactNode;
  description?: React.ReactNode;
  isDefault?: boolean;
  disabled?: boolean;
};
export type SelectProps = Omit<FocusableProps, 'children'> & {
  options: readonly SelectOption[];
  value?: string;
  defaultValue?: string;
  onValueChange?: (value: string) => void;
  open?: boolean;
  defaultOpen?: boolean;
  onOpenChange?: (open: boolean) => void;
  label?: React.ReactNode;
  description?: React.ReactNode;
  error?: React.ReactNode;
  /** A drawn value row: the label sits at the left of the same control as its value. */
  rowLabel?: React.ReactNode;
  placeholder?: React.ReactNode;
  textStyle?: StyleProp<TextStyle>;
  pickerContext?: React.ReactNode;
  pickerTitle?: React.ReactNode;
  pickerDescription?: React.ReactNode;
  pickerFooter?: React.ReactNode;
};
export type RichSelectProps = SelectProps;

const NativeSelect = forwardRef<View, SelectProps & {rich: boolean}>(function NativeSelect({
  options, value, defaultValue, onValueChange, open, defaultOpen = false, onOpenChange,
  label, description, error, placeholder, rich, style, textStyle, pickerContext, pickerTitle, pickerDescription, pickerFooter, onPress, disabled, accessibilityLabel,
  accessibilityState, testID, rowLabel, ...props
}, ref) {
  const theme = useTheme();
  const grow = theme.fontScale > 1 || theme.textScale > 1;
  const trigger = useRef<View>(null);
  const selectedRow = useRef<View>(null);
  const list = useRef<FlatList<SelectOption>>(null);
  const [current, setCurrent] = useControlValue(value, defaultValue ?? enabledChoice(options) ?? '', onValueChange);
  const [isOpen, setOpen] = useControlValue(open, defaultOpen, onOpenChange);
  const selected = options.find(option => option.value === current);
  const focusedValue = enabledChoice(options, current);
  const initialIndex = options.findIndex(option => option.value === focusedValue);
  const rowHeight = (theme.isTV ? rich ? 120 : 64 : rich ? 80 : 52) * (theme.fontScale ?? 1);
  const rowPitch = rowHeight + (theme.isTV ? 10 : 0);
  useEffect(() => {
    if (isOpen && initialIndex >= 0) list.current?.scrollToIndex({index: initialIndex, animated: false});
  }, [isOpen, initialIndex, rowPitch]);
  const setRef = useCallback((node: View | null) => {
    trigger.current = node;
    if (typeof ref === 'function') ref(node); else if (ref) ref.current = node;
  }, [ref]);
  const blocked = disabled || focusedValue === undefined;
  const title = rowLabel ?? label ?? accessibilityLabel ?? selected?.label ?? placeholder;
  return <View style={styles.container}>
    {label !== undefined ? <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{label}</Text> : null}
    <Focusable {...props} ref={setRef} accessibilityRole="combobox" accessibilityLabel={accessibilityLabel ?? (typeof label === 'string' ? label : undefined)}
      accessibilityState={{...accessibilityState, expanded: isOpen, disabled: blocked}}
      accessibilityValue={props.accessibilityValue ?? (typeof selected?.label === 'string' ? {text: selected.label} : undefined)}
      testID={testID} disabled={blocked} focusKind="row" focusRadius={theme.isTV ? 14 : 10}
      onPress={event => {if (!blocked) {setOpen(!isOpen); onPress?.(event);}}}
      style={state => {
        const supplied = typeof style === 'function' ? style(state) : style, resolved = StyleSheet.flatten(supplied);
        const minimum = typeof resolved?.height === 'number' ? resolved.height : theme.isTV ? 64 : 44;
        return [styles.trigger, {height: theme.isTV ? 64 : 44, borderRadius: theme.isTV ? 14 : 10,
          paddingHorizontal: theme.isTV ? 20 : 16, gap: theme.isTV ? 20 : 14,
          borderColor: error ? theme.colors.danger : isOpen ? theme.colors.accent : 'transparent',
          backgroundColor: theme.colors.raisedBright, opacity: blocked ? .45 : 1}, supplied,
          grow ? {height: undefined, minHeight: Math.max(minimum, typeof resolved?.minHeight === 'number' ? resolved.minHeight : 0), paddingVertical: resolved?.paddingVertical ?? 4} : undefined];
      }}>
      {rowLabel !== undefined
        ? <><Text numberOfLines={grow ? undefined : 1} ellipsizeMode="tail" style={[theme.type.bodyStrong, {fontFamily: fontFamilies[700], color: theme.colors.text, flex: 1, minWidth: 0}, textStyle]}>{rowLabel}</Text>
          <Text numberOfLines={grow ? undefined : 1} ellipsizeMode="tail" style={[theme.type.bodyStrong, {fontFamily: fontFamilies[700], color: theme.colors.textTertiary, flexShrink: 1}, textStyle]}>{selected?.label ?? placeholder}</Text></>
        : <Text numberOfLines={grow ? undefined : 1} ellipsizeMode="tail" style={[theme.type.bodyStrong, styles.label, {fontFamily: fontFamilies[700], color: selected ? theme.colors.text : theme.colors.textTertiary}, textStyle]}>{selected?.label ?? placeholder}</Text>}
      <Icon name="i-updown" size={theme.isTV ? 26 : 18} color={theme.colors.textTertiary} />
    </Focusable>
    {error !== undefined ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger}]}>{error}</Text>
      : description !== undefined ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{description}</Text> : null}
    <Dialog open={isOpen} onOpenChange={next => setOpen(next)} headerContext={pickerContext} title={pickerTitle ?? title} description={pickerDescription} scrollable={false}
      footer={pickerFooter ?? (theme.isTV && !blocked ? <PickerHint /> : undefined)} accessibilityLabel={accessibilityLabel}
      placement={theme.isTV ? 'side' : undefined} width={theme.isTV ? 640 : undefined}
      titleTextStyle={theme.isTV ? {...nativeType({size: 46, weight: 800, tracking: -.035, lineHeight: 47.84}, theme.textScale), marginTop: pickerContext == null ? 0 : -4} : undefined}
      descriptionTextStyle={theme.isTV ? {...nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 31.9}, theme.textScale), marginTop: 12} : undefined}
      contentStyle={{paddingTop: theme.isTV ? pickerDescription == null ? 32 : 26 : 0}}
      initialFocusRef={!blocked && initialIndex >= 0 ? selectedRow : undefined} returnFocusRef={trigger} testID={testID ? `${testID}-picker` : undefined}>
      <FlatList ref={list} data={options} keyExtractor={option => option.value} accessibilityRole="radiogroup"
        initialScrollIndex={initialIndex >= 0 ? initialIndex : undefined}
        getItemLayout={(_, index) => ({length: rowHeight, offset: rowPitch * index, index})}
        initialNumToRender={12} maxToRenderPerBatch={12} windowSize={5}
        removeClippedSubviews={false} ItemSeparatorComponent={theme.isTV ? () => <View style={{height: 10}} /> : undefined}
        style={{height: Math.min(options.length * rowPitch - (theme.isTV && options.length ? 10 : 0), theme.isTV ? 600 : 352), flexGrow: 0, overflow: 'visible'}}
        contentContainerStyle={{paddingHorizontal: theme.isTV ? 0 : 8}}
        renderItem={({item: option, index}) => <Focusable ref={index === initialIndex ? selectedRow : undefined}
          disabled={blocked || option.disabled} focusKind={theme.isTV ? 'button' : 'row'} focusRadius={theme.isTV ? 14 : 9} focusInset={!theme.isTV}
          hasTVPreferredFocus={theme.isTV && index === initialIndex} accessibilityRole="radio" accessibilityState={{checked: option.value === current}}
          accessibilityLabel={typeof option.label === 'string' ? option.label : undefined}
          accessibilityHint={rich && typeof option.description === 'string' ? option.description : undefined}
          accessibilityValue={option.isDefault ? {text: defaultI18n.t('next.kit.default')} : undefined}
          testID={testID ? `${testID}-option-${option.value}` : undefined}
          onPress={() => {if (!blocked && !option.disabled) {setCurrent(option.value); setOpen(false);}}}
          style={[styles.option, {height: rowHeight, gap: theme.isTV ? 16 : 10, paddingHorizontal: theme.isTV ? 26 : 12,
            borderRadius: theme.isTV ? 14 : 9, opacity: blocked || option.disabled ? .45 : 1,
            backgroundColor: theme.isTV ? colorAlpha(theme.colors.text, .14) : option.value === current ? colorAlpha(theme.colors.text, .1) : 'transparent'}]}>
          {state => <>
          {theme.isTV && state.focused ? <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: 14, backgroundColor: theme.colors.text}]} /> : null}
          {!theme.isTV ? <View style={{width: 20}}>{option.value === current ? <Icon name="i-check" size={20} color={theme.colors.accentStrong} /> : null}</View> : null}
          <View style={styles.label}>
            <View style={styles.optionHeading}><Text numberOfLines={1} ellipsizeMode="tail" style={[theme.type.button, styles.label, {fontSize: (theme.isTV ? 24 : 15) * theme.textScale, color: theme.isTV && state.focused ? theme.colors.ink : theme.colors.text}]}>{option.label}</Text>
              {option.isDefault ? <Text style={[theme.type.micro, {color: theme.colors.textSecondary, backgroundColor: colorAlpha(theme.colors.text, .1), borderRadius: 6, paddingHorizontal: 7, paddingVertical: 2}]}>{defaultI18n.t('next.kit.default')}</Text> : null}
            </View>
            {rich && option.description !== undefined ? <Text numberOfLines={2} style={[theme.type.caption, {color: theme.isTV && state.focused ? colorAlpha(theme.colors.ink, .66) : theme.colors.textTertiary, marginTop: 4}]}>{option.description}</Text> : null}
          </View>
          {theme.isTV && option.value === current ? <Icon name="i-check" size={26} color={theme.colors.accentStrong} /> : null}
          </>}
        </Focusable>} />
    </Dialog>
  </View>;
});

function PickerHint() {
  const theme = useTheme();
  const hintType = nativeType({size: 20, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale);
  const keyType = nativeType({size: 17, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale);
  const key = (label: string) => <View style={{height: 36, minWidth: 44, paddingHorizontal: 10, borderRadius: 9, borderWidth: 1.5,
    borderColor: colorAlpha(theme.colors.text, .3), alignItems: 'center', justifyContent: 'center'}}><Text style={[keyType, {color: theme.colors.textSecondary}]}>{label}</Text></View>;
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
    {key(defaultI18n.t('title.ok'))}<Text style={[hintType, {color: theme.colors.textTertiary}]}>{defaultI18n.t('next.controls.native.choose')}</Text>
    <View style={{width: 16}} /><View style={{width: 16}} />
    {key(defaultI18n.t('action.back'))}<Text style={[hintType, {color: theme.colors.textTertiary}]}>{defaultI18n.t('action.close')}</Text>
  </View>;
}

export const Select = forwardRef<View, SelectProps>(function Select(props, ref) {return <NativeSelect {...props} rich={false} ref={ref} />;});
export const RichSelect = forwardRef<View, RichSelectProps>(function RichSelect(props, ref) {return <NativeSelect {...props} rich ref={ref} />;});

const styles = StyleSheet.create({container: {gap: 8, alignSelf: 'stretch'}, trigger: {alignSelf: 'stretch', flexDirection: 'row', alignItems: 'center', borderWidth: 2}, label: {flex: 1, minWidth: 0}, option: {flexDirection: 'row', alignItems: 'center'}, optionHeading: {flexDirection: 'row', alignItems: 'center', gap: 8}});
