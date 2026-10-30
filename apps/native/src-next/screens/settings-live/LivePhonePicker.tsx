import React, {useRef, useState} from 'react';
import {Text, View, type StyleProp, type ViewStyle} from 'react-native';
import {Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Menu, type MenuAction} from '../../kit/overlays';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';

/** phone-guide's inline controls retain their genuine native opener and use the
 * existing anchored menu. No settings, source or remembered-view store lives here. */
export function NativeLivePhonePicker({value, options, onChange, children, accessibilityLabel, testID, style, disabled, placement = 'bottom-start', menuWidth = 230, secondary = false, note}: {
  value: string;
  options: readonly Omit<MenuAction, 'onSelect'>[];
  onChange: (value: string) => void;
  children: React.ReactNode;
  accessibilityLabel: string;
  testID: string;
  style?: StyleProp<ViewStyle>;
  disabled?: boolean;
  placement?: 'bottom-start' | 'bottom-end';
  menuWidth?: number;
  secondary?: boolean;
  note?: string;
}) {
  const theme = useTheme(), anchor = useRef<View>(null), [open, setOpen] = useState(false);
  const selectedLabel = options.find(option => option.id === value)?.label;
  return <>
    <Focusable ref={anchor} testID={testID} accessibilityRole="combobox" accessibilityLabel={accessibilityLabel}
      accessibilityState={{expanded: open, disabled}} accessibilityValue={typeof selectedLabel === 'string' ? {text: selectedLabel} : undefined} disabled={disabled} onPress={() => setOpen(true)}
      style={[{height: 32, flexDirection: 'row', alignItems: 'center', gap: 4, opacity: disabled ? .45 : 1}, style]}>
      {typeof children === 'string' ? <Text numberOfLines={1} style={[shellType(13.5, 700), {color: secondary ? theme.colors.textSecondary : theme.colors.text}]}>{children}</Text> : children}
      <Icon name="i-down" size={15} color={theme.colors.textTertiary}/>
    </Focusable>
    <Menu testID={`${testID}-menu`} open={open} onOpenChange={setOpen} anchorRef={anchor} returnFocusRef={anchor}
      title={accessibilityLabel} accessibilityLabel={accessibilityLabel} presentation="anchored" placement={placement}
      footer={note ? <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{note}</Text> : undefined}
      width={menuWidth} initialFocusId={value} items={options.map(option => ({...option, selected: option.selected ?? option.id === value, onSelect: () => onChange(option.id)}))}/>
  </>;
}
