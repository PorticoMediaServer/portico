import React, {forwardRef} from 'react';
import {Pressable, StyleSheet, View, type PressableProps} from 'react-native';
import {Icon} from '../Icon';
import {Focusable} from '../controls/Focusable';
import {useTheme} from '../theme';
import {selectionCircleGeometry, selectionIntent, type SelectionIntent} from './selection-geometry';

export type SelectionCircleProps = Omit<PressableProps, 'children' | 'accessibilityLabel'> & {
  selected: boolean;
  selecting?: boolean;
  disabled?: boolean;
  accessibilityLabel: string;
  onSelectedChange?: (next: boolean, intent: SelectionIntent) => void;
  size?: number;
  variant?: 'card' | 'row';
  interactive?: boolean;
};

/** Artwork rings and title colour belong to the card; this component owns only the circle. */
export const SelectionCircle = forwardRef<View, SelectionCircleProps>(function SelectionCircle({
  selected, selecting = false, disabled = false, accessibilityLabel, onSelectedChange,
  size, variant = 'card', interactive = true, style, onPress, accessibilityState, ...props
}, ref) {
  const {isTV, colors} = useTheme();
  const geometry = selectionCircleGeometry(isTV, size);
  const visible = selected || selecting;
  const face = <View pointerEvents="none" style={[styles.face, {
    width: geometry.diameter, height: geometry.diameter, borderRadius: geometry.diameter / 2,
    borderWidth: selected ? 0 : geometry.border,
    borderColor: variant === 'row' ? 'rgba(237,245,250,.8)' : 'rgba(237,245,250,.92)',
    backgroundColor: selected ? colors.accentStrong : variant === 'row' ? 'transparent' : isTV ? 'rgba(7,16,24,.45)' : 'rgba(7,16,24,.42)',
    boxShadow: variant === 'row' ? undefined : selected ? '0 2px 8px rgba(0,0,0,.5)' : '0 1px 5px rgba(0,0,0,.5)',
  }]}>{selected ? <Icon name="i-check" size={geometry.check} color="#03121c" /> : null}</View>;
  if (!interactive) return <Pressable ref={ref} testID={props.testID} nativeID={props.nativeID} disabled
    pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" focusable={false}
    style={state => [{opacity: visible ? disabled ? .42 : 1 : 0}, typeof style === 'function' ? style(state) : style]}>{face}</Pressable>;
  return <Focusable {...props} ref={ref} disabled={disabled || !visible} focusable={visible ? props.focusable : false}
    accessibilityLabel={accessibilityLabel} accessibilityRole="checkbox"
    accessibilityElementsHidden={!visible} importantForAccessibility={visible ? 'yes' : 'no-hide-descendants'}
    accessibilityState={{...accessibilityState, checked: selected, disabled}}
    focusRadius={geometry.diameter / 2}
    onPress={event => {
      if (disabled || !visible) return;
      const shiftKey = (event.nativeEvent as typeof event.nativeEvent & {shiftKey?: boolean}).shiftKey;
      onSelectedChange?.(!selected, selectionIntent(selected, selecting, shiftKey));
      onPress?.(event);
    }} style={state => [{opacity: visible ? disabled ? .42 : 1 : 0}, typeof style === 'function' ? style(state) : style]}>{face}</Focusable>;
});
const styles = StyleSheet.create({face: {alignItems: 'center', justifyContent: 'center'}});
