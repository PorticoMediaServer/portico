import React, {useId} from 'react';
import {ScrollView, StyleSheet, Text, View, type StyleProp, type TextStyle, type ViewStyle} from 'react-native';
import {tabsSpec} from '@portico/design';
import {focusTestID} from '../focus';
import {useTheme} from '../theme';
import {Focusable, type FocusableProps} from './Focusable';
import {colorAlpha, enabledChoice, minimumHitSlop} from './control-model';
import {useControlValue} from './useControlValue';

export type TabItem = {value: string; label: React.ReactNode; count?: string | number; disabled?: boolean; panel?: React.ReactNode};
export type TabsProps = {
  items: readonly TabItem[];
  value?: string;
  defaultValue?: string;
  onValueChange?: (value: string) => void;
  disabled?: boolean;
  /** Section position markers retain typography but offer no focus or action. */
  passive?: boolean;
  accessibilityLabel?: string;
  style?: StyleProp<ViewStyle>;
  contentContainerStyle?: StyleProp<ViewStyle>;
  itemProps?: (item: TabItem) => Omit<FocusableProps, 'children'>;
  testID?: string;
};
export type SegmentedProps = Omit<TabsProps, 'contentContainerStyle'> & {variant?: 'glass' | 'browse' | 'phone'};

/** Native icon/custom labels live in a View; plain text receives the kit typography. */
function ChoiceLabel({children, style, wrap = false, count, countStyle}: {children: React.ReactNode; style: StyleProp<TextStyle>; wrap?: boolean; count?: string | number; countStyle?: StyleProp<TextStyle>}) {
  const label = typeof children === 'string' || typeof children === 'number'
    ? <Text numberOfLines={wrap ? undefined : 1} style={[style, wrap && {flexShrink: 1}]}>{children}</Text>
    : <View style={{flexDirection: 'row', alignItems: 'center', flexShrink: 1}}>{children}</View>;
  return count === undefined ? label : <View style={{flexDirection: 'row', alignItems: 'center'}}>{label}<Text style={countStyle}>{count}</Text></View>;
}

export function Tabs({items, value, defaultValue, onValueChange, disabled, passive = false, accessibilityLabel, style, contentContainerStyle, itemProps, testID}: TabsProps) {
  const theme = useTheme();
  const id = useId();
  const [current, setCurrent] = useControlValue(value, defaultValue ?? enabledChoice(items) ?? '', onValueChange);
  const active = items.find(item => item.value === current);
  const countStyle: TextStyle = {marginLeft: 6, paddingVertical: 1, paddingHorizontal: 7, borderRadius: 9, overflow: 'hidden',
    fontFamily: 'Manrope-Bold', fontSize: 12 * theme.textScale, fontWeight: '700', backgroundColor: theme.colors.raisedBright, color: theme.colors.textSecondary};
  return <View style={style} testID={testID}>
    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{overflow: 'visible'}}
      contentContainerStyle={[styles.tabs, {gap: tabsSpec.gap[theme.platform], paddingHorizontal: theme.gutter, borderBottomColor: theme.colors.line}, contentContainerStyle]}
      accessibilityRole={passive ? undefined : 'tablist'} accessibilityLabel={accessibilityLabel}>
      {items.map(item => {
        const childProps = itemProps?.(item);
        const blocked = disabled || item.disabled;
        const selected = item.value === current;
        if (passive) return <View key={item.value} testID={testID ? `${testID}-item-${item.value}` : undefined} focusable={false}
          accessibilityState={{selected}} style={{paddingTop: tabsSpec.paddingTop[theme.platform], paddingBottom: tabsSpec.paddingBottom[theme.platform]}}>
          <ChoiceLabel wrap={theme.fontScale > 1} count={item.count} countStyle={countStyle} style={[theme.type.tab, {color: selected ? theme.colors.text : theme.colors.textTertiary}]}>{item.label}</ChoiceLabel>
          {selected ? <View pointerEvents="none" style={[styles.underline, {height: tabsSpec.underline[theme.platform], backgroundColor: theme.colors.accent}]} /> : null}
        </View>;
        return <Focusable {...childProps} testID={childProps?.testID ?? (testID ? `${testID}-item-${item.value}` : undefined)} focusId={childProps?.focusId ?? (testID ? focusTestID(testID, item.value) : childProps?.testID)} key={item.value} nativeID={`${id}-${item.value}`} accessibilityRole="tab"
          accessibilityLabel={childProps?.accessibilityLabel ?? (item.count !== undefined && (typeof item.label === 'string' || typeof item.label === 'number') ? `${item.label}, ${item.count}` : undefined)}
          disabled={blocked} accessibilityState={{...childProps?.accessibilityState, selected}}
          focusRadius={4} focusInset focusKind="row" onPress={event => {if (!blocked) {setCurrent(item.value); childProps?.onPress?.(event);}}}
          style={state => [{paddingTop: tabsSpec.paddingTop[theme.platform], paddingBottom: tabsSpec.paddingBottom[theme.platform], opacity: blocked ? .45 : 1},
            typeof childProps?.style === 'function' ? childProps.style(state) : childProps?.style]}>
          <ChoiceLabel wrap={theme.fontScale > 1} count={item.count} countStyle={countStyle} style={[theme.type.tab, {color: selected ? theme.colors.text : theme.colors.textTertiary}]}>{item.label}</ChoiceLabel>
          {selected ? <View pointerEvents="none" style={[styles.underline, {height: tabsSpec.underline[theme.platform], backgroundColor: theme.colors.accent}]} /> : null}
        </Focusable>;
      })}
    </ScrollView>
    {active?.panel !== undefined ? <View role="tabpanel" accessibilityLabelledBy={`${id}-${active.value}`}>{active.panel}</View> : null}
  </View>;
}

export function Segmented({items, value, defaultValue, onValueChange, variant = 'glass', disabled, accessibilityLabel, style, itemProps, testID}: SegmentedProps) {
  const theme = useTheme();
  const [current, setCurrent] = useControlValue(value, defaultValue ?? enabledChoice(items) ?? '', onValueChange);
  const phone = variant === 'phone';
  const height = theme.isTV ? 64 : phone ? 28 : 34;
  const slop = minimumHitSlop(height + 6, theme.isTV);
  return <View accessibilityRole="radiogroup" accessibilityLabel={accessibilityLabel} testID={testID}
    style={[styles.segmented, {alignSelf: theme.isTV ? 'stretch' : 'flex-start', borderRadius: theme.isTV ? 18 : phone ? 9 : 11, padding: theme.isTV ? 8 : 3, gap: theme.isTV ? 8 : 0,
      backgroundColor: theme.isTV ? colorAlpha(theme.colors.text, .06) : variant === 'glass' ? theme.colors.ink : phone ? theme.colors.raisedBright : theme.colors.raisedHigh,
      borderColor: variant === 'glass' ? theme.colors.line : 'transparent', borderWidth: !theme.isTV && variant === 'glass' ? 1 : 0}, style]}>
    {items.map(item => {
      const childProps = itemProps?.(item);
      const blocked = disabled || item.disabled;
      const selected = item.value === current;
      return <Focusable {...childProps} testID={childProps?.testID ?? (testID ? `${testID}-item-${item.value}` : undefined)} focusId={childProps?.focusId ?? (testID ? focusTestID(testID, item.value) : childProps?.testID)} key={item.value} accessibilityRole="radio" accessibilityState={{...childProps?.accessibilityState, checked: selected}}
        disabled={blocked} focusKind="row" focusRadius={theme.isTV ? 12 : phone ? 7 : 8} focusInset
        hitSlop={childProps?.hitSlop ?? {top: slop, bottom: slop}}
        onPress={event => {if (!blocked) {setCurrent(item.value); childProps?.onPress?.(event);}}}
        style={state => [styles.segment, {minHeight: height, borderRadius: theme.isTV ? 12 : phone ? 7 : 8, paddingHorizontal: theme.isTV ? 20 : phone ? 11 : 14,
          flex: theme.isTV ? 1 : undefined, borderWidth: theme.isTV ? 1.5 : 0, borderColor: theme.isTV && selected ? colorAlpha(theme.colors.accentStrong, .7) : 'transparent',
          backgroundColor: !theme.isTV && selected ? variant === 'browse' ? theme.colors.raisedBright : colorAlpha(theme.colors.text, .16) : 'transparent', opacity: blocked ? .45 : 1},
          typeof childProps?.style === 'function' ? childProps.style(state) : childProps?.style]}>
        {state => <>
          {theme.isTV ? <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: 12, backgroundColor: selected ? colorAlpha(theme.colors.accent, state.focused ? .4 : .26) : state.focused ? colorAlpha(theme.colors.text, .2) : 'transparent'}]} /> : null}
          <ChoiceLabel wrap={theme.fontScale > 1} style={[theme.type.button, {color: selected || state.focused ? theme.colors.text : theme.isTV ? theme.colors.textSecondary : theme.colors.textTertiary, fontSize: (theme.isTV ? 24 : phone ? 13 : 14) * theme.textScale}]}>{item.label}</ChoiceLabel>
        </>}
      </Focusable>;
    })}
  </View>;
}

const styles = StyleSheet.create({tabs: {flexDirection: 'row', alignItems: 'stretch', borderBottomWidth: 1}, underline: {position: 'absolute', left: 0, right: 0, bottom: -1, borderRadius: 3}, segmented: {flexDirection: 'row', alignSelf: 'flex-start'}, segment: {alignItems: 'center', justifyContent: 'center', flexShrink: 1, minWidth: 38}});
